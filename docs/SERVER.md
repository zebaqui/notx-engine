# notx Server

## Overview

`notx server` runs a persistent, local-first daemon that exposes the notx storage engine over two independent protocol layers: an HTTP/JSON API and a gRPC API. Both layers share a single storage backend. Either layer can be disabled independently at startup. The server is designed to run on a developer machine, a self-hosted VM, or a containerised environment.

Entry point: `internal/cli/server.go` (cobra command) → `internal/server/server.go` (`Run()`).

---

## First Run — Auto-Init

When `notx server` is run for the first time on a machine with no existing configuration, it automatically initialises everything needed before starting:

| Step                  | What happens                                                                                                                                                                                                                                                                                           |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **1. Config file**    | `~/.notx/config.json` is written from built-in defaults. A notice is printed to stdout.                                                                                                                                                                                                                |
| **2. Data directory** | `~/.notx/data/notes/` and `~/.notx/data/index/` are created (mode `0755`).                                                                                                                                                                                                                             |
| **3. Admin device**   | A well-known admin device (`urn:notx:device:00000000-0000-0000-0000-000000000000`) is registered with `approval_status: approved`. This device is restored to approved on every subsequent startup, so the admin UI can always reach data endpoints regardless of the `--device-auto-approve` setting. |

All three steps are **idempotent** — running `notx server` a second time is safe and produces no duplicates or overwrites.

### First-run output

```
  ✓  First run — created default config at /Users/you/.notx/config.json
       Run notx config to customise it.

time=... level=INFO msg="admin device registered" device_urn=urn:notx:device:00000000-0000-0000-0000-000000000000
time=... level=INFO msg="notx server starting" http=true http_addr=:4060 grpc=true grpc_addr=:50051 ...
```

After first run you can immediately start the admin UI on the same machine:

```bash
notx admin          # serves on :9090, proxies API to localhost:4060
```

No manual configuration is required for a local single-machine setup.

---

## Configuration

All server configuration is expressed in a single struct defined in `internal/server/config/config.go`:

```go
type Config struct {
    EnableHTTP       bool
    EnableGRPC       bool
    HTTPPort         int
    GRPCPort         int
    Host             string
    DataDir          string
    TLSCertFile      string
    TLSKeyFile       string
    TLSCAFile        string
    ShutdownTimeout  time.Duration
    MaxPageSize      int
    DefaultPageSize  int
    LogLevel         string
    DeviceOnboarding DeviceOnboardingConfig
    Admin            AdminConfig
    Pairing          ServerPairingConfig
}
```

The `ServerPairingConfig` sub-struct controls the server-to-server pairing subsystem:

```go
type ServerPairingConfig struct {
    Enabled              bool
    BootstrapPort        int           // default 50052
    CertTTL              time.Duration // default 720h (30 days)
    SecretTTL            time.Duration // default 15m
    CADir                string        // default "<data-dir>/ca"
    RenewalCheckInterval time.Duration // default 6h
    RenewalThreshold     time.Duration // default 168h (7 days)
    PeerAuthority        string
    PeerSecret           string
    PeerCertDir          string
}
```

Defaults are provided by `Config.Default()`:

| Field                          | Default                                                | Notes                                                  |
| ------------------------------ | ------------------------------------------------------ | ------------------------------------------------------ |
| `EnableHTTP`                   | `true`                                                 |                                                        |
| `EnableGRPC`                   | `true`                                                 |                                                        |
| `HTTPPort`                     | `4060`                                                 |                                                        |
| `GRPCPort`                     | `50051`                                                |                                                        |
| `Host`                         | `""` (all interfaces)                                  | Empty string binds to `0.0.0.0`                        |
| `DataDir`                      | `~/.notx/data`                                         |                                                        |
| `TLSCertFile`                  | `""`                                                   | Empty = plaintext (dev only)                           |
| `TLSKeyFile`                   | `""`                                                   |                                                        |
| `TLSCAFile`                    | `""`                                                   | Non-empty enables mTLS                                 |
| `ShutdownTimeout`              | `30s`                                                  |                                                        |
| `MaxPageSize`                  | `200`                                                  |                                                        |
| `DefaultPageSize`              | `50`                                                   |                                                        |
| `LogLevel`                     | `"info"`                                               |                                                        |
| `DeviceOnboarding.AutoApprove` | `false`                                                | Set `true` to skip manual approval step                |
| `Admin.DeviceURN`              | `urn:notx:device:00000000-0000-0000-0000-000000000000` | Built-in admin device; always approved                 |
| `Admin.OwnerURN`               | `urn:notx:usr:00000000-0000-0000-0000-000000000000`    | Owner of the admin device                              |
| `Pairing.Enabled`              | `false`                                                | Opt-in; set `true` or pass `--pairing`                 |
| `Pairing.BootstrapPort`        | `50052`                                                | Bootstrap listener port                                |
| `Pairing.CertTTL`              | `720h` (30 days)                                       | Validity of issued server certificates                 |
| `Pairing.SecretTTL`            | `15m`                                                  | Validity of a generated pairing secret                 |
| `Pairing.CADir`                | `<data-dir>/ca`                                        | Where the authority CA key and cert are stored         |
| `Pairing.RenewalCheckInterval` | `6h`                                                   | How often a joining server checks cert expiry          |
| `Pairing.RenewalThreshold`     | `168h` (7 days)                                        | TTL remaining at which renewal is triggered            |
| `Pairing.PeerAuthority`        | `""`                                                   | Authority gRPC endpoint for a joining server           |
| `Pairing.PeerSecret`           | `""`                                                   | Pairing secret (used once, then cleared)               |
| `Pairing.PeerCertDir`          | `""`                                                   | Directory for the joining server's client cert and key |

### Config File Seeding

Before cobra parses CLI flags, the config file at `~/.notx/config.json` is read by `internal/clientconfig`. The `server.*` section of that file seeds the cobra flag defaults for `notx server`. CLI flags always win over config file values.

If the file does not exist, `notx server` creates it from built-in defaults before doing anything else (see [First Run](#first-run--auto-init) above).

Example `~/.notx/config.json`:

```json
{
  "server": {
    "http_addr": ":4060",
    "grpc_addr": ":50051",
    "enable_http": true,
    "enable_grpc": true,
    "shutdown_timeout_sec": 30
  },
  "storage": {
    "data_dir": "/Users/you/.notx/data"
  },
  "log": {
    "level": "info"
  }
}
```

Set persistent values interactively with `notx config`.

---

## Storage Layer

The storage backend is implemented in `internal/repo/file/provider.go`. It uses three co-located artefacts per note and a shared Badger v4 index. There is no database server dependency.

### Directory Layout

All artefacts live under `DataDir` (default `~/.notx/data`):

```
<dataDir>/
├── notes/
│   ├── notx_note_<id>.notx         # creation-time stub
│   └── notx_note_<id>.meta.json    # live header (mutable)
├── events/
│   └── notx_note_<id>.jsonl        # append-only event journal
├── index/
│   └── (Badger v4 database files)
├── ca/
│   ├── ca.key          ← EC P-256 CA private key (mode 0600, authority only)
│   └── ca.crt          ← Self-signed CA certificate (mode 0644)
├── servers/
│   └── notx_srv_<id>.json   ← (concept only – stored in Badger via server: prefix)
└── pairing_secrets/
    └── <id>.json       ← single-use pairing secret records (bcrypt hash only)
```

**`ca/`** — Created automatically on first startup when `--pairing` is enabled. The `ca.key` file (mode `0600`) is never served over the network. The `ca.crt` is the only part distributed — it is embedded in every issued certificate chain and available via the unauthenticated `GetCACertificate` RPC.

**`servers/`** — Server records are stored in the Badger index under the `server:` key prefix, not as individual JSON files. The directory entry in the tree above is conceptual only; the actual storage is inside `index/`.

**`pairing_secrets/`** — Single-use pairing secret records are stored as JSON files here. Only the bcrypt hash of the plaintext secret is ever written; the plaintext is printed once to stdout and then discarded. This directory is shared between the running server and the `notx server pairing add-secret` CLI command.

### Filename Sanitisation

URNs are not used verbatim in filenames because colons are illegal or problematic on several filesystems. The URN `urn:notx:note:abc-123` is sanitised to `urn_notx_note_abc-123` (colons replaced with underscores) before constructing any file path. This transform is applied consistently everywhere in the provider.

### The Three Storage Artefacts

Each note produces exactly three artefacts. They are not interchangeable — each has a distinct role.

#### 1. `.notx` stub (`notes/<name>.notx`)

Written once at note creation by `writeNotxStub()`. Never updated after that. It is a human-readable plaintext file whose header lines follow the standard notx comment format (see `NOTX_FORMAT.md`):

```
# notx/1.0
# note_urn:      urn:notx:note:<id>
# note_type:     normal
# name:          my-note
# created_at:    2025-01-01T00:00:00Z
# head_sequence: 0
```

The stub is a static creation-time anchor. Events are **not** written back into it after creation. Its value is human portability: you can open the `notes/` directory and read which notes exist without querying the index or parsing JSON.

#### 2. `.meta.json` sidecar (`notes/<name>.meta.json`)

A mutable JSON file updated on every write operation: `Create`, `Update`, `AppendEvent`, and `Delete`. It mirrors the current state of the note's header fields:

```json
{
  "urn": "urn:notx:note:<id>",
  "name": "my-note",
  "note_type": "normal",
  "project_urn": "urn:notx:proj:<id>",
  "folder_urn": null,
  "deleted": false,
  "created_at": "2025-01-01T00:00:00Z",
  "updated_at": "2025-01-02T12:00:00Z",
  "head_sequence": 7
}
```

Read by `readMeta()`. This avoids re-parsing the `.notx` file for fast header reads on `Get` and list operations.

#### 3. `.jsonl` event journal (`events/<name>.jsonl`)

An append-only newline-delimited JSON file. One line per event. Written by `appendToJournal()`. Each line deserialises to a `journaledEvent` struct:

```json
{
  "urn": "urn:notx:event:<id>",
  "note_urn": "urn:notx:note:<id>",
  "sequence": 1,
  "author_urn": "urn:notx:usr:<id>",
  "created_at": "2025-01-01T00:00:00Z",
  "entries": [{ "ln": 1, "op": "insert", "c": "Hello, world" }]
}
```

For secure notes, the `entries` array is replaced with an `encrypted` field containing ciphertext. The server never sees plaintext for secure notes — only the ciphertext payload is stored.

The `.jsonl` journal is the authoritative event stream. `Get()` replays it from line 1 to materialise the current document state.

### Badger Index (`internal/repo/index/index.go`)

A Badger v4 embedded key-value store in `<dataDir>/index/`. It is derived from the `.meta.json` sidecars and event journals and is used exclusively for list and search operations. It can be rebuilt from the file artefacts if corrupted.

**Key schema** (all keys are plain `[]byte`):

| Key pattern        | Value                       | Purpose                         |
| ------------------ | --------------------------- | ------------------------------- |
| `note:<urn>`       | JSON-encoded `IndexEntry`   | Note metadata for list/search   |
| `name:<urn>`       | Note name string            | Name lookup by URN              |
| `search:<t>:<urn>` | Empty                       | Inverted full-text search index |
| `proj:<urn>`       | JSON-encoded `ProjectEntry` | Project metadata                |
| `folder:<urn>`     | JSON-encoded `FolderEntry`  | Folder metadata                 |
| `usr:<urn>`        | JSON-encoded `UserEntry`    | User metadata                   |
| `device:<urn>`     | JSON-encoded `DeviceEntry`  | Registered device metadata      |
| `server:<urn>`     | JSON-encoded `ServerEntry`  | Paired server record + cert     |

**Tokenisation** (applied at write time to note names and content):

1. Lowercase all input.
2. Strip all non-alphanumeric characters.
3. Split on whitespace boundaries.
4. Discard tokens shorter than 2 characters.
5. Deduplicate.

Each surviving token produces one `search:<token>:<urn>` key. A search query tokenises the `q` parameter by the same rules and intersects the resulting key sets.

**Secure note search isolation**: Secure notes are **never** tokenised for the search index. Only their `IndexEntry` metadata (`note:<urn>` key) is written. A search query will never return a secure note, regardless of the query string.

### Artefact Roles — Summary

| Artefact         | Mutable | Source of truth for              |
| ---------------- | ------- | -------------------------------- |
| `.notx` stub     | No      | Human-readable creation record   |
| `.meta.json`     | Yes     | Live header (name, flags, seq)   |
| `.jsonl` journal | Yes     | Complete event history, content  |
| Badger index     | Yes     | List, search, and server records |

None of these are redundant. Removing any one of them breaks a distinct code path.

---

## HTTP/JSON API

Implemented in `internal/server/http/handler.go`. Listens on `Host:HTTPPort` (default `:4060`).

### Routes

| Method   | Path                     | Description                            |
| -------- | ------------------------ | -------------------------------------- |
| `GET`    | `/v1/notes`              | List notes (paginated)                 |
| `POST`   | `/v1/notes`              | Create a note                          |
| `GET`    | `/v1/notes/{urn}`        | Get a note by URN                      |
| `PUT`    | `/v1/notes/{urn}`        | Update a note                          |
| `DELETE` | `/v1/notes/{urn}`        | Delete a note (soft-delete)            |
| `POST`   | `/v1/events`             | Append an event to a note              |
| `GET`    | `/v1/notes/{urn}/events` | Stream a note's event history          |
| `GET`    | `/v1/search?q=...`       | Full-text search across notes          |
| `GET`    | `/healthz`               | Liveness probe → `{"status":"ok"}`     |
| `GET`    | `/readyz`                | Readiness probe → `{"status":"ready"}` |

All endpoints set and expect `Content-Type: application/json`.

`GET /v1/notes/{urn}/events` returns events as chunked JSON (SSE-style streaming). The connection remains open and each event is flushed as a newline-delimited JSON object as it becomes available.

### Error Format

All error responses use a consistent envelope:

```json
{ "error": "note not found" }
```

HTTP status codes follow standard semantics: `400` for malformed input, `404` for missing resources, `409` for conflicts, `500` for internal errors.

### Middleware

`withMiddleware` wraps all routes with two behaviours:

1. **Structured request logging** — emits method, path, response status, and elapsed duration at `INFO` level.
2. **Panic recovery** — catches any `panic` in a handler, logs the stack trace, and returns a `500` response. The server process does not crash.

---

## gRPC API

Implemented in `internal/server/grpc/` (`server.go` + `service.go`). Listens on `Host:GRPCPort` (default `:50051`).

Proto definition: `internal/server/proto/notx.proto` (package `notx.v1`).

### Services

**`NoteService`**:

| RPC               | Type          | Description                          |
| ----------------- | ------------- | ------------------------------------ |
| `GetNote`         | Unary         | Fetch a single note by URN           |
| `ListNotes`       | Unary         | Paginated list with optional filters |
| `CreateNote`      | Unary         | Create a new note                    |
| `DeleteNote`      | Unary         | Soft-delete a note                   |
| `AppendEvent`     | Unary         | Append an event to a note            |
| `StreamEvents`    | Server-stream | Stream the event history for a note  |
| `SearchNotes`     | Unary         | Full-text search                     |
| `ShareSecureNote` | Unary         | Share a secure note with a device    |

**`DeviceService`**:

| RPC                  | Type  | Description                              |
| -------------------- | ----- | ---------------------------------------- |
| `RegisterDevice`     | Unary | Register a new device and its public key |
| `GetDevicePublicKey` | Unary | Fetch a registered device's public key   |
| `ListDevices`        | Unary | List all registered devices              |
| `RevokeDevice`       | Unary | Revoke a device's registration           |
| `InitiatePairing`    | Unary | Begin the browser pairing handshake      |
| `CompletePairing`    | Unary | Complete the browser pairing handshake   |

**`ServerPairingService`** (available on both listeners when `--pairing` is enabled):

| RPC                | Listener       | Auth             | Description                                       |
| ------------------ | -------------- | ---------------- | ------------------------------------------------- |
| `RegisterServer`   | Bootstrap only | Pairing secret   | Initial registration — issues mTLS client cert    |
| `RenewCertificate` | Primary only   | mTLS client cert | Renew an expiring cert without a secret           |
| `GetCACertificate` | Both           | None (public)    | Fetch the authority CA cert (trust anchor)        |
| `ListServers`      | Primary only   | mTLS client cert | List all registered peer servers                  |
| `RevokeServer`     | Primary only   | mTLS client cert | Hard-revoke a server (immediate handshake reject) |

### Server Reflection

Server reflection is enabled unconditionally. Any gRPC client that supports the reflection protocol — including `grpcurl` — can introspect the available services and methods without a pre-compiled proto file:

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 notx.v1.NoteService/ListNotes
```

### Transport Security

TLS configuration is built by `buildTransportCredentials()` based on which `Config` fields are populated:

| `TLSCertFile` | `TLSKeyFile` | `TLSCAFile` | Mode                       |
| ------------- | ------------ | ----------- | -------------------------- |
| empty         | empty        | —           | Plaintext (dev only)       |
| set           | set          | empty       | TLS 1.3 (server auth only) |
| set           | set          | set         | mTLS (mutual auth)         |

mTLS is activated when `TLSCAFile` is non-empty. The CA cert is used to verify client certificates. This is the recommended configuration for production deployments.

The same cert/key pair governs the HTTP server when TLS is enabled on that layer.

#### Dual-Listener Architecture (Pairing)

When `--pairing` is enabled, a **second gRPC listener** starts on the bootstrap port (default `:50052`). The two listeners have different TLS requirements:

| Listener  | Port    | TLS mode                                    | Exposes                                    |
| --------- | ------- | ------------------------------------------- | ------------------------------------------ |
| Primary   | `50051` | mTLS (client cert required when configured) | All services                               |
| Bootstrap | `50052` | TLS only (no client cert)                   | `RegisterServer` + `GetCACertificate` only |

The bootstrap port is open only during initial pairing. It can be firewalled once all servers are paired — existing connected servers are unaffected. The port is configurable via `--pairing-port`.

### Interceptors and Keepalive

Two interceptors are applied to all methods (both unary and streaming):

1. **Logging interceptor** — records method name, status code, and duration. The `pairing_secret` field is **always redacted** for `RegisterServer` calls and is never written to any log.
2. **Panic recovery interceptor** — catches panics, logs the stack, and returns a `codes.Internal` status. The server process does not crash.

Keepalive settings:

| Parameter    | Value |
| ------------ | ----- |
| Idle timeout | 5m    |
| Max age      | 30m   |

---

## Lifecycle and Graceful Shutdown

`server.Run()` starts both servers concurrently and then blocks waiting for `SIGINT` or `SIGTERM`.

On signal receipt:

1. A context with a deadline of `Config.ShutdownTimeout` (default `30s`) is created.
2. Both shutdown sequences are run **concurrently**:
   - **HTTP**: `http.Server.Shutdown(ctx)` — stops accepting new connections and waits for in-flight requests to complete within the deadline.
   - **gRPC**: `GracefulStop()` — stops accepting new RPCs and waits for in-flight RPCs to complete. If the shutdown deadline is exceeded before all RPCs drain, `Stop()` is called to force-terminate.
3. When pairing is enabled, the bootstrap listener is also stopped gracefully in the same pass.
4. Once all servers have returned, `Run()` returns and the process exits cleanly.

No in-flight request or RPC is dropped as long as it completes within `ShutdownTimeout`.

---

## Running the Server

```bash
# First run — config is created automatically, then the server starts
notx server

# Start with existing config (reads ~/.notx/config.json)
notx server

# Override specific values for one run
notx server --http-port 8080 --grpc=false --data-dir /var/notx

# Auto-approve all newly registered devices (useful for development)
notx server --device-auto-approve

# gRPC only, with TLS
notx server --http=false --tls-cert server.crt --tls-key server.key

# mTLS (requires client cert signed by the CA)
notx server --tls-cert server.crt --tls-key server.key --tls-ca ca.crt
```

To persist configuration across runs, use `notx config` rather than passing flags each time.

### Typical local setup (two terminals)

```bash
# Terminal 1 — API + gRPC server
notx server

# Terminal 2 — Admin UI (reads api_addr from ~/.notx/config.json)
notx admin
```

The admin UI at `http://localhost:9090` proxies all `/v1/*` calls to the API server at `http://localhost:4060`. No extra configuration is needed when both processes run on the same machine with default settings.

### Admin device

Every server startup ensures the built-in admin device exists and is approved. To make requests to data endpoints from scripts or the admin UI, pass the following header:

```
X-Device-ID: urn:notx:device:00000000-0000-0000-0000-000000000000
```

This device bypasses the normal approval/revocation checks and is restored to `approved` automatically on each restart, even if it was manually revoked in the database.

### Server Pairing

```bash
# Start an authority server (generates CA on first run, opens bootstrap port 50052)
notx server --pairing

# Authority with a custom bootstrap port and 7-day cert TTL
notx server --pairing --pairing-port 50053 --pairing-cert-ttl 168h

# Generate a pairing secret for a joining server
notx server pairing add-secret --label "datacenter-b" --data-dir /var/notx/data

# Start a joining server (registers on first startup, then connects via mTLS)
notx server --peer-authority grpc.authority.example.com:50052 \
            --peer-secret "NTXP-ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z" \
            --peer-cert-dir /var/notx/certs

# List all paired servers
notx server pairing list-servers

# Revoke a server
notx server pairing revoke urn:notx:srv:01932c4f-89ab-7def-8012-3456789abcde
```

See the [Server Pairing](#server-pairing) section below for a full description of the lifecycle and security model.

---

## Disabling a Layer

Both protocol layers are enabled by default. Either can be disabled:

```bash
# HTTP only
notx server --grpc=false

# gRPC only
notx server --http=false
```

Disabling a layer means its listener is never opened. The storage backend is initialised regardless — it is shared by both layers.

---

## Server Pairing

Server pairing enables two or more notx server instances to trust each other for data-replication, federation, or gateway scenarios. Trust is established via a lightweight PKI: the **authority server** owns a CA, issues short-lived mTLS client certificates to **joining servers**, and can hard-revoke any peer at any time.

### Authority Mode vs Joining Mode

**Authority mode** is activated by passing `--pairing` at startup. The server:

- Auto-generates an EC P-256 CA key-pair on first startup and stores it under `<data-dir>/ca/` (see [Directory Layout](#directory-layout)).
- Opens a second gRPC listener on the bootstrap port (default `50052`) that accepts TLS without a client certificate, exposing only `RegisterServer` and `GetCACertificate`.
- Maintains an in-memory deny-set of revoked certificate serials, checked at every TLS handshake on the primary listener.
- Exposes all `ServerPairingService` RPCs to admin clients on the primary port (`50051`) over mTLS.

**Joining mode** is activated by passing `--peer-authority` (and `--peer-secret` on first registration). The server:

- On startup, checks `<cert-dir>/server.crt`; if valid and not near expiry it loads the cert and connects to the authority over mTLS on port `50051`.
- If no cert exists (or it has expired), dials the authority's bootstrap port (`50052`), calls `RegisterServer` with the pairing secret, writes the returned cert and CA cert to disk, then zeros the secret from memory.
- Runs a background renewal goroutine that checks expiry every `RenewalCheckInterval` (default `6h`) and renews automatically when less than `RenewalThreshold` (default `7 days`) remain.

### Authority CA

The CA is a self-signed EC P-256 key-pair stored in `<data-dir>/ca/`:

| File     | Permissions | Contents                         |
| -------- | ----------- | -------------------------------- |
| `ca.key` | `0600`      | EC P-256 private key (PEM)       |
| `ca.crt` | `0644`      | Self-signed CA certificate (PEM) |

The `ca.key` is **never** transmitted over any network interface. The `ca.crt` is embedded in every issued certificate chain and is available to any caller (including unauthenticated ones) via `GetCACertificate`. Joining servers store a copy at `<cert-dir>/ca.crt` and use it as their only trust anchor when connecting to the authority.

### Pairing Secret

An admin generates a pairing secret on the authority with `notx server pairing add-secret`. The secret:

- Has the format `NTXP-ABCDE-FGHIJ-KLMNO-PQRST-UVWXY` — a `NTXP-` prefix ("notx pairing") followed by 26 base32 characters (130 bits of entropy). The prefix makes secrets visually distinct and enables secret-scanning tools (GitHub, GitLab, etc.) to detect accidental commits.
- Is **single-use**: once consumed by a successful `RegisterServer` call, any subsequent call with the same secret is rejected with `codes.Unauthenticated`.
- Has a configurable **TTL** (default `15m`). An expired secret is refused even if unused.
- Is **stored as a bcrypt hash** in `<data-dir>/pairing_secrets/<id>.json`. The plaintext is printed once to stdout and never written to disk.
- Can carry an optional **label** (e.g., `"datacenter-b"`) used in audit log entries to make registration events meaningful.

### `RegisterServer` Protocol Flow

1. **Admin** generates a pairing secret on the authority and distributes it out-of-band to the joining server's operator (secure message, secrets manager, CI variable, etc.).
2. **Joining server** generates an EC P-256 key-pair and PKCS#10 CSR locally. The private key never leaves the joining server.
3. **Joining server** dials the authority's bootstrap port (`50052`) over TLS (no client cert required) and calls `RegisterServer`, sending its self-assigned `urn:notx:srv:<id>` URN, the CSR, the plaintext secret, a human name, and its advertised gRPC endpoint.
4. **Authority** bcrypt-compares the secret against all stored hashes, checks it is unexpired and unused, then atomically marks it consumed.
5. **Authority** signs the CSR with its CA, producing an X.509 client certificate (`ExtKeyUsage: ClientAuth`, EC P-256, 30-day TTL by default, `CN = <server_urn>`).
6. **Authority** stores a server record (URN, name, endpoint, cert PEM, cert serial, expiry) in the Badger index under the `server:` prefix and returns the signed cert and CA cert to the joining server.
7. **Joining server** writes the cert and CA cert to disk, configures its gRPC client to present the cert on all future calls to the authority, and zeros the secret from memory.
8. **All subsequent calls** from the joining server to the authority use mTLS on port `50051` — no secret is ever needed again.

### Hard Revocation

Revocation is enforced at the TLS handshake layer, not via an RPC interceptor. When `RevokeServer` is called:

1. The server record is marked `revoked = true` in the Badger index.
2. The certificate's serial number is added to an **in-memory deny-set**.
3. A `tls.Config.VerifyPeerCertificate` callback on the primary listener checks every connecting client's certificate serial against the deny-set. A revoked server's connection is torn down during the TLS handshake — before any RPC handler is invoked — and receives a TLS alert.

The deny-set is rebuilt from the repository on every server startup. It is kept in memory (one entry per revoked server) so the lookup is O(1) and adds no measurable latency to the handshake.

### Automatic Certificate Renewal

The joining server runs a background goroutine that fires on `RenewalCheckInterval` (default `6h`). When the remaining cert validity drops below `RenewalThreshold` (default `7 days`), it:

1. Generates a new EC P-256 key-pair and PKCS#10 CSR (key rotation is recommended but optional).
2. Calls `RenewCertificate` over the existing mTLS channel on port `50051`, presenting its current cert as the authenticator. No pairing secret is required.
3. Writes the new cert to `server.crt.tmp`, then atomically renames it to `server.crt`.
4. Signals the gRPC client to reload credentials from disk. The existing connection continues uninterrupted; the new cert takes effect on the next dial.

### Audit Log Events

Every pairing-related event emits a structured `slog` line at `INFO` level. The `pairing_secret` field is **never** included in any log entry.

| Event                                 | Key fields                                                                 |
| ------------------------------------- | -------------------------------------------------------------------------- |
| Secret generated                      | `event=pairing_secret_created label= id= expires_at=`                      |
| Secret consumed (success)             | `event=pairing_secret_consumed server_urn= label= remote_addr=`            |
| Secret rejected                       | `event=pairing_secret_rejected reason=(wrong\|expired\|used) remote_addr=` |
| Certificate issued                    | `event=server_cert_issued server_urn= expires_at=`                         |
| Certificate renewed                   | `event=server_cert_renewed server_urn= expires_at=`                        |
| Server revoked                        | `event=server_revoked server_urn= revoked_by=`                             |
| TLS handshake rejected (revoked cert) | `event=server_handshake_rejected server_urn= serial=`                      |
| Renewal triggered (joining side)      | `event=cert_renewal_triggered server_urn= days_remaining=`                 |
| Renewal succeeded (joining side)      | `event=cert_renewal_success server_urn= new_expires_at=`                   |
| Renewal failed (joining side)         | `event=cert_renewal_failed server_urn= error=`                             |

---

## Port Reference

| Layer            | Default Port | Config Field            |
| ---------------- | ------------ | ----------------------- |
| HTTP             | `4060`       | `HTTPPort`              |
| gRPC (primary)   | `50051`      | `GRPCPort`              |
| gRPC (bootstrap) | `50052`      | `Pairing.BootstrapPort` |

All ports are configurable independently. The bind address for all listeners is controlled by `Host` (default: all interfaces). The bootstrap listener is only started when `Pairing.Enabled` is `true`.
