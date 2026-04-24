# Feather notx-engine — Overview

> **Feather** is the local, single-owner edition of the notx engine. It is a
> deliberate reduction: less surface area, fewer moving parts, no trust
> negotiation, and no network dependencies. Everything it does, it does on your
> machine, for you, privately.

---

## Table of Contents

1. [Philosophy](#1-philosophy)
2. [Storage Model](#2-storage-model)
3. [Note Security Model](#3-note-security-model)
4. [HTTP API](#4-http-api)
5. [CLI — notx & notxctl](#5-cli--notx--notxctl)
6. [AI Credential Store](#6-ai-credential-store)
7. [Snip Plugin System](#7-snip-plugin-system)
8. [Embeddable Library Interface](#8-embeddable-library-interface)
9. [What the Engine Deliberately Does Not Do](#9-what-the-engine-deliberately-does-not-do)

---

## 1. Philosophy

Feather is built around four commitments:

| Commitment            | What it means in practice                                                                                                                                                                |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Local-first**       | All data lives on disk or in a local SQLite database. No network call is required to read, write, search, or export your notes.                                                          |
| **Privacy-first**     | The engine never phones home. AI credentials are stored in an AES-256-GCM encrypted file, not in a cloud vault. Secure notes are end-to-end encrypted and never touch plaintext storage. |
| **No trust required** | Because the engine runs on localhost and is accessed only by the owner, there is no authentication layer. You already own the machine.                                                   |
| **Embeddable**        | The engine is a plain Go library. Any Go program — including the notx cloud layer — can import it and swap backends via the provider interface.                                          |

The design philosophy is that **complexity belongs at the cloud layer**. Device
management, user identity, server-to-server sync, and multi-tenancy are all
cloud concerns. The local engine has no opinion on any of them.

---

## 2. Storage Model

Feather supports two storage modes. The mode is selected at startup and affects
only where note content lives; the index layer is always SQLite.

### 2.1 Hybrid mode (default)

```notx-engine/docs/FEATHER_OVERVIEW.md#L1-1
(diagram placeholder — not a real code block)
```

- **Note content** is stored as `.notx` files on disk, one file per note.
  The file format is the existing notx append-log format (header + event log).
- **Index, full-text search, links, and context graph** are stored in a local
  SQLite database alongside the files. The index is derived from content and
  can be rebuilt from the files at any time.
- **Secure note content** is stored as `.notx` files with encrypted event
  bodies. The index entry for a secure note contains only its URN, type, and
  timestamps — never any content.

### 2.2 Full-SQLite mode

All note content and index data are stored in a single SQLite database. This
mode is appropriate when the engine is embedded in a larger application that
already manages a database file, or when filesystem layout is inconvenient (e.g.
mobile or WASM targets).

### 2.3 Export format — `.gnotx` packages

The CLI can export any selection of notes as a `.gnotx` package: a gzipped tar
archive containing the raw `.notx` files plus a manifest. Packages are
self-contained and can be imported into any Feather instance.

```
my-export.gnotx
├── manifest.json       # metadata: export date, note count, engine version
├── notes/
│   ├── <urn>.notx
│   └── ...
```

### 2.4 Repository interfaces

All storage access goes through the `repo` package interfaces. The active
backend is injected at startup; nothing in the core or HTTP layers imports a
concrete backend directly.

| Interface           | Responsibility                                 |
| ------------------- | ---------------------------------------------- |
| `NoteRepository`    | CRUD, event append, full-text search           |
| `ProjectRepository` | Projects and folders                           |
| `LinkRepository`    | Anchors, backlinks, external links             |
| `ContextRepository` | Bursts, candidates, inferences (context graph) |

---

## 3. Note Security Model

Every note has an immutable `NoteType` assigned at creation time. There are
exactly two types:

| Property                        | `NoteTypeNormal`                     | `NoteTypeSecure`                     |
| ------------------------------- | ------------------------------------ | ------------------------------------ |
| Storage                         | Plaintext `.notx` file or SQLite row | Encrypted `.notx` file or SQLite row |
| Full-text indexed               | ✅ Yes                               | ❌ Never                             |
| Content readable by engine      | ✅ Yes                               | ❌ No — ciphertext only              |
| Encryption                      | TLS in transit only                  | AES-256-GCM, E2E                     |
| Sync (when cloud layer present) | Automatic                            | Explicit, relay-only                 |

**The type cannot be changed after creation.** This is enforced at the
repository layer (`ErrNoteTypeImmutable`).

Secure notes are encrypted with a **user-provided key** that is never stored
on the machine. The engine receives the key only at the moment of a read or
write operation — supplied by the caller (e.g. prompted by the CLI or passed
by the embedding application). The engine stores only ciphertext; it holds no
copy of the key, no wrapped key material, and no key-derivation seed on disk.
If the user does not provide the key, the content cannot be read or written —
not by the engine, not by any process on the machine, not by anyone with
filesystem access. The key and the ciphertext must never reside on the same
machine at rest.

The `SecurityPolicy` struct (returned by `core.NoteSecurityPolicy`) is the
single source of truth for what each type may and may not do. No call site
negotiates or overrides it.

---

## 4. HTTP API

The HTTP server listens on `127.0.0.1` only. There is no authentication
middleware — if you can reach the socket, you are the owner.

### 4.1 Route surface

| Method                 | Path                              | Description                          |
| ---------------------- | --------------------------------- | ------------------------------------ |
| `GET`                  | `/healthz`                        | Liveness probe                       |
| `GET`                  | `/readyz`                         | Readiness probe                      |
| **Notes**              |                                   |                                      |
| `GET POST`             | `/v1/notes`                       | List / create notes                  |
| `GET PATCH DELETE`     | `/v1/notes/{urn}`                 | Read / update / delete a note        |
| `POST`                 | `/v1/events`                      | Append an event to a note            |
| **Snips**              |                                   |                                      |
| `GET POST`             | `/v1/snips`                       | List / create typed notes (snips)    |
| **Projects & Folders** |                                   |                                      |
| `GET POST`             | `/v1/projects`                    | List / create projects               |
| `GET PATCH DELETE`     | `/v1/projects/{urn}`              | Read / update / delete a project     |
| `GET POST`             | `/v1/folders`                     | List / create folders                |
| `GET PATCH DELETE`     | `/v1/folders/{urn}`               | Read / update / delete a folder      |
| **Search**             |                                   |                                      |
| `GET`                  | `/v1/search`                      | Full-text search over normal notes   |
| **Links**              |                                   |                                      |
| `GET POST`             | `/v1/links/anchors`               | List / create anchors                |
| `GET PATCH DELETE`     | `/v1/links/anchors/{id}`          | Read / update / delete an anchor     |
| `GET POST`             | `/v1/links/backlinks`             | List / create backlinks              |
| `GET`                  | `/v1/links/backlinks/recent`      | Recently added backlinks             |
| `GET`                  | `/v1/links/outbound`              | Outbound links from a note           |
| `GET`                  | `/v1/links/referrers`             | Notes that link to a given note      |
| `GET POST`             | `/v1/links/external`              | External (URL) links                 |
| **Context Graph**      |                                   |                                      |
| `GET`                  | `/v1/context/stats`               | Context graph statistics             |
| `GET`                  | `/v1/context/bursts`              | List content bursts                  |
| `GET`                  | `/v1/context/bursts/search`       | Semantic search over bursts          |
| `GET`                  | `/v1/context/bursts/{id}`         | Single burst                         |
| `GET`                  | `/v1/context/candidates`          | List link candidates                 |
| `GET PATCH`            | `/v1/context/candidates/{id}`     | Read / promote / dismiss a candidate |
| `GET PATCH`            | `/v1/context/config/{projectURN}` | Per-project context configuration    |
| `GET`                  | `/v1/context/inferences`          | List inferences                      |
| `GET PATCH`            | `/v1/context/inferences/{id}`     | Accept / reject an inference         |
| **AI Credentials**     |                                   |                                      |
| `GET POST`             | `/v1/ai/credentials`              | List / add AI provider credentials   |
| `DELETE`               | `/v1/ai/credentials/{provider}`   | Remove credentials for a provider    |

Snip plugins may register additional routes under their own namespace via
`RegisterHTTP`. The engine calls this during startup after all plugins have
been initialised.

### 4.2 Response conventions

- All responses are `application/json`.
- Errors use `{"error": "<message>"}` with an appropriate HTTP status code.
- List endpoints support cursor-based pagination via `page_token` and
  `page_size` query parameters.

### 4.3 Localhost-only binding

The server never binds to `0.0.0.0`. The bind address is always
`127.0.0.1:<port>`. The default port is `7430`. This is enforced in the
server startup code, not left as a configuration option that could be
accidentally exposed.

---

## 5. CLI — notx & notxctl

There are two CLI binaries with distinct roles:

- **`notx`** is the **primary client**. It speaks to the local HTTP API and
  provides the full command set for everyday engine use — creating notes,
  searching, managing projects, exporting packages, and managing AI
  credentials. This is what end-users and scripts interact with.

- **`notxctl`** is the **developer / testing tool**. It speaks directly to
  the local gRPC server and is used for low-level inspection, integration
  testing, and engine administration tasks that are not exposed over HTTP.
  It is not intended for regular use.

### 5.1 Command groups (`notx`)

| Group     | Commands                                                       | Description                                 |
| --------- | -------------------------------------------------------------- | ------------------------------------------- |
| `note`    | `create`, `get`, `list`, `edit`, `delete`, `history`, `diff`   | Full note lifecycle                         |
| `event`   | `append`                                                       | Append a raw event to a note                |
| `snip`    | `create`, `get`, `list`, `delete`                              | Typed note management                       |
| `project` | `create`, `get`, `list`, `rename`, `delete`                    | Project management                          |
| `folder`  | `create`, `get`, `list`, `rename`, `delete`                    | Folder management                           |
| `search`  | `query`                                                        | Full-text search                            |
| `link`    | `anchor`, `backlink`, `outbound`, `referrers`, `external`      | Link graph operations                       |
| `context` | `stats`, `bursts`, `candidates`, `promote`, `dismiss`, `infer` | Context graph operations                    |
| `export`  | `pack`, `unpack`                                               | Create and import `.gnotx` packages         |
| `ai`      | `credentials add`, `credentials list`, `credentials remove`    | AI credential management                    |
| `server`  | `start`, `stop`, `status`                                      | Engine lifecycle (when not run as a daemon) |

### 5.2 Output formats

Every command supports `--format` with at least two values:

- `table` (default) — human-readable aligned table
- `json` — machine-readable JSON, suitable for piping to `jq`

### 5.3 Configuration

`notx` reads its target endpoint from (in priority order):

1. `--engine-url` flag
2. `NOTX_ENGINE_URL` environment variable
3. `~/.config/notx/engine.toml` — `engine_url` key
4. Default: `http://127.0.0.1:7430`

---

## 6. AI Credential Store

The engine manages an encrypted local credential store for AI provider API
keys. This replaces any dependency on an external secrets manager (such as
HashiCorp Vault) for local operation.

### 6.1 Design

- Credentials are stored in a single file: `$NOTX_DATA_DIR/ai_credentials.enc`
- The file is encrypted with **AES-256-GCM**.
- The encryption key is derived from a passphrase using **Argon2id** (or from
  the OS keychain when available).
- The plaintext is a JSON object mapping provider name to credential fields.

### 6.2 Supported providers

| Provider key | Credential fields              |
| ------------ | ------------------------------ |
| `openai`     | `api_key`                      |
| `anthropic`  | `api_key`                      |
| `ollama`     | `base_url`                     |
| `custom`     | `base_url`, `api_key`, `model` |

### 6.3 Access

Credentials are read into memory at the moment they are needed and are not
held in process memory beyond the lifetime of the request. The encrypted file
is the only persistent form.

The credential store is **process-private**. The engine process is the only
entity that ever holds decrypted key material in memory, and only for the
duration of a single AI inference call — it is zeroed immediately after use.

The HTTP API (`/v1/ai/credentials`) does **not** expose credential values
under any circumstances — not even to localhost callers. The endpoints exist
solely to list which providers are configured (by name) and to add or remove
entries. A breach of the HTTP server, whether via a malicious local process or
a compromised CLI session, yields no key material. The plaintext credentials
are inaccessible without direct process-level memory access, which requires a
separate privilege escalation entirely outside the engine's threat model.

---

## 7. Snip Plugin System

A **snip** is a typed note — a `core.Note` with a non-empty `SnipType` field
that causes it to be routed through a registered plugin for structured
handling.

### 7.1 Plugin contract

Every snip plugin implements the `snip.SnipPlugin` interface:

| Method group    | Methods                                                                            |
| --------------- | ---------------------------------------------------------------------------------- |
| Identity        | `Type() string`, `Version() string`, `Description() string`, `Schema() SnipSchema` |
| Lifecycle       | `Init(ctx, env)`, `Start(ctx)`, `Stop(ctx)`                                        |
| Event callbacks | `OnNoteCreated`, `OnEventAppended`, `OnNoteDeleted`, `OnParentAnchorBroken`        |
| Transport       | `RegisterGRPC(s)`, `RegisterHTTP(mux, middleware)`                                 |

### 7.2 Schema declaration

Each plugin declares a `SnipSchema` describing its structured fields, which
fields are projected into SQLite index columns for filtering, which fields are
included in full-text search, and whether the snip type is end-to-end encrypted
(following the same rules as `NoteTypeSecure`).

### 7.3 Registration

Plugins are registered at startup via `snip.Registry.Register(plugin)`. Calling
`Register` twice with the same `Type()` string panics — this prevents silent
double-registration. The registry is passed into the HTTP handler and each
plugin's `RegisterHTTP` is called once during route setup.

### 7.4 Plugin environment

The `PluginEnv` struct injected into `Init` provides:

- `DB` — the shared SQLite connection (for plugin-managed index tables)
- `NoteRepo` — full note repository access
- `ProjRepo` — project/folder repository access
- `Config` — engine configuration
- `Log` — a scoped structured logger

---

## 8. Embeddable Library Interface

Feather is designed to be imported as a Go library, not just run as a binary.
The `notx-engine` module exposes a stable provider interface that allows any
host application to embed the engine with its own storage backend.

### 8.1 Provider pattern

The `repo` package defines pure interfaces. A host application supplies
concrete implementations:

```notx-engine/repo/repo.go#L174-269
// NoteRepository — create, get, list, update, delete, append-event, search
```

The engine's service layer and HTTP handler work against these interfaces
exclusively. No import of a concrete backend (`repo/file`, `repo/sqlite`,
`repo/memory`) is required by callers of the library.

### 8.2 Embedding example

```notx-engine/docs/FEATHER_OVERVIEW.md#L1-1
// (illustrative — not a real file in the tree)
```

A minimal embedding:

1. Instantiate a backend (e.g. `repo/sqlite.NewProvider(db)`)
2. Construct the HTTP handler: `http.New(cfg, noteRepo, projRepo, linkRepo, contextRepo, registry, log)`
3. Call `handler.Serve(ctx)` — the handler binds to localhost and is ready

The cloud layer (notx-cloud) uses this pattern to embed Feather as its
local-data authority while adding its own multi-tenant routing on top.

### 8.3 Stability guarantees

- The `core` package types (`Note`, `Event`, `URN`, `NoteType`, `SecurityPolicy`, etc.) are stable public API.
- The `repo` interfaces are stable public API.
- The `snip.SnipPlugin` interface is stable public API.
- Internal packages under `internal/` are not part of the public API and may change without notice.

---

## 9. What the Engine Deliberately Does Not Do

These capabilities are intentionally absent from Feather. They belong at the
cloud or application layer, not in the local engine.

| Capability                                           | Where it belongs                                                                                |
| ---------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| **Device registration & approval**                   | notx cloud — devices register with the cloud, not with each other's local engines               |
| **Device authentication (X-Device-ID middleware)**   | Cloud API gateway; the local engine is single-owner on localhost                                |
| **User management (multi-user, identity)**           | notx cloud — the local engine is single-owner by definition                                     |
| **Server-to-server pairing (mTLS CA, certificates)** | Cloud infrastructure — peer trust is a cloud routing concern                                    |
| **Relay**                                            | Cloud infrastructure — the relay mediates between devices via the cloud, not via local engines  |
| **Sync stream (gRPC SyncStream, StreamRegistry)**    | Cloud infrastructure — the cloud engine handles sync fan-out                                    |
| **Secrets management via external vault**            | Cloud infrastructure (HashiCorp Vault etc.); the local engine uses its own encrypted file store |
| **Multi-tenancy / namespacing**                      | Cloud infrastructure — the local engine has exactly one owner                                   |

This boundary is intentional and load-bearing. Keeping these concerns out of
the local engine means the engine stays auditable, portable, and operable
entirely offline.

---

_Document version: Feather 1.0 — engine architecture_
