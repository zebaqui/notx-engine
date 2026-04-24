# Feather Migration — Delta from Current Engine

This document lists only the concrete changes required to transform the current
notx-engine into the Feather engine. It is organized as actionable sections:
Remove, Simplify, Add, and Rename/Restructure. No background context — just the
delta.

---

## 1. Remove

These packages, files, and concepts are deleted entirely. Nothing in Feather
depends on them.

### 1.1 Server pairing & mTLS CA

| Item                                                      | Reason                                                                                                         |
| --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `pairing/` (entire package)                               | Server-to-server pairing, mTLS CA, and pairing hub are cloud concerns. Feather has no concept of peer engines. |
| `ca/` (entire directory)                                  | CA key material and certificate infrastructure used exclusively by pairing.                                    |
| `core/server.go`                                          | `core.Server` type exists only to represent paired peer servers.                                               |
| `repo/pairing.go`                                         | `ServerRepository` and `PairingSecretStore` interfaces back the pairing flow.                                  |
| `internal/pairing/`                                       | Internal implementation of the pairing handshake.                                                              |
| `proto/server.proto` + generated `.pb.go` / `_grpc.pb.go` | gRPC types for server pairing.                                                                                 |
| `proto/relay.proto` + generated `.pb.go` / `_grpc.pb.go`  | gRPC relay types used exclusively by pairing infrastructure.                                                   |

### 1.2 Relay

| Item                                  | Reason                                                                                               |
| ------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `http/relay.go`                       | HTTP relay handler — forwards encrypted events between paired servers. No paired servers in Feather. |
| `internal/relay/`                     | Internal relay implementation.                                                                       |
| `proto/relay.proto` + generated files | (covered above)                                                                                      |

### 1.3 Sync subsystem

| Item                                 | Reason                                                                                           |
| ------------------------------------ | ------------------------------------------------------------------------------------------------ |
| `sync/` (entire package)             | Public facade for gRPC `SyncStream`, `StreamRegistry`, and cloud↔local sync. Cloud-only concern. |
| `internal/sync/`                     | Concrete `StreamRegistry` and `SyncServer` implementations.                                      |
| `internal/server/grpc/`              | gRPC server that hosts `SyncStream` on the mTLS listener.                                        |
| `internal/grpcclient/`               | gRPC client used by the local engine to call back into the cloud sync service.                   |
| `repo/sync.go`                       | `SyncRepository` and `SyncLogEntry` — sync history table backed by cloud infrastructure.         |
| `proto/sync.proto` + generated files | gRPC `SyncStream` bidirectional stream definitions.                                              |
| `http/sync.go`                       | HTTP handlers for `/v1/sync/*` — status, log, pending, trigger, stream.                          |

### 1.4 Device management

| Item                                                                                                                                | Reason                                                                                         |
| ----------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `core/` — `ObjectTypeDevice` URN type in `urn.go`                                                                                   | Device URNs are a cloud identity concept; the local engine does not register or track devices. |
| `repo/repo.go` — `DeviceRepository`, `DeviceListOptions`, `DeviceListResult`                                                        | No device storage in Feather.                                                                  |
| `http/device.go`                                                                                                                    | All device registration, approval, status-poll, and revocation HTTP handlers.                  |
| `http/middleware.go` — `DeviceAuthMiddleware`, `withDeviceAuthMiddleware`, `withDeviceExistsAuth`, `withDeviceExistsAuthMiddleware` | All auth middleware that validates `X-Device-ID` headers. Localhost needs no auth.             |
| `proto/device.proto` + generated files                                                                                              | gRPC device types.                                                                             |

### 1.5 User management

| Item                                                                   | Reason                                                                             |
| ---------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| `core/user.go`                                                         | `core.User` type. Multi-user identity is a cloud concern; Feather is single-owner. |
| `repo/repo.go` — `UserRepository`, `UserListOptions`, `UserListResult` | No user storage in Feather.                                                        |
| `http/user.go`                                                         | HTTP handlers for `/v1/users` and `/v1/users/{urn}`.                               |
| `proto/user.proto` + generated files                                   | gRPC user types.                                                                   |

### 1.6 Cloud / admin infrastructure

| Item                                                                                                                             | Reason                                                                                                                            |
| -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `internal/cloud/`                                                                                                                | Cloud-specific adapter code (Vault credential fetching, cloud API client, etc.).                                                  |
| `internal/admin/`                                                                                                                | Admin HTTP server used for internal cloud operations, not relevant to local engine.                                               |
| `internal/tui/`                                                                                                                  | TUI elements tied to cloud admin flows. Remove if only used for cloud admin; keep if reused by `notxctl`. Evaluate per-file.      |
| `http/handler.go` — `secretStore`, `pairing`, `relaySvc`, `syncRepo`, `syncBus`, `deviceSvc`, `deviceAdminSvc`, `userSvc` fields | All fields that back removed subsystems. `Handler` struct slims down significantly.                                               |
| `http/handler.go` — `buildTLSConfig`                                                                                             | TLS config builder used for mTLS pairing listener. Feather HTTP is plaintext localhost only.                                      |
| `http/pairing.go`                                                                                                                | HTTP handlers for `/v1/servers/*` and `/v1/pairing-secrets`.                                                                      |
| `mobile/`                                                                                                                        | iOS/Swift bridging layer (`Notx.xcframework`). Out of scope for Feather engine; lives in a separate mobile SDK project if needed. |
| `Notx.xcframework`                                                                                                               | (same as above)                                                                                                                   |

---

## 2. Simplify

These packages stay but are stripped of the parts that backed removed subsystems.

### 2.1 `core/urn.go`

- Remove `ObjectTypeDevice` and `ObjectTypeServer` constants.
- Remove any URN validation branches that reference `device` or `srv` object types.
- Keep: `note`, `event`, `usr` (author attribution only — no `UserRepository`), `org`, `proj`, `folder`.

### 2.2 `core/security.go`

- Remove `SyncPolicyExplicitRelay` — the relay-based sync policy for secure
  notes is a cloud concept. In Feather, secure notes are simply never
  auto-synced; the local engine does not need to express _how_ they would be
  relayed.
- Keep: `NoteTypeNormal`, `NoteTypeSecure`, `IsE2EE`, `ServerCanReadContent`,
  `ServerIndexingAllowed`, and `SecurityPolicy` — the E2E encryption
  distinction is a local storage and indexing concern that remains.

### 2.3 `repo/repo.go`

- Remove: `DeviceRepository`, `DeviceListOptions`, `DeviceListResult`.
- Remove: `UserRepository`, `UserListOptions`, `UserListResult`.
- Keep everything else: `NoteRepository`, `ProjectRepository`,
  `LinkRepository`, `ContextRepository`, all list/search option types,
  `IndexEntry`, pagination types.

### 2.4 `http/handler.go`

- Remove all fields that back removed subsystems (see §1.6 above).
- Remove `DeviceAuthMiddleware` and all device/auth middleware variants.
- Remove `buildTLSConfig`.
- In `New(...)`, drop parameters for device, user, pairing, relay, and sync
  services.
- The `Handler` struct reduces to: `noteSvc`, `projSvc`, `folderSvc`,
  `contextSvc`, `linkSvc`, `plugins`, `aiCredStore`, `log`, `mux`, `server`.
- Bind address must be hardcoded / validated to `127.0.0.1` only.

### 2.5 `http/handler.go` — `routes()`

Remove all route registrations for deleted subsystems:

- `/v1/devices` and `/v1/devices/`
- `/v1/users` and `/v1/users/`
- `/v1/servers/*`, `/v1/pairing-secrets`, `/v1/servers/outbound-pair`
- `/v1/sync/*`
- `/v1/notes/receive/` (server-to-server push endpoint)
- The relay routes

Add new route group:

- `GET POST /v1/ai/credentials`
- `DELETE /v1/ai/credentials/{provider}`

Remove `withDeviceAuthMiddleware` wrapper from all remaining routes — they
use the plain `withMiddleware` wrapper (logging, recovery, request-id only).

### 2.6 `snip/plugin.go` — `SnipPlugin` interface

- Remove `RegisterGRPC(s *grpc.Server)` method. Feather has no gRPC server.
  Plugins that need transport beyond HTTP can use HTTP only.
- Remove `google.golang.org/grpc` import from `plugin.go`.

### 2.7 `snip/plugin.go` — `PluginEnv`

- Remove `Namespace string` field. Not meaningful in a single-owner engine.
- Keep: `DB`, `NoteRepo`, `ProjRepo`, `Config`, `Log`.

### 2.8 `proto/` — retained protos

Delete all `.proto` files and generated Go files for removed subsystems (device,
user, server, relay, sync). Retain only:

| Proto file            | Keep?                       |
| --------------------- | --------------------------- |
| `note.proto`          | ✅                          |
| `project.proto`       | ✅                          |
| `folder.proto`        | ✅                          |
| `link.proto`          | ✅                          |
| `context.proto`       | ✅                          |
| `device.proto`        | ❌ Remove                   |
| `user.proto`          | ❌ Remove                   |
| `server.proto`        | ❌ Remove                   |
| `relay.proto`         | ❌ Remove                   |
| `sync.proto`          | ❌ Remove                   |
| `snips/` subdirectory | ✅ (evaluate per-snip-type) |

### 2.9 `cmd/notx` — primary client CLI

`cmd/notx/main.go` currently boots the HTTP server. In Feather this binary has
a dual role depending on the sub-command:

- When invoked without a sub-command (or as a daemon) it starts the engine
  HTTP server as before.
- When invoked with a sub-command (`note`, `snip`, `project`, `search`,
  `export`, `ai`, etc.) it acts as the **primary client**, talking to the
  local HTTP API.

This is the binary end-users and scripts interact with for all everyday
operations. The full command set described in `FEATHER_OVERVIEW.md §5.1` lives
here.

Commands to **remove** from `cmd/notx`:

- Any `device *` sub-commands (registration, approval, revocation)
- Any `user *` sub-commands
- Any `server *` / `pairing *` sub-commands (pairing secrets, server list/revoke)
- Any `sync *` sub-commands

Commands to **add** to `cmd/notx`:

- `notx ai credentials add|list|remove`
- `notx export pack|unpack` (produces/consumes `.gnotx` packages)
- `notx server start|stop|status` (engine lifecycle)

### 2.10 `cmd/notxctl` — gRPC dev/test tool

`cmd/notxctl` is **not** the primary client. It is a low-level developer and
testing tool that speaks directly to the local gRPC server. Its scope narrows
significantly in Feather:

- **Keep**: gRPC-level inspection commands, raw proto send/receive, stream
  monitoring, engine health probes.
- **Add**: `notxctl index rebuild` (admin operation to re-derive the SQLite
  index from `.notx` files on disk — too low-level for the primary CLI).
- **Remove**: any commands that mirrored the HTTP API (those move to `notx`).

`notxctl` is not documented as a user-facing tool. It does not need `--format`
output options or human-friendly help text beyond what is useful for
debugging.

### 2.11 `config/`

- Remove any config fields that reference pairing endpoints, TLS certificates,
  Vault / external secrets manager addresses, relay URLs, or device auth secrets.
- Add: `ai_credentials_path` (path to the encrypted credential file),
  `ai_credentials_key_source` (`"passphrase"` | `"keychain"`).

---

## 3. Add

New capabilities introduced in Feather that have no counterpart in the current
engine.

### 3.1 AI credential store

| New item                     | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `credentials/` (new package) | `Store` type: AES-256-GCM encrypted JSON file. Methods: `Get(provider)`, `Set(provider, creds)`, `Delete(provider)`, `List() []ProviderEntry`. Key derivation via Argon2id. OS keychain integration optional. Decrypted key material is zeroed from memory immediately after use and is never written to disk in plaintext.                                                                                                                                       |
| `http/ai.go` (new file)      | HTTP handlers for `/v1/ai/credentials`. List and mutate which providers are configured. **The handlers never return credential values under any circumstances — not even to localhost callers.** Responses contain only provider names and masked previews (e.g. `sk-...a1b2`). A full breach of the HTTP server yields no key material; accessing plaintext credentials requires direct process-level memory access, which is outside the engine's threat model. |

### 3.2 `.gnotx` export/import

| New item                                              | Description                                                                                                                                                                                               |
| ----------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `export/` (new package)                               | `Pack(notes []core.Note, dest io.Writer) error` and `Unpack(src io.Reader) ([]core.Note, Manifest, error)`. Produces a gzipped tar of `.notx` files + `manifest.json`. The archive extension is `.gnotx`. |
| `cmd/notx` — `export pack` / `export unpack` commands | Primary CLI surface for creating and importing `.gnotx` packages. Lives in `cmd/notx`, not `notxctl`.                                                                                                     |

### 3.3 Index rebuild

| New item                                          | Description                                                                                                                             |
| ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `repo/file` — `RebuildIndex(ctx, sqliteDB) error` | Walks all `.notx` files in the data directory and re-derives the SQLite index from scratch. Needed when the index is lost or corrupted. |
| `cmd/notxctl` — `index rebuild` command           | Admin surface for index rebuild. Lives in `notxctl` because it is a low-level recovery operation, not an everyday user action.          |

### 3.4 Secure note key model

This is a **breaking behavioral change** from the current engine. The current
model stores per-note Content Encryption Keys (CEKs) and wrapped key material
on disk alongside the ciphertext. The Feather model does not.

**Current model (remove):**

- Per-note CEK generated at creation time and stored on disk (wrapped).
- `UpdateEventWrappedKeys` in `NoteRepository` — updates the stored wrapped key
  when a new device is granted access.
- `ReceiveSharedNote` — accepts a note + pre-wrapped keys from another device.
- Any key derivation or key-wrapping logic in the storage backends (`repo/file`,
  `repo/sqlite`).
- Any wrapped key columns in SQLite schemas.

**Feather model (add):**

- Secure notes are written and read with a **caller-supplied key** passed as a
  parameter to the relevant `NoteRepository` methods (or injected via a
  `SecureNoteKey` in the request context).
- The engine stores only ciphertext. It holds no key material, no wrapped keys,
  and no key-derivation seeds on disk or in the index.
- The key is never written to any file, database, or log by the engine. It
  exists only in process memory for the duration of a single read or write
  call, and is zeroed immediately after.
- The `UpdateEventWrappedKeys` method is removed from `NoteRepository`.
- `ReceiveSharedNote` is removed (server-to-server sharing is gone).
- If any existing `.notx` files on disk contain wrapped key material in their
  headers, a one-time migration utility (`notxctl migrate-keys`) should strip
  that material and re-encrypt the event bodies with a user-provided key.

The net effect: **the key and the ciphertext never reside on the same machine
at rest.** The user supplies the key at runtime; without it the content is
unreadable by any process, including the engine itself.

---

## 4. Rename / Restructure

| Current location        | New location                         | Reason                                                                                                                                                                                                                                                                                             |
| ----------------------- | ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `http/from_repos.go`    | `http/from_repos.go` (keep, no move) | No change needed; still maps repo types to HTTP response types.                                                                                                                                                                                                                                    |
| `cmd/notx/main.go`      | Expand in-place                      | Gains the full primary-client CLI command set. No need to move; entry-point binary stays the same.                                                                                                                                                                                                 |
| `cmd/notxctl/main.go`   | Keep, narrow scope                   | Remains the gRPC dev/test tool. Remove any HTTP-client commands that move to `cmd/notx`.                                                                                                                                                                                                           |
| `repo/pairing.go`       | Deleted (see §1.1)                   | —                                                                                                                                                                                                                                                                                                  |
| `repo/sync.go`          | Deleted (see §1.3)                   | —                                                                                                                                                                                                                                                                                                  |
| `core/server.go`        | Deleted (see §1.1)                   | —                                                                                                                                                                                                                                                                                                  |
| `core/user.go`          | `core/author.go` (optional rename)   | If author attribution (URN strings on events) is retained without the full `User` record, a minimal `AuthorURN` type alias or helper may be cleaner than keeping the full `User` struct. Evaluate whether `User` is still referenced by `repo.NoteRepository` auth flows; if not, delete outright. |
| `sync/` (public facade) | Deleted (see §1.3)                   | The public `sync` package was an intentional re-export boundary for notx-cloud. Cloud embeds Feather via the `repo` interfaces instead.                                                                                                                                                            |

---

_Document version: Feather 1.0 — migration delta_
