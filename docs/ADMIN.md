# notx Admin UI

## Overview

`notx admin` serves a self-contained admin dashboard compiled into the binary. No external files are required at runtime. The frontend is a Vite + React + TypeScript SPA embedded into the Go binary via `//go:embed`. The admin server acts as a thin HTTP layer: it reverse-proxies API calls to the notx API server and serves the embedded SPA for all other requests.

Entry point: `internal/cli/admin.go` (cobra command `admin`) → `internal/admin/admin.go` (`Handler()`).

---

## Build Pipeline

The admin UI must be built before `go build` runs. `scripts/build.sh` orchestrates the full pipeline.

### Steps

**Step 1 — Build the SPA** (`--skip-ui` bypasses this step):

```bash
npm --prefix ui/admin run build
```

Runs `tsc -b && vite build` inside `ui/admin/`. Output: `ui/admin/dist/` containing:

```
ui/admin/dist/
├── index.html
└── assets/
    ├── index-<hash>.js
    └── index-<hash>.css
```

The content-hashed filenames are Vite's default cache-busting strategy. `index.html` references them with `<script type="module">` and `<link rel="stylesheet">` tags.

**Step 1b — Stage into the Go embed directory:**

```bash
rm -rf internal/admin/ui
cp -R ui/admin/dist internal/admin/ui
```

`internal/admin/ui/` is the directory targeted by the `//go:embed` directive. It is populated by the build script, never committed to source control (listed in `.gitignore`).

**Step 2 — Compile the Go binary:**

```bash
go build \
  -ldflags "-s -w \
    -X 'github.com/zebaqui/notx-engine/internal/buildinfo.Version=${VERSION}' \
    -X 'github.com/zebaqui/notx-engine/internal/buildinfo.Commit=${COMMIT}' \
    -X 'github.com/zebaqui/notx-engine/internal/buildinfo.BuildTime=${BUILD_TIME}'" \
  -o bin/notx \
  ./cmd/notx
```

The `-ldflags` inject three variables into `internal/buildinfo/buildinfo.go`:

| Variable    | Source                        | Default (no build script) |
| ----------- | ----------------------------- | ------------------------- |
| `Version`   | `$VERSION` env var            | `"dev"`                   |
| `Commit`    | `git rev-parse --short HEAD`  | `"unknown"`               |
| `BuildTime` | `date -u +%Y-%m-%dT%H:%M:%SZ` | `"unknown"`               |

These values are logged at startup by `runAdmin()` in `internal/cli/admin.go`.

### Make Targets

| Target               | Effect                                                        |
| -------------------- | ------------------------------------------------------------- |
| `make build`         | Full pipeline: UI build → embed stage → Go binary             |
| `make build-skip-ui` | Skips `npm run build`; reuses existing `ui/admin/dist/`       |
| `make build-go`      | Go binary only; no embed staging — use for rapid Go iteration |
| `make admin-build`   | Only builds the SPA (`ui/admin/dist/`); does not compile Go   |
| `make admin-dev`     | Starts the Vite dev server at `http://localhost:5173`         |
| `make clean`         | Removes `bin/notx`, `ui/admin/dist/`, `internal/admin/ui/`    |

`make build-skip-ui` fails with an error if `ui/admin/dist/` does not exist. Run the full `make build` at least once per fresh clone.

### Gitignored Artefacts

The following paths are generated at build time and are not committed:

```
/internal/admin/ui/       # Go embed staging directory
/ui/admin/dist/           # Vite output
/ui/admin/node_modules/   # npm dependencies
/bin/                     # compiled binary
```

---

## Embedded File Server (`internal/admin/admin.go`)

### Embed Directive

```go
//go:embed ui
var ui embed.FS
```

The `ui/` directory (i.e. the staged `internal/admin/ui/`) is embedded as a read-only `embed.FS` at compile time. The prefix is stripped with `fs.Sub` so that URL paths map directly to file paths within the embedded FS:

```go
sub, err := fs.Sub(ui, "ui")
```

After `fs.Sub`, path `"index.html"` maps to what was `internal/admin/ui/index.html` at embed time.

### `Handler(apiBase string) http.Handler`

`Handler` is the single exported symbol. It returns an `http.Handler` with three routing tiers, evaluated in order:

**Tier 1 — API reverse proxy.**
Paths matched by `isAPIPath()`:

| Pattern    | Match rule          |
| ---------- | ------------------- |
| `/v1/`     | `strings.HasPrefix` |
| `/healthz` | `strings.HasPrefix` |
| `/readyz`  | `strings.HasPrefix` |

Matched requests are forwarded to `apiBase` using `httputil.NewSingleHostReverseProxy`. The `Director` function rewrites the `Host` header to the target host so the upstream sees its own hostname:

```go
proxy.Director = func(r *http.Request) {
    originalDirector(r)
    r.Host = target.Host
}
```

If `apiBase` is not a valid URL, `Handler` panics at startup.

**Tier 2 — Static file serving.**
For all other paths, the handler strips the leading `/` and attempts `sub.Open(path)`. If the file exists in the embedded FS it is served verbatim via `http.FileServer(http.FS(sub))`. This covers:

- `assets/index-<hash>.js`
- `assets/index-<hash>.css`
- `favicon.svg`
- Any other file Vite emits into `dist/`

An empty path (request to `/`) is normalised to `"index.html"` before the probe.

**Tier 3 — SPA fallback.**
If neither tier 1 nor tier 2 matches, the request URL is rewritten to `/` and `index.html` is served. This is the client-side router fallback that allows deep links to work after a browser reload.

---

## CLI Command (`internal/cli/admin.go`)

### Usage

```
notx admin [flags]
```

### Flags

Defaults are seeded from the `admin.*` section of `~/.notx/config.yml` via `internal/clientconfig.Load()` before cobra parses flags. CLI flags always override config file values.

| Flag     | Type     | Config key          | Default                 | Description                              |
| -------- | -------- | ------------------- | ----------------------- | ---------------------------------------- |
| `--port` | `int`    | `admin.addr` (port) | `9090`                  | TCP port to listen on                    |
| `--host` | `string` | `admin.addr` (host) | `""` (all interfaces)   | Bind address                             |
| `--api`  | `string` | `admin.api_addr`    | `http://localhost:4060` | Base URL of the notx API server to proxy |

`portFromAddr` and `hostFromAddr` (defined in `internal/cli/admin.go`) split the `addr` string from the config to seed the two separate flags.

### Startup Output

```
▶  notx admin   →  http://0.0.0.0:9090
⇒  proxying API →  http://localhost:4060
```

The structured log line (`log.Info`) additionally records `version`, `commit`, and `built_at` from `internal/buildinfo`.

### HTTP Server Parameters

| Parameter      | Value |
| -------------- | ----- |
| `ReadTimeout`  | 15s   |
| `WriteTimeout` | 30s   |
| `IdleTimeout`  | 60s   |

### Request Logging

All requests pass through `withRequestLogger`, defined in `internal/cli/admin.go`. It wraps the root `http.ServeMux` and emits one structured log line per request at `DEBUG` level:

| Field      | Value                           |
| ---------- | ------------------------------- |
| `method`   | HTTP method                     |
| `path`     | `r.URL.Path`                    |
| `status`   | Response status code            |
| `duration` | Wall time from receipt to close |

Status capture is handled by `statusWriter`, a minimal `http.ResponseWriter` wrapper that intercepts `WriteHeader`.

### Graceful Shutdown

On `SIGINT` or `SIGTERM`:

1. The signal context fires and the shutdown path is entered.
2. `http.Server.Shutdown` is called with a 10-second context deadline.
3. In-flight requests have up to 10 seconds to complete.
4. Any error from `srv.Serve` that raced with shutdown is drained before `runAdmin` returns.

The process exits cleanly without dropping in-flight requests that complete within the drain window.

---

## Configuration

`~/.notx/config.yml` `admin` section:

```yaml
admin:
  addr: :9090 # host:port for the admin HTTP server
  api_addr: http://localhost:4060 # notx API server to proxy to
```

Edit directly or use `notx config` (interactive). The file is read once at process startup by `clientconfig.Load()`; changes require a restart.

---

## Frontend (`ui/admin/`)

### Tech Stack

| Package                 | Version | Role                             |
| ----------------------- | ------- | -------------------------------- |
| `react`                 | ^19.2.4 | UI rendering                     |
| `react-dom`             | ^19.2.4 | DOM renderer                     |
| `@tanstack/react-query` | ^5.95.2 | Server state, caching, polling   |
| `axios`                 | ^1.14.0 | HTTP client                      |
| `lucide-react`          | ^1.7.0  | Icon set                         |
| `typescript`            | ~5.9.3  | Type checking                    |
| `vite`                  | ^8.0.1  | Dev server + production bundler  |
| `@vitejs/plugin-react`  | ^6.0.1  | Vite React plugin (Fast Refresh) |

No CSS framework. All styling is hand-written in `ui/admin/src/index.css` using CSS custom properties (dark theme).

### Source Tree

```
ui/admin/src/
├── api/
│   ├── client.ts       axios instance + all API call functions
│   └── types.ts        TypeScript types mirroring Go HTTP JSON wire types
├── components/
│   └── NoteDrawer.tsx  slide-in detail panel for a single note
├── pages/
│   ├── OverviewPage.tsx   health status + note statistics dashboard
│   ├── NotesPage.tsx      paginated note list with full-text search
│   ├── ProjectsPage.tsx   project + folder management (CRUD)
│   ├── DevicesPage.tsx    device registration and revocation
│   ├── UsersPage.tsx      user management (CRUD)
│   └── ConfigPage.tsx     server configuration viewer
├── Shell.tsx           sidebar layout + topbar; renders <Outlet /> for the active route
├── main.tsx            QueryClient + TanStack Router route tree + root render
└── index.css           full design system (dark theme, CSS variables)
```

### Entry Point (`src/main.tsx`)

Creates a single `QueryClient`, builds the TanStack Router route tree, and mounts `<RouterProvider>`:

```
QueryClient defaults:
  retry:               1
  staleTime:           10 000 ms
  refetchOnWindowFocus: false
```

`QueryClientProvider` is rendered inside the root route component so every page route automatically has access to the query client.

### Routing (`src/main.tsx` + `src/Shell.tsx`)

Client-side routing uses **TanStack Router** (`@tanstack/react-router`). The full route tree is defined inline in `main.tsx`:

| Path        | Component          | Notes                                     |
| ----------- | ------------------ | ----------------------------------------- |
| `/`         | —                  | Redirects to `/overview` via `beforeLoad` |
| `/overview` | `<OverviewPage />` |                                           |
| `/notes`    | `<NotesPage />`    |                                           |
| `/projects` | `<ProjectsPage />` |                                           |
| `/devices`  | `<DevicesPage />`  |                                           |
| `/users`    | `<UsersPage />`    |                                           |
| `/config`   | `<ConfigPage />`   |                                           |

All page routes are children of a single root route whose component is `<Shell>`. `Shell` renders the sidebar and topbar and places an `<Outlet />` where the active page mounts.

The router is created with `defaultPreload: "intent"` so page components are preloaded on hover/focus of sidebar nav buttons.

**Active link detection.** `Shell` reads the current pathname via `useRouterState().location.pathname` and applies the `active` CSS class to the matching nav button. Navigation is triggered by `useNavigate()`.

**Deep links.** Because all unknown paths are served as `index.html` by the Go SPA fallback, navigating directly to e.g. `/users` or `/projects` correctly boots the router and lands on the right page. The `"/"` redirect ensures the root path always forwards to `/overview`.

**Router type registration.** `main.tsx` augments the `@tanstack/react-router` module with `interface Register { router: typeof router }` so all `useNavigate`, `Link`, and route hook calls are fully type-safe.

### API Client (`src/api/client.ts`)

An `axios` instance is created with `baseURL: "/"`. All API calls are relative to the current origin. No server address is hardcoded in the frontend code.

In development the Vite dev server (`vite.config.ts`) proxies the following paths to `http://localhost:4060`:

| Proxied path | Target                  |
| ------------ | ----------------------- |
| `/v1`        | `http://localhost:4060` |
| `/healthz`   | `http://localhost:4060` |
| `/readyz`    | `http://localhost:4060` |

In production (embedded binary) the Go admin server performs the same proxying. The frontend is unaware of which environment it is running in.

#### Exported Functions

| Function           | HTTP call                                                        | Return type            |
| ------------------ | ---------------------------------------------------------------- | ---------------------- |
| `fetchHealth()`    | `GET /healthz` + `GET /readyz` (parallel)                        | `HealthStatus`         |
| `fetchNotes()`     | `GET /v1/notes?{params}`                                         | `ListNotesResponse`    |
| `fetchNote()`      | `GET /v1/notes/{urn}`                                            | `NoteDetail`           |
| `deleteNote()`     | `DELETE /v1/notes/{urn}`                                         | `void`                 |
| `searchNotes()`    | `GET /v1/search?q={q}&{params}`                                  | `SearchNotesResponse`  |
| `fetchProjects()`  | `GET /v1/projects?{params}`                                      | `ListProjectsResponse` |
| `fetchProject()`   | `GET /v1/projects/{urn}`                                         | `Project`              |
| `createProject()`  | `POST /v1/projects`                                              | `Project`              |
| `updateProject()`  | `PATCH /v1/projects/{urn}`                                       | `Project`              |
| `deleteProject()`  | `DELETE /v1/projects/{urn}`                                      | `void`                 |
| `fetchFolders()`   | `GET /v1/folders?{params}`                                       | `ListFoldersResponse`  |
| `fetchFolder()`    | `GET /v1/folders/{urn}`                                          | `Folder`               |
| `createFolder()`   | `POST /v1/folders`                                               | `Folder`               |
| `updateFolder()`   | `PATCH /v1/folders/{urn}`                                        | `Folder`               |
| `deleteFolder()`   | `DELETE /v1/folders/{urn}`                                       | `void`                 |
| `fetchDevices()`   | `GET /v1/devices?{params}`                                       | `ListDevicesResponse`  |
| `fetchDevice()`    | `GET /v1/devices/{urn}`                                          | `Device`               |
| `registerDevice()` | `POST /v1/devices`                                               | `Device`               |
| `updateDevice()`   | `PATCH /v1/devices/{urn}`                                        | `Device`               |
| `revokeDevice()`   | `DELETE /v1/devices/{urn}`                                       | `void`                 |
| `fetchUsers()`     | `GET /v1/users?{params}`                                         | `ListUsersResponse`    |
| `fetchUser()`      | `GET /v1/users/{urn}`                                            | `User`                 |
| `createUser()`     | `POST /v1/users`                                                 | `User`                 |
| `updateUser()`     | `PATCH /v1/users/{urn}`                                          | `User`                 |
| `deleteUser()`     | `DELETE /v1/users/{urn}`                                         | `void`                 |
| `fetchMetrics()`   | `GET /v1/notes?include_deleted=true&page_size=200` (client-side) | `ServerMetrics`        |

`fetchMetrics()` does not call a dedicated metrics endpoint. It fetches up to 200 notes and computes counts client-side. `total_events` is always `0` in the current implementation (per-note GETs would be required; that is omitted for performance).

### TypeScript Types (`src/api/types.ts`)

Wire types mirror the Go HTTP JSON layer:

| Type                   | Description                                                                            |
| ---------------------- | -------------------------------------------------------------------------------------- |
| `NoteType`             | `"normal" \| "secure"`                                                                 |
| `NoteHeader`           | Metadata returned by list endpoints                                                    |
| `LineEntry`            | Single line operation (`op: "set" \| "delete"`)                                        |
| `Event`                | One event in a note's journal                                                          |
| `NoteDetail`           | Full note: `header` + `events[]`                                                       |
| `ListNotesResponse`    | `notes: NoteHeader[]` + `next_page_token: string`                                      |
| `SearchResult`         | `note: NoteHeader` + `excerpt: string`                                                 |
| `SearchNotesResponse`  | `results: SearchResult[]` + `next_page_token: string`                                  |
| `Project`              | Project record with `urn`, `name`, `description`, `deleted`, timestamps                |
| `ListProjectsResponse` | `projects: Project[]` + `next_page_token: string`                                      |
| `Folder`               | Folder record with `urn`, `project_urn`, `name`, `description`, `deleted`, timestamps  |
| `ListFoldersResponse`  | `folders: Folder[]` + `next_page_token: string`                                        |
| `Device`               | Device record with `urn`, `name`, `owner_urn`, `public_key_b64`, `revoked`, timestamps |
| `ListDevicesResponse`  | `devices: Device[]`                                                                    |
| `User`                 | User record with `urn`, `display_name`, `email?`, `deleted`, timestamps                |
| `ListUsersResponse`    | `users: User[]` + `next_page_token: string`                                            |
| `ServerConfig`         | Admin-only synthetic type; assembled from `STATIC_CONFIG`                              |
| `HealthStatus`         | `http_ok`, `ready_ok`, `checked_at` (assembled client-side)                            |
| `ServerMetrics`        | Note counts assembled from the list endpoint                                           |

`ServerConfig` and `ServerMetrics` are not returned by any server endpoint; they are constructed entirely in the frontend.

`ServerConfig` and `ServerMetrics` are not returned by any server endpoint; they are constructed entirely in the frontend.

---

## Pages

### OverviewPage (`src/pages/OverviewPage.tsx`)

Displays server health and note statistics.

**Queries:**

| Query key     | Function         | Poll interval | Description                        |
| ------------- | ---------------- | ------------- | ---------------------------------- |
| `["health"]`  | `fetchHealth()`  | 15 000 ms     | Liveness + readiness probe results |
| `["metrics"]` | `fetchMetrics()` | 30 000 ms     | Aggregated note counts             |

**Health probes.** `fetchHealth()` fires `GET /healthz` and `GET /readyz` concurrently via `Promise.allSettled`. Either can fail independently without masking the other result. Results surface as two `HealthCard` components showing a coloured indicator dot and an `up` / `down` badge.

**Note statistics.** `fetchMetrics()` calls `fetchNotes({ page_size: 200, include_deleted: true })` and computes:

| Metric        | Derivation                                     |
| ------------- | ---------------------------------------------- |
| Total         | `notes.length`                                 |
| Active        | `total − deleted`                              |
| Normal        | `filter(n => n.note_type === "normal").length` |
| Secure        | `filter(n => n.note_type === "secure").length` |
| Deleted       | `filter(n => n.deleted).length`                |
| Deletion rate | `(deleted / total) × 100`, formatted to 1 d.p. |

A manual **Refresh** button triggers both query refetches. An error banner is shown if either query is in an error state, indicating the server is unreachable.

---

### NotesPage (`src/pages/NotesPage.tsx`)

Paginated note list with full-text search.

**State:**

| State variable   | Purpose                                         |
| ---------------- | ----------------------------------------------- |
| `searchRaw`      | Raw input field value                           |
| `pageToken`      | Current cursor token                            |
| `tokenHistory`   | Array of cursor tokens indexed by page number   |
| `pageIndex`      | 0-based current page number                     |
| `includeDeleted` | Whether soft-deleted notes are included         |
| `selectedNote`   | The `NoteHeader` currently open in `NoteDrawer` |

**Search debounce.** `searchRaw` is fed through a `useDebounced` hook (350 ms delay). When the debounced value is non-empty, the list query is disabled and the search query is enabled. This prevents a request on every keystroke.

**List query** (active when not searching):

```
GET /v1/notes?page_size=20&page_token={pageToken}&include_deleted={bool}
```

Query key: `["notes", "list", pageToken, includeDeleted]`

**Search query** (active when `searchQuery.trim().length > 0`):

```
GET /v1/search?q={searchQuery}&page_size=20
```

Query key: `["notes", "search", searchQuery]`

**Pagination.** Cursor-based via `next_page_token`. `tokenHistory` stores the token for each visited page so backward navigation (`goPrev`) can restore the correct cursor without re-fetching all prior pages. Pagination controls are hidden during search mode.

**Table columns:** Name (with note-type icon), URN, Type badge, Status badge, Created, Updated.

Clicking any row sets `selectedNote` and opens `NoteDrawer`.

---

### UsersPage (`src/pages/UsersPage.tsx`)

Full CRUD management for user records.

**Queries:**

| Query key         | Function                          | Description                               |
| ----------------- | --------------------------------- | ----------------------------------------- |
| `["users", bool]` | `fetchUsers({ include_deleted })` | Paginated user list; re-fetched on toggle |

**State:**

| State variable   | Purpose                                                |
| ---------------- | ------------------------------------------------------ |
| `search`         | Client-side filter string (display_name / email / URN) |
| `includeDeleted` | Whether soft-deleted users are shown                   |
| `selected`       | URN of the user row currently open in the detail panel |
| `showCreate`     | Whether the "New User" creation modal is open          |
| `deleting`       | URN of the user pending soft-delete confirmation       |

**Create flow.** Clicking **New User** opens `UserCreateModal`. The modal auto-generates a `notx:usr:<uuid-v4>` URN, accepts a required `display_name` and optional `email`, then calls `POST /v1/users`. On success the query is invalidated and the modal closes.

**Detail panel.** Clicking a table row opens `UserPanel` — a right-side slide-in with all fields displayed. An inline edit form allows updating `display_name` and `email` via `PATCH /v1/users/{urn}`. The **Delete** button opens `ConfirmDeleteModal` which calls `DELETE /v1/users/{urn}` (soft-delete).

**Filtering.** `include_deleted` toggle controls the server-side query parameter. The `search` input filters the returned list client-side across `display_name`, `email`, and the full URN string.

**Table columns:** URN (last 8 hex chars of UUID prefixed with `…`), Display Name, Email, Status badge (`active` / `deleted`), Created.

---

### ConfigPage (`src/pages/ConfigPage.tsx`)

Displays server configuration and live health probe results.

This page does not call a `/admin/config` endpoint — none exists yet. Configuration values are read from `STATIC_CONFIG`, a constant defined in the file that reflects the compiled-in defaults from `internal/server/config/config.go`:

| Field                | Value shown |
| -------------------- | ----------- |
| `http_port`          | `4060`      |
| `grpc_port`          | `50051`     |
| `host`               | `0.0.0.0`   |
| `data_dir`           | `./data`    |
| `enable_http`        | `true`      |
| `enable_grpc`        | `true`      |
| `tls_enabled`        | `false`     |
| `mtls_enabled`       | `false`     |
| `shutdown_timeout_s` | `30`        |
| `max_page_size`      | `200`       |
| `default_page_size`  | `50`        |
| `log_level`          | `"info"`    |

**Live health section.** The page independently polls `["health"]` (same query key as `OverviewPage`, served from the React Query cache) and overlays the real `/healthz` and `/readyz` results in a "Live probe results" table at the bottom of the page.

When the server is unreachable an error banner reads: _"Server unreachable — configuration shown below reflects compiled defaults."_

---

### NoteDrawer (`src/components/NoteDrawer.tsx`)

Slide-in detail panel triggered by clicking a row in `NotesPage`.

**Props:**

| Prop      | Type         | Description                   |
| --------- | ------------ | ----------------------------- |
| `note`    | `NoteHeader` | The note to display           |
| `onClose` | `() => void` | Called when the drawer closes |

**Detail query:**

```
GET /v1/notes/{urn}
```

Query key: `["note", note.urn]`

The query is disabled (`enabled: false`) when `note.note_type === "secure"`. The server never has plaintext for secure notes so there is nothing useful to fetch.

**Content reconstruction.** For normal notes, the event stream from `NoteDetail.events` is replayed in sequence order to reconstruct the current document state:

```
lines: Map<line_number, content>

for each event in events:
  for each entry in event.entries:
    if entry.op === "set":   lines.set(entry.line_number, entry.content)
    if entry.op === "delete": lines.delete(entry.line_number)

result: sort by line_number, join values with "\n"
```

This mirrors the replay algorithm described in `NOTX_FORMAT.md`.

**Sections rendered:**

| Section         | Normal note                                         | Secure note                              |
| --------------- | --------------------------------------------------- | ---------------------------------------- |
| Status badges   | Type + active/deleted                               | Type (yellow lock icon) + active/deleted |
| Metadata table  | URN, type, project URN, folder URN, timestamps      | Same                                     |
| Event stream    | Per-event rows: seq, author URN, time, entry counts | "Event stream is encrypted" notice       |
| Content preview | Reconstructed plaintext                             | Not rendered                             |

**Dismissal.** Clicking the overlay backdrop (outside the drawer panel) calls `onClose`. The `X` button in the drawer header also calls `onClose`.

---

## Development Workflow

### Hot-reload dev server

```bash
# Terminal 1 — notx API server
notx server

# Terminal 2 — Vite dev server
make admin-dev   # → http://localhost:5173
```

`make admin-dev` runs `cd ui/admin && npm run dev`. Vite proxies `/v1/*`, `/healthz`, and `/readyz` to `http://localhost:4060` as configured in `ui/admin/vite.config.ts`. Fast Refresh is active; React component changes reflect instantly without a full page reload.

npm dependencies are not installed automatically by `make admin-dev`. Install them first if `node_modules/` is absent:

```bash
make admin-install   # → cd ui/admin && npm install
```

### Embedded production build

```bash
make build
notx admin   # → http://localhost:9090 (proxies API to :4060)
```

### Iterating on Go only (UI unchanged)

```bash
make build-skip-ui
notx admin
```

Requires `ui/admin/dist/` to already exist from a prior `make build` or `make admin-build`.

### Iterating on the SPA only (Go unchanged)

```bash
make admin-build      # rebuilds ui/admin/dist/
make build-skip-ui    # re-stages and recompiles Go binary
notx admin
```

---

## Port Reference

| Service         | Default port | Configured by                      |
| --------------- | ------------ | ---------------------------------- |
| notx admin UI   | `9090`       | `admin.addr` / `--port`            |
| notx HTTP API   | `4060`       | `server.http_port` / `--http-port` |
| notx gRPC API   | `50051`      | `server.grpc_port` / `--grpc-port` |
| Vite dev server | `5173`       | `ui/admin/vite.config.ts`          |

---

## Known Limitations

- **`ConfigPage` shows compiled-in defaults.** There is no `/admin/config` endpoint. The configuration table always reflects the values hardcoded in `STATIC_CONFIG` in `src/pages/ConfigPage.tsx`, not the server's actual runtime configuration. A real config endpoint would need to be added to `internal/server/http/handler.go` and wired through `fetchConfig()` in `src/api/client.ts`.

- **`total_events` is always 0.** `fetchMetrics()` intentionally omits the per-note event count because fetching it would require one `GET /v1/notes/{urn}` per note. The field is present in `ServerMetrics` for forward compatibility.

- **`OverviewPage` metrics cap at 200 notes.** `fetchMetrics()` passes `page_size=200` (the API maximum). Note counts above 200 will be under-reported. A dedicated server-side metrics endpoint would resolve this.
