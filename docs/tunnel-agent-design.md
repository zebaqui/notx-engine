# Tunnel Agent — Design Document

> **Status: DRAFT — awaiting confirmation before implementation**

---

## 1. The Core Problem

The existing `RelayService` executes outbound HTTP requests **server-side**.
The server is on the public internet. `AllowLocalhost` is `false` by default
and must stay that way — the server must never reach `169.254.169.254`,
`127.0.0.1`, or any RFC-1918 range on its own initiative.

But there is a real use-case that the current engine cannot serve:

> A developer has a service running on their laptop at `localhost:3000`.
> They want a browser (or another notx client) to be able to trigger
> requests against that service **through** the notx server, without:
>
> - Opening a public port on their laptop
> - Relaxing the server's `AllowLocalhost` policy
> - Requiring the browser to have direct network access to the laptop

The solution is an **agent process** — a small Go binary that runs on the
developer's machine, dials the notx server outbound, and executes HTTP
requests locally on behalf of callers.

---

## 2. Who Is Who — Roles Clarified

This is the most important section. The previous draft was ambiguous about
which process connects to what. Here is the complete, unambiguous picture.

```
┌─────────────────────────────────────────────────────────────────────┐
│  CALLER                                                             │
│  (a browser, notxctl, or any HTTP client)                          │
│                                                                     │
│  • Has no direct network path to the developer's laptop            │
│  • Speaks JSON over HTTPS to the notx server                       │
│  • Identifies itself with X-Device-ID                               │
│  • Sends:  POST /v1/tunnel/execute                                  │
│            { "agent_urn": "notx:device:<agent-uuid>",              │
│              "request":   { "method": "GET",                       │
│                             "url": "http://localhost:3000/api" } } │
└────────────────────────┬────────────────────────────────────────────┘
                         │  HTTPS / JSON  (caller → server)
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│  notx SERVER  (public internet)                                     │
│                                                                     │
│  • Validates the caller device                                      │
│  • Validates the agent_urn device (registered, not revoked)        │
│  • Enforces: URL must be localhost / 127.x.x.x only                │
│  • Looks up whether that agent has an active long-poll connection   │
│  • Enqueues a TunnelTask onto the agent's channel                  │
│  • Blocks (up to 30 s) waiting for the agent's response            │
│  • Returns the upstream response to the caller                      │
└─────────┬──────────────────────────────────────────────────────────┘
          │                            ▲
          │  SSE stream: task pushed   │  HTTP POST: response pushed back
          │  server → agent            │  agent → server
          │  GET /v1/tunnel/poll       │  POST /v1/tunnel/respond
          ▼                            │
┌─────────────────────────────────────┴──────────────────────────────┐
│  TUNNEL AGENT  (developer's laptop / home server / LAN machine)    │
│  Binary: `notx-agent`                                              │
│                                                                     │
│  • Runs on the machine where localhost services live               │
│  • Dials the notx server OUTBOUND — no inbound port needed         │
│  • Authenticates with its own registered device URN                │
│  • Holds a persistent SSE connection to receive tasks              │
│  • For each task: validates URL is localhost, executes locally,     │
│    POSTs result back to the server                                 │
│  • Reconnects automatically on disconnect                          │
└─────────────────────────┬───────────────────────────────────────────┘
                          │  http://localhost:<port>/...
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│  LOCAL SERVICE  (anything listening on localhost)                   │
│  e.g. localhost:3000, localhost:8080, localhost:5432-via-http-proxy │
└─────────────────────────────────────────────────────────────────────┘
```

### Key point: the agent connects to the server, not the other way around

The server **never** dials the agent's machine. The agent process opens two
outbound HTTP connections to the notx server:

1. `GET /v1/tunnel/poll` — a long-lived SSE stream. The server pushes
   `TunnelTask` events down this stream whenever a caller sends a request
   targeting this agent.

2. `POST /v1/tunnel/respond` — a short-lived unary call. The agent fires
   this once per task to deliver the result back to the server.

This is the **same pattern** the notx server already uses for device status
monitoring (`GET /v1/devices/:urn/status/stream` in `sse_device.go`), just
inverted: the agent is the SSE consumer, the server is the SSE producer.

### Why HTTP/SSE instead of gRPC streaming?

The agent could use gRPC (the existing `grpcclient` package is available).
However, the browser-as-caller path already goes through the HTTP layer, and
SSE is simpler to proxy, debug, and test. More importantly: future versions
of the agent could be implemented in non-Go languages or even run in a
browser service worker — HTTP/SSE works everywhere.

The agent uses the same HTTP transport as all other notx clients. No new
protocol is introduced.

---

## 3. Goals

| #   | Goal                                                                                                          |
| --- | ------------------------------------------------------------------------------------------------------------- |
| G1  | Allow any registered notx caller to proxy HTTP requests through a connected agent to that agent's `localhost` |
| G2  | The agent requires no inbound port, firewall rule, or public IP — it dials out                                |
| G3  | The server enforces all security rules — agent is trusted for execution, not for routing decisions            |
| G4  | Reuse the existing device identity model (device URN, approval, revocation) for agent authentication          |
| G5  | The agent is strictly `localhost`-only — it must never relay requests to remote hosts                         |
| G6  | The existing `RelayService` and its `AllowLocalhost = false` policy are completely unchanged                  |
| G7  | A browser caller needs no special capabilities — it just calls a new HTTP endpoint                            |
| G8  | Ship as a single self-contained binary: `notx-agent`                                                          |

---

## 4. Non-Goals

| #   | Non-goal                                                |
| --- | ------------------------------------------------------- |
| N1  | Relaxing `AllowLocalhost` on the server-side executor   |
| N2  | Raw TCP / WebSocket tunnelling                          |
| N3  | Agent-to-agent relay (multi-hop)                        |
| N4  | Routing to non-localhost addresses from the agent       |
| N5  | Persistent named subdomains or DNS assignment           |
| N6  | `ExecuteFlow` support for tunnel steps (deferred to v2) |

---

## 5. HTTP API

All tunnel endpoints live under `/v1/tunnel/` on the existing HTTP server.

### 5.1 Caller-facing endpoints

#### `POST /v1/tunnel/execute`

The caller asks the server to forward a request to a connected agent's
localhost. This endpoint replaces neither `/v1/relay/execute` nor
`/v1/relay/execute-flow` — it is a new, parallel path.

**Request** (requires `X-Device-ID`):

```json
{
  "agent_urn": "notx:device:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  "request": {
    "method": "GET",
    "url": "http://localhost:3000/api/status",
    "headers": { "Accept": "application/json" },
    "body": ""
  },
  "variables": {
    "port": "3000"
  }
}
```

- `agent_urn` — the device URN of the target agent. Must be registered
  and not revoked. The agent must have an active poll connection.
- `request.url` — **must** be `http://localhost:<port>/...` or
  `http://127.0.0.1:<port>/...`. The server rejects any other host with
  `403 Forbidden`.
- `variables` — optional `{{var}}` interpolation applied to url, headers,
  and body before dispatch (reuses the existing interpolation engine from
  `internal/relay/interpolate.go`).

**Response** (same shape as `/v1/relay/execute`):

```json
{
  "response": {
    "status": 200,
    "headers": { "content-type": "application/json" },
    "body": "{\"status\":\"ok\"}",
    "duration_ms": 14
  }
}
```

**Error cases**:

| HTTP status | Condition                                                    |
| ----------- | ------------------------------------------------------------ |
| 400         | Malformed request body                                       |
| 401         | Missing or unrecognised caller device (`X-Device-ID`)        |
| 403         | Caller device revoked; or target URL is not localhost        |
| 404         | `agent_urn` not registered                                   |
| 503         | Agent is not currently connected (no active poll)            |
| 504         | Agent did not respond within the task timeout (default 30 s) |

---

### 5.2 Agent-facing endpoints

These endpoints are called **by the agent process**, not by browsers or
other callers. They require the agent's own device URN in `X-Device-ID`.

#### `GET /v1/tunnel/poll`

Long-lived SSE stream. The agent opens this once and keeps it open. The
server pushes `TunnelTask` events down the stream as callers submit
requests targeting this agent.

The agent's `X-Device-ID` header identifies which agent is connecting.
Only one active poll connection per agent URN is allowed — a second
connection from the same URN evicts the first (the first stream receives
a terminal `evicted` event before being closed).

**SSE event format**:

```
event: task
data: {
data:   "task_id":         "018f1234-abcd-7000-8000-000000000001",
data:   "method":          "GET",
data:   "url":             "http://localhost:3000/api/status",
data:   "headers":         { "Accept": "application/json" },
data:   "body":            "",
data:   "deadline_unix_ms": 1720000030000
data: }

event: task
data: { ... next task ... }

: keepalive

event: evicted
data: { "reason": "replaced by newer connection" }
```

The `deadline_unix_ms` field is a Unix timestamp in milliseconds. If the
agent has not posted a response by this time the server unblocks the
caller with a timeout error. The agent should abandon work past deadline.

#### `POST /v1/tunnel/respond`

The agent posts the result of a completed task. Requires `X-Device-ID`.

**Request**:

```json
{
  "task_id": "018f1234-abcd-7000-8000-000000000001",
  "status": 200,
  "headers": { "content-type": "application/json" },
  "body": "{\"status\":\"ok\"}",
  "duration_ms": 14,
  "error": ""
}
```

- `task_id` must match a task previously sent to this agent.
- `error` — non-empty if the agent failed to execute the request locally
  (e.g. connection refused). The server maps this to a `502 Bad Gateway`
  to the caller.

**Response**: `200 OK` with `{"accepted": true}` if the task was known
and not yet expired. `404` if the task ID is unknown or already expired.

---

## 6. Server-Side Components

### 6.1 `TunnelBroker` (`internal/tunnel/broker.go`)

An in-memory data structure that lives for the lifetime of the server
process. It manages the mapping between agent URNs, their active SSE
connections, and their in-flight tasks.

```
TunnelBroker
  agents: map[agent_urn] → AgentSlot
    AgentSlot
      taskCh:    chan TunnelTask      // server writes, agent reads via SSE
      responses: map[task_id] → chan TunnelResult  // agent writes, caller reads
      evictCh:   chan struct{}        // closed when agent is evicted
```

**Lifecycle**:

- **Agent connects** (`GET /v1/tunnel/poll`): `RegisterAgent(urn)` creates
  a new `AgentSlot`, evicts any existing slot for that URN.
- **Caller submits task** (`POST /v1/tunnel/execute`): `Dispatch(urn, task)`
  returns a channel the caller blocks on. Returns `ErrAgentNotConnected`
  immediately if no slot exists.
- **Agent responds** (`POST /v1/tunnel/respond`): `Deliver(task_id, result)`
  sends the result to the channel the caller is blocking on. Deletes the
  response channel entry immediately after delivery (no replay).
- **Agent disconnects** (SSE stream closes): `DeregisterAgent(urn)` closes
  all pending response channels with an `ErrAgentDisconnected` error,
  unblocking every waiting caller immediately.
- **Task deadline expires**: the caller's context deadline fires, the
  caller stops blocking and returns a 504. The task's response channel
  entry is cleaned up by the broker's GC loop.

The broker is intentionally **in-memory only** for v1. Tasks do not
survive a server restart. This is acceptable because tunnel tasks are
interactive (a human is waiting) — they are not batch jobs.

### 6.2 `TunnelHandler` (`internal/server/http/tunnel_handler.go`)

Three handler functions wired into the existing `Handler` struct:

- `handleTunnelExecute` — caller-facing. Validates the caller device,
  validates the target URL (localhost only), calls `broker.Dispatch`,
  blocks on the result channel, writes the response.
- `handleTunnelPoll` — agent-facing. Validates the agent device, calls
  `broker.RegisterAgent`, then streams `TunnelTask` events as SSE until
  the agent disconnects or is evicted.
- `handleTunnelRespond` — agent-facing. Validates the agent device, calls
  `broker.Deliver`.

Route registration (inside `Handler.routes()`):

```
POST /v1/tunnel/execute   — withDeviceAuthMiddleware(handleTunnelExecute)
GET  /v1/tunnel/poll      — withDeviceAuthMiddleware(handleTunnelPoll)
POST /v1/tunnel/respond   — withDeviceAuthMiddleware(handleTunnelRespond)
```

No gRPC changes are needed. The tunnel service is HTTP-native, matching
the SSE pattern already established in `sse_device.go`.

### 6.3 Localhost URL validation (`internal/tunnel/policy.go`)

A separate, minimal policy struct — distinct from `relay.Policy` and not
touching `AllowLocalhost`:

```go
type TunnelPolicy struct {
    // AllowedPorts, when non-empty, restricts which localhost ports
    // the server will dispatch tasks to. Empty means all ports are allowed.
    AllowedPorts []int

    // MaxBodyBytes caps request and response body sizes. Default: 4 MiB.
    MaxBodyBytes int64

    // TaskTimeoutSecs is how long the server waits for the agent to respond.
    // Default: 30.
    TaskTimeoutSecs int

    // MaxConcurrentTasksPerAgent caps in-flight tasks per agent.
    // Default: 10.
    MaxConcurrentTasksPerAgent int
}

// ValidateTargetURL returns a non-nil error if the URL is not a safe
// localhost target. It only accepts:
//   - scheme: http (https is intentionally excluded — localhost TLS is rare
//     and the tunnel transport itself is protected end-to-end by the server's TLS)
//   - host:   localhost, 127.0.0.1, or ::1
//   - port:   any port (or from AllowedPorts if configured)
func (p *TunnelPolicy) ValidateTargetURL(rawURL string) error { ... }
```

---

## 7. Agent Process

### 7.1 What the agent is

`notx-agent` is a small, long-running Go binary that runs on the
developer's machine. It has no HTTP server of its own (except an optional
`/healthz` endpoint for process supervisors). It acts purely as a client
to the notx server.

### 7.2 Authentication

The agent authenticates with the notx server using a **registered device URN**
and the standard `X-Device-ID` header — the same mechanism every other notx
client uses. There is no special agent credential type.

Before running the agent, the operator:

1. Registers a device for the agent via `POST /v1/devices` (or `notxctl`).
2. Gets that device approved (auto-approve if configured, or manual).
3. Sets the device URN in the agent's config.

The agent is just another device. Revoking the device immediately and
permanently disconnects the agent (the server will reject its poll connection
on the next heartbeat or reconnect attempt, because `validateDevice` runs
on every request).

### 7.3 Run loop

```
startup
  │
  ├─ load config (server addr, device URN, allowed ports, ...)
  ├─ validate config
  │
  └─ loop forever:
       │
       ├─ dial notx server (plain HTTP client, no grpcclient needed)
       ├─ GET /v1/tunnel/poll  (X-Device-ID: <agent-urn>)
       │   │
       │   └─ read SSE stream:
       │        ├─ event: task   → spawn goroutine to execute locally
       │        │                  then POST /v1/tunnel/respond
       │        ├─ event: evicted → log, reconnect immediately
       │        └─ event: keepalive → no-op
       │
       └─ on disconnect: wait (exponential backoff, max 30 s), reconnect
```

Local task execution (inside the agent, not the server):

```
receive TunnelTask {task_id, method, url, headers, body, deadline}
  │
  ├─ validate url: must be localhost/127.0.0.1/::1 (second enforcement layer)
  ├─ validate port: must be in AllowedPorts (if configured)
  ├─ check deadline: if already past, skip and do not respond
  │
  ├─ build context with deadline (context.WithDeadline)
  ├─ execute http.NewRequestWithContext → local service
  ├─ read response (size-limited to MaxBodyBytes)
  │
  └─ POST /v1/tunnel/respond {task_id, status, headers, body, duration_ms}
```

### 7.4 Agent config

The agent reads from a small config file or CLI flags. It reuses
`internal/clientconfig` for the server address and TLS settings.

```
notx-agent \
  --server      https://my.notx.server:4060   \
  --device-urn  notx:device:<uuid>            \
  --allowed-ports 3000,8080                   \
  --health-port 14060
```

Or via `~/.notx/config.json` with a new `[agent]` section:

```json
{
  "agent": {
    "server_http_addr": "https://my.notx.server:4060",
    "device_urn": "notx:device:<uuid>",
    "allowed_ports": [3000, 8080],
    "health_port": 14060,
    "reconnect_max_secs": 30
  }
}
```

---

## 8. Security Model

| Threat                                                   | Mitigation                                                                                                                                                                                   |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Caller forges `agent_urn` to target another user's agent | `agent_urn` must be a registered device; the server validates it exists and is not revoked. Future: per-device ACL (caller must be in agent's allow-list)                                    |
| Server dispatches request to non-localhost target        | `TunnelPolicy.ValidateTargetURL` runs server-side before enqueue; only `localhost` / `127.x.x.x` / `::1` accepted. This is independent of `relay.Policy.AllowLocalhost`                      |
| Agent receives task targeting non-localhost              | Agent runs a second identical check client-side. Belt-and-suspenders                                                                                                                         |
| Agent used to port-scan localhost                        | `AllowedPorts` list in `TunnelPolicy` and `AgentConfig` restricts reachable ports                                                                                                            |
| Task replay                                              | Each `task_id` is a UUID v4; the response channel is deleted on first delivery                                                                                                               |
| Task flood / resource exhaustion                         | `MaxConcurrentTasksPerAgent` cap; tasks beyond the cap are rejected `429` to the caller                                                                                                      |
| Agent impersonation                                      | Any process can claim a device URN — but the URN must be registered and approved. Revoke the device to permanently block an agent                                                            |
| Stale tasks after agent disconnect                       | Broker drains all response channels with `ErrAgentDisconnected` on deregister, unblocking all waiting callers with `503` immediately                                                         |
| Caller observing another caller's response               | Each caller blocks on its own per-`task_id` channel; channels are not shared                                                                                                                 |
| `AllowLocalhost` policy bypass                           | The tunnel path has no connection to `relay.Policy`. The existing server-side executor is not involved. There is no code path that enables the server's `net/http` client to reach localhost |

---

## 9. Connection Model — The Critical Detail

To make this impossible to misread, here is the exact sequence of TCP
connections established during a tunnel request:

```
Step 1 — Agent dials server (happens once at startup, stays open):

  [agent machine]  ──TCP SYN──►  [notx server :4060]
  GET /v1/tunnel/poll HTTP/1.1
  X-Device-ID: notx:device:<agent-urn>
  Accept: text/event-stream
                   ◄──────────  HTTP 200 (SSE stream open, server holds it)


Step 2 — Caller submits request (happens per request):

  [browser]  ──TCP SYN──►  [notx server :4060]
  POST /v1/tunnel/execute HTTP/1.1
  X-Device-ID: notx:device:<caller-urn>
  { "agent_urn": "...", "request": { "url": "http://localhost:3000/..." } }

               (server validates, enqueues task, blocks waiting for response)

Step 3 — Server pushes task to agent (over the already-open SSE stream):

  [notx server]  ──SSE event──►  [agent machine]
  event: task
  data: { "task_id": "uuid", "method": "GET", "url": "http://localhost:3000/..." }

Step 4 — Agent executes locally (no server involvement):

  [agent machine]  ──TCP SYN──►  [localhost:3000]
  GET /api/status HTTP/1.1
                  ◄──────────  HTTP 200 { "status": "ok" }

Step 5 — Agent posts result to server:

  [agent machine]  ──TCP SYN──►  [notx server :4060]
  POST /v1/tunnel/respond HTTP/1.1
  X-Device-ID: notx:device:<agent-urn>
  { "task_id": "uuid", "status": 200, "body": "..." }

Step 6 — Server unblocks and responds to caller:

  [notx server]  ──HTTP 200──►  [browser]
  { "response": { "status": 200, "body": "..." } }
```

**No step involves an inbound connection to the agent machine.**
**No step involves the server reaching `localhost` on its own machine.**
**The browser never connects to the agent machine.**

---

## 10. File Layout

```
notx-engine/
├── internal/
│   ├── tunnel/
│   │   ├── broker.go           # TunnelBroker: in-memory task dispatch
│   │   ├── broker_test.go
│   │   ├── policy.go           # TunnelPolicy: localhost URL validation
│   │   └── policy_test.go
│   └── server/
│       └── http/
│           └── tunnel_handler.go   # handleTunnelExecute / Poll / Respond
├── cmd/
│   └── notx-agent/
│       └── main.go             # Agent binary entry point
├── internal/
│   └── agent/
│       ├── agent.go            # Run loop: SSE consumer + local executor
│       └── agent_test.go
├── docs/
│   └── tunnel-agent.md         # User-facing "how to run the agent" guide
└── tests/
    └── smoke/
        └── tunnel_test.go      # End-to-end smoke test
```

No `.proto` files are added. No gRPC changes are made. The tunnel system
is HTTP-native throughout.

---

## 11. What Does Not Change

| Component                                   | Status                                                                                           |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `relay.Policy` and `AllowLocalhost`         | **Unchanged** — stays `false` in production                                                      |
| `RelayService.Execute` and `ExecuteFlow`    | **Unchanged** — existing server-side relay is unaffected                                         |
| `internal/relay/` package                   | **Unchanged** — `interpolate.go` is reused by the tunnel handler for `{{variable}}` substitution |
| `relay.proto` / `relay.pb.go`               | **Unchanged** — no new fields on existing messages                                               |
| Device registration / approval / revocation | **Unchanged** — the agent is just another device                                                 |
| `internal/grpcclient`                       | **Unchanged** — the agent uses plain HTTP, not gRPC                                              |
| `internal/server/grpc/`                     | **Unchanged** — no new gRPC services                                                             |

---

## 12. Open Questions — Confirmation Needed

Before implementation begins, please confirm your preference on the
following:

1. **Caller authorization** — In v1, any approved device can dispatch
   tasks to any other approved device's agent. Should we add an explicit
   **per-agent allow-list** (the agent owner configures which caller device
   URNs may send it tasks), or is the shared-server trust model acceptable
   for v1?

2. **Single active agent per URN** — The design evicts the older connection
   when the same agent URN reconnects. Is this the right behaviour, or
   should we allow multiple parallel connections (e.g. for redundancy)?

3. **`AllowedPorts` enforcement** — Currently proposed as a client-side
   config on the agent. Should the server also carry a per-agent port
   allowlist (stored alongside the device record), or is client-side
   enforcement sufficient?

4. **`ExecuteFlow` tunnel steps** — Confirmed out of scope for v1?

5. **Binary name and location** — `cmd/notx-agent` as a standalone binary,
   or `notx agent` as a sub-command of the existing `notx` / `notxctl` CLI?

6. **HTTPS to localhost** — The design restricts the agent to `http://localhost`
   only (not `https://`), since localhost TLS is uncommon and the
   server-to-agent transport is already TLS-protected. Is this acceptable,
   or do you need `https://localhost` support for services with self-signed certs?
