# notx API Reference

A complete reference for all client-facing endpoints exposed by the notx engine.
The server provides two transports — choose the one that fits your client stack.

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
   - [Device authentication](#20-device-authentication)
   - [Notes](#21-notes)
   - [Events](#22-events)
   - [Search](#23-search)
   - [Projects](#24-projects)
   - [Folders](#25-folders)
   - [Health probes](#26-health-probes)
   - [Devices](#27-devices) — includes SSE status stream
   - [Users](#28-users)
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
urn:notx:<object-type>:<id>
```

| Segment       | Rules                                                            | Example                                |
| ------------- | ---------------------------------------------------------------- | -------------------------------------- |
| `urn:notx`    | Fixed scheme prefix for all notx URNs                            | `urn:notx`                             |
| `object-type` | one of `note`, `event`, `usr`, `org`, `proj`, `folder`, `device` | `note`                                 |
| `id`          | standard UUID (8-4-4-4-12 hex) or the sentinel `anon`            | `1a9670dd-1a65-481a-ad17-03d77de021e5` |

Full example: `urn:notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5`

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

- A **project** (`urn:notx:proj:<id>`) is the top-level grouping.
- A **folder** (`urn:notx:folder:<id>`) belongs to exactly one project and can
  contain notes.
- A note references its project and folder via the `project_urn` and
  `folder_urn` fields on its header. The note itself stores those URNs; the
  project and folder records hold no back-references to notes.
- Deleting a project or folder is a **soft-delete** — the record is marked
  `deleted: true` and remains visible with `include_deleted=true`.

### Users

Users are **index-only** entities identified by a `usr` URN:

```
urn:notx:usr:<id>
```

Example: `urn:notx:usr:3f8a1b2c-4d5e-6f7a-8b9c-0d1e2f3a4b5c`

A user record stores a display name, an optional email address, and lifecycle
timestamps. Users are soft-deleted; a deleted user's URN continues to appear
in note `author_urn` fields and device `owner_urn` fields — it is never
garbage-collected.

The special sentinel URN `urn:notx:usr:anon` identifies an anonymous author
and is never backed by a real user record.

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

### 2.0 Device authentication

All **data endpoints** (notes, events, search, projects, and folders) require
the caller to identify its device via the `X-Device-ID` request header. The
value must be the fully-qualified device URN of a registered, approved, and
non-revoked device.

```
X-Device-ID: urn:notx:device:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
```

The following endpoints are **exempt** from device authentication and can be
called without the header:

| Endpoint group         | Reason                                                                |
| ---------------------- | --------------------------------------------------------------------- |
| `POST /v1/devices`     | A device must be able to register itself before it has been approved. |
| `GET /v1/devices/*`    | A device must be able to check its own status before being approved.  |
| `PATCH /v1/devices/*`  | Admin approval / rejection actions.                                   |
| `DELETE /v1/devices/*` | Revocation.                                                           |
| `* /v1/users`          | User management does not require a device context.                    |
| `* /v1/users/*`        | User management does not require a device context.                    |
| `/healthz`, `/readyz`  | Infrastructure probes.                                                |

#### Device onboarding flow

The server supports two onboarding modes controlled by the
`--device-auto-approve` startup flag:

| Mode                          | Flag                          | Behaviour                                                                                                                                           |
| ----------------------------- | ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Manual approval** (default) | `--device-auto-approve=false` | New devices start with `approval_status: "pending"`. An administrator must call `PATCH /v1/devices/:urn/approve` before the device can access data. |
| **Auto-approve**              | `--device-auto-approve=true`  | New devices immediately receive `approval_status: "approved"` and can start pulling data straight away.                                             |

#### Approval status values

| Value      | Meaning                                                                   |
| ---------- | ------------------------------------------------------------------------- |
| `pending`  | Registered but awaiting administrator approval.                           |
| `approved` | Approved — may access all data endpoints.                                 |
| `rejected` | Explicitly rejected. The device must re-register with a new URN to retry. |

#### Authentication errors

| Status | Condition                                                |
| ------ | -------------------------------------------------------- |
| `401`  | `X-Device-ID` header is missing.                         |
| `401`  | The device URN is not registered.                        |
| `403`  | The device is registered but its status is `"pending"`.  |
| `403`  | The device is registered but its status is `"rejected"`. |
| `403`  | The device has been revoked.                             |

---

### 2.1 Notes

> **Requires `X-Device-ID`** — see [Device authentication](#20-device-authentication).

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
  "notes": ["<NoteHeader>", "..."],
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
  "urn": "urn:notx:note:<id>",
  "name": "Meeting notes",
  "note_type": "normal",
  "project_urn": "urn:notx:proj:<id>",
  "folder_urn": "urn:notx:folder:<id>"
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
  "note": "<NoteHeader>"
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
  "header": "<NoteHeader>",
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
  "project_urn": "urn:notx:proj:<id>",
  "folder_urn": "urn:notx:folder:<id>",
  "deleted": false
}
```

Pass an empty string (`""`) for `project_urn` or `folder_urn` to clear the
association.

**Response `200 OK`**

```json
{
  "note": "<NoteHeader>"
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

**Request body**

```json
{
  "content": "# Title\n\nParagraph one.\nParagraph two.",
  "author_urn": "urn:notx:usr:<id>"
}
```

| Field        | Type   | Required | Description                                                      |
| ------------ | ------ | -------- | ---------------------------------------------------------------- |
| `content`    | string | **yes**  | The complete new document text. Lines are separated by `\n`.     |
| `author_urn` | string | no       | URN of the author. Defaults to `urn:notx:usr:anon` when omitted. |

**Response `201 Created`** — content changed

```json
{
  "note_urn": "urn:notx:note:<id>",
  "sequence": 4,
  "changed": true
}
```

**Response `200 OK`** — content identical, no event written

```json
{
  "note_urn": "urn:notx:note:<id>",
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

---

### 2.2 Events

> **Requires `X-Device-ID`** — see [Device authentication](#20-device-authentication).

#### Append event

```
POST /v1/events
```

Appends a new event to a note's event stream. Use this when you have already
computed the exact line-level diff you want to record, or when writing to a
`secure` note. For whole-document updates on a normal note prefer
`POST /v1/notes/:urn/content` which handles the diff automatically.

**Request body**

```json
{
  "note_urn": "urn:notx:note:<id>",
  "sequence": 1,
  "author_urn": "urn:notx:usr:<id>",
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
  "note_urn": "urn:notx:note:<id>",
  "count": 3,
  "events": ["<Event>", "..."]
}
```

**Errors**

| Status | Condition             |
| ------ | --------------------- |
| `400`  | Invalid `from` value. |
| `404`  | Note not found.       |

---

### 2.3 Search

> **Requires `X-Device-ID`** — see [Device authentication](#20-device-authentication).

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
      "note": "<NoteHeader>",
      "excerpt": "...the matching portion of the note content..."
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

> **Requires `X-Device-ID`** — see [Device authentication](#20-device-authentication).

Projects are index-only grouping entities with no `.notx` file on disk.

#### List projects

```
GET /v1/projects
```

**Query parameters**

| Parameter         | Type    | Required | Description                                           |
| ----------------- | ------- | -------- | ----------------------------------------------------- |
| `page_size`       | integer | no       | Number of results per page (default `50`, max `200`). |
| `page_token`      | string  | no       | Cursor returned by a previous response.               |
| `include_deleted` | boolean | no       | When `true`, soft-deleted projects are included.      |

**Response `200 OK`**

```json
{
  "projects": ["<Project>", "..."],
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
  "urn": "urn:notx:proj:<id>",
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
are **not** automatically deleted. The project can be restored by sending
`PATCH` with `"deleted": false`.

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

> **Requires `X-Device-ID`** — see [Device authentication](#20-device-authentication).

Folders are index-only sub-grouping entities that live inside a project.

#### List folders

```
GET /v1/folders
```

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
  "folders": ["<Folder>", "..."],
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
  "urn": "urn:notx:folder:<id>",
  "project_urn": "urn:notx:proj:<id>",
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
via `folder_urn` are not automatically affected. The folder can be restored by
sending `PATCH` with `"deleted": false`.

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

Health probe endpoints are **always open** and do not require `X-Device-ID`.

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

### 2.7 Devices

Device endpoints are **open** — they do not require `X-Device-ID` — so that
devices can register themselves and check their approval status before being
granted data access.

Device URNs follow the format `urn:notx:device:<id>`.

---

#### Register device

```
POST /v1/devices
```

Registers a new device. The initial `approval_status` is determined by the
server's onboarding configuration:

- `"pending"` when `--device-auto-approve=false` (default).
- `"approved"` when `--device-auto-approve=true`.

**Request body**

```json
{
  "urn": "urn:notx:device:<id>",
  "name": "MacBook Pro (work)",
  "owner_urn": "urn:notx:usr:<id>",
  "public_key_b64": "MCowBQYDK2VwAyEA..."
}
```

| Field            | Type   | Required | Description                                                                        |
| ---------------- | ------ | -------- | ---------------------------------------------------------------------------------- |
| `urn`            | string | **yes**  | Client-assigned device URN (`urn:notx:device:<id>`).                               |
| `name`           | string | **yes**  | Human-readable label (e.g. `"iPhone 15 Pro"`).                                     |
| `owner_urn`      | string | **yes**  | URN of the user who owns this device (`urn:notx:usr:<id>`).                        |
| `public_key_b64` | string | no       | Base64-encoded Ed25519 public key (32 bytes). Required for secure-note operations. |

**Response `201 Created`** — returns the full `Device` object including the
initial `approval_status`.

**Errors**

| Status | Condition                              |
| ------ | -------------------------------------- |
| `400`  | Missing or invalid fields.             |
| `409`  | A device with this URN already exists. |

---

#### Get device approval status

```
GET /v1/devices/:urn/status
```

Returns a lightweight summary of the device's current approval and revocation
state. Designed for polling by a freshly registered device that has not yet
been approved — no `X-Device-ID` header is required.

**Path parameter**: `:urn` — percent-encoded device URN.

**Response `200 OK`**

```json
{
  "urn": "urn:notx:device:<id>",
  "approval_status": "pending",
  "revoked": false,
  "approved": false
}
```

| Field             | Type   | Description                                                                            |
| ----------------- | ------ | -------------------------------------------------------------------------------------- |
| `urn`             | string | Device URN.                                                                            |
| `approval_status` | string | Current status: `"pending"`, `"approved"`, or `"rejected"`.                            |
| `revoked`         | bool   | `true` if the device has been permanently revoked. Omitted when `false`.               |
| `approved`        | bool   | Convenience boolean: `true` only when `approval_status == "approved"` and not revoked. |

**Errors**

| Status | Condition         |
| ------ | ----------------- |
| `404`  | Device not found. |

---

#### Stream device approval status (SSE)

```
GET /v1/devices/:urn/status/stream
```

Streams approval-status changes as a [Server-Sent Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events) (SSE) stream. Use this instead of polling `GET /v1/devices/:urn/status` when waiting for a freshly registered device to be approved.

No `X-Device-ID` header is required — this endpoint is intentionally open so a pending device can subscribe to its own approval state before being granted data access.

**Path parameter**: `:urn` — percent-encoded device URN.

**Response headers**

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

**Stream behaviour**

| Condition                                                     | Server action                                                              |
| ------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Connection opened                                             | Emits the current status immediately, then begins polling.                 |
| Status changes                                                | Emits a new `status` event with the updated payload.                       |
| Terminal state reached (`approved`, `rejected`, or `revoked`) | Emits a final `status` event and **closes the stream**.                    |
| No terminal state after 5 minutes                             | Emits a `timeout` event and closes the stream — reconnect to keep waiting. |
| Client disconnects                                            | Server exits the poll loop immediately.                                    |

**Event: `status`**

Emitted on connection and on every status change.

```
event: status
data: {"urn":"urn:notx:device:<id>","approval_status":"pending","approved":false}

event: status
data: {"urn":"urn:notx:device:<id>","approval_status":"approved","approved":true}
```

| Field             | Type   | Description                                                                            |
| ----------------- | ------ | -------------------------------------------------------------------------------------- |
| `urn`             | string | Device URN.                                                                            |
| `approval_status` | string | Current status: `"pending"`, `"approved"`, or `"rejected"`.                            |
| `revoked`         | bool   | `true` if the device has been permanently revoked. Omitted when `false`.               |
| `approved`        | bool   | Convenience boolean: `true` only when `approval_status == "approved"` and not revoked. |

**Event: `timeout`**

Emitted when the stream reaches the 5-minute wall-clock limit without a terminal state. The client should reconnect.

```
event: timeout
data: {"reason":"stream timeout, reconnect to continue waiting"}
```

**Event: `error`**

Emitted if the device record disappears while the stream is open (e.g. hard-deleted).

```
event: error
data: {"error":"device no longer exists"}
```

**Keepalive comments**

A blank SSE comment line (`: keepalive`) is written every 2 seconds between status checks to prevent proxies and load-balancers from closing idle connections.

**Errors** (before the stream is opened)

| Status | Condition                                   |
| ------ | ------------------------------------------- |
| `404`  | Device not found.                           |
| `500`  | Server does not support response streaming. |

**Client example (JavaScript)**

```js
const es = new EventSource(
  `/v1/devices/${encodeURIComponent(urn)}/status/stream`,
);

es.addEventListener("status", (e) => {
  const status = JSON.parse(e.data);
  if (status.approved) {
    console.log("Device approved — ready to use");
    es.close();
  } else if (status.approval_status === "rejected" || status.revoked) {
    console.log("Device rejected or revoked");
    es.close();
  }
});

es.addEventListener("timeout", () => {
  console.log("Timed out waiting — reconnecting...");
  es.close();
  // reconnect logic here
});
```

---

#### Approve device

```
PATCH /v1/devices/:urn/approve
```

Administrator action. Sets the device's `approval_status` to `"approved"`,
allowing it to access all data endpoints.

- Cannot approve a revoked device (`409`).
- Cannot approve a rejected device (`409`) — the device must re-register with
  a new URN.

**Response `200 OK`** — returns the updated `Device` object.

**Errors**

| Status | Condition                                                       |
| ------ | --------------------------------------------------------------- |
| `404`  | Device not found.                                               |
| `409`  | Device is revoked, or has been rejected and cannot be approved. |

---

#### Reject device

```
PATCH /v1/devices/:urn/reject
```

Administrator action. Sets the device's `approval_status` to `"rejected"`.
A rejected device is permanently barred from data access. To re-onboard the
device, register it again under a new URN.

- Cannot reject a revoked device (`409`).

**Response `200 OK`** — returns the updated `Device` object.

**Errors**

| Status | Condition                  |
| ------ | -------------------------- |
| `404`  | Device not found.          |
| `409`  | Device is already revoked. |

---

#### List devices

```
GET /v1/devices
```

Returns all registered devices, optionally filtered by owner.

**Query parameters**

| Parameter         | Type    | Required | Description                                                |
| ----------------- | ------- | -------- | ---------------------------------------------------------- |
| `owner_urn`       | string  | no       | Filter to devices belonging to this user URN.              |
| `include_revoked` | boolean | no       | When `true`, revoked devices are included in the response. |

**Response `200 OK`**

```json
{
  "devices": ["<Device>", "..."]
}
```

---

#### Get device

```
GET /v1/devices/:urn
```

**Path parameter**: `:urn` — percent-encoded device URN.

**Response `200 OK`** — returns the full `Device` object.

**Errors**

| Status | Condition         |
| ------ | ----------------- |
| `404`  | Device not found. |

---

#### Update device

```
PATCH /v1/devices/:urn
```

Updates mutable device metadata. Only fields present in the request body are
changed. `urn`, `owner_urn`, `public_key_b64`, and `registered_at` are
immutable after registration.

**Request body** (all fields optional)

```json
{
  "name": "MacBook Pro (personal)",
  "last_seen_at": "2024-06-01T12:00:00Z"
}
```

| Field          | Type   | Required | Description                                       |
| -------------- | ------ | -------- | ------------------------------------------------- |
| `name`         | string | no       | Updated human-readable label.                     |
| `last_seen_at` | string | no       | RFC 3339 timestamp of the device's last activity. |

**Response `200 OK`** — returns the updated `Device` object.

**Errors**

| Status | Condition            |
| ------ | -------------------- |
| `400`  | Invalid field value. |
| `404`  | Device not found.    |

---

#### Revoke device

```
DELETE /v1/devices/:urn
```

Permanently revokes a device by setting `revoked: true`. The device record is
retained for audit purposes but all future requests using this device URN will
be rejected with `403`. **This action cannot be undone.**

> Unlike projects and folders, devices cannot be un-revoked. Register a new
> device URN if access needs to be re-established.

**Response `200 OK`**

```json
{
  "revoked": true
}
```

**Errors**

| Status | Condition         |
| ------ | ----------------- |
| `404`  | Device not found. |

---

### 2.8 Users

User endpoints are **open** and do not require `X-Device-ID`.

#### List users

```
GET /v1/users
```

**Query parameters**

| Parameter         | Type    | Default | Description                                     |
| ----------------- | ------- | ------- | ----------------------------------------------- |
| `page_size`       | integer | `50`    | Maximum number of users to return (max `200`).  |
| `page_token`      | string  | —       | Opaque continuation token from a previous call. |
| `include_deleted` | boolean | `false` | Include soft-deleted users in the result set.   |

**Response `200 OK`**

```json
{
  "users": [
    {
      "urn": "urn:notx:usr:3f8a1b2c-4d5e-6f7a-8b9c-0d1e2f3a4b5c",
      "display_name": "Alice Example",
      "email": "alice@example.com",
      "created_at": "2025-01-15T11:00:00Z",
      "updated_at": "2025-01-15T11:00:00Z"
    }
  ],
  "next_page_token": ""
}
```

---

#### Create user

```
POST /v1/users
```

**Request body**

```json
{
  "urn": "urn:notx:usr:<id>",
  "display_name": "Alice Example",
  "email": "alice@example.com"
}
```

| Field          | Required | Description                                                       |
| -------------- | -------- | ----------------------------------------------------------------- |
| `urn`          | **yes**  | A valid `urn:notx:usr:<id>` URN. Must be unique across all users. |
| `display_name` | **yes**  | Human-readable name shown in the UI. Must be non-empty.           |
| `email`        | no       | Optional contact address.                                         |

**Response `201 Created`** — the newly created `User` object.

**Errors**

| Status | Condition                                  |
| ------ | ------------------------------------------ |
| `400`  | Missing or invalid `urn` / `display_name`. |
| `409`  | A user with that URN already exists.       |

---

#### Get user

```
GET /v1/users/:urn
```

`:urn` must be percent-encoded (e.g. `urn%3Anotx%3Ausr%3A3f8a1b2c-...`).

**Response `200 OK`** — returns a `User` object.

**Errors**

| Status | Condition       |
| ------ | --------------- |
| `404`  | User not found. |

---

#### Update user

```
PATCH /v1/users/:urn
```

All fields are optional. Only supplied fields are updated.

**Request body**

```json
{
  "display_name": "Alice Updated",
  "email": "alice-new@example.com",
  "deleted": false
}
```

| Field          | Type    | Description                                                      |
| -------------- | ------- | ---------------------------------------------------------------- |
| `display_name` | string  | New display name. Must be non-empty if supplied.                 |
| `email`        | string  | New email address. Pass `""` to clear.                           |
| `deleted`      | boolean | Set to `true` to soft-delete; `false` to restore a deleted user. |

**Response `200 OK`** — the updated `User` object.

**Errors**

| Status | Condition             |
| ------ | --------------------- |
| `400`  | Invalid request body. |
| `404`  | User not found.       |

---

#### Delete user

```
DELETE /v1/users/:urn
```

Soft-deletes the user by setting `deleted: true`. The URN remains valid in
author and owner references and is never permanently removed.

**Response `200 OK`**

```json
{
  "deleted": true
}
```

**Errors**

| Status | Condition       |
| ------ | --------------- |
| `404`  | User not found. |

---

## 3. gRPC API

Package: `notx.v1`  
Default address: `:50051`

Import the generated client stubs from the `.proto` source at
`internal/server/proto/notx.proto`. Server reflection is enabled on all
environments, so tools like `grpcurl` work out of the box:

```
grpcurl -plaintext localhost:50051 list
```

> **Note:** The gRPC `DeviceService` maintains its own in-memory device store
> that is separate from the HTTP device repository. It is intended for
> cryptographic key exchange (secure note sharing) rather than onboarding
> management, which is handled exclusively over HTTP.

---

### 3.1 NoteService

Backed by the file-based note repository. All RPCs that write data require a
consistent note URN.

```
service NoteService {
  rpc GetNote         (GetNoteRequest)          returns (GetNoteResponse)
  rpc ListNotes       (ListNotesRequest)         returns (ListNotesResponse)
  rpc CreateNote      (CreateNoteRequest)        returns (CreateNoteResponse)
  rpc DeleteNote      (DeleteNoteRequest)        returns (DeleteNoteResponse)
  rpc AppendEvent     (AppendEventRequest)       returns (AppendEventResponse)
  rpc StreamEvents    (StreamEventsRequest)      returns (stream EventProto)
  rpc SearchNotes     (SearchNotesRequest)       returns (SearchNotesResponse)
  rpc ShareSecureNote (ShareSecureNoteRequest)   returns (ShareSecureNoteResponse)
}
```

---

#### GetNote

Returns the note header and the full event stream for the given URN.

```
GetNoteRequest {
  urn: string   // urn:notx:note:<id>
}

GetNoteResponse {
  header: NoteHeader
  events: repeated EventProto
}
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### ListNotes

Paginated list of note headers with optional filters.

```
ListNotesRequest {
  project_urn:     string    // optional
  folder_urn:      string    // optional
  note_type:       NoteType  // NOTE_TYPE_UNSPECIFIED = no type filter
  include_deleted: bool
  page_size:       int32     // 0 = server default (50)
  page_token:      string
}

ListNotesResponse {
  notes:           repeated NoteHeader
  next_page_token: string
}
```

---

#### CreateNote

Creates a new note header. Events are added separately via `AppendEvent`.

```
CreateNoteRequest {
  header: NoteHeader   // urn and name are required
}

CreateNoteResponse {
  header: NoteHeader
}
```

**Errors**: `INVALID_ARGUMENT` (missing urn or name), `ALREADY_EXISTS`

---

#### DeleteNote

Soft-deletes a note by URN.

```
DeleteNoteRequest {
  urn: string
}

DeleteNoteResponse {
  deleted: bool
}
```

**Errors**: `INVALID_ARGUMENT` (empty urn), `NOT_FOUND`

---

#### AppendEvent

Appends a single event to the note's event stream. For normal notes the server
stores the plaintext entries and advances the index. For secure notes it stores
the encrypted blob verbatim.

The `event.sequence` field doubles as the optimistic concurrency value — the
server rejects the append with `ABORTED` if the current head sequence does not
immediately precede the submitted sequence.

```
AppendEventRequest {
  event: EventProto
}

AppendEventResponse {
  sequence: int32   // server-assigned sequence of the stored event
}
```

**Errors**: `INVALID_ARGUMENT`, `NOT_FOUND`, `ABORTED` (sequence conflict)

---

#### StreamEvents

Server-streaming RPC that pushes all stored events for a note starting from
`from_sequence`. The stream closes after delivery of the last stored event
(snapshot delivery — not a live subscription).

```
StreamEventsRequest {
  note_urn:      string
  from_sequence: int32   // inclusive; 0 = stream from sequence 1
}

// Stream response: repeated EventProto
```

**Errors**: `INVALID_ARGUMENT` (empty note_urn), `NOT_FOUND`

---

#### SearchNotes

Full-text search over normal note content with pagination. Secure notes are
**never** included in results.

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
  excerpt: string    // short snippet of matching content
}
```

**Errors**: `INVALID_ARGUMENT` (empty query)

---

#### ShareSecureNote

Relay-only operation. The calling device re-wraps the Content Encryption Key
(CEK) for one or more target devices and submits the wrapped keys here. The
server updates the `per_device_keys` map on every stored encrypted event for
the note without ever decrypting the payload.

```
ShareSecureNoteRequest {
  note_urn:     string
  wrapped_keys: map<string, bytes>   // device URN string → wrapped CEK bytes
}

ShareSecureNoteResponse {
  events_updated: int32
}
```

**Errors**: `INVALID_ARGUMENT` (empty note_urn, empty wrapped_keys, or note is
not `secure`), `NOT_FOUND`

---

### 3.2 ProjectService

Manages projects and folders. Both are index-only entities backed by the Badger
index — they have no `.notx` file counterpart.

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

#### Project message shape

```
ProjectProto {
  urn:         string
  name:        string
  description: string
  deleted:     bool
  created_at:  Timestamp
  updated_at:  Timestamp
}
```

#### Folder message shape

```
FolderProto {
  urn:         string
  project_urn: string
  name:        string
  description: string
  deleted:     bool
  created_at:  Timestamp
  updated_at:  Timestamp
}
```

---

#### CreateProject

```
CreateProjectRequest {
  urn:         string   // urn:notx:proj:<id>
  name:        string
  description: string   // optional
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
  page_size:       int32    // 0 = server default (50)
  page_token:      string
}

ListProjectsResponse {
  projects:        repeated ProjectProto
  next_page_token: string
}
```

---

#### UpdateProject

Updates mutable fields. `urn` is immutable. Any field set to its zero value is
written as-is; use `GetProject` first if you only want to update a subset.

```
UpdateProjectRequest {
  urn:         string   // required
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
  urn:         string   // urn:notx:folder:<id>
  project_urn: string   // urn:notx:proj:<id>  — required
  name:        string
  description: string   // optional
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
  page_size:       int32    // 0 = server default (50)
  page_token:      string
}

ListFoldersResponse {
  folders:         repeated FolderProto
  next_page_token: string
}
```

---

#### UpdateFolder

Updates mutable fields. `project_urn` is immutable after creation.

```
UpdateFolderRequest {
  urn:         string   // required
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

The gRPC `DeviceService` maintains an **in-memory** device registry used
exclusively for cryptographic key exchange during secure note operations
(public key lookup and device pairing). It is separate from the HTTP device
repository used for onboarding and access control.

Use the HTTP `POST /v1/devices` endpoint to onboard a device for data access.
Use these RPCs when you need to retrieve or share cryptographic keys.

```
service DeviceService {
  rpc RegisterDevice     (RegisterDeviceRequest)      returns (RegisterDeviceResponse)
  rpc GetDevicePublicKey (GetDevicePublicKeyRequest)   returns (GetDevicePublicKeyResponse)
  rpc ListDevices        (ListDevicesRequest)          returns (ListDevicesResponse)
  rpc RevokeDevice       (RevokeDeviceRequest)         returns (RevokeDeviceResponse)
  rpc InitiatePairing    (InitiatePairingRequest)      returns (InitiatePairingResponse)
  rpc CompletePairing    (CompletePairingRequest)      returns (CompletePairingResponse)
}
```

---

#### RegisterDevice

Registers a device's Ed25519 public key into the in-memory cryptographic
store. The private key must **never** appear in this request. `public_key`
must be exactly 32 bytes.

```
RegisterDeviceRequest {
  device_urn:  string   // urn:notx:device:<id>
  device_name: string
  owner_urn:   string   // urn:notx:usr:<id>
  public_key:  bytes    // Ed25519 public key — exactly 32 bytes
}

RegisterDeviceResponse {
  device_urn:    string
  registered_at: Timestamp
}
```

**Errors**: `INVALID_ARGUMENT` (empty fields, wrong key length, invalid URN type)

---

#### GetDevicePublicKey

Retrieves the Ed25519 public key for a registered device. Used by other
devices when wrapping a CEK to share a secure note.

```
GetDevicePublicKeyRequest  { device_urn: string }

GetDevicePublicKeyResponse {
  device_urn: string
  public_key: bytes    // Ed25519 public key, 32 bytes
}
```

**Errors**: `INVALID_ARGUMENT` (empty device_urn), `NOT_FOUND`

---

#### ListDevices

Lists all devices registered to a given owner in the in-memory store.

```
ListDevicesRequest {
  owner_urn: string   // required
}

ListDevicesResponse {
  devices: repeated DeviceInfo
}

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

Removes a device from the in-memory cryptographic registry. Future
`GetDevicePublicKey` calls for this URN will return `NOT_FOUND`.

> Note: this does **not** affect the HTTP device repository or the
> `approval_status` of the device there. To revoke HTTP data access, call
> `DELETE /v1/devices/:urn`.

```
RevokeDeviceRequest  { device_urn: string }
RevokeDeviceResponse { revoked: bool }
```

**Errors**: `INVALID_ARGUMENT` (empty device_urn), `NOT_FOUND`

---

#### InitiatePairing

Called by an already-registered device to generate a short-lived pairing
session token. The token must be transferred out-of-band (e.g. encoded in a
QR code) to the new device, which then calls `CompletePairing`. The session
expires after **5 minutes**.

```
InitiatePairingRequest {
  initiator_device_urn: string   // must already be registered
}

InitiatePairingResponse {
  session_token: string
  expires_at:    Timestamp       // UTC; 5 minutes from issuance
}
```

**Errors**: `INVALID_ARGUMENT` (empty initiator_device_urn),
`PERMISSION_DENIED` (initiator device not registered)

---

#### CompletePairing

Called by the new device to finalise pairing. On success the new device is
registered in the in-memory store under the same owner as the initiator and
the session token is consumed (single-use).

```
CompletePairingRequest {
  session_token: string
  device_urn:    string   // urn:notx:device:<id>
  device_name:   string
  public_key:    bytes    // Ed25519 public key, exactly 32 bytes
}

CompletePairingResponse {
  device_urn:    string
  registered_at: Timestamp
}
```

**Errors**:

- `INVALID_ARGUMENT` — empty fields, wrong key length, or invalid URN format.
- `NOT_FOUND` — session token is unknown or has already been consumed.
- `DEADLINE_EXCEEDED` — session token has expired.
- `FAILED_PRECONDITION` — the initiator device is no longer registered.

---

## 4. Shared types

### NoteHeader

Carried by every note-related response (HTTP and gRPC).

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

---

### Device

Returned by all HTTP device endpoints.

| Field             | Type   | Description                                                                      |
| ----------------- | ------ | -------------------------------------------------------------------------------- |
| `urn`             | string | Unique device identifier (`urn:notx:device:<id>`).                               |
| `name`            | string | Human-readable device label.                                                     |
| `owner_urn`       | string | URN of the owning user (`urn:notx:usr:<id>`).                                    |
| `public_key_b64`  | string | Base64-encoded Ed25519 public key. Empty string if not provided at registration. |
| `approval_status` | string | Onboarding state: `"pending"`, `"approved"`, or `"rejected"`.                    |
| `revoked`         | bool   | `true` if the device has been permanently revoked. Omitted when `false`.         |
| `registered_at`   | string | RFC 3339 timestamp of when the device was registered.                            |
| `last_seen_at`    | string | RFC 3339 timestamp of the device's last activity. Omitted if never updated.      |

---

### Project

Returned by all project endpoints and by `ProjectProto` over gRPC.

| Field         | Type   | Description                                       |
| ------------- | ------ | ------------------------------------------------- |
| `urn`         | string | Unique project identifier (`urn:notx:proj:<id>`). |
| `name`        | string | Human-readable display name.                      |
| `description` | string | Optional summary. Empty string if not set.        |
| `deleted`     | bool   | `true` if the project has been soft-deleted.      |
| `created_at`  | string | RFC 3339 creation timestamp.                      |
| `updated_at`  | string | RFC 3339 last-modified timestamp.                 |

---

### Folder

Returned by all folder endpoints and by `FolderProto` over gRPC.

| Field         | Type   | Description                                        |
| ------------- | ------ | -------------------------------------------------- |
| `urn`         | string | Unique folder identifier (`urn:notx:folder:<id>`). |
| `project_urn` | string | URN of the owning project. Immutable.              |
| `name`        | string | Human-readable display name.                       |
| `description` | string | Optional summary. Empty string if not set.         |
| `deleted`     | bool   | `true` if the folder has been soft-deleted.        |
| `created_at`  | string | RFC 3339 creation timestamp.                       |
| `updated_at`  | string | RFC 3339 last-modified timestamp.                  |

---

### User

Returned by all user endpoints.

| Field          | Type   | Description                                                     |
| -------------- | ------ | --------------------------------------------------------------- |
| `urn`          | string | Unique user identifier (`urn:notx:usr:<id>`).                   |
| `display_name` | string | Human-readable name shown in the UI.                            |
| `email`        | string | Optional contact address. Omitted when empty.                   |
| `deleted`      | bool   | `true` if the user has been soft-deleted. Omitted when `false`. |
| `created_at`   | string | RFC 3339 creation timestamp.                                    |
| `updated_at`   | string | RFC 3339 last-modified timestamp.                               |

---

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
(`EncryptedEventProto`) instead of `entries`. The HTTP API does not expose the
encrypted payload directly.

---

### LineEntry

| Field         | Type    | Description                                                                                     |
| ------------- | ------- | ----------------------------------------------------------------------------------------------- |
| `op`          | string  | `"set"` — write content to line. `"set_empty"` — write an empty line. `"delete"` — remove line. |
| `line_number` | integer | 1-based target line number.                                                                     |
| `content`     | string  | Line text. Present only when `op` is `"set"`.                                                   |

---

### EncryptedEventProto (gRPC only)

| Field             | Type               | Description                                              |
| ----------------- | ------------------ | -------------------------------------------------------- |
| `nonce`           | bytes              | AES-GCM nonce (12 bytes).                                |
| `payload`         | bytes              | AES-256-GCM ciphertext of the serialised line entries.   |
| `per_device_keys` | map<string, bytes> | Maps device URN → ECDH-wrapped CEK copy for that device. |

---

## 5. Error handling

### HTTP status codes

| Status | Meaning                                                                   |
| ------ | ------------------------------------------------------------------------- |
| `200`  | Success.                                                                  |
| `201`  | Resource created.                                                         |
| `400`  | Bad request — missing or invalid input.                                   |
| `401`  | Unauthorized — missing or unrecognised `X-Device-ID` header.              |
| `403`  | Forbidden — device is pending, rejected, or revoked.                      |
| `404`  | Resource not found.                                                       |
| `405`  | Method not allowed on this endpoint.                                      |
| `409`  | Conflict — duplicate URN, sequence mismatch, or invalid state transition. |
| `500`  | Internal server error.                                                    |

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
