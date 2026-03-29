# notx API Reference

A complete reference for all client-facing endpoints exposed by the notx engine.
The server provides two transports that are functionally equivalent — choose the one
that fits your client stack.

| Transport | Default port | Format           |
| --------- | ------------ | ---------------- |
| HTTP/JSON | `4060`       | REST + JSON      |
| gRPC      | `50051`      | Protocol Buffers |

Both ports and the bind host are configurable at startup (see `--http-port`,
`--grpc-port`, `--host`).

---

## Contents

1. [Common concepts](#1-common-concepts)
2. [HTTP API](#2-http-api)
   - [Notes](#21-notes)
   - [Events](#22-events)
   - [Search](#23-search)
   - [Projects](#24-projects)
   - [Folders](#25-folders)
   - [Health probes](#26-health-probes)
3. [gRPC API](#3-grpc-api)
   - [NoteService](#31-noteservice)
   - [ProjectService](#32-projectservice)
   - [DeviceService](#33-deviceservice)
4. [Shared types](#4-shared-types)
5. [Error handling](#5-error-handling)

---

## 1. Common concepts

### URNs

Every resource is identified by a URN with the following format:

```
<namespace>:<object-type>:<uuid>
```

| Segment       | Rules                                                            | Example                                |
| ------------- | ---------------------------------------------------------------- | -------------------------------------- |
| `namespace`   | lowercase alphanumeric + hyphens, 1–63 chars                     | `notx`                                 |
| `object-type` | one of `note`, `event`, `usr`, `org`, `proj`, `folder`, `device` | `note`                                 |
| `uuid`        | standard UUID (8-4-4-4-12 hex) or the sentinel `anon`            | `1a9670dd-1a65-481a-ad17-03d77de021e5` |

Full example: `notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5`

When URNs appear in HTTP URL path segments they must be **percent-encoded**
(`:` → `%3A`).

### Note types

| Value    | Description                                                          |
| -------- | -------------------------------------------------------------------- |
| `normal` | Plaintext note. Content is readable by the server and admin.         |
| `secure` | End-to-end encrypted note. The server stores opaque ciphertext only. |

`note_type` is **immutable** after creation.

### Projects and folders

Projects and folders are **index-only** entities — they have no `.notx` file on
disk. They exist solely in the Badger index and are used to organise notes
logically.

- A **project** (`notx:proj:<uuid>`) is the top-level grouping.
- A **folder** (`notx:folder:<uuid>`) belongs to exactly one project and can
  contain notes.
- A note references its project and folder via the `project_urn` and
  `folder_urn` fields on its header. The note itself stores those URNs; the
  project and folder records hold no back-references to notes.
- Deleting a project or folder is a **soft-delete** — the record is marked
  `deleted: true` and remains visible with `include_deleted=true`.

### Pagination

List and search endpoints use opaque cursor-based pagination.

- Pass `page_size` (integer, default `50`, max `200`) to control page length.
- If the response includes a non-empty `next_page_token`, pass it as
  `page_token` on the next request to fetch the following page.
- An empty or absent `next_page_token` means you have reached the last page.

### Timestamps

All timestamps are **RFC 3339 / ISO 8601** strings in UTC, e.g.
`2025-01-15T11:00:00Z`.

---

## 2. HTTP API

Base path: `/v1`

All request and response bodies are JSON. Successful responses always carry
`Content-Type: application/json`. Error bodies follow the shape:

```json
{ "error": "<human-readable message>" }
```

---

### 2.1 Notes

#### List notes

```
GET /v1/notes
```

Returns a paginated list of note headers.

**Query parameters**

| Parameter         | Type    | Required | Description                                           |
| ----------------- | ------- | -------- | ----------------------------------------------------- |
| `page_size`       | integer | no       | Number of results per page (default `50`, max `200`). |
| `page_token`      | string  | no       | Cursor returned by a previous response.               |
| `project_urn`     | string  | no       | Filter to notes belonging to this project URN.        |
| `folder_urn`      | string  | no       | Filter to notes inside this folder URN.               |
| `note_type`       | string  | no       | Filter by type: `normal` or `secure`.                 |
| `include_deleted` | boolean | no       | When `true`, soft-deleted notes are included.         |

**Response `200 OK`**

```json
{
  "notes": [ <NoteHeader>, ... ],
  "next_page_token": "<string or empty>"
}
```

---

#### Create note

```
POST /v1/notes
```

**Request body**

```json
{
  "urn": "notx:note:<uuid>",
  "name": "Meeting notes",
  "note_type": "normal",
  "project_urn": "notx:proj:<uuid>",
  "folder_urn": "notx:folder:<uuid>"
}
```

| Field         | Type   | Required | Description                         |
| ------------- | ------ | -------- | ----------------------------------- |
| `urn`         | string | **yes**  | Client-assigned URN for the note.   |
| `name`        | string | **yes**  | Human-readable display name.        |
| `note_type`   | string | **yes**  | `"normal"` or `"secure"`.           |
| `project_urn` | string | no       | Associate with an existing project. |
| `folder_urn`  | string | no       | Place inside an existing folder.    |

**Response `201 Created`**

```json
{
  "note": <NoteHeader>
}
```

**Errors**

| Status | Condition                            |
| ------ | ------------------------------------ |
| `400`  | Missing or invalid fields.           |
| `409`  | A note with this URN already exists. |

---

#### Get note

```
GET /v1/notes/:urn
```

Returns the note's metadata and its current fully-materialised content.
For `secure` notes, `content` is always an empty string — content is
end-to-end encrypted and never readable by the server.

**Path parameter**: `:urn` — percent-encoded note URN.

**Response `200 OK`**

```json
{
  "header":  <NoteHeader>,
  "content": "# Meeting notes\n\nLine one\nLine two"
}
```

**Errors**

| Status | Condition       |
| ------ | --------------- |
| `404`  | Note not found. |

---

#### Update note

```
PATCH /v1/notes/:urn
```

Partially updates note metadata. Only the fields present in the request body
are changed. `note_type` cannot be changed after creation.

**Request body** (all fields optional)

```json
{
  "name": "Renamed title",
  "project_urn": "notx:proj:<uuid>",
  "folder_urn": "notx:folder:<uuid>",
  "deleted": false
}
```

Pass an empty string (`""`) for `project_urn` or `folder_urn` to clear the
association.

**Response `200 OK`**

```json
{
  "note": <NoteHeader>
}
```

**Errors**

| Status | Condition            |
| ------ | -------------------- |
| `400`  | Invalid field value. |
| `404`  | Note not found.      |

---

#### Delete note

```
DELETE /v1/notes/:urn
```

Soft-deletes a note (sets `deleted: true`). The note remains retrievable
with `include_deleted=true` and its event stream is preserved.

**Response `200 OK`**

```json
{
  "deleted": true
}
```

**Errors**

| Status | Condition       |
| ------ | --------------- |
| `404`  | Note not found. |

---

#### Replace content

```
POST /v1/notes/:urn/content
```

Replaces the full content of a note by computing a diff against the current
state and appending a single event containing only the changed lines. If the
submitted content is identical to the current state no event is written and
`changed` is `false`.

This is the recommended way for clients that work with whole documents (e.g.
a CLI piping a file, or an editor saving a buffer) rather than constructing
line-level diffs manually.

Not supported for `secure` notes — content must be encrypted client-side and
submitted via `POST /v1/events` instead.

**Path parameter**: `:urn` — percent-encoded note URN.

**Request body**

```json
{
  "content": "# Title\n\nParagraph one.\nParagraph two.",
  "author_urn": "notx:usr:4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d"
}
```

| Field        | Type   | Required | Description                                                         |
| ------------ | ------ | -------- | ------------------------------------------------------------------- |
| `content`    | string | **yes**  | The complete new document text. Lines are separated by `\n`.        |
| `author_urn` | string | no       | URN of the author. Defaults to `<namespace>:usr:anon` when omitted. |

**Response `201 Created`** — content changed

```json
{
  "note_urn": "notx:note:<uuid>",
  "sequence": 4,
  "changed": true
}
```

**Response `200 OK`** — content identical, no event written

```json
{
  "note_urn": "notx:note:<uuid>",
  "sequence": 3,
  "changed": false
}
```

| Field      | Type    | Description                                              |
| ---------- | ------- | -------------------------------------------------------- |
| `note_urn` | string  | URN of the note.                                         |
| `sequence` | integer | Head sequence after the call (new event seq if changed). |
| `changed`  | bool    | `true` if a new event was written.                       |

**Errors**

| Status | Condition                                                        |
| ------ | ---------------------------------------------------------------- |
| `400`  | Invalid request body, invalid `author_urn`, or note is `secure`. |
| `404`  | Note not found.                                                  |
| `409`  | Concurrent write conflict — retry with the latest content.       |

**Diff behaviour**

The server performs a line-by-line comparison between the stored document and
the submitted content:

- Lines that are **unchanged** produce no entry.
- Lines that **changed** produce a `set` (or `set_empty` for blank lines) entry.
- Lines that are **added** (new document is longer) produce `set` / `set_empty` entries.
- Lines that are **removed** (new document is shorter) produce `delete` entries,
  applied highest-to-lowest so index arithmetic is stable on replay.

The resulting `LineEntry` list is identical to what you would pass to
`POST /v1/events` manually — the event stream stays canonical and can always be
replayed from scratch.

---

### 2.2 Events

#### Append event

```
POST /v1/events
```

Appends a new event to a note's event stream. Use this when you have already
computed the exact line-level diff you want to record, or when writing to a
`secure` note. For whole-document updates from a normal note prefer
`POST /v1/notes/:urn/content` which handles the diff automatically.

Each event carries one or more line-level operations (`set`, `set_empty`,
`delete`) that are applied in order to produce the new document state.

**Request body**

```json
{
  "note_urn": "notx:note:<uuid>",
  "sequence": 1,
  "author_urn": "notx:usr:<uuid>",
  "created_at": "2025-01-15T11:00:00Z",
  "entries": [
    { "op": "set", "line_number": 1, "content": "# Title" },
    { "op": "set", "line_number": 2, "content": "First paragraph." },
    { "op": "delete", "line_number": 3 }
  ],
  "expect_sequence": 0
}
```

| Field             | Type        | Required | Description                                                                                                                        |
| ----------------- | ----------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `note_urn`        | string      | **yes**  | URN of the target note.                                                                                                            |
| `sequence`        | integer     | **yes**  | Sequence number for this event. Must be `>= 1`.                                                                                    |
| `author_urn`      | string      | **yes**  | URN of the user or device authoring this event.                                                                                    |
| `created_at`      | string      | no       | RFC 3339 timestamp. Defaults to server time if omitted.                                                                            |
| `entries`         | LineEntry[] | **yes**  | One or more line operations. Must not be empty.                                                                                    |
| `expect_sequence` | integer     | no       | Optimistic concurrency guard. If non-zero, the append is rejected when the note's current head sequence does not match this value. |

**LineEntry fields**

| Field         | Type    | Required                  | Description                            |
| ------------- | ------- | ------------------------- | -------------------------------------- |
| `op`          | string  | **yes**                   | `"set"`, `"set_empty"`, or `"delete"`. |
| `line_number` | integer | **yes**                   | 1-based line number. Must be `>= 1`.   |
| `content`     | string  | required for `"set"` only | Text content of the line.              |

**Response `201 Created`**

```json
{
  "sequence": 1
}
```

**Errors**

| Status | Condition                                                        |
| ------ | ---------------------------------------------------------------- |
| `400`  | Missing or invalid fields.                                       |
| `404`  | Note not found.                                                  |
| `409`  | Sequence conflict (`expect_sequence` mismatch or duplicate seq). |

---

#### Stream events

```
GET /v1/notes/:urn/events
```

Returns the full event stream for a note, optionally starting from a given
sequence number. Use this to replay history or sync an offline client.

**Query parameters**

| Parameter | Type    | Required | Description                                                          |
| --------- | ------- | -------- | -------------------------------------------------------------------- |
| `from`    | integer | no       | Return events with sequence `>= from`. Defaults to `1` (all events). |

**Response `200 OK`**

```json
{
  "note_urn": "notx:note:<uuid>",
  "count":    3,
  "events":   [ <Event>, ... ]
}
```

**Errors**

| Status | Condition             |
| ------ | --------------------- |
| `400`  | Invalid `from` value. |
| `404`  | Note not found.       |

---

### 2.3 Search

#### Search notes

```
GET /v1/search?q=<query>
```

Full-text search across all normal (non-deleted) notes. Returns matching note
headers together with a short content excerpt highlighting the match.
Secure notes are **never** included in search results.

**Query parameters**

| Parameter    | Type    | Required | Description                                 |
| ------------ | ------- | -------- | ------------------------------------------- |
| `q`          | string  | **yes**  | Search query string.                        |
| `page_size`  | integer | no       | Results per page (default `50`, max `200`). |
| `page_token` | string  | no       | Cursor from a previous response.            |

**Response `200 OK`**

```json
{
  "results": [
    {
      "note":    <NoteHeader>,
      "excerpt": "…the matching portion of the note content…"
    }
  ],
  "next_page_token": "<string or empty>"
}
```

**Errors**

| Status | Condition              |
| ------ | ---------------------- |
| `400`  | `q` parameter missing. |

---

### 2.4 Projects

Projects are index-only grouping entities. They have no `.notx` file on disk.

#### List projects

```
GET /v1/projects
```

Returns a paginated list of all projects.

**Query parameters**

| Parameter         | Type    | Required | Description                                           |
| ----------------- | ------- | -------- | ----------------------------------------------------- |
| `page_size`       | integer | no       | Number of results per page (default `50`, max `200`). |
| `page_token`      | string  | no       | Cursor returned by a previous response.               |
| `include_deleted` | boolean | no       | When `true`, soft-deleted projects are included.      |

**Response `200 OK`**

```json
{
  "projects": [ <Project>, ... ],
  "next_page_token": "<string or empty>"
}
```

---

#### Create project

```
POST /v1/projects
```

**Request body**

```json
{
  "urn": "notx:proj:<uuid>",
  "name": "Q3 Planning",
  "description": "All notes related to Q3 planning."
}
```

| Field         | Type   | Required | Description                          |
| ------------- | ------ | -------- | ------------------------------------ |
| `urn`         | string | **yes**  | Client-assigned URN for the project. |
| `name`        | string | **yes**  | Human-readable display name.         |
| `description` | string | no       | Optional summary.                    |

**Response `201 Created`** — returns the created `Project` object.

**Errors**

| Status | Condition                               |
| ------ | --------------------------------------- |
| `400`  | Missing or invalid fields.              |
| `409`  | A project with this URN already exists. |

---

#### Get project

```
GET /v1/projects/:urn
```

**Path parameter**: `:urn` — percent-encoded project URN.

**Response `200 OK`** — returns the `Project` object.

**Errors**

| Status | Condition          |
| ------ | ------------------ |
| `404`  | Project not found. |

---

#### Update project

```
PATCH /v1/projects/:urn
```

Partially updates project metadata. Only fields present in the request body
are changed.

**Request body** (all fields optional)

```json
{
  "name": "Renamed project",
  "description": "Updated description.",
  "deleted": false
}
```

**Response `200 OK`** — returns the updated `Project` object.

**Errors**

| Status | Condition            |
| ------ | -------------------- |
| `400`  | Invalid field value. |
| `404`  | Project not found.   |

---

#### Delete project

```
DELETE /v1/projects/:urn
```

Soft-deletes a project (sets `deleted: true`). Associated notes and folders
are **not** automatically deleted — their `project_urn` references remain
intact. The project can be restored by sending `PATCH` with `"deleted": false`.

**Response `200 OK`**

```json
{
  "deleted": true
}
```

**Errors**

| Status | Condition          |
| ------ | ------------------ |
| `404`  | Project not found. |

---

### 2.5 Folders

Folders are index-only sub-grouping entities that live inside a project.

#### List folders

```
GET /v1/folders
```

Returns a paginated list of folders, optionally scoped to a single project.

**Query parameters**

| Parameter         | Type    | Required | Description                                           |
| ----------------- | ------- | -------- | ----------------------------------------------------- |
| `project_urn`     | string  | no       | Filter to folders belonging to this project URN.      |
| `page_size`       | integer | no       | Number of results per page (default `50`, max `200`). |
| `page_token`      | string  | no       | Cursor returned by a previous response.               |
| `include_deleted` | boolean | no       | When `true`, soft-deleted folders are included.       |

**Response `200 OK`**

```json
{
  "folders": [ <Folder>, ... ],
  "next_page_token": "<string or empty>"
}
```

---

#### Create folder

```
POST /v1/folders
```

**Request body**

```json
{
  "urn": "notx:folder:<uuid>",
  "project_urn": "notx:proj:<uuid>",
  "name": "Meeting notes",
  "description": "Weekly sync notes."
}
```

| Field         | Type   | Required | Description                         |
| ------------- | ------ | -------- | ----------------------------------- |
| `urn`         | string | **yes**  | Client-assigned URN for the folder. |
| `project_urn` | string | **yes**  | URN of the owning project.          |
| `name`        | string | **yes**  | Human-readable display name.        |
| `description` | string | no       | Optional summary.                   |

**Response `201 Created`** — returns the created `Folder` object.

**Errors**

| Status | Condition                              |
| ------ | -------------------------------------- |
| `400`  | Missing or invalid fields.             |
| `409`  | A folder with this URN already exists. |

---

#### Get folder

```
GET /v1/folders/:urn
```

**Path parameter**: `:urn` — percent-encoded folder URN.

**Response `200 OK`** — returns the `Folder` object.

**Errors**

| Status | Condition         |
| ------ | ----------------- |
| `404`  | Folder not found. |

---

#### Update folder

```
PATCH /v1/folders/:urn
```

Partially updates folder metadata. Only fields present in the request body
are changed. `project_urn` is immutable after creation.

**Request body** (all fields optional)

```json
{
  "name": "Renamed folder",
  "description": "Updated description.",
  "deleted": false
}
```

**Response `200 OK`** — returns the updated `Folder` object.

**Errors**

| Status | Condition            |
| ------ | -------------------- |
| `400`  | Invalid field value. |
| `404`  | Folder not found.    |

---

#### Delete folder

```
DELETE /v1/folders/:urn
```

Soft-deletes a folder (sets `deleted: true`). Notes that reference this folder
via `folder_urn` are **not** automatically affected. The folder can be
restored by sending `PATCH` with `"deleted": false`.

**Response `200 OK`**

```json
{
  "deleted": true
}
```

**Errors**

| Status | Condition         |
| ------ | ----------------- |
| `404`  | Folder not found. |

---

### 2.6 Health probes

#### Liveness

```
GET /healthz
```

Returns `200 OK` when the process is alive.

```json
{ "status": "ok" }
```

#### Readiness

```
GET /readyz
```

Returns `200 OK` when the server is ready to serve traffic (storage
initialised, index open).

```json
{ "status": "ready" }
```

---

## 3. gRPC API

Package: `notx.v1`
Default address: `:50051`

Import the generated client stubs from the `.proto` source at
`internal/server/proto/notx.proto`.

---

### 3.1 NoteService

```
service NoteService {
  rpc GetNote         (GetNoteRequest)         returns (GetNoteResponse)
  rpc ListNotes       (ListNotesRequest)        returns (ListNotesResponse)
  rpc CreateNote      (CreateNoteRequest)       returns (CreateNoteResponse)
  rpc DeleteNote      (DeleteNoteRequest)       returns (DeleteNoteResponse)
  rpc AppendEvent     (AppendEventRequest)      returns (AppendEventResponse)
  rpc StreamEvents    (StreamEventsRequest)     returns (stream EventProto)
  rpc SearchNotes     (SearchNotesRequest)      returns (SearchNotesResponse)
  rpc ShareSecureNote (ShareSecureNoteRequest)  returns (ShareSecureNoteResponse)
}
```

---

#### GetNote

Returns the note header and the full event stream for the given URN.

```
GetNoteRequest  { urn: string }
GetNoteResponse { header: NoteHeader, events: repeated EventProto }
```

**Errors**: `NOT_FOUND`, `INVALID_ARGUMENT` (empty urn)

---

#### ListNotes

Paginated list of note headers with optional filters.

```
ListNotesRequest {
  page_size:       int32
  page_token:      string
  project_urn:     string
  folder_urn:      string
  note_type:       NoteType          // NOTE_TYPE_UNSPECIFIED = no filter
  include_deleted: bool
}

ListNotesResponse {
  notes:           repeated NoteHeader
  next_page_token: string
}
```

---

#### CreateNote

Creates a new note. The `header` message is used as the creation template.

```
CreateNoteRequest  { header: NoteHeader }
CreateNoteResponse { header: NoteHeader }
```

**Errors**: `INVALID_ARGUMENT` (missing urn or name), `ALREADY_EXISTS`

---

#### DeleteNote

Soft-deletes a note.

```
DeleteNoteRequest  { urn: string }
DeleteNoteResponse { deleted: bool }
```

**Errors**: `NOT_FOUND`, `INVALID_ARGUMENT` (empty urn)

---

#### AppendEvent

Appends a single event to the note's event stream.

```
AppendEventRequest  { event: EventProto }
AppendEventResponse { sequence: int32 }
```

The `event.sequence` field doubles as the optimistic concurrency value —
the server rejects the append with `ABORTED` if the current head sequence
does not match.

**Errors**: `INVALID_ARGUMENT`, `NOT_FOUND`, `ABORTED` (sequence conflict)

---

#### StreamEvents

Server-streaming RPC that pushes every event from `from_sequence` onwards.
The stream closes after all currently stored events have been sent (snapshot
delivery, not a live subscription).

```
StreamEventsRequest { note_urn: string, from_sequence: int32 }
// stream of: EventProto
```

**Errors**: `INVALID_ARGUMENT` (empty note_urn), `NOT_FOUND`

---

#### SearchNotes

Full-text search with pagination. Secure notes are never included.

```
SearchNotesRequest {
  query:      string
  page_size:  int32
  page_token: string
}

SearchNotesResponse {
  results:         repeated SearchResult
  next_page_token: string
}

SearchResult {
  header:  NoteHeader
  excerpt: string
}
```

**Errors**: `INVALID_ARGUMENT` (empty query)

---

#### ShareSecureNote

Relay-only operation used during secure note sharing. The client re-wraps the
Content Encryption Key (CEK) for one or more target devices and submits the
wrapped keys here. The server updates the `per_device_keys` map on the stored
encrypted events without ever decrypting them.

```
ShareSecureNoteRequest {
  note_urn:     string
  wrapped_keys: map<string, bytes>   // device URN → wrapped CEK bytes
}

ShareSecureNoteResponse {
  events_updated: int32
}
```

**Errors**: `INVALID_ARGUMENT` (empty note_urn or empty wrapped_keys, or note
is not of type `secure`), `NOT_FOUND`

---

### 3.2 ProjectService

Manages projects and folders. Both are index-only entities — they exist solely
in the Badger index and have no `.notx` file counterpart.

```
service ProjectService {
  rpc CreateProject (CreateProjectRequest)  returns (CreateProjectResponse)
  rpc GetProject    (GetProjectRequest)     returns (GetProjectResponse)
  rpc ListProjects  (ListProjectsRequest)   returns (ListProjectsResponse)
  rpc UpdateProject (UpdateProjectRequest)  returns (UpdateProjectResponse)
  rpc DeleteProject (DeleteProjectRequest)  returns (DeleteProjectResponse)

  rpc CreateFolder  (CreateFolderRequest)   returns (CreateFolderResponse)
  rpc GetFolder     (GetFolderRequest)      returns (GetFolderResponse)
  rpc ListFolders   (ListFoldersRequest)    returns (ListFoldersResponse)
  rpc UpdateFolder  (UpdateFolderRequest)   returns (UpdateFolderResponse)
  rpc DeleteFolder  (DeleteFolderRequest)   returns (DeleteFolderResponse)
}
```

---

#### CreateProject

```
CreateProjectRequest {
  urn:         string   // notx:proj:<uuid>
  name:        string
  description: string
}

CreateProjectResponse { project: ProjectProto }
```

**Errors**: `INVALID_ARGUMENT` (empty urn or name), `ALREADY_EXISTS`

---

#### GetProject

```
GetProjectRequest  { urn: string }
GetProjectResponse { project: ProjectProto }
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### ListProjects

```
ListProjectsRequest {
  include_deleted: bool
  page_size:       int32
  page_token:      string
}

ListProjectsResponse {
  projects:        repeated ProjectProto
  next_page_token: string
}
```

---

#### UpdateProject

Updates the mutable fields of a project. Any field set to its zero value is
written as-is; use `GetProject` first to read-modify-write if you only want
to change a subset of fields.

```
UpdateProjectRequest {
  urn:         string
  name:        string
  description: string
  deleted:     bool
}

UpdateProjectResponse { project: ProjectProto }
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### DeleteProject

Soft-deletes a project.

```
DeleteProjectRequest  { urn: string }
DeleteProjectResponse { deleted: bool }
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### CreateFolder

```
CreateFolderRequest {
  urn:         string   // notx:folder:<uuid>
  project_urn: string   // notx:proj:<uuid>
  name:        string
  description: string
}

CreateFolderResponse { folder: FolderProto }
```

**Errors**: `INVALID_ARGUMENT` (empty urn, project_urn, or name), `ALREADY_EXISTS`

---

#### GetFolder

```
GetFolderRequest  { urn: string }
GetFolderResponse { folder: FolderProto }
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### ListFolders

```
ListFoldersRequest {
  project_urn:     string   // optional; empty = all folders
  include_deleted: bool
  page_size:       int32
  page_token:      string
}

ListFoldersResponse {
  folders:         repeated FolderProto
  next_page_token: string
}
```

---

#### UpdateFolder

Updates the mutable fields of a folder. `project_urn` is immutable after
creation and cannot be changed via this RPC.

```
UpdateFolderRequest {
  urn:         string
  name:        string
  description: string
  deleted:     bool
}

UpdateFolderResponse { folder: FolderProto }
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### DeleteFolder

Soft-deletes a folder.

```
DeleteFolderRequest  { urn: string }
DeleteFolderResponse { deleted: bool }
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

### 3.3 DeviceService

Used for cryptographic device identity — required only for clients that handle
`secure` notes.

```
service DeviceService {
  rpc RegisterDevice     (RegisterDeviceRequest)     returns (RegisterDeviceResponse)
  rpc GetDevicePublicKey (GetDevicePublicKeyRequest)  returns (GetDevicePublicKeyResponse)
  rpc ListDevices        (ListDevicesRequest)         returns (ListDevicesResponse)
  rpc RevokeDevice       (RevokeDeviceRequest)        returns (RevokeDeviceResponse)
  rpc InitiatePairing    (InitiatePairingRequest)     returns (InitiatePairingResponse)
  rpc CompletePairing    (CompletePairingRequest)     returns (CompletePairingResponse)
}
```

---

#### RegisterDevice

Registers a new device and its Ed25519 public key.

```
RegisterDeviceRequest {
  device_urn:  string          // notx:device:<uuid>
  device_name: string
  owner_urn:   string          // notx:usr:<uuid>
  public_key:  bytes           // Ed25519 public key, exactly 32 bytes
}

RegisterDeviceResponse {
  device_urn:    string
  registered_at: Timestamp
}
```

**Errors**: `INVALID_ARGUMENT` (empty fields, wrong key length, invalid URN type)

---

#### GetDevicePublicKey

Retrieves the public key for a registered device. Used by other devices when
wrapping a CEK for sharing.

```
GetDevicePublicKeyRequest  { device_urn: string }
GetDevicePublicKeyResponse { device_urn: string, public_key: bytes }
```

**Errors**: `INVALID_ARGUMENT`, `NOT_FOUND`

---

#### ListDevices

Lists all devices registered to an owner.

```
ListDevicesRequest  { owner_urn: string }
ListDevicesResponse { devices: repeated DeviceInfo }

DeviceInfo {
  device_urn:    string
  device_name:   string
  public_key:    bytes
  registered_at: Timestamp
  last_seen_at:  Timestamp
}
```

**Errors**: `INVALID_ARGUMENT` (empty owner_urn)

---

#### RevokeDevice

Permanently removes a device registration. Any future requests using this
device URN will receive `NOT_FOUND`.

```
RevokeDeviceRequest  { device_urn: string }
RevokeDeviceResponse { revoked: bool }
```

**Errors**: `INVALID_ARGUMENT`, `NOT_FOUND`

---

#### InitiatePairing

Called by an already-registered device to generate a short-lived pairing
session token. The token must be transferred out-of-band (e.g. QR code) to
the new device, which then calls `CompletePairing`. The session expires after
**5 minutes**.

```
InitiatePairingRequest  { initiator_device_urn: string }
InitiatePairingResponse { session_token: string, expires_at: Timestamp }
```

**Errors**: `INVALID_ARGUMENT`, `PERMISSION_DENIED` (initiator not registered)

---

#### CompletePairing

Called by the new device to finalise pairing. On success, the new device is
registered under the same owner as the initiator and the session token is
consumed.

```
CompletePairingRequest {
  session_token: string
  device_urn:    string          // notx:device:<uuid>
  device_name:   string
  public_key:    bytes           // Ed25519 public key, exactly 32 bytes
}

CompletePairingResponse {
  device_urn:    string
  registered_at: Timestamp
}
```

**Errors**: `INVALID_ARGUMENT`, `NOT_FOUND` (invalid or already-used token),
`DEADLINE_EXCEEDED` (session expired), `FAILED_PRECONDITION` (initiator
device no longer registered)

---

## 4. Shared types

### NoteHeader

Carried by every note-related response.

| Field         | Type   | Description                               |
| ------------- | ------ | ----------------------------------------- |
| `urn`         | string | Unique note identifier.                   |
| `name`        | string | Human-readable display name.              |
| `note_type`   | string | `"normal"` or `"secure"`.                 |
| `project_urn` | string | Owning project URN, or empty.             |
| `folder_urn`  | string | Containing folder URN, or empty.          |
| `deleted`     | bool   | `true` if the note has been soft-deleted. |
| `created_at`  | string | RFC 3339 creation timestamp.              |
| `updated_at`  | string | RFC 3339 last-modified timestamp.         |

### Project

Returned by all project endpoints and by `ProjectProto` over gRPC.

| Field         | Type   | Description                                     |
| ------------- | ------ | ----------------------------------------------- |
| `urn`         | string | Unique project identifier (`notx:proj:<uuid>`). |
| `name`        | string | Human-readable display name.                    |
| `description` | string | Optional summary. Empty string if not set.      |
| `deleted`     | bool   | `true` if the project has been soft-deleted.    |
| `created_at`  | string | RFC 3339 creation timestamp.                    |
| `updated_at`  | string | RFC 3339 last-modified timestamp.               |

### Folder

Returned by all folder endpoints and by `FolderProto` over gRPC.

| Field         | Type   | Description                                      |
| ------------- | ------ | ------------------------------------------------ |
| `urn`         | string | Unique folder identifier (`notx:folder:<uuid>`). |
| `project_urn` | string | URN of the owning project. Immutable.            |
| `name`        | string | Human-readable display name.                     |
| `description` | string | Optional summary. Empty string if not set.       |
| `deleted`     | bool   | `true` if the folder has been soft-deleted.      |
| `created_at`  | string | RFC 3339 creation timestamp.                     |
| `updated_at`  | string | RFC 3339 last-modified timestamp.                |

### Event

Represents a single immutable write applied to a note.

| Field        | Type        | Description                                                                |
| ------------ | ----------- | -------------------------------------------------------------------------- |
| `urn`        | string      | Event URN (optional; omitted for older events).                            |
| `note_urn`   | string      | URN of the note this event belongs to.                                     |
| `sequence`   | integer     | Monotonically increasing sequence number within the note, starting at `1`. |
| `author_urn` | string      | URN of the author.                                                         |
| `created_at` | string      | RFC 3339 timestamp of when this event was written.                         |
| `entries`    | LineEntry[] | Ordered list of line operations. Empty for secure note events.             |

For **secure notes** the gRPC `EventProto` carries an `encrypted` field
(`EncryptedEventProto`) instead of `entries`. The HTTP API does not expose
the encrypted payload.

### LineEntry

| Field         | Type    | Description                                                                                     |
| ------------- | ------- | ----------------------------------------------------------------------------------------------- |
| `op`          | string  | `"set"` — write content to line. `"set_empty"` — write an empty line. `"delete"` — remove line. |
| `line_number` | integer | 1-based target line number.                                                                     |
| `content`     | string  | Line text. Present only when `op` is `"set"`.                                                   |

### EncryptedEventProto (gRPC only)

| Field             | Type               | Description                                              |
| ----------------- | ------------------ | -------------------------------------------------------- |
| `nonce`           | bytes              | AES-GCM nonce (12 bytes).                                |
| `payload`         | bytes              | AES-256-GCM ciphertext of the serialised line entries.   |
| `per_device_keys` | map<string, bytes> | Maps device URN → ECDH-wrapped CEK copy for that device. |

---

## 5. Error handling

### HTTP status codes

| Status | Meaning                                        |
| ------ | ---------------------------------------------- |
| `200`  | Success.                                       |
| `201`  | Resource created.                              |
| `400`  | Bad request — missing or invalid input.        |
| `404`  | Resource not found.                            |
| `405`  | Method not allowed on this endpoint.           |
| `409`  | Conflict — duplicate URN or sequence mismatch. |
| `500`  | Internal server error.                         |

All error responses carry a JSON body:

```json
{ "error": "description of what went wrong" }
```

### gRPC status codes

| Code                  | HTTP equivalent | Typical cause                                        |
| --------------------- | --------------- | ---------------------------------------------------- |
| `OK`                  | `200` / `201`   | Success.                                             |
| `INVALID_ARGUMENT`    | `400`           | Missing required field or malformed value.           |
| `NOT_FOUND`           | `404`           | Requested resource does not exist.                   |
| `ALREADY_EXISTS`      | `409`           | Attempt to create a resource with a duplicate URN.   |
| `ABORTED`             | `409`           | Optimistic concurrency failure (sequence conflict).  |
| `PERMISSION_DENIED`   | `403`           | Caller is not allowed to perform this action.        |
| `DEADLINE_EXCEEDED`   | `408`           | Operation timed out (e.g. pairing session expired).  |
| `FAILED_PRECONDITION` | `412`           | System state does not allow this operation.          |
| `UNAVAILABLE`         | `503`           | Streaming send failed; client should retry.          |
| `INTERNAL`            | `500`           | Unexpected server error.                             |
| `UNIMPLEMENTED`       | `501`           | RPC exists in the schema but is not yet implemented. |
