# notx-engine — Deployment & Embedding Guide

This document explains the three ways to run `notx-engine`:

1. **Standalone HTTP server** — REST/JSON API for web clients and tooling
2. **Standalone gRPC server** — for `notxctl` and typed service-to-service calls
3. **Both layers simultaneously** — the default production mode
4. **Embedded library** — drop the engine directly into your own Go application,
   no HTTP or gRPC required

---

## Architecture Overview

After the three-stage layered refactor the engine has a clean separation of
concerns:

```
┌───────────────────────────────────────────────────────────┐
│  http.Handler   (JSON ↔ service types)                    │  optional
├───────────────────────────────────────────────────────────┤
│  grpc.Server    (proto ↔ service types)                   │  optional
├───────────────────────────────────────────────────────────┤
│  service.Engine  ← THE business logic                     │  always
│   Notes · Projects · Folders · Context · Links · Props    │
├───────────────────────────────────────────────────────────┤
│  repo.*   (NoteRepository, ProjectRepository, …)          │  always
├───────────────────────────────────────────────────────────┤
│  SQLite provider  /  file provider  /  memory provider    │  your choice
└───────────────────────────────────────────────────────────┘
```

The HTTP and gRPC layers are **optional transport adapters**. The `service.Engine`
is the only mandatory component — it owns all business logic and can be used
directly without any network layer.

---

## 1. Running as a Standalone HTTP Server

### What it does

Listens on `127.0.0.1:7430` (default) and exposes the full REST/JSON API.
The gRPC listener is disabled.

### Configuration

```go
cfg := config.Default()
cfg.EnableHTTP = true
cfg.EnableGRPC = false
cfg.HTTPPort   = 7430
cfg.DataDir    = "./data"
```

### Wiring

```go
package main

import (
    "log/slog"
    "os"

    "github.com/zebaqui/notx-engine/config"
    "github.com/zebaqui/notx-engine/internal/server"
    "github.com/zebaqui/notx-engine/repo/sqlite"
)

func main() {
    cfg := config.Default()
    cfg.EnableHTTP = true
    cfg.EnableGRPC = false

    provider, err := sqlite.Open(cfg.DataDir)
    if err != nil {
        slog.Error("open storage", "err", err)
        os.Exit(1)
    }
    defer provider.Close()

    log := slog.Default()

    srv, err := server.New(cfg,
        provider,          // NoteRepository
        provider,          // ProjectRepository
        provider,          // ContextRepository  (nil disables context graph)
        provider,          // LinkRepository     (nil disables link graph)
        log,
        nil,               // snip plugins
        provider,          // PropSchemaRepo     (nil disables prop schemas)
    )
    if err != nil {
        slog.Error("build server", "err", err)
        os.Exit(1)
    }

    if err := srv.Run(); err != nil {
        slog.Error("server exited", "err", err)
        os.Exit(1)
    }
}
```

### What starts

```
time=… level=INFO msg="notx engine ready"
time=… level=INFO msg="http server starting" addr=127.0.0.1:7430
```

The gRPC port is never opened. Every REST endpoint documented in `docs/API.md`
is available immediately.

---

## 2. Running as a Standalone gRPC Server

### What it does

Listens on `127.0.0.1:50051` (default) and exposes the five protobuf services
(`NoteService`, `ProjectService`, `FolderService`, `ContextService`,
`LinkService`). The HTTP listener is disabled.

### Configuration

```go
cfg := config.Default()
cfg.EnableHTTP = false
cfg.EnableGRPC = true
cfg.GRPCPort   = 50051
cfg.DataDir    = "./data"
```

### Wiring

```go
cfg := config.Default()
cfg.EnableHTTP = false
cfg.EnableGRPC = true

provider, _ := sqlite.Open(cfg.DataDir)
defer provider.Close()

srv, err := server.New(cfg,
    provider, provider, provider, provider,
    slog.Default(), nil, provider,
)
if err != nil { /* … */ }

srv.Run()
```

### What starts

```
time=… level=INFO msg="notx engine ready"
time=… level=INFO msg="grpc server starting" addr=127.0.0.1:50051
```

Server reflection is always registered so `grpcurl` works out of the box:

```bash
grpcurl -plaintext localhost:50051 list
# notx.NoteService
# notx.ProjectService
# notx.FolderService
# notx.ContextService
# notx.LinkService
```

---

## 3. Running Both Layers Simultaneously (Default)

### What it does

This is the default production mode. Both listeners start on their respective
ports. HTTP and gRPC share the **same** `service.Engine` instance — a write
through HTTP is immediately visible to a gRPC client, and vice versa.

### Configuration

```go
cfg := config.Default()
// EnableHTTP = true  (already the default)
// EnableGRPC = true  (already the default)
```

### Wiring

```go
cfg := config.Default()  // both enabled by default

provider, _ := sqlite.Open(cfg.DataDir)
defer provider.Close()

srv, err := server.New(cfg,
    provider, provider, provider, provider,
    slog.Default(), nil, provider,
)
if err != nil { /* … */ }

srv.Run()  // blocks until SIGINT / SIGTERM
```

### What starts

```
time=… level=INFO msg="notx engine ready"
time=… level=INFO msg="http server starting"  addr=127.0.0.1:7430
time=… level=INFO msg="grpc server starting"  addr=127.0.0.1:50051
```

### Shared service layer — why this matters

```
server.New()
    │
    ├─ service.Engine  ← ONE instance, shared by both transports
    │       Notes / Projects / Folders / Context / Links / Props
    │
    ├─ http.Handler    ← calls eng.Notes, eng.Projects, … directly
    │
    └─ grpc.Server     ← thin proto adapters wrapping the same engine
           NewNoteServer(eng.Notes)
           NewProjectServer(eng.Projects)
           …
```

There is no in-process RPC hop. The HTTP handler calls the service interfaces
directly. The gRPC handlers are thin proto translators that call the same
service interfaces. Both transports share the same snip plugin registry,
context backfill goroutines, and pagination settings.

---

## 4. Embedding the Engine (No HTTP, No gRPC)

This is the scenario for apps that want the full engine capabilities — note
persistence, event sourcing, full-text search, context graph, link graph — but
don't need network listeners at all.

### Why embedding is now clean

Before the layered refactor, embedding required constructing `grpcsvc.*Server`
structs (which carry proto dependencies) even when no gRPC was needed.
`NewFromRepos` wired those structs under the hood.

After the refactor, `service.Engine` is a plain Go struct with no transport
dependencies. You build it once and call its fields directly.

### Building the engine

```go
import (
    "github.com/zebaqui/notx-engine/service"
    "github.com/zebaqui/notx-engine/repo/sqlite"
)

provider, err := sqlite.Open("./data")
if err != nil { /* … */ }
defer provider.Close()

eng := service.New(
    provider,   // NoteRepository
    provider,   // ProjectRepository
    provider,   // ContextRepository  — pass nil to disable context graph
    provider,   // LinkRepository     — pass nil to disable link graph
    provider,   // PropSchemaRepo     — pass nil to disable prop schemas
    50,         // defaultPageSize    — 0 uses built-in default (50)
    200,        // maxPageSize        — 0 uses built-in default (200)
)
```

### Calling the engine directly

```go
ctx := context.Background()

// ── Notes ─────────────────────────────────────────────────────────────────

noteURN := core.NewURN(core.ObjectTypeNote)
note    := core.NewNote(noteURN, "My first note", time.Now().UTC())

if err := eng.Notes.Create(ctx, note); err != nil {
    // service.ErrInvalidInput  → bad argument
    // repo.ErrAlreadyExists    → note URN already taken
}

// Append content as an event
ev := &core.Event{
    URN:       core.NewURN(core.ObjectTypeEvent),
    NoteURN:   noteURN,
    Sequence:  1,
    AuthorURN: core.AnonURN(),
    CreatedAt: time.Now().UTC(),
    Entries:   core.DiffLines(nil, core.SplitLines("# Hello\nWorld")),
}
_ = eng.Notes.AppendEvent(ctx, ev, repo.AppendEventOptions{ExpectSequence: 1})

// Read back
fetched, _, err := eng.Notes.Get(ctx, noteURN.String())
content := fetched.Content()  // "# Hello\nWorld"

// ── Projects ──────────────────────────────────────────────────────────────

projURN := core.NewURN(core.ObjectTypeProject)
proj    := &core.Project{URN: projURN, Name: "Work"}
_ = eng.Projects.Create(ctx, proj)

// List all projects
result, _ := eng.Projects.List(ctx, repo.ProjectListOptions{PageSize: 20})
for _, p := range result.Projects {
    fmt.Println(p.Name)
}

// ── Search ────────────────────────────────────────────────────────────────

results, _ := eng.Notes.Search(ctx, repo.SearchOptions{Query: "Hello"})
for _, r := range results.Results {
    fmt.Printf("%s — %s\n", r.Note.Name, r.Excerpt)
}
```

### Wiring snip plugins

Snip plugins are optional structured-data extensions (e.g. `todo`, `bash_history`).
Wire them after building the engine but before serving any requests:

```go
registry := snip.NewRegistry()
for _, plugin := range myPlugins {
    env := snip.PluginEnv{
        DB:       provider.DB(),
        NoteRepo: provider,
        ProjRepo: provider,
        Config:   cfg,
        Log:      slog.Default().With("plugin", plugin.Type()),
    }
    if err := plugin.Init(ctx, env); err != nil { /* … */ }
    registry.Register(plugin)
}

// WireSnipRegistry sets the registry on eng.Notes so that
// OnNoteCreated / OnEventAppended hooks are dispatched after writes.
eng.WireSnipRegistry(registry)
```

### Optional: wrap in HTTP for later

If you later decide to expose the embedded engine over HTTP you don't have to
change your engine construction at all — just wrap it:

```go
// Same eng from above
h := httpsvc.New(
    cfg,
    eng.Notes, eng.Projects, eng.Folders,
    eng.Context, eng.Links,
    slog.Default(),
    nil,        // plugins (HTTP route registration only)
    eng.Props,
)
// h is an http.Handler — use it with any net/http server
http.ListenAndServe(":7430", h)
```

---

## 5. Using `NewFromRepos` (Quick Embed)

For applications that only need the HTTP handler and don't care about running
a gRPC server, `http.NewFromRepos` is a convenience constructor that builds
the service engine and the HTTP handler in one call:

```go
import (
    httpsvc "github.com/zebaqui/notx-engine/http"
    "github.com/zebaqui/notx-engine/repo/sqlite"
)

provider, _ := sqlite.Open("./data")
defer provider.Close()

h := httpsvc.NewFromRepos(
    cfg,
    provider,   // NoteRepository
    provider,   // ProjectRepository
    provider,   // ContextRepository
    provider,   // LinkRepository
    provider,   // PropSchemaRepo
    nil,        // snip plugins
    slog.Default(),
)

http.ListenAndServe(":7430", h)
```

`NewFromRepos` creates a `service.Engine` internally and passes its service
fields directly to `http.New`. No gRPC adapters are created.

---

## 6. Service Interface Reference

`service.Engine` exposes six typed fields. Each is an interface backed by the
concrete implementation in `service/`:

| Field      | Interface               | Backing struct       | Optional? |
|------------|-------------------------|----------------------|-----------|
| `Notes`    | `service.NoteService`   | `noteService`        | no        |
| `Projects` | `service.ProjectService`| `projectService`     | no        |
| `Folders`  | `service.FolderService` | `folderService`      | no        |
| `Context`  | `service.ContextService`| `contextService`     | yes (nil if no ContextRepository) |
| `Links`    | `service.LinkService`   | `linkService`        | yes (nil if no LinkRepository) |
| `Props`    | `service.PropService`   | `propService`        | yes (nil if no PropSchemaRepo) |

All methods on every service:

- Accept `context.Context` as the first argument
- Use `core.*` and `repo.*` types — no proto, no gRPC
- Return plain Go errors; check with `errors.Is`:

```go
if errors.Is(err, service.ErrInvalidInput) {
    // validation failure — bad argument
}
if errors.Is(err, repo.ErrNotFound) {
    // resource does not exist
}
if errors.Is(err, repo.ErrAlreadyExists) {
    // duplicate create
}
if errors.Is(err, repo.ErrSequenceConflict) {
    // optimistic concurrency conflict on AppendEvent
}
```

---

## 7. Config Reference

```go
cfg := config.Default()

// Toggle transports
cfg.EnableHTTP = true     // default: true
cfg.EnableGRPC = true     // default: true

// Ports (both bind to cfg.Host)
cfg.HTTPPort = 7430       // default: 7430
cfg.GRPCPort = 50051      // default: 50051
cfg.Host     = "127.0.0.1"  // default: "127.0.0.1"

// Storage
cfg.DataDir = "./data"    // default: "./data"

// Pagination (applied by the service layer)
cfg.DefaultPageSize = 50  // default: 50
cfg.MaxPageSize     = 200 // default: 200

// Lifecycle
cfg.ShutdownTimeout = 30 * time.Second  // default: 30s

// Logging
cfg.LogLevel = "info"  // "debug" | "info" | "warn" | "error"
```

`config.Default()` is production-safe out of the box. Override only what you
need. Call `cfg.Validate()` before passing to `server.New` to catch port
conflicts and invalid values early.

---

## 8. Graceful Shutdown

`server.Run()` blocks until `SIGINT` or `SIGTERM` is received, then:

1. Stops accepting new connections on both listeners
2. Waits up to `cfg.ShutdownTimeout` for in-flight requests to drain
3. Calls `Stop()` on all snip plugins
4. Force-closes any remaining connections
5. Returns `nil` on clean exit, a joined error on unexpected failures

For programmatic shutdown (tests, embeddings), use `RunWithContext`:

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

go func() {
    if err := srv.RunWithContext(ctx); err != nil {
        log.Error("server", "err", err)
    }
}()

// … do stuff …

cancel() // triggers graceful shutdown
```

---

## 9. Repository Providers

The engine is provider-agnostic. Pass any implementation of the `repo.*`
interfaces.

| Provider | Package | Use case |
|---|---|---|
| `sqlite.Provider` | `repo/sqlite` | Production — persists notes as `.notx` files + SQLite index |
| `file.Provider` | `repo/file` | Legacy file-only provider |
| `memory.Provider` | `repo/memory` | Unit tests, ephemeral in-process use |

The `sqlite.Provider` implements all five repository interfaces
(`NoteRepository`, `ProjectRepository`, `ContextRepository`,
`LinkRepository`, `PropSchemaRepo`) so a single instance can be passed for
all five arguments:

```go
provider, err := sqlite.Open("./data")
// provider satisfies: NoteRepository, ProjectRepository,
//                     ContextRepository, LinkRepository, PropSchemaRepo
eng := service.New(provider, provider, provider, provider, provider, 0, 0)
```

Pass `nil` for any optional interface to disable the corresponding service:

```go
eng := service.New(
    provider,   // NoteRepository   — required
    provider,   // ProjectRepository — required
    nil,        // ContextRepository — context graph disabled, eng.Context == nil
    nil,        // LinkRepository    — link graph disabled,    eng.Links   == nil
    nil,        // PropSchemaRepo    — props disabled,         eng.Props   == nil
    0, 0,       // use built-in pagination defaults
)
```
