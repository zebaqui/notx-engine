# notx CLI

## Overview

`notx` is a command-line client for the notx engine. It can create notes by uploading files over gRPC, manage server and client configuration, start the HTTP+gRPC server, and serve the admin UI. All sub-commands read defaults from `~/.notx/config.json` before cobra parses flags, so effective defaults shown in `--help` output reflect the user's actual configuration.

Entry point: `cmd/notx/main.go` → `internal/cli.Execute()` → cobra root command in `internal/cli/root.go`.

---

## Configuration

### File location

```
~/.notx/config.json
```

The directory is created with mode `0700` if it does not exist. The file is written atomically via a temp-file rename. `notx server` creates the file automatically on first run if it does not exist — see [`EnsureConfig`](#ensureconfig) below.

### Package and struct

**Package**: `internal/clientconfig/config.go`  
**Struct**: `Config`

```json
{
  "client": {
    "grpc_addr": "localhost:50051",
    "namespace": "notx",
    "insecure": true
  },
  "server": {
    "http_addr": ":4060",
    "grpc_addr": ":50051",
    "enable_http": true,
    "enable_grpc": true,
    "shutdown_timeout_sec": 30
  },
  "admin": {
    "addr": ":9090",
    "api_addr": "http://localhost:4060"
  },
  "storage": {
    "data_dir": "~/.notx/data"
  },
  "tls": {
    "cert_file": "",
    "key_file": "",
    "ca_file": ""
  },
  "log": {
    "level": "info"
  }
}
```

### Load

`clientconfig.Load()` reads `~/.notx/config.json` and unmarshals it on top of `Default()`, so any field not present in the file retains its default value. If the file does not exist, `Load()` returns `Default()` silently — no error is returned and no file is written.

Every cobra command seeds its flag defaults from `clientconfig.Load()` inside its `init()` function. This means `notx server --help` shows the actual effective values from the config file, not hard-coded defaults.

### Save

`clientconfig.Save(cfg)` writes atomically to `~/.notx/config.json`:

1. Marshal `cfg` to pretty-printed JSON (`json.MarshalIndent`, 4-space indent).
2. Append a trailing newline.
3. Write to a temp file in the same directory.
4. `os.Rename()` the temp file over the target path.

The directory is created with mode `0700` if missing.

### EnsureConfig

`clientconfig.EnsureConfig()` creates `~/.notx/config.json` from built-in defaults if and only if the file does not already exist. It returns `(created bool, err error)`.

`notx server` calls this at startup before any other work. This means running `notx server` on a fresh machine is always safe — the config file, data directories, and admin device are all initialised automatically. All other commands (`notx admin`, `notx add`, etc.) call `Load()` which silently uses defaults when the file is absent, so they are unaffected if `notx server` has not been run yet.

### TLS helpers

| Function        | Condition                              | Result |
| --------------- | -------------------------------------- | ------ |
| `TLSEnabled()`  | `cert_file != ""` AND `key_file != ""` | `true` |
| `MTLSEnabled()` | `TLSEnabled()` AND `ca_file != ""`     | `true` |

---

## Commands

### `notx [file] [flags]` — default command

When the root command receives a positional argument that does not match a sub-command, cobra calls `rootCmd.RunE = runAddNoteFromRoot`, which delegates to `runAddNote`. This is the primary ergonomic entry point for creating notes.

```bash
notx meeting-notes.txt
notx meeting-notes.txt --delete
notx meeting-notes.txt --secure --addr localhost:50051
```

If no arguments are given, `cmd.Help()` is printed and the process exits.

`rootCmd.SilenceErrors = true` and `rootCmd.SilenceUsage = true` suppress cobra's default error output so a custom error handler can control formatting.

Flags are identical to `notx add` (see below) and are mirrored on the root command so they work without specifying the `add` sub-command.

---

### `notx add <file> [flags]`

**File**: `internal/cli/addnote.go`

Creates a note from a local file by connecting to the notx gRPC server, creating a note header, then appending the file's content as a single event.

#### Flags

| Flag       | Short | Type     | Description                                           |
| ---------- | ----- | -------- | ----------------------------------------------------- |
| `--addr`   |       | `string` | Override `client.grpc_addr` for this invocation only  |
| `--delete` | `-d`  | `bool`   | Delete the source file after successful note creation |
| `--secure` |       | `bool`   | Create a secure note (`NoteType = NOTE_TYPE_SECURE`)  |

#### Execution flow

1. **Resolve path** — `filepath.Abs(args[0])` + `os.Stat()`. Exits with an error if the path does not exist or is a directory.
2. **Derive note name** — strips the file extension from the basename. `meeting-notes.txt` → `"meeting-notes"`.
3. **Read lines** — `readLines()` uses a `bufio.Scanner` and strips trailing blank lines from the result.
4. **Load config** — `clientconfig.Load()` provides `grpc_addr` and `namespace`. The `--addr` flag overrides `grpc_addr` if provided.
5. **Build credentials** — `buildClientCredentials(cfg)` (see [gRPC client credentials](#grpc-client-credentials) below).
6. **Dial** — `grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(creds))`. The dial is lazy and non-blocking.
7. **Create stub** — `pb.NewNoteServiceClient(conn)`.
8. **CreateNote RPC** — `client.CreateNote(ctx, &pb.CreateNoteRequest{Header: &pb.NoteHeader{...}})`. The note URN is `<namespace>:note:<uuid>` where UUID is a random v4 generated by `github.com/google/uuid`.
9. **AppendEvent RPC** — only executed if the file has at least one line. `client.AppendEvent(ctx, &pb.AppendEventRequest{Event: &pb.EventProto{...}})`:
   - Event URN: `<namespace>:event:<uuid>` (fresh random v4)
   - Author URN: `<namespace>:usr:anon`
   - Sequence: `1`
   - One `LineEntryProto` per line; `op=0` (SET) for non-blank lines, `op=1` (SET_EMPTY) for blank lines
10. **Print result** — a formatted table showing name, URN, type, line count, and server address.
11. **Delete source** — if `--delete` was passed, `os.Remove(absPath)` is called. This only runs after both RPCs have succeeded.

**Context timeout**: 30 seconds for both RPCs.

---

### `notx config`

**File**: `internal/cli/config.go`

Manages the configuration file at `~/.notx/config.yml`. Three modes of operation depending on whether a sub-command is provided.

#### `notx config` (no sub-command) — interactive editor

Walks through every config field interactively:

- Prints the field label, hint, and current value (current value rendered in cyan).
- Pressing Enter with no input keeps the current value.
- For optional TLS string fields: enter `-` to clear the field to an empty string.
- Bool fields accept: `true`, `false`, `yes`, `no`, `1`, `0`, `y`, `n`.
- Enum fields (e.g. `log.level`) validate input against the allowed set and re-prompt on invalid input.

After all fields are collected, a summary table is displayed and the user is prompted `Save? [Y/n]`. Entering `Y` or pressing Enter calls `clientconfig.Save(cfg)`.

Prompt helpers used internally: `promptString`, `promptStringAllowEmpty`, `promptBool`, `promptInt`, `promptEnum`.

#### `notx config show`

Prints the currently effective configuration as a formatted table grouped by section. The config file path is shown at the top of the output. Config is loaded via `clientconfig.Load()` — if the file does not exist, the defaults are shown.

#### `notx config reset`

Prompts for confirmation, then calls `clientconfig.Save(clientconfig.Default())`, overwriting `~/.notx/config.json` with the compiled-in defaults.

---

### `notx server [flags]`

**File**: `internal/cli/server.go`

Starts the notx HTTP+gRPC server. On first run, calls `clientconfig.EnsureConfig()` to create `~/.notx/config.json` if absent, then prints a notice to stdout. Flag defaults are seeded from the `server.*` section of the config file. See `docs/SERVER.md` for the complete server reference.

---

### `notx admin [flags]`

**File**: `internal/cli/admin.go`

Serves the embedded admin SPA and reverse-proxies API requests to the notx API server. Flag defaults are seeded from the `admin.*` section of `~/.notx/config.json`. See `docs/ADMIN.md` for the complete admin reference.

---

### `notx info <file.notx>`

**File**: `internal/cli/info.go`

Parses a `.notx` file on disk via `core.NewNoteFromFile()` and passes the result to `tui.DisplayAnalysis()` for formatted terminal output. Does not require a running server.

```bash
notx info ~/.notx/data/notes/notx_note_abc123.notx
```

---

### `notx validate <file.notx>`

**File**: `internal/cli/validate.go`

Parses a `.notx` file, runs `validate.Validate()` against it, and calls `tui.DisplayValidationReport()`. Does not require a running server.

Exits with code `1` if validation fails. Exits with code `0` if the file is valid.

```bash
notx validate ~/.notx/data/notes/notx_note_abc123.notx
echo $?   # 0 = valid, 1 = invalid
```

---

## gRPC Client Credentials

`buildClientCredentials(cfg)` in `internal/cli/addnote.go` selects transport credentials for the gRPC dial based on the config state:

| Condition                                                 | Credentials used                                                                                                   |
| --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `cfg.Client.Insecure == true` AND `TLSEnabled() == false` | `insecure.NewCredentials()` — plaintext, no TLS                                                                    |
| `TLSEnabled() == true`                                    | `credentials.NewTLS(...)` — TLS 1.3, cert+key loaded from config; CA pool loaded from `ca_file` if `MTLSEnabled()` |
| Neither of the above (fallback)                           | `credentials.NewTLS(...)` with system roots — TLS, no client cert                                                  |

For mTLS, the `ca_file` PEM is used to verify the server certificate. The client cert (`cert_file` + `key_file`) is presented to the server on the TLS handshake.

---

## URN Generation

All URNs are generated at the CLI side before the RPC is sent. The server stores whatever URNs the client supplies.

| URN        | Format                        | Example                                           |
| ---------- | ----------------------------- | ------------------------------------------------- |
| Note URN   | `<namespace>:note:<uuid-v4>`  | `notx:note:f47ac10b-58cc-4372-a567-0e02b2c3d479`  |
| Event URN  | `<namespace>:event:<uuid-v4>` | `notx:event:550e8400-e29b-41d4-a716-446655440000` |
| Author URN | `<namespace>:usr:anon`        | `notx:usr:anon`                                   |

`<namespace>` is read from `cfg.Client.Namespace` (default: `"notx"`). UUIDs are generated with `github.com/google/uuid` (`uuid.New().String()`). The author URN is a fixed anonymous sentinel — `notx add` does not support authenticated authorship.

---

## Build Info

**File**: `internal/buildinfo/buildinfo.go`

Three package-level variables are declared with fallback values:

```go
var (
    Version   = "dev"
    Commit    = "unknown"
    BuildTime = "unknown"
)
```

`scripts/build.sh` injects real values at link time via `-ldflags`:

```
-X 'github.com/zebaqui/notx-engine/internal/buildinfo.Version=${VERSION}'
-X 'github.com/zebaqui/notx-engine/internal/buildinfo.Commit=${COMMIT}'
-X 'github.com/zebaqui/notx-engine/internal/buildinfo.BuildTime=${BUILD_TIME}'
```

| Variable    | Source                        | Fallback    |
| ----------- | ----------------------------- | ----------- |
| `Version`   | `$VERSION` env var            | `"dev"`     |
| `Commit`    | `git rev-parse --short HEAD`  | `"unknown"` |
| `BuildTime` | `date -u +%Y-%m-%dT%H:%M:%SZ` | `"unknown"` |

These values appear in the `notx admin` startup log as structured fields: `"version"`, `"commit"`, `"built_at"`. When building with `make build-go` (raw `go build`, no build script), all three variables retain their fallback values.

---

## Build System

### `scripts/build.sh`

Orchestrates the full build pipeline. Accepts two optional flags:

```bash
scripts/build.sh [--skip-ui] [--output <path>]
```

| Flag              | Effect                                                            |
| ----------------- | ----------------------------------------------------------------- |
| `--skip-ui`       | Skips `npm run build`. Errors if `ui/admin/dist/` does not exist. |
| `--output <path>` | Destination for the compiled binary. Default: `bin/notx`.         |

Pipeline steps when run without flags:

1. `npm --prefix ui/admin run build` — compiles the admin SPA into `ui/admin/dist/`.
2. `rm -rf internal/admin/ui && cp -R ui/admin/dist internal/admin/ui` — stages the build output into the Go embed directory.
3. `go build -ldflags "..." -o bin/notx ./cmd/notx` — compiles the binary with injected build info.

### Make targets

| Target                | What it does                                                                           |
| --------------------- | -------------------------------------------------------------------------------------- |
| `make build`          | Full pipeline: `scripts/build.sh` — admin UI build, embed stage, Go binary             |
| `make build-skip-ui`  | `scripts/build.sh --skip-ui` — skips `npm run build`, reuses existing `ui/admin/dist/` |
| `make build-go`       | Raw `go build ./cmd/notx` only — no UI step, no embed staging, no ldflags injection    |
| `make admin-dev`      | `cd ui/admin && npm run dev` — Vite hot-reload dev server on `:5173`                   |
| `make admin-install`  | `npm install` inside `ui/admin/`                                                       |
| `make admin-build`    | `npm run build` inside `ui/admin/` — produces `ui/admin/dist/` without compiling Go    |
| `make generate-proto` | Regenerates `.pb.go` files from `internal/server/proto/notx.proto`                     |
| `make clean`          | Removes `bin/notx`, `ui/admin/dist/`, `internal/admin/ui/`                             |

`make build-skip-ui` is the right target when iterating on Go only (UI unchanged). `make build-go` skips embed staging entirely — the binary will not contain the admin UI and `notx admin` will fail at runtime unless the embed directory already exists from a prior full build.

---

## Command Routing Summary

| Input                        | Cobra route           | Handler                             |
| ---------------------------- | --------------------- | ----------------------------------- |
| `notx meeting-notes.txt`     | `rootCmd.RunE`        | `runAddNoteFromRoot` → `runAddNote` |
| `notx add meeting-notes.txt` | `addNoteCmd.RunE`     | `runAddNote`                        |
| `notx config`                | `configCmd.RunE`      | interactive editor                  |
| `notx config show`           | `configShowCmd.RunE`  | print table                         |
| `notx config reset`          | `configResetCmd.RunE` | confirm + save defaults             |
| `notx server`                | `serverCmd.RunE`      | start HTTP+gRPC server              |
| `notx admin`                 | `adminCmd.RunE`       | start admin UI server               |
| `notx info <file>`           | `infoCmd.RunE`        | parse + display analysis            |
| `notx validate <file>`       | `validateCmd.RunE`    | parse + validate, exit 1 on failure |
| `notx` (no args)             | `rootCmd.RunE`        | `cmd.Help()`                        |
