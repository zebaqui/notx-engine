# SNIPS — Typed Notes in notx

---

## 1. Motivation

A full notx `Note` is a **fleet-grade entity**: event-sourced, append-only, collaborative,
with context graph participation, anchor linking, E2EE, and complete auditable history. This
power is appropriate for narrative, exploratory, and long-lived knowledge — but it is **not
always the right tool**.

Consider two concrete situations:

- **Global Todo Board**: todos already exist as Markdown checkboxes scattered across project
  notes. You need to index them, track their status (`Backlog / Doing / Done`), and attach
  comments — without creating a separate full Note per task or splitting the source of truth.
- **Bash History**: you want shell commands to be first-class records — tagged, searchable,
  auditable, with statistics on how many times each was run and all its exact variations —
  but a command entry has no narrative body and no reason to be a free-form document.

Both share a common shape: **narrow domain, typed schema, purpose-built lifecycle**. Smaller
in scope, not smaller in rigor.

The natural analogy:

| GitHub        | notx                                       |
| ------------- | ------------------------------------------ |
| Repository    | `Note` — rich, self-contained, fleet-grade |
| Gist / Commit | `Snip` — small, typed, domain-specific     |

A **Snip** is not a separate entity type. It **is a note** — stored, indexed, appended to,
synced, and secured by the exact same engine machinery that handles every other note. What
changes is the _domain_: a snip's materialized content must conform to a declared typed
schema. That constraint is enforced by a thin **sidecar plugin** that wraps the engine
without replacing any part of it.

---

## 2. Core Design Principles

1. **A snip is a note.** The engine has one entity: the `Note`. A snip is a `Note` with a
   `snip_type` header field set. Every engine path that operates on notes — `CreateNote`,
   `AppendEvent`, `StreamEvents`, `SearchNotes`, `ShareSecureNote`, sync,
   FTS5 indexing, anchor resolution, context graph — operates on snips without modification.

2. **Full event sourcing, same format.** Snips use the exact `.notx` wire format:
   `# notx/1.0` header, event stream with `->` blocks, optional snapshot blocks with `=>`
   markers. `ParserV1` parses a snip file without modification. No new grammar. No new file
   extension.

3. **Schema validated at materialization, not at the event level.** Events carry `LineEntry`
   operations against YAML lines — the same as any note. Schema validation runs when
   `Content()` is called. An event that produces an invalid schema state is a validation
   error at the application layer, not a format error at the parser layer.

4. **Plugins are observers, not owners.** A `SnipPlugin` is a typed observer that hooks into
   the existing note write path. It does not own a repository, does not own storage, and does
   not replace any engine component. It receives `NoteRepository` access from the engine and
   uses it directly.

5. **All body fields are optional at the engine level.** Every snip body field defaults to a
   zero value if absent. The plugin is responsible for interpreting and validating its own
   field requirements. The schema system does not distinguish Required from Optional — there
   is one flat `Fields` map.

6. **Plugins are opt-in.** No plugin is active by default. Users enable plugins explicitly
   via `notx plugin enable <type>`. A disabled plugin's snips remain in the `.notx` files
   untouched; the plugin simply doesn't load, doesn't register hooks, and doesn't mount
   routes.

7. **One file extension.** Both notes and snips use the `.notx` extension. Identity is
   determined by the header (`snip_type` present vs. absent), not by the file name.

8. **Composable with notes, not competing with them.** A snip never replaces a note. It
   augments a note (sidecar), indexes content from a note (todo record), or captures domain
   data that would pollute a note if embedded (bash history entry, bookmark).

9. **SQLite as the sole derived store.** There is no Badger materialization cache for snips.
   The `.notx` file is the ground truth; SQLite holds the derived query index, updated on
   every event append in the same transaction. Replay from file is the fallback when needed.

10. **Same security boundary.** Snips inherit the security policy of their parent note or
    declare their own. Secure snips are E2EE per-event in exactly the same way secure notes
    are — each event carries its own CEK wrapped per recipient device.

---

## 3. What a Snip Is — and Is Not

### Is

- A `core.Note` with `SnipType` set — stored and indexed identically to any other note
- Event-sourced using the exact `.notx` wire format — every field change is a recorded event
- Optionally a **sidecar** anchored to a specific location in a parent Note via `ParentAnchor`
- Managed by its type's sidecar plugin for schema validation, indexing, and domain APIs
- Reachable through `GetNote`, `AppendEvent`, `StreamEvents` by `note_urn` — same as any note
- Listed and filtered via the dedicated `ListSnips` RPC (never returned by `ListNotes`)
- Syncable via `notx snip pull` — a subset of the note sync protocol filtered to snip types
- Linkable from notes via `notx:lnk:id:`
- Capable of full content-at-sequence history (`ContentAt(seq)`)

### Is Not

- A new entity type in the engine — the engine knows only `Note`
- A participant in the context graph (no bursts extracted, no candidate pairs)
- A replacement for a Note (no narrative prose, no free-form document structure)
- An anchor host within its own body (snips declare no internal anchor table)
- Stored in a Badger materialization cache

---

## 4. Engine Changes — Extending `core.Note`

A snip is created by adding two new optional fields to `core.Note` and teaching `ParserV1`
to populate them from the header.

### 4.1 New fields on `core.Note`

```go
type Note struct {
    // ... all existing fields unchanged ...

    // SnipType, when non-nil, marks this note as a typed snip.
    // Immutable after creation — set in the file header and never changed.
    // Value is a registered snip_type string (e.g. "todo", "bash_history").
    SnipType *string

    // ParentAnchor is the anchor ID within ParentURN that this snip is
    // bound to. Non-nil only for sidecar snips. ParentURN already exists
    // on Note; ParentAnchor is the new companion field.
    ParentAnchor *string
}
```

`ParentURN` already exists on `core.Note` (used for hierarchical notes). Snips reuse it as
the sidecar binding to the parent note. `ParentAnchor` is the companion field that pins the
snip to a specific anchor within that parent note.

### 4.2 Parser changes (`ParserV1`)

`parseLines` already pulls arbitrary `# key: value` pairs into a `map[string]string`.
Two new lines in the post-header population block:

```go
if snipType, ok := metadata["snip_type"]; ok && snipType != "" {
    note.SnipType = &snipType
}
if parentAnchor, ok := metadata["parent_anchor"]; ok && parentAnchor != "" {
    note.ParentAnchor = &parentAnchor
}
```

The `required` field check in `parseHeader` already gates on `note_urn`, `name`,
`created_at`, and `head_sequence`. No changes to required fields — `snip_type` is optional
at the parser level. Plugins enforce their own "required for this type" rules at the
application layer.

### 4.3 File header for a snip

A snip file is a valid `.notx` file. The only additions vs. a regular note header are
`snip_type` and (optionally) `parent_anchor`.

```
# notx/1.0
# note_urn:      urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
# snip_type:     todo
# name:          Implement anchor drift detection for multi-line deletions
# authority:     urn:notx:srv:01HZSERVER1234567890123
# namespace:     acme
# parent_urn:    urn:notx:note:01HZPARENTNOTE1234567
# parent_anchor: todo-implement-drift
# project_urn:   urn:notx:proj:01HZX3K8PROJID123456789
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 3
```

| Field           | Required | Mutable | Notes                                           |
| --------------- | -------- | ------- | ----------------------------------------------- |
| `notx/1.0`      | ✅       | ❌      | Version sentinel, must be first line            |
| `note_urn`      | ✅       | ❌      | Standard note identity — `urn:notx:note:<uuid>` |
| `snip_type`     | ✅       | ❌      | Marks this note as a typed snip; immutable      |
| `name`          | ✅       | ✅      | Human label; for snips, derived from content    |
| `created_at`    | ✅       | ❌      | Immutable                                       |
| `head_sequence` | ✅       | ✅      | Advanced on every event append                  |
| `parent_urn`    | ❌       | ✅      | URN of the parent Note (sidecar binding)        |
| `parent_anchor` | ❌       | ✅      | Anchor ID within the parent note                |
| `project_urn`   | ❌       | ✅      | Inherited from parent note if absent            |
| `authority`     | ❌       | ✅      | Server URN, informational                       |
| `namespace`     | ❌       | ✅      | Label only, not identity                        |
| `deleted`       | ❌       | ✅      | Soft-delete flag                                |

`snip_type` is immutable for the same reason `note_type` is: changing the declared type
after creation would invalidate all existing per-type index queries.

### 4.4 Event stream — content is typed YAML

The event stream is byte-for-byte identical to the `.notx` spec. The semantic difference is
what the lines represent: instead of free-form prose, they are lines of a **flat YAML
document** whose shape is defined by `snip_type`.

Example — a `todo` snip created at sequence 1, status moved to `doing` at sequence 2, a
comment appended at sequence 3:

```
# notx/1.0
# note_urn:      urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
# snip_type:     todo
# name:          Implement anchor drift detection for multi-line deletions
# parent_urn:    urn:notx:note:01HZPARENTNOTE1234567
# parent_anchor: todo-implement-drift
# project_urn:   urn:notx:proj:01HZX3K8PROJID123456789
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 3

1:2025-01-15T09:00:00Z:urn:notx:usr:01HZAUTHOR1234567890
->
1 | status: backlog
2 | text: "Implement anchor drift detection for multi-line deletions"
3 | file_path: core/links.go
4 | line_number: 142
5 | checkbox_state: open

2:2025-01-15T11:30:00Z:urn:notx:usr:01HZAUTHOR1234567890
->
1 | status: doing

3:2025-01-16T14:22:00Z:urn:notx:usr:01HZAUTHOR1234567890
->
6 | comments:
7 |   - id: 01HZX3K8COMMENT12345
8 |     at: 2025-01-16T14:22:00Z
9 |     text: "Blocked on the batch-delete ordering fix first"
```

Materialized content at `head_sequence: 3`:

```yaml
status: doing
text: "Implement anchor drift detection for multi-line deletions"
file_path: core/links.go
line_number: 142
checkbox_state: open
comments:
  - id: 01HZX3K8COMMENT12345
    at: 2025-01-16T14:22:00Z
    text: "Blocked on the batch-delete ordering fix first"
```

---

## 5. NoteService Changes — `ListSnips` and Snip Pull

### 5.1 `ListNotes` excludes snips

`ListNotes` returns only regular notes — notes where `snip_type IS NULL`. Snips never appear
in `ListNotes` results. This keeps the note browsing experience clean and consistent for
clients that are unaware of snips.

### 5.2 New `ListSnips` RPC

A new `ListSnips` RPC is added to `NoteService`. It is the only entry point for listing
snips, regardless of whether the caller has the plugin for that type installed.

```proto
// Added to NoteService in note.proto
rpc ListSnips(ListSnipsRequest) returns (ListSnipsResponse);

message ListSnipsRequest {
  // snip_type filters to one registered type (e.g. "todo", "bash_history").
  // Empty = return all snip types, including unknown ones.
  string snip_type    = 1;
  string project_urn  = 2;
  string parent_urn   = 3;  // filter to sidecar snips of one note
  string parent_anchor = 4; // filter to one specific anchor
  bool   include_deleted = 5;
  int32  page_size    = 6;
  string page_token   = 7;
}

message ListSnipsResponse {
  repeated NoteHeader snips  = 1; // NoteHeader.snip_type is populated
  string next_page_token     = 2;
}
```

Unknown snip types (no plugin loaded for that `snip_type`) surface here alongside known
types. The engine makes no distinction — they are all notes with a `snip_type` field. The
caller decides how to render an unknown type (generic YAML fallback).

### 5.3 HTTP

```
GET /v1/snips                → ListSnips (all types)
GET /v1/snips?snip_type=todo → ListSnips filtered to todo
```

This is separate from `/v1/notes` (which excludes snips) and from the plugin-specific
routes at `/v1/snips/<type>/`.

### 5.4 `notx snip pull`

`notx snip pull` is a subset of the existing sync protocol. It issues a `ListSnips` to the
server filtered by the requested type(s), then for each snip URN calls the standard note
pull path to fetch and apply any events beyond the local head. Snips participate in the
same sync lifecycle as notes — they are notes.

```
notx snip pull                    # pull all snip types
notx snip pull --type todo        # pull only todo snips
notx snip pull --type bash_history
```

---

## 6. The Sidecar Plugin System

Each snip type is implemented as a **sidecar plugin** — a self-contained Go component that
registers lifecycle hooks, gRPC services, and HTTP routes into the engine at startup. Plugins
run in-process as goroutines alongside the engine, not as separate OS processes.

The model mirrors exactly how `grpcsvc.NoteServer`, `grpcsvc.ContextServer`, and
`grpcsvc.LinkServer` are constructed and wired in `internal/server/server.go`, and how
`http.Handler` registers routes for each domain.

### 6.1 The `SnipPlugin` Interface

```go
// SnipPlugin is the contract every snip type sidecar must satisfy.
type SnipPlugin interface {

    // ── Identity ────────────────────────────────────────────────────────────

    // Type returns the snip_type string this plugin handles (e.g. "todo").
    // Must be lowercase, max 64 chars, no spaces.
    Type() string

    // Version returns the plugin's own semantic version string.
    Version() string

    // Description returns a one-line human description for tooling and logs.
    Description() string

    // ── Schema ──────────────────────────────────────────────────────────────

    // Schema returns the schema descriptor for this snip type.
    // Called once at startup to validate incoming snips and to build the
    // per-type SQLite index table.
    Schema() SnipSchema

    // ── Lifecycle ───────────────────────────────────────────────────────────

    // Init is called once after all repositories are ready but before the
    // HTTP and gRPC listeners open. The plugin stores env for later use and
    // runs one-time setup (applying its SQLite migrations via env.DB).
    Init(ctx context.Context, env PluginEnv) error

    // Start is called after the listeners open. Long-running background
    // goroutines (file watchers, scanners) are launched here. Start must
    // return promptly; blocking work belongs in goroutines.
    Start(ctx context.Context) error

    // Stop is called during graceful shutdown. The plugin signals all
    // background goroutines to stop and waits for them, or respects ctx.
    Stop(ctx context.Context) error

    // ── Engine hook callbacks ────────────────────────────────────────────────
    // These are called by the engine's note write path after each operation.
    // They run synchronously in the same transaction — keep them fast.

    // OnNoteCreated is called after a new snip of this type is persisted
    // (sequence 1 written).
    OnNoteCreated(ctx context.Context, note *core.Note) error

    // OnEventAppended is called after each subsequent event append on a note
    // of this snip type.
    OnEventAppended(ctx context.Context, note *core.Note, event *core.Event) error

    // OnNoteDeleted is called after a soft-delete event is written.
    OnNoteDeleted(ctx context.Context, noteURN core.URN) error

    // OnParentAnchorBroken is called when the engine's anchor-break detector
    // reports that this snip's parent_anchor no longer exists in the parent
    // note. The plugin decides how to handle the orphan.
    OnParentAnchorBroken(ctx context.Context, noteURN core.URN) error

    // ── Service registration ─────────────────────────────────────────────────

    // RegisterGRPC registers the plugin's gRPC service(s) onto the engine's
    // shared gRPC server. Called before the gRPC listener opens.
    RegisterGRPC(s *grpc.Server)

    // RegisterHTTP registers the plugin's HTTP routes onto the engine's mux.
    // All routes must be scoped under /v1/snips/<type>/. The middleware
    // argument is the engine's withDeviceAuthMiddleware wrapper — the plugin
    // must apply it to every authenticated route.
    RegisterHTTP(
        mux *http.ServeMux,
        middleware func(http.HandlerFunc) http.HandlerFunc,
    )
}
```

### 6.2 `SnipSchema` — Type Descriptor

```go
// SnipSchema describes the structure and indexing rules for one snip type.
// All body fields are optional at the engine level — the plugin validates
// its own field requirements in OnNoteCreated / OnEventAppended.
type SnipSchema struct {
    // Fields defines all known body fields. Every field is optional by default;
    // a zero value (empty string, 0, nil) is used when the field is absent.
    // The engine uses this map to build the per-type SQLite index table and to
    // populate FTS5 entries. The plugin is responsible for validating business rules.
    Fields map[string]FieldSpec

    // IndexColumns lists Fields that are promoted to real SQLite columns in
    // the per-type index table (snips_<type>).
    IndexColumns []IndexColumn

    // DisplayField is the field used to generate link previews and board card titles.
    DisplayField string

    // StatusField, when set, names the field that drives board column grouping.
    StatusField  string
    StatusValues []string

    // FTSFields lists fields whose content is indexed in snips_fts for text search.
    // Snips are excluded from the context graph (no bursts, no candidate pairs)
    // regardless of this setting — FTSFields enables only direct text search.
    FTSFields []string

    // Secure, when true, forces E2EE regardless of parent note type.
    Secure bool
}

type FieldSpec struct {
    Kind    FieldKind
    Default any // zero value for the kind when field is absent in YAML
}
```

### 6.3 `PluginEnv` — Engine Access

```go
// PluginEnv is the handle the engine passes to SnipPlugin.Init.
// It gives each plugin controlled, read-write access to engine infrastructure.
// Plugins use NoteRepo directly — snips are notes.
type PluginEnv struct {
    // DB is the engine's SQLite database handle. The plugin uses it to apply
    // its own migrations and run per-type index queries.
    DB *sql.DB

    // NoteRepo is the engine's note repository. Plugins create and append
    // events to snips through this — the same interface used for all notes.
    NoteRepo repo.NoteRepository

    // ProjRepo gives read access to project and folder metadata.
    ProjRepo repo.ProjectRepository

    // Config is the engine's full configuration.
    Config *config.Config

    // Log is a structured logger pre-tagged with the plugin type.
    Log *slog.Logger
}
```

### 6.4 Engine Hook Dispatch

The engine's note write path calls registered plugin hooks after each operation. The
dispatch lives in `NoteService.AppendEvent` and `NoteService.CreateNote`:

```go
// After writing the event or creating the note, the engine checks whether
// the note has a snip_type and, if so, dispatches to the registered plugin.

func (s *NoteService) dispatchSnipHook(ctx context.Context, note *core.Note, event *core.Event) {
    if note.SnipType == nil {
        return // regular note — no plugin dispatch
    }
    plugin, ok := s.snipRegistry.Get(*note.SnipType)
    if !ok {
        return // unknown type — no-op, not an error
    }
    if event == nil {
        _ = plugin.OnNoteCreated(ctx, note)
    } else {
        _ = plugin.OnEventAppended(ctx, note, event)
    }
}
```

Plugin hooks that return errors are logged but **never fail the write**. The note/snip is
always persisted; the plugin index update is best-effort and self-healing on next startup.

**Todo plugin hook behavior:**

- `OnNoteCreated` (sequence = 1): the todo plugin performs a **full checkbox scan** of the
  note content via `syncCheckboxes`, seeding the index with one row per checkbox found.
  Each new row gets `status: backlog`.
- `OnEventAppended` (sequence > 1): the todo plugin performs **surgical index updates** via
  `applyEventToIndex`, walking `event.Entries` directly rather than rescanning the full
  content. Each entry type maps to a targeted SQL operation (see §8.2 for details).

### 6.5 Engine Wiring in `server.go`

```go
// In server.New(...), plugins are passed as []snip.SnipPlugin alongside repos.

registry := snip.NewRegistry()

for _, p := range plugins {
    env := snip.PluginEnv{
        DB:       sqlDB,
        NoteRepo: r,
        ProjRepo: projRepo,
        Config:   cfg,
        Log:      log.With("plugin", p.Type()),
    }
    if err := p.Init(ctx, env); err != nil {
        return nil, fmt.Errorf("snip plugin %s: init: %w", p.Type(), err)
    }
    registry.Register(p)
    p.RegisterGRPC(s.grpcServer)
    p.RegisterHTTP(s.httpHandler.Mux(), s.httpHandler.DeviceAuthMiddleware())
}

// Pass registry into NoteService so dispatch hooks can reach it.
noteSvc.SetSnipRegistry(registry)

// After listeners open:
for _, p := range plugins {
    if err := p.Start(ctx); err != nil {
        return nil, fmt.Errorf("snip plugin %s: start: %w", p.Type(), err)
    }
}

// During shutdown:
for _, p := range plugins {
    _ = p.Stop(shutdownCtx)
}
```

### 6.6 HTTP and gRPC Conventions

Every plugin mounts its routes under `/v1/snips/<type>/` and its gRPC services in the
`notx.v1` package alongside the existing engine services.

```
/v1/snips/todo/           → TodoPlugin HTTP routes
/v1/snips/bash_history/   → BashHistoryPlugin HTTP routes
/v1/snips/<custom>/       → user-defined plugin routes
```

Proto files live in `proto/snips/<type>.proto`. Generated files live in `proto/snips/`.
Built-in plugins ship with the binary. User-defined plugins load from `config.SnipPluginDir`
at startup.

---

## 7. Storage Model

Because snips are notes, their storage follows exactly the same model as all other notes.

### 7.1 Ground Truth: `.notx` Files

Each snip is stored as a `.notx` file. All other stores are derived from the file and
rebuildable on demand.

### 7.2 Main Note Index: SQLite

The existing `notes` table in SQLite already indexes all notes. Snips appear in this table
with `snip_type` and `parent_anchor` populated. No new generic table is needed.

The existing `notes` SQLite table gains two new columns:

```sql
ALTER TABLE notes ADD COLUMN snip_type     TEXT;     -- NULL for regular notes
ALTER TABLE notes ADD COLUMN parent_anchor TEXT;     -- NULL for non-sidecar snips

CREATE INDEX notes_snip_type     ON notes(snip_type)    WHERE snip_type IS NOT NULL;
CREATE INDEX notes_parent_anchor ON notes(parent_anchor) WHERE parent_anchor IS NOT NULL;
```

`parent_urn` is already a column on the `notes` table (it maps from `Note.ParentURN`).

### 7.3 Per-Type Index Tables (Plugin-Owned)

Each plugin creates its own type-specific index table in its `Init` migration. These tables
hold promoted body fields as real columns, enabling efficient board and stats queries without
touching the event stream.

> **Cloud/Postgres deployment note:** In the cloud (Postgres) deployment, plugin index tables
> are owned by the platform migration system (e.g. migration 032 for `engine_snips_todo`),
> not created by the plugin at runtime. `Init` is a no-op in that environment — it stores the
> `PluginEnv` and returns immediately without executing any DDL.
>
> The Postgres todo table uses `(namespace, note_urn, text)` as its composite primary key,
> where `text` is the checkbox label and is the **stable identity** of a todo item. The
> `line_number` column is a mutable sort column updated on every shift operation — it is
> **not** part of the primary key and must not be used as a stable identifier.

```sql
-- Created by TodoPlugin.Init
CREATE TABLE IF NOT EXISTS snips_todo (
    note_urn       TEXT PRIMARY KEY REFERENCES notes(note_urn),
    status         TEXT NOT NULL DEFAULT 'backlog',
    checkbox_state TEXT NOT NULL DEFAULT 'open',
    file_path      TEXT NOT NULL DEFAULT '',
    anchor_id      TEXT,
    orphaned       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX snips_todo_status ON snips_todo(status);
CREATE INDEX snips_todo_file   ON snips_todo(file_path);

-- Created by BashHistoryPlugin.Init
CREATE TABLE IF NOT EXISTS snips_bash_history (
    note_urn        TEXT PRIMARY KEY REFERENCES notes(note_urn),
    canonical_form  TEXT NOT NULL,
    shell           TEXT NOT NULL,
    exit_code       INTEGER NOT NULL DEFAULT 0,
    hostname        TEXT NOT NULL DEFAULT '',
    working_dir     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX snips_bash_history_canon ON snips_bash_history(canonical_form);
CREATE INDEX snips_bash_history_shell ON snips_bash_history(shell);

-- Stats and variations tables for bash_history
CREATE TABLE IF NOT EXISTS bash_history_stats (
    note_urn        TEXT PRIMARY KEY,
    canonical_form  TEXT NOT NULL,
    run_count       INTEGER NOT NULL DEFAULT 0,
    success_count   INTEGER NOT NULL DEFAULT 0,
    failure_count   INTEGER NOT NULL DEFAULT 0,
    first_run_at    TEXT NOT NULL,
    last_run_at     TEXT NOT NULL,
    recent_dirs     TEXT NOT NULL DEFAULT '[]',
    recent_hosts    TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS bash_history_variations (
    note_urn        TEXT NOT NULL REFERENCES notes(note_urn),
    command         TEXT NOT NULL,
    run_count       INTEGER NOT NULL DEFAULT 1,
    last_run_at     TEXT NOT NULL,
    last_exit_code  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (note_urn, command)
);
```

These tables are updated by the plugin's `OnNoteCreated` and `OnEventAppended` hooks, which
run in the same SQLite transaction as the note write. They are derived data — dropping and
rebuilding them by replaying all snip events of that type is always safe.

### 7.4 FTS5 Search

Plugins that declare `FTSFields` have their content indexed in the engine's existing
`notes_fts` FTS5 table (or a shared `snips_fts` table if the engine separates them). Secure
snips are never indexed, consistent with the note security model.

### 7.5 Why No Badger?

Badger was used for notes to cache materialized content and avoid replaying the full event
stream on every read. For snips:

- Event streams are short by design — a `todo` snip rarely exceeds a few dozen events; a
  `bash_history` snip grows linearly but each event is a single-line update
- The per-type SQLite index tables already serve the most common access patterns (board
  view, list, stats) without touching the event stream at all
- Full content replay (`ContentAt(seq)`, history view, export) is infrequent and fast

If profiling reveals replay is a bottleneck for a specific plugin, the plugin can call
`BuildSnapshot` and store checkpoints in its own SQLite table — no engine changes needed.

---

## 8. Built-in Plugin: `todo`

The `todo` plugin discovers Markdown checkboxes across project notes, creates a sidecar snip
for each, and exposes a board-oriented API for tracking and moving tasks.

### 8.1 Schema

```go
SnipSchema{
    Fields: map[string]FieldSpec{
        "text":           {Kind: FieldKindString,   Default: ""},
        "file_path":      {Kind: FieldKindString,   Default: ""},
        "checkbox_state": {Kind: FieldKindString,   Default: "open"},
        "status":         {Kind: FieldKindString,   Default: "backlog"},
        "line_number":    {Kind: FieldKindInt,       Default: 0},
        "anchor_id":      {Kind: FieldKindString,   Default: ""},
        "comments":       {Kind: FieldKindYAMLList, Default: nil},
        "tags":           {Kind: FieldKindStrList,  Default: nil},
    },
    IndexColumns: []IndexColumn{
        {Field: "status",         SQLType: "TEXT"},
        {Field: "file_path",      SQLType: "TEXT"},
        {Field: "checkbox_state", SQLType: "TEXT"},
        {Field: "anchor_id",      SQLType: "TEXT"},
    },
    DisplayField: "text",
    StatusField:  "status",
    StatusValues: []string{"backlog", "doing", "done"},
    FTSFields:    []string{"text"},
}
```

**Source of truth rules:**

- `checkbox_state` mirrors the Markdown source — updated by the scanner as events on the
  snip via `NoteRepo.AppendEvent`
- `status` is snip-owned — updated by user actions as events
- Conflict rule (read-time projection, never written back): if `checkbox_state == done` then
  `status` is treated as `done`. **Checkbox wins.**

### 8.2 Scanner

The todo plugin indexes checkboxes through two distinct code paths depending on whether a
note is being created for the first time or an event is being appended to an existing note.

#### Initial scan (`OnNoteCreated`)

Triggered when a new note is created (sequence = 1). The plugin calls `syncCheckboxes`,
which performs a full parse of the note content:

1. Walks all `.notx` project files in configured directories using `NoteRepo.ListNotes`
2. For each `- [ ]` / `- [x]` line found:
   - Resolves or creates a stable anchor via existing `SlugFromText` + anchor-creation
     machinery (a new event appended to the parent Note via `NoteRepo.AppendEvent`)
   - Looks up any existing `todo` snip with matching `parent_urn + parent_anchor` via
     `ListSnips(snip_type: "todo", parent_urn: ..., parent_anchor: ...)`
   - If none: creates a new snip via `NoteRepo.CreateNote` then `NoteRepo.AppendEvent`
     (sequence 1 sets initial YAML body, `status: backlog`)
   - If exists and `checkbox_state` changed: appends a single-line update event
3. For todos that have disappeared: appends a soft-delete event
4. `fsnotify` watcher triggers immediate re-scan of changed files
5. Full re-scan on a configurable interval (default: 5 minutes)

#### Incremental updates (`OnEventAppended`)

Triggered on every subsequent event (sequence > 1). Instead of rescanning the full note
content, the plugin calls `applyEventToIndex`, which walks `event.Entries` in order and
applies targeted SQL for each entry:

| Entry type                     | Content type        | SQL operations                                                                                           |
| ------------------------------ | ------------------- | -------------------------------------------------------------------------------------------------------- |
| `LineOpInsert`                 | checkbox line       | Shift all rows with `line_number >= N` down by 1, then INSERT a new todo row at N with `status: backlog` |
| `LineOpInsert`                 | plain text          | Shift all rows with `line_number >= N` down by 1 only (no new todo row)                                  |
| `LineOpDelete`                 | any                 | DELETE the todo row at line N (if any), then shift all rows with `line_number > N` up by 1               |
| `LineOpSet` / `LineOpSetEmpty` | checkbox → checkbox | UPDATE `text` and `checkbox_state` for the row at line N; `status` is preserved                          |
| `LineOpSet` / `LineOpSetEmpty` | checkbox → plain    | DELETE the todo row at line N                                                                            |
| `LineOpSet` / `LineOpSetEmpty` | plain → checkbox    | INSERT a new todo row at line N with `status: backlog`                                                   |
| `LineOpSet` / `LineOpSetEmpty` | plain → plain       | No-op (no index row exists or is affected)                                                               |

The offset counter from streaming semantics applies here too: each entry's effective line
number is `entry.LineNumber + offset`, where `offset` starts at 0 and is incremented by
`LineOpInsert` (+1) and decremented by `LineOpDelete` (-1) as entries are processed.

> **Important:** `status` (`backlog` / `doing` / `done`) is **never modified by scanner
> operations** — only the initial INSERT seeds it as `backlog`. All subsequent scanner
> updates preserve the current status. This ensures that board moves made by the user are
> never overwritten by a content scan.

### 8.3 gRPC Service

```proto
// proto/snips/todo.proto
syntax = "proto3";
package notx.v1;
import "google/protobuf/timestamp.proto";
option go_package = "github.com/zebaqui/notx-engine/proto/snips;snipspb";

message TodoComment {
  string id         = 1;
  string text       = 2;
  string author_urn = 3;
  google.protobuf.Timestamp at = 4;
}

message TodoRecord {
  string note_urn       = 1;
  string parent_urn     = 2;
  string parent_anchor  = 3;
  string project_urn    = 4;
  string file_path      = 5;
  string text           = 6;
  string status         = 7; // backlog | doing | done
  string checkbox_state = 8; // open | done
  int32  head_sequence  = 9;
  repeated TodoComment comments = 10;
  repeated string tags          = 11;
  google.protobuf.Timestamp created_at = 12;
  google.protobuf.Timestamp updated_at = 13;
}

message TodoBoard {
  repeated TodoRecord backlog = 1;
  repeated TodoRecord doing   = 2;
  repeated TodoRecord done    = 3;
}

service TodoService {
  // GetBoard returns all non-deleted todos grouped by status column.
  rpc GetBoard(GetBoardRequest) returns (GetBoardResponse);

  // ListTodos returns todos with optional filtering and pagination.
  rpc ListTodos(ListTodosRequest) returns (ListTodosResponse);

  // GetTodo returns a single todo with its comment list and event history.
  rpc GetTodo(GetTodoRequest) returns (GetTodoResponse);

  // MoveStatus updates the todo's status (backlog ↔ doing only).
  // Moving to done is handled by MarkDone.
  rpc MoveStatus(MoveStatusRequest) returns (MoveStatusResponse);

  // MarkDone marks a todo done: appends an event to the snip (status=done,
  // checkbox_state=done) then appends an event to the parent Note toggling
  // the Markdown checkbox to [x]. Both use NoteRepo.AppendEvent.
  rpc MarkDone(MarkDoneRequest) returns (MarkDoneResponse);

  // AddComment appends a comment entry to the todo's event stream.
  rpc AddComment(AddCommentRequest) returns (AddCommentResponse);

  // TriggerScan immediately re-scans the configured project directories.
  rpc TriggerScan(TriggerScanRequest) returns (TriggerScanResponse);
}

message GetBoardRequest {
  string project_urn = 1;
  string query       = 2;
}
message GetBoardResponse { TodoBoard board = 1; }

message ListTodosRequest {
  string project_urn  = 1;
  string status       = 2;
  string file_path    = 3;
  string query        = 4;
  bool   include_done = 5;
  int32  page_size    = 6;
  string page_token   = 7;
}
message ListTodosResponse {
  repeated TodoRecord todos = 1;
  string next_page_token    = 2;
}

message GetTodoRequest  { string note_urn = 1; }
message GetTodoResponse {
  TodoRecord todo                    = 1;
  repeated TodoHistoryEntry history  = 2;
}
message TodoHistoryEntry {
  int32  sequence   = 1;
  string author_urn = 2;
  string summary    = 3;
  google.protobuf.Timestamp at = 4;
}

message MoveStatusRequest  { string note_urn = 1; string status = 2; }
message MoveStatusResponse { TodoRecord todo = 1; }

message MarkDoneRequest  { string note_urn = 1; string author_urn = 2; }
message MarkDoneResponse {
  TodoRecord todo = 1;
  int32 note_seq  = 2; // new head_sequence of the parent Note after [x] toggle
}

message AddCommentRequest {
  string note_urn   = 1;
  string text       = 2;
  string author_urn = 3;
}
message AddCommentResponse {
  TodoComment comment = 1;
  int32 head_sequence = 2;
}

message TriggerScanRequest  { string project_urn = 1; }
message TriggerScanResponse {
  int32 todos_created = 1;
  int32 todos_updated = 2;
  int32 todos_deleted = 3;
}
```

### 8.4 HTTP Routes

| Method | Path                                | gRPC          | Description                       |
| ------ | ----------------------------------- | ------------- | --------------------------------- |
| GET    | `/v1/snips/todo/board`              | `GetBoard`    | Full board grouped by status      |
| GET    | `/v1/snips/todo`                    | `ListTodos`   | Paginated list with filters       |
| GET    | `/v1/snips/todo/:note_urn`          | `GetTodo`     | Single todo with history          |
| PATCH  | `/v1/snips/todo/:note_urn/status`   | `MoveStatus`  | Move between backlog / doing      |
| POST   | `/v1/snips/todo/:note_urn/done`     | `MarkDone`    | Mark done, toggle parent checkbox |
| POST   | `/v1/snips/todo/:note_urn/comments` | `AddComment`  | Append a comment                  |
| POST   | `/v1/snips/todo/scan`               | `TriggerScan` | Trigger immediate project scan    |

Query parameters for `GET /v1/snips/todo`:

| Parameter      | Type   | Description                           |
| -------------- | ------ | ------------------------------------- |
| `project_urn`  | string | Filter to one project                 |
| `status`       | string | `backlog`, `doing`, `done`, or absent |
| `file_path`    | string | Filter to one source file             |
| `q`            | string | Text search against `text` field      |
| `include_done` | bool   | Include done todos (default: false)   |
| `page_size`    | int    | Default: 50                           |
| `page_token`   | string | Cursor for next page                  |

---

## 9. Built-in Plugin: `bash_history`

The `bash_history` plugin captures shell commands as first-class notx records. It reads from
the shell's actual history file(s), normalizes commands, tracks statistics (run count,
variations, working directories, exit codes), and exposes a rich query API. An optional shell
hook enables real-time ingestion.

### 9.1 Schema

```go
SnipSchema{
    Fields: map[string]FieldSpec{
        "command":        {Kind: FieldKindString, Default: ""},
        "shell":          {Kind: FieldKindString, Default: ""},
        "exit_code":      {Kind: FieldKindInt,    Default: 0},
        "canonical_form": {Kind: FieldKindString, Default: ""},
        "working_dir":    {Kind: FieldKindString, Default: ""},
        "duration_ms":    {Kind: FieldKindInt,    Default: 0},
        "hostname":       {Kind: FieldKindString, Default: ""},
        "tags":           {Kind: FieldKindStrList,Default: nil},
        "note":           {Kind: FieldKindString, Default: ""},
    },
    IndexColumns: []IndexColumn{
        {Field: "canonical_form", SQLType: "TEXT"},
        {Field: "shell",          SQLType: "TEXT"},
        {Field: "exit_code",      SQLType: "INTEGER"},
        {Field: "hostname",       SQLType: "TEXT"},
        {Field: "working_dir",    SQLType: "TEXT"},
    },
    DisplayField: "command",
    FTSFields:    []string{"command", "note"},
}
```

### 9.2 History Import — CLI and Cron, No New Port

The `bash_history` plugin does **not** run a long-lived file watcher process and does **not**
open a new port. All ingestion is driven by the CLI binary calling into the running engine
through its existing gRPC/HTTP port, or by direct SQLite access when the engine is not
running.

#### Periodic import via cron

When the user enables the plugin with `notx plugin enable bash_history`, the engine writes
a crontab entry (or LaunchAgent plist on macOS, systemd timer on Linux) that calls the
import CLI command once per minute:

```
# Written by: notx plugin enable bash_history
* * * * * /usr/local/bin/notx snip bash_history sync
```

`notx snip bash_history sync` reads from each configured history file starting at the last
checkpointed byte offset (stored in `bash_history_offsets` in SQLite), parses new entries,
and ingests them via the engine's existing gRPC endpoint or directly into SQLite if the
engine is offline. The `--full` flag resets offsets and reimports everything.

#### Shell hook (optional, real-time)

For users who want real-time capture, `notx plugin enable bash_history --hook` also writes
a shell rc snippet that calls the CLI binary directly on every command — no curl, no HTTP
token in the environment:

```sh
# Written to ~/.zshrc by: notx plugin enable bash_history --hook
_notx_history_hook() {
  notx snip bash_history push \
    --command "$1" \
    --shell   zsh  \
    --exit    $?   \
    --dir     "$PWD" \
    --host    "$(hostname -s)" &>/dev/null &
}
autoload -Uz add-zsh-hook
add-zsh-hook precmd _notx_history_hook
```

`notx snip bash_history push` is a fast CLI call that writes to the local SQLite index
directly (bypassing gRPC) and queues an event for the next sync if the engine is not running.
No device token is exposed in the shell environment.

#### Summary

| Mechanism        | How it works                                  | When to use                    |
| ---------------- | --------------------------------------------- | ------------------------------ |
| Cron (default)   | `notx snip bash_history sync` every minute    | Always enabled with plugin     |
| Shell hook (opt) | `notx snip bash_history push` on each command | Real-time, opt-in via `--hook` |
| Manual           | `notx snip bash_history sync --full`          | Force full reimport            |

### 9.3 Command Normalization and Deduplication

#### `canonical_form`

The `canonical_form` field normalizes a command to a stable grouping key:

1. Trim and collapse whitespace
2. Replace quoted string arguments with `"*"`
3. Replace `--flag=value` / `-f value` with `--flag=*` / `-f *`
4. Replace path-like arguments (containing `/` or `~`) with `<path>`
5. Replace UUID-shaped tokens with `<id>`
6. Replace purely numeric tokens with `<n>`

Examples:

| Raw command                        | `canonical_form`                  |
| ---------------------------------- | --------------------------------- |
| `git commit -m "fix auth bug"`     | `git commit -m "*"`               |
| `git push origin feature/login`    | `git push origin <path>`          |
| `docker run -p 8080:80 nginx:1.25` | `docker run -p <n>:<n> nginx:<n>` |
| `kubectl get pod abc-123-def`      | `kubectl get pod <id>`            |
| `cd ~/projects/notx-engine`        | `cd <path>`                       |
| `grep -r "TODO" src/`              | `grep -r "*" <path>`              |

#### Deduplication model

**One snip per `canonical_form` per device.** When the watcher encounters a command whose
`canonical_form` matches an existing snip, it appends a new event to that snip recording
the new run (updating `command`, `exit_code`, `working_dir`, `duration_ms`). It does not
create a new snip. The event stream becomes the complete run history for each command family.

`OnEventAppended` updates `bash_history_stats` and `bash_history_variations` in the same
transaction:

- `bash_history_stats.run_count` increments
- `bash_history_stats.last_run_at` advances
- `bash_history_variations` upserts the exact raw command string

This lets you answer in a single SQLite query:

- **How many times was this command run?** → `run_count` on the stats row
- **What are all the exact forms I've used?** → `bash_history_variations WHERE note_urn = ?`
- **When did I last run it successfully?** → `last_run_at` + `success_count`
- **Which directories do I use it in?** → `recent_dirs` JSON array on the stats row

### 9.4 gRPC Service

```proto
// proto/snips/bash_history.proto
syntax = "proto3";
package notx.v1;
import "google/protobuf/timestamp.proto";
option go_package = "github.com/zebaqui/notx-engine/proto/snips;snipspb";

message CommandVariation {
  string command        = 1;
  int32  run_count      = 2;
  int32  last_exit_code = 3;
  google.protobuf.Timestamp last_run_at = 4;
}

message CommandStats {
  int32  run_count     = 1;
  int32  success_count = 2;
  int32  failure_count = 3;
  repeated string recent_dirs  = 4;
  repeated string recent_hosts = 5;
  google.protobuf.Timestamp first_run_at = 6;
  google.protobuf.Timestamp last_run_at  = 7;
}

message CommandRecord {
  string note_urn       = 1;
  string command        = 2; // most recent exact form
  string canonical_form = 3;
  string shell          = 4;
  string hostname       = 5;
  int32  head_sequence  = 6;
  string note           = 7; // user annotation
  repeated string tags  = 8;
  CommandStats stats    = 9;
  repeated CommandVariation variations = 10;
  google.protobuf.Timestamp created_at = 11;
  google.protobuf.Timestamp updated_at = 12;
}

service BashHistoryService {
  rpc GetCommand(GetCommandRequest) returns (GetCommandResponse);
  rpc ListCommands(ListCommandsRequest) returns (ListCommandsResponse);
  rpc SearchCommands(SearchCommandsRequest) returns (SearchCommandsResponse);
  rpc GetVariations(GetVariationsRequest) returns (GetVariationsResponse);
  rpc GetStats(GetStatsRequest) returns (GetStatsResponse);
  rpc AnnotateCommand(AnnotateCommandRequest) returns (AnnotateCommandResponse);
  rpc TriggerSync(TriggerSyncRequest) returns (TriggerSyncResponse);
}

message GetCommandRequest {
  string note_urn       = 1; // either note_urn or canonical_form required
  string canonical_form = 2;
}
message GetCommandResponse { CommandRecord command = 1; }

message ListCommandsRequest {
  string shell         = 1;
  string hostname      = 2;
  string working_dir   = 3;
  bool   failures_only = 4;
  string query         = 5;
  string sort_by       = 6; // "last_run_at" | "run_count" | "canonical_form"
  bool   sort_desc     = 7;
  int32  page_size     = 8;
  string page_token    = 9;
}
message ListCommandsResponse {
  repeated CommandRecord commands = 1;
  string next_page_token          = 2;
}

message SearchCommandsRequest {
  string query      = 1;
  int32  page_size  = 2;
  string page_token = 3;
}
message SearchCommandsResponse {
  repeated CommandRecord results = 1;
  string next_page_token         = 2;
}

message GetVariationsRequest {
  string note_urn       = 1;
  string canonical_form = 2;
}
message GetVariationsResponse {
  string canonical_form              = 1;
  repeated CommandVariation variations = 2;
}

message GetStatsRequest {
  string note_urn       = 1;
  string canonical_form = 2;
}
message GetStatsResponse {
  string canonical_form = 1;
  CommandStats stats    = 2;
}

message AnnotateCommandRequest { string note_urn = 1; string note = 2; }
message AnnotateCommandResponse { CommandRecord command = 1; }

message TriggerSyncRequest  { bool full_reimport = 1; }
message TriggerSyncResponse {
  int32 entries_ingested = 1;
  int32 snips_created    = 2;
  int32 snips_updated    = 3;
}
```

### 9.5 HTTP Routes

All routes use the engine's existing HTTP port. No new port is opened.

| Method | Path                                          | gRPC              | Description                              |
| ------ | --------------------------------------------- | ----------------- | ---------------------------------------- |
| GET    | `/v1/snips/bash_history`                      | `ListCommands`    | Paginated list, sortable, filterable     |
| GET    | `/v1/snips/bash_history/search`               | `SearchCommands`  | FTS search over command strings          |
| GET    | `/v1/snips/bash_history/:note_urn`            | `GetCommand`      | Full record with stats and variations    |
| GET    | `/v1/snips/bash_history/:note_urn/variations` | `GetVariations`   | All exact forms of one canonical command |
| GET    | `/v1/snips/bash_history/:note_urn/stats`      | `GetStats`        | Run count, success/failure, dirs         |
| PATCH  | `/v1/snips/bash_history/:note_urn/note`       | `AnnotateCommand` | Add or replace user annotation           |
| POST   | `/v1/snips/bash_history/sync`                 | `TriggerSync`     | Trigger a history sync (same as CLI)     |

Query parameters for `GET /v1/snips/bash_history`:

| Parameter       | Type   | Description                                            |
| --------------- | ------ | ------------------------------------------------------ |
| `shell`         | string | Filter to one shell                                    |
| `hostname`      | string | Filter to one hostname                                 |
| `working_dir`   | string | Filter to commands run in this directory               |
| `failures_only` | bool   | Only commands with at least one failure                |
| `q`             | string | FTS filter                                             |
| `sort_by`       | string | `last_run_at` (default), `run_count`, `canonical_form` |
| `sort_desc`     | bool   | Default: true                                          |
| `page_size`     | int    | Default: 50                                            |
| `page_token`    | string | Cursor for next page                                   |

---

## 10. Plugin Management — `notx plugin`

Plugins are **not active by default**. A user must explicitly enable a plugin. Enabling writes
the plugin's configuration into the engine's config file, applies its SQLite migrations on
next startup, and (for plugins that require periodic tasks) installs the cron/system timer.

No plugin opens a new network port. Plugins either:

- Register routes on the engine's existing HTTP and gRPC ports via `RegisterHTTP` / `RegisterGRPC`
- Expose CLI sub-commands under `notx snip <type>` for use in shell hooks and cron

### `notx plugin` sub-commands

```
notx plugin list
    List all available plugins (built-in and loaded from SnipPluginDir),
    showing name, version, description, and enabled/disabled status.

notx plugin enable <type> [flags]
    Enable a plugin. Writes config, applies migrations on next engine start,
    installs cron entry if the plugin declares a periodic task.

    Flags:
      --hook    (bash_history only) also install the shell rc hook for real-time capture

notx plugin disable <type>
    Disable a plugin. Removes cron entries and unregisters routes.
    Existing snip files are untouched — the data is never deleted by disable.

notx plugin status
    Show all enabled plugins, their version, last sync time (if applicable),
    and how many snips of each type exist.

notx plugin rebuild <type>
    Drop and rebuild the per-type SQLite index tables by replaying all snip
    events of that type. Useful after a migration or index corruption.
```

### Per-plugin CLI under `notx snip <type>`

Each plugin may expose a `notx snip <type>` sub-command for operations that need to run
outside the engine process (cron, shell hooks, CI scripts):

```
notx snip bash_history sync [--full]
    Import new entries from history files since the last checkpointed offset.
    --full resets offsets and reimports everything.

notx snip bash_history push --command <cmd> --shell <sh> --exit <code> --dir <dir>
    Ingest a single command entry. Called by the shell hook. Writes directly
    to SQLite if the engine is offline; queues for sync on next start.

notx snip todo scan [--project <urn>]
    Trigger an immediate scan of Markdown todos across configured project notes.
```

These commands communicate with the running engine through its existing gRPC/HTTP port when
available, and fall back to direct SQLite writes when offline. No new port is ever required.

---

## 11. Relationship to Notes

### 11.1 Sidecar Snip

A snip is a **sidecar** when it carries `parent_urn` + `parent_anchor`. It attaches typed
metadata to a specific location in a note without modifying the note's event stream (except
for the one-time anchor creation event when the anchor does not yet exist, which goes through
the standard `NoteRepo.AppendEvent` path).

```
Note  (urn:notx:note:AAAA)                   stored + indexed as a note
 └─ Anchor: "todo-implement-drift"  line:142
     └─ Snip (urn:notx:note:BBBB)            also stored + indexed as a note
         snip_type:     todo                   ← marks it as a typed snip
         parent_urn:    urn:notx:note:AAAA
         parent_anchor: todo-implement-drift
         status:        doing                  ← lives in snip event stream
```

The note's Markdown content is the canonical task text and checkbox state. The snip is the
canonical task lifecycle record. Neither duplicates the other.

### 11.2 Standalone Snip

A snip with no `parent_urn` is **standalone**. `bash_history` snips are always standalone.

### 11.3 Linking from Note Content

A note can embed a link token pointing to a snip using the existing vocabulary:

```
notx:lnk:id:urn:notx:note:BBBB
```

No change to the link resolution path. The link renderer checks `note.SnipType` and uses
the registered plugin's `DisplayField` to generate a brief preview.

### 11.4 What Snips Never Do to Notes

- A snip **never modifies the Note's event stream** on its own initiative. The one exception
  is `MarkDone` on a `todo` snip, which appends an event to the parent Note to toggle `[x]`
  — via `NoteRepo.AppendEvent`, the standard path.
- Snips are **not context graph participants**. No bursts are extracted, no candidate pairs
  generated. The context graph stays focused on long-form knowledge notes.

---

## 12. Snip Lifecycle

```
Created  (sequence 1 — initial YAML body set as first event via NoteRepo.CreateNote)
  │
  ├─ (sidecar) bound to parent Note anchor on creation
  │
  ▼
Active ──── events appended via NoteRepo.AppendEvent ────► Active
  │         head_sequence advances
  │         plugin's OnEventAppended updates per-type index in same transaction
  │
  ├─ soft-delete event (sets deleted = true in header)
  │
  ▼
Deleted
  │
  └─ orphan check (sidecar only):
       DetectAnchorBreaks fires on parent Note edit
       → plugin's OnParentAnchorBroken(ctx, noteURN) called
       → plugin decides: flag for review, soft-delete, or notify user
       → GC pass (manual or scheduled) may hard-delete orphaned snips
```

History is fully available: `ContentAt(seq)` returns the materialized YAML at any past
sequence using the nearest-snapshot + replay algorithm, identical to notes.

---

## 13. Differences from a Note at a Glance

| Dimension               | Note                                        | Snip                                              |
| ----------------------- | ------------------------------------------- | ------------------------------------------------- |
| Engine entity type      | `core.Note`                                 | **`core.Note`** (same type, same code paths)      |
| File format             | `.notx` — header + event stream + snapshots | **Same format, same extension**                   |
| Header identity field   | `note_urn`                                  | **`note_urn`** (identical)                        |
| Extra header fields     | —                                           | `snip_type`, `parent_anchor`                      |
| Mutability              | Append-only event log                       | **Same: append-only event log**                   |
| History                 | Full replay at any sequence                 | **Same: full replay at any sequence**             |
| Parser                  | `ParserV1`                                  | **Same: `ParserV1`** + two new header field reads |
| Repository              | `repo.NoteRepository`                       | **Same: `repo.NoteRepository`**                   |
| gRPC CRUD               | `NoteService`                               | **Same: `NoteService`** + plugin domain RPCs      |
| Content shape           | Free-form prose / Markdown                  | Typed YAML, validated at materialization          |
| Schema validation       | None                                        | Per `snip_type` schema descriptor                 |
| Context graph           | Full participant (bursts, candidates)       | Not a participant                                 |
| Internal anchor hosting | Yes (anchor table in header)                | No                                                |
| Security / E2EE         | Per-event, normal or secure                 | **Same: per-event, normal or secure**             |
| SQLite index            | `notes` table                               | **Same `notes` table** + per-type plugin tables   |
| Badger cache            | Not used                                    | Not used                                          |
| Lifecycle owner         | Core engine                                 | Core engine + plugin observer hooks               |

The key distinction is **scope, schema constraint, and plugin-provided domain APIs**. The
engine treats a snip identically to a note at every layer. The plugin layer is the only place
where "snip" and "note" are meaningfully different concepts.

---

## 14. Open Questions

1. **Schema versioning**: When a snip type's schema adds new fields, older snips will be
   missing those fields and receive their default zero values. For non-breaking additions
   this is transparent. For breaking changes (a field that now drives index behavior),
   the plugin's `Init` migration should include a rebuild step that replays all snip
   events and repopulates the per-type index. A `schema_version` field in the snip header
   is an option if finer control is needed.

2. **Snip reconciliation step** (required, not optional): When a `todo` snip is created
   offline (a checkbox added to a note while the server is unreachable), the parent Note's
   event — which triggers the anchor creation and snip creation — is queued locally. On
   reconnect, the sync layer pushes the Note events first; the `todo` plugin's
   `OnEventAppended` hook fires on the server side as those events are applied, triggering
   the scanner reconciliation pass. The client-side plugin must also reconcile its local
   SQLite index against the server's state after a sync completes. This reconciliation step
   is a defined requirement for all sidecar plugins, not just `todo`.

3. **Generic board renderer**: Should the board be a generic engine feature (any snip type
   with a `StatusField` declared gets a board automatically) or hand-coded per plugin?
   The generic approach is more powerful but increases MVP surface area. Deferred to first
   non-todo plugin that needs a board.

4. **Context graph future opt-in**: Snips are currently and permanently excluded from the
   context graph (no bursts, no candidate pairs). If a future snip type has a free-form
   text field that would benefit from semantic discovery, the `FTSFields` mechanism already
   enables direct text search. Context graph participation can be revisited when a concrete
   use case exists.
