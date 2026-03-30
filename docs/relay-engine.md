# Relay Execution Engine

The notx relay engine is a **gRPC-first HTTP relay** — all execution logic
lives in the gRPC layer and is exposed via a thin HTTP adapter.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        HTTP Client                          │
│                  POST /v1/relay/execute                     │
│                POST /v1/relay/execute-flow                  │
└───────────────────────────┬─────────────────────────────────┘
                            │ JSON
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                  HTTP Adapter Layer                         │
│              (internal/server/http/relay_handler.go)        │
│                                                             │
│  • Decode JSON body                                         │
│  • Extract X-Device-ID header                               │
│  • Map to gRPC proto messages                               │
│  • Forward to RelayServiceServer                            │
│  • Translate response back to JSON                          │
│                                                             │
│  NO business logic lives here.                              │
└───────────────────────────┬─────────────────────────────────┘
                            │ gRPC (in-process)
                            ▼
┌─────────────────────────────────────────────────────────────┐
│              RelayServiceServer (source of truth)           │
│         (internal/server/grpc/relay_service.go)             │
│                                                             │
│  1. Device validation (exists, not revoked)                 │
│  2. Variable interpolation  {{var}} → value                 │
│  3. Policy enforcement (allowlist, blocked IPs, SSRF)       │
│  4. HTTP execution with timeout + size limits               │
│  5. JSON extraction for flows                               │
│  6. Event emission (relay.executed)                         │
└───────────────────────────┬─────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                  Relay Package                              │
│                  (internal/relay/)                          │
│                                                             │
│  policy.go      — Security constraints + SSRF protection    │
│  executor.go    — net/http client with policy enforcement   │
│  interpolate.go — {{variable}} placeholder resolution       │
│  extract.go     — JSON path + header extraction             │
│  context.go     — Mutable execution state for flows         │
└─────────────────────────────────────────────────────────────┘
```

## API

### `POST /v1/relay/execute`

Execute a single HTTP request.

**Request body:**
```json
{
  "variables": { "host": "api.example.com" },
  "request": {
    "method": "POST",
    "url": "https://{{host}}/v1/token",
    "headers": { "Content-Type": "application/json" },
    "body": "{\"client_id\": \"abc\"}"
  }
}
```

**Response body:**
```json
{
  "response": {
    "status": 200,
    "headers": { "content-type": "application/json" },
    "body": "{\"access_token\":\"tok-xyz\"}",
    "duration_ms": 142
  }
}
```

---

### `POST /v1/relay/execute-flow`

Execute a multi-step pipeline. Each step can extract values from its response
and make them available as variables for subsequent steps.

**Request body:**
```json
{
  "variables": { "base": "https://api.example.com" },
  "steps": [
    {
      "id": "login",
      "request": {
        "method": "POST",
        "url": "{{base}}/auth/token",
        "headers": { "Content-Type": "application/json" },
        "body": "{\"username\":\"alice\",\"password\":\"secret\"}"
      },
      "extract": {
        "token": "response.body.access_token"
      }
    },
    {
      "id": "get-profile",
      "request": {
        "method": "GET",
        "url": "{{base}}/v1/me",
        "headers": { "Authorization": "Bearer {{token}}" }
      }
    }
  ]
}
```

---

## Variable Interpolation

Use `{{varName}}` (double curly braces) anywhere in:
- URL
- Header values
- Request body

Variables are resolved from the `variables` map before each step executes.
After a step with `extract` rules, the extracted values are merged into the
variable set and available to all subsequent steps.

---

## Extraction Paths

| Path | Description |
|------|-------------|
| `response.status` | HTTP status code as a string |
| `response.headers.<name>` | Response header value (lowercase name) |
| `response.body.<key>` | Top-level JSON field |
| `response.body.<a>.<b>.<c>` | Nested JSON field (dot-separated) |

---

## Security Model

All security enforcement happens inside the gRPC layer:

| Check | Rule |
|-------|------|
| Device identity | Must be registered and not revoked |
| Scheme | Only `http` and `https` allowed |
| Host allowlist | Optional per-deployment allowlist |
| Blocked IPs | `169.254.0.0/16`, `fc00::/7`, `fe80::/10`, CGNAT |
| Private ranges | RFC-1918 blocked unless `AllowLocalhost=true` |
| Request timeout | 10 seconds default (configurable) |
| Request body | 1 MiB max (configurable) |
| Response body | 4 MiB max (configurable) |
| Redirects | 5 max (configurable) |
| Steps per flow | 20 max (configurable) |
| TLS verification | Always enabled (cannot be disabled) |

---

## Error Codes

| gRPC Code | HTTP Status | Cause |
|-----------|-------------|-------|
| `INVALID_ARGUMENT` | 400 | Malformed request, bad method, missing fields |
| `PERMISSION_DENIED` | 403 | Blocked host, unknown/revoked device |
| `DEADLINE_EXCEEDED` | 504 | Request timeout |
| `ABORTED` | 502 | Flow step execution failure |
| `INTERNAL` | 500 | Unexpected server error |

---

## Configuration

Add to your server config:

```go
cfg.Relay = config.RelayPolicyConfig{
    AllowedHosts:         []string{"api.example.com"},
    AllowLocalhost:       false, // true for dev only
    MaxSteps:             20,
    MaxRequestBodyBytes:  1 << 20,  // 1 MiB
    MaxResponseBodyBytes: 4 << 20,  // 4 MiB
    RequestTimeoutSecs:   10,
    MaxRedirects:         5,
}
