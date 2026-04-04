# notx URN Specification

## Overview

A **URN** (Uniform Resource Name) is a standardized, globally unique identifier for any entity in a notx ecosystem. Every identifiable object—notes, users, projects, organizations, folders, devices, and events—has a URN that is **immutable and globally unique** across all notx servers.

The URN scheme is built on a single foundational principle: **identity is independent of location, ownership, and namespace.** A URN tells you _what_ an object is; it says nothing about _where_ it lives, _who_ owns it, or _which logical group_ it belongs to. Those concerns are expressed as separate, explicit metadata fields on the object itself.

The URN scheme enables:

- **Global uniqueness** — Every object has exactly one URN, forever, regardless of which server created it or hosts it
- **Immutable identity** — URNs never change, even when objects are transferred between servers
- **Location independence** — Data can be exchanged between servers without rewriting any IDs
- **Self-description** — A URN tells you the object type and its unique ID; authority and namespace are metadata
- **Simple federation** — Cross-server references work identically to local references; the `authority` field routes resolution

## URN Syntax

All notx URNs follow this structure:

```
urn:notx:<type>:<id>
```

The scheme prefix `urn:notx:` is fixed and mandatory for all notx identifiers. There is no namespace, server, or instance identifier in the URN itself.

### Segments

| Segment  | Description                                                                                  | Example                    |
| -------- | -------------------------------------------------------------------------------------------- | -------------------------- |
| `urn`    | Literal string `urn`. Fixed.                                                                 | `urn`                      |
| `notx`   | Literal string `notx`. The notx URN namespace identifier. Fixed.                             | `notx`                     |
| `<type>` | The object type. One of the defined types from the table below.                              | `note`, `usr`, `srv`       |
| `<id>`   | A ULID (26 uppercase alphanumeric characters) or UUID v4/v7. Unique across all notx objects. | `01HZX3K8J9X2M4P7R8T1Y6ZQ` |

### Delimiters

The delimiter between all segments is **colon** (`:`, ASCII 0x3A). There are no spaces, hyphens between segments, or other characters outside of the `<id>` segment itself.

```
urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
urn:notx:usr:01HZX3K8ABCDEF1234567890
urn:notx:srv:01HZX3K8SERVERID123456
```

### ID Format

The `<id>` segment is either a **ULID** or a **UUID**. Both are valid and may coexist in a deployment.

#### ULID (Preferred)

ULID (Universally Unique Lexicographically Sortable Identifier) is the preferred format for new implementations:

- 26 characters, uppercase alphanumeric (Crockford Base32)
- Encodes a 48-bit millisecond timestamp followed by 80 bits of randomness
- Lexicographically sortable by creation time
- No hyphens

```
01HZX3K8J9X2M4P7R8T1Y6ZQ
```

#### UUID (Acceptable)

Standard UUID v4 or v7 (36 characters, lowercase hex with hyphens) is also accepted:

```
018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
```

UUID v7 (time-ordered) is preferred over v4 (random) when UUIDs must be used, for the same index-locality reasons that motivate ULID preference.

### Special Sentinel IDs

Certain `<id>` values are reserved as sentinels with fixed meaning:

| Sentinel ID | Used in Type | Meaning                     |
| ----------- | ------------ | --------------------------- |
| `anon`      | `usr`        | Anonymous or unknown author |

The full sentinel URN for anonymous authorship is `urn:notx:usr:anon`. See [Special Sentinel Values](#special-sentinel-values).

## Identity, Authority, Namespace, and Location

These four concepts are **strictly separated** in the notx model. Conflating them is the most common source of federation bugs.

| Concept       | What it is                                      | Where it lives                       | Is it in the URN?        |
| ------------- | ----------------------------------------------- | ------------------------------------ | ------------------------ |
| **Identity**  | What the object _is_ — its permanent, global ID | The `id` field on the object         | **Yes** — the URN itself |
| **Authority** | Which server _owns_ the object                  | The `authority` field (a server URN) | No                       |
| **Namespace** | Which logical group the object belongs to       | The `namespace` field (a string)     | No                       |
| **Location**  | Which server _currently holds_ the object       | The routing table / authority field  | No                       |

### Why Namespace Is Not in the URN

In earlier designs, the namespace was encoded directly in the URN (e.g., `acme:note:<uuid>`). This caused several problems:

- Objects had to have their IDs rewritten when transferred between servers
- Two servers using the same namespace string (e.g., both calling themselves `acme`) created ambiguous URNs
- Clients needed to know the namespace to construct or validate any URN
- The format violated the principle that identity should be stable and independent of deployment topology

The new model solves all of these by making namespace a **metadata label only**, attached to the object but not part of its identity. Two servers can both have a namespace of `"acme"` with no conflict, because their objects are distinguished by globally unique IDs, not by namespace strings.

## Object Data Model

Every notx object carries its authority and namespace as **explicit, separate metadata fields** — never encoded in the URN. The canonical shape of any notx object envelope is:

```json
{
  "id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZSERVER123456789ABCDEF",
  "namespace": "acme",
  "content": { "...": "..." }
}
```

| Field       | Type       | Nullable | Description                                                                                                   |
| ----------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------- |
| `id`        | URN string | No       | Globally unique, immutable object identity. Never changes.                                                    |
| `authority` | URN string | No       | The `urn:notx:srv:<id>` of the server that owns this object. May change only via explicit ownership transfer. |
| `namespace` | string     | Yes      | Logical grouping label for UI, filtering, and routing hints. NOT an identity boundary.                        |
| `content`   | object     | Varies   | The object's type-specific payload fields.                                                                    |

### Authority

The `authority` field holds a **server URN** (`urn:notx:srv:<id>`). It identifies the authoritative source of truth for this object. When resolving an object that is not in local storage, the authority field tells the resolver which server to contact.

Authority may change only through an explicit ownership-transfer operation. It is never derived from or affected by the namespace.

### Namespace

Namespace is a **logical grouping label** with the following properties:

- Set by the server or client when an object is created
- Used for UI grouping, query filtering, and routing hints
- **Not required to be globally unique** — two different servers may independently use the same namespace string
- **Not an identity boundary** — objects with the same namespace on different servers are not related by that fact alone
- **Not part of any URN** — the namespace does not appear in the object's `id`, `authority`, or any reference to the object
- May be `null` for objects that do not belong to any logical group

## Defined Object Types

All notx object types follow the same URN pattern: `urn:notx:<type>:<id>`.

### Core Types

| Type    | Description                          | Example URN                               |
| ------- | ------------------------------------ | ----------------------------------------- |
| `note`  | A note document with content history | `urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ`  |
| `event` | A history event within a note        | `urn:notx:event:01HZX3K8EVENTID123456789` |

### Infrastructure Types

| Type  | Description            | Example URN                              |
| ----- | ---------------------- | ---------------------------------------- |
| `srv` | A notx server instance | `urn:notx:srv:01HZX3K8SERVERID123456789` |

Server URNs are globally unique identifiers for notx server instances. They are assigned once at server initialization and never change. The server URN is the value used in the `authority` field of all objects the server owns.

### Security Types

| Type     | Description                                                              | Example URN                                |
| -------- | ------------------------------------------------------------------------ | ------------------------------------------ |
| `device` | A registered user device with a cryptographic identity (E2EE key holder) | `urn:notx:device:01HZX3K8DEVICEID12345678` |

The `device` type is used exclusively by the security model. A device URN identifies a specific physical or virtual device (desktop, mobile, browser) that holds an Ed25519 key pair. The **private key never leaves the device**; only the public key and the device URN are registered on the server.

Device URNs appear in encrypted event blocks to identify which device can decrypt a given wrapped Content Encryption Key (CEK):

```
key[urn:notx:device:01HZX3K8DEVICEID12345678]: <base64-wrapped-cek>
```

See [NOTX_SECURITY_MODEL.md](./NOTX_SECURITY_MODEL.md) for the full device identity and key management specification.

### User and Organization Types

| Type  | Description                 | Example URN                             |
| ----- | --------------------------- | --------------------------------------- |
| `usr` | A registered user or author | `urn:notx:usr:01HZX3K8ABCDEF1234567890` |
| `org` | An organization             | `urn:notx:org:01HZX3K8ORGID12345678901` |

### Project and Folder Types

| Type     | Description               | Example URN                               |
| -------- | ------------------------- | ----------------------------------------- |
| `proj`   | A project container       | `urn:notx:proj:01HZX3K8PROJID123456789`   |
| `folder` | A folder within a project | `urn:notx:folder:01HZX3K8FOLDERID1234567` |

### Special Sentinel Values

The following are reserved URN values with fixed, global meaning:

| URN                 | Type  | Meaning                                                |
| ------------------- | ----- | ------------------------------------------------------ |
| `urn:notx:usr:anon` | `usr` | Anonymous or unknown author. A single global sentinel. |

**`urn:notx:usr:anon`** is the one global sentinel for anonymous authorship. Because namespace is no longer part of identity, there is no need for per-instance anonymous sentinels. Any unauthenticated edit or edit with an unknown author is attributed to `urn:notx:usr:anon` regardless of which server recorded it.

A parser encountering `urn:notx:usr:anon` must treat it as "author unknown" without attempting a database lookup or remote resolution.

## Parsing and Validation

### Parsing Rules

To parse a URN string:

1. Split on `:` (colon) to get tokens
2. Assert that token[0] is exactly `urn`
3. Assert that token[1] is exactly `notx`
4. Token[2] is the `<type>`
5. All remaining tokens joined with `:` form the `<id>` (this accommodates the `anon` sentinel and any future sentinel IDs)

```
Input:  "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ"
Tokens: ["urn", "notx", "note", "01HZX3K8J9X2M4P7R8T1Y6ZQ"]
Result: type="note", id="01HZX3K8J9X2M4P7R8T1Y6ZQ"

Input:  "urn:notx:usr:anon"
Tokens: ["urn", "notx", "usr", "anon"]
Result: type="usr", id="anon"

Input:  "urn:notx:srv:01HZX3K8SERVERID123456789"
Tokens: ["urn", "notx", "srv", "01HZX3K8SERVERID123456789"]
Result: type="srv", id="01HZX3K8SERVERID123456789"
```

### Validation

A valid notx URN must satisfy all of the following:

- [ ] Begins with the literal prefix `urn:notx:` (case-sensitive)
- [ ] `<type>` is one of the defined types, or an unrecognized type treated as opaque for forward compatibility
- [ ] `<id>` matches one of:
  - ULID: `^[0-9A-Z]{26}$` (26 uppercase Crockford Base32 characters)
  - UUID: `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
  - Sentinel: `^anon$` (only valid for type `usr`)
- [ ] Total URN length ≤ 256 characters (practical limit)
- [ ] No embedded whitespace, null bytes, or control characters

Invalid URNs must be rejected early with a clear error. Silent misinterpretation of a malformed URN can cause data integrity issues.

## Entity Metadata Schemas

Every URN type has an associated **metadata schema**—the set of fields present when the entity is stored or resolved.

### `note` Entity

| Field           | Type           | Nullable | Description                                                         |
| --------------- | -------------- | -------- | ------------------------------------------------------------------- |
| `id`            | URN string     | No       | `urn:notx:note:<id>` — globally unique, immutable                   |
| `authority`     | URN string     | No       | `urn:notx:srv:<id>` — the server that owns this note                |
| `namespace`     | string         | Yes      | Logical grouping label (e.g., `"acme"`, `"personal"`)               |
| `name`          | string         | No       | Display name or title of the note                                   |
| `content`       | string         | No       | Current materialized content (empty string if no events)            |
| `head_sequence` | integer        | No       | Sequence number of the last applied event (0 if none)               |
| `project_id`    | URN or null    | Yes      | `urn:notx:proj:<id>` — the note's containing project                |
| `folder_id`     | URN or null    | Yes      | `urn:notx:folder:<id>` — the note's containing folder               |
| `parent_id`     | URN or null    | Yes      | `urn:notx:note:<id>` — parent note for hierarchical structures      |
| `node_links`    | map[string]URN | No       | Graph links: node ID → target note URN (may reference remote notes) |
| `deleted`       | boolean        | No       | Soft-delete flag (default: false)                                   |
| `created_at`    | ISO-8601 UTC   | No       | Note creation timestamp (immutable)                                 |
| `updated_at`    | ISO-8601 UTC   | No       | Timestamp of last change (derived from last event)                  |

**Fields never exposed to other servers:**

- `content` — the note's actual text/data
- `head_sequence` — internal implementation detail
- `node_links` — relationship metadata

### `usr` Entity

| Field         | Type         | Nullable | Description                                          |
| ------------- | ------------ | -------- | ---------------------------------------------------- |
| `id`          | URN string   | No       | `urn:notx:usr:<id>` — globally unique, immutable     |
| `authority`   | URN string   | No       | `urn:notx:srv:<id>` — the server that owns this user |
| `namespace`   | string       | Yes      | Logical grouping label                               |
| `name`        | string       | No       | Display name (e.g., `"Alice Smith"`)                 |
| `email`       | string       | No       | Primary email address (private; never cross-server)  |
| `profile_pic` | URL or null  | Yes      | Avatar image URL (absolute, CDN or server)           |
| `org_id`      | URN or null  | Yes      | `urn:notx:org:<id>` — primary organization           |
| `created_at`  | ISO-8601 UTC | No       | Account creation timestamp                           |

**Safe for cross-server display:** `id`, `name`, `profile_pic`

**Never exposed cross-server:** `email`, `org_id`

### `proj` Entity

| Field        | Type         | Nullable | Description                                             |
| ------------ | ------------ | -------- | ------------------------------------------------------- |
| `id`         | URN string   | No       | `urn:notx:proj:<id>` — globally unique, immutable       |
| `authority`  | URN string   | No       | `urn:notx:srv:<id>` — the server that owns this project |
| `namespace`  | string       | Yes      | Logical grouping label                                  |
| `name`       | string       | No       | Project display name                                    |
| `org_id`     | URN or null  | Yes      | `urn:notx:org:<id>` — owning organization               |
| `created_at` | ISO-8601 UTC | No       | Project creation timestamp                              |

### `org` Entity

| Field        | Type         | Nullable | Description                                                               |
| ------------ | ------------ | -------- | ------------------------------------------------------------------------- |
| `id`         | URN string   | No       | `urn:notx:org:<id>` — globally unique, immutable                          |
| `authority`  | URN string   | No       | `urn:notx:srv:<id>` — the server that owns this organization              |
| `namespace`  | string       | Yes      | Logical grouping label                                                    |
| `name`       | string       | No       | Organization display name (e.g., `"Acme Corp"`)                           |
| `org_slug`   | string       | No       | Short, URL-safe identifier (e.g., `acme`). Used in UI, URLs, and sharing. |
| `created_at` | ISO-8601 UTC | No       | Organization creation timestamp                                           |

**`org_slug` vs `id`:**

- `id` is the globally unique identity: `urn:notx:org:01HZX3K8ORGID12345678901`
- `org_slug` is a human-friendly short name for UI and URL use: `acme`

The `org_slug` is scoped to a single server. It has no global uniqueness guarantee and does not appear in any URN.

### `folder` Entity

| Field        | Type         | Nullable | Description                                            |
| ------------ | ------------ | -------- | ------------------------------------------------------ |
| `id`         | URN string   | No       | `urn:notx:folder:<id>` — globally unique, immutable    |
| `authority`  | URN string   | No       | `urn:notx:srv:<id>` — the server that owns this folder |
| `namespace`  | string       | Yes      | Logical grouping label                                 |
| `name`       | string       | No       | Folder display name                                    |
| `project_id` | URN or null  | Yes      | `urn:notx:proj:<id>` — containing project              |
| `parent_id`  | URN or null  | Yes      | `urn:notx:folder:<id>` — parent folder                 |
| `created_at` | ISO-8601 UTC | No       | Folder creation timestamp                              |

### `event` Entity

Events are internal to each server and are never exposed to other servers.

| Field        | Type         | Nullable | Description                                               |
| ------------ | ------------ | -------- | --------------------------------------------------------- |
| `id`         | URN string   | No       | `urn:notx:event:<id>` — globally unique, immutable        |
| `authority`  | URN string   | No       | `urn:notx:srv:<id>` — the server that owns this event     |
| `note_id`    | URN string   | No       | `urn:notx:note:<id>` — the note this event belongs to     |
| `sequence`   | integer      | No       | Monotonically increasing position within the note (≥ 1)   |
| `author_id`  | URN string   | No       | `urn:notx:usr:<id>` or `urn:notx:usr:anon`                |
| `label`      | string       | No       | Human-readable description (e.g., `"Edit at 2:34 PM"`)    |
| `payload`    | string       | No       | Lane-format delta. See NOTX_FORMAT.md for format details. |
| `created_at` | ISO-8601 UTC | No       | When the event was recorded                               |

**Field details:**

- **`sequence`** — The event's position within its note. Events are ordered by sequence. No gaps are allowed within a note's event log.
- **`label`** — A user-friendly description auto-generated by the client (may be edited post-hoc without changing the event's content).
- **`payload`** — The lane-format representation of line changes.

### `snapshot` Entity (Optimization)

Snapshots are not independently addressable and have no URN. They are internal metadata associated with a note at a specific sequence point:

| Field        | Type         | Description                                              |
| ------------ | ------------ | -------------------------------------------------------- |
| `note_id`    | URN string   | `urn:notx:note:<id>` — the note this snapshot belongs to |
| `sequence`   | integer      | Sequence of the last event included in this snapshot     |
| `content`    | string       | Full materialized content at `sequence`                  |
| `created_at` | ISO-8601 UTC | When the snapshot was written                            |

Snapshots are purely an optimization layer. They are never exchanged between servers.

### `srv` Entity

| Field        | Type         | Nullable | Description                                                                |
| ------------ | ------------ | -------- | -------------------------------------------------------------------------- |
| `id`         | URN string   | No       | `urn:notx:srv:<id>` — globally unique, immutable server identity           |
| `name`       | string       | No       | Human-readable server name (e.g., `"Acme Internal notx"`)                  |
| `endpoint`   | URL string   | No       | The base URL of this server's API (e.g., `"https://notes.acme.internal"`)  |
| `public_key` | string       | No       | Server's Ed25519 public key (base64), used for inter-server authentication |
| `created_at` | ISO-8601 UTC | No       | When this server was initialized                                           |

The `srv` entity is how servers register themselves in routing tables and authenticate to one another.

## URN Resolution

**Resolution** is the process of taking a URN and returning the entity's data. Because URNs are globally unique and do not encode a server address, resolution follows a consistent algorithm regardless of whether the object is local or remote.

### Resolution Algorithm

```
resolve(urn):
  1. Validate the URN format.
  2. Look up the URN in local storage.
  3. If found locally → return full local data.
  4. If not found:
       a. Look up the URN in the local routing table to find the authority server URN.
       b. If no entry in routing table → return "not found" (or query a known peer for routing info).
       c. Resolve the authority server URN to an endpoint URL.
       d. Fetch the object from the remote server via its API.
       e. Cache the result locally with an appropriate TTL.
       f. Return the fetched data.
```

### Local Resolution

When an object with the given URN exists in local storage, return the full local metadata immediately. No namespace check is needed — the URN is the key.

```
resolve("urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ")
  → query local storage WHERE id = "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ"
  → found: return { id, authority, namespace, name, content, head_sequence, ... }
  → not found: proceed to remote resolution
```

### Remote Resolution

When an object is not in local storage, the routing table maps the object's URN (or its authority server URN) to an endpoint:

```
Routing table:
  authority URN                           → endpoint URL
  urn:notx:srv:01HZSERVER123456789ABCDEF  → https://notes.acme.internal/api/v1
  urn:notx:srv:01HZSERVER987654321FEDCBA  → https://notes.partner.io/api/v1

resolve("urn:notx:note:01HZX3K8REMOTENOTE1234")
  → not in local storage
  → look up routing table for this URN → authority = "urn:notx:srv:01HZSERVER987654321FEDCBA"
  → resolve authority endpoint → "https://notes.partner.io/api/v1"
  → GET https://notes.partner.io/api/v1/objects/urn:notx:note:01HZX3K8REMOTENOTE1234
  → receive { id, authority, namespace, name, author_id, created_at }
  → cache locally with TTL = 1 hour
  → return result
```

Cross-server fetches return only safe, non-content metadata. Full content (note text, event logs) is never transmitted by a remote resolution query.

### Routing Table

The routing table maps:

```
object_id  →  authority (server URN)  →  endpoint URL
```

Entries are populated by:

- Server registration (when a peer server introduces itself)
- Object import (the imported object carries its `authority` field)
- Explicit routing configuration by the server operator

The routing table is an implementation detail. Conformant implementations may use any backing store (database table, in-memory cache, distributed KV store).

### Sentinel Resolution

The sentinel `urn:notx:usr:anon` always resolves to a fixed, hardcoded record:

```json
{
  "id": "urn:notx:usr:anon",
  "authority": null,
  "namespace": null,
  "name": "Anonymous",
  "profile_pic": null
}
```

No database lookup or remote query is performed for this sentinel. Implementations must short-circuit resolution when the URN is `urn:notx:usr:anon`.

## URN Construction

### Generating New URNs

To create a new entity, generate a new ULID (preferred) or UUID and combine it with the type prefix:

```go
import (
    "fmt"
    "github.com/oklog/ulid/v2"
    "math/rand"
    "time"
)

func NewURN(objectType string) string {
    entropy := ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)
    id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
    return fmt.Sprintf("urn:notx:%s:%s", objectType, id)
}

// Usage
noteURN   := NewURN("note")   // "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ"
userURN   := NewURN("usr")    // "urn:notx:usr:01HZX3K8ABCDEF1234567890"
serverURN := NewURN("srv")    // "urn:notx:srv:01HZX3K8SERVERID123456789"
```

Or with UUIDs:

```go
import (
    "fmt"
    "github.com/google/uuid"
)

func NewURN(objectType string) string {
    id := uuid.New().String() // UUID v4
    return fmt.Sprintf("urn:notx:%s:%s", objectType, id)
}
```

### Server Initialization

A server generates its own URN exactly once, at initialization, and stores it permanently:

```go
func InitializeServer() string {
    serverURN := NewURN("srv")
    // Persist serverURN to configuration store — never regenerate
    return serverURN
}
```

The server URN is the value placed in the `authority` field of every object the server creates or accepts ownership of.

### ID Uniqueness

ULIDs and UUIDs are generated locally without coordination. The probability of collision is negligible for practical deployments (2^80 bits of randomness in ULID, 2^122 in UUID v4). No central authority assigns or validates IDs.

## Encoding and Escaping

### URI/URL Encoding

When a URN appears in a URL or REST API path, it must be percent-encoded. Colons (`:`) are reserved characters in URIs and must be encoded as `%3A`:

```
Literal URN:    urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
Percent-encoded: urn%3Anotx%3Anote%3A01HZX3K8J9X2M4P7R8T1Y6ZQ
In URL path:    /api/v1/objects/urn%3Anotx%3Anote%3A01HZX3K8J9X2M4P7R8T1Y6ZQ
```

Always percent-decode before parsing. Always percent-encode before embedding in a URL.

### JSON Encoding

In JSON, URNs are plain strings. No special encoding is required beyond standard JSON string escaping:

```json
{
  "id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZX3K8SERVERID123456789",
  "namespace": "acme",
  "name": "Quarterly Planning",
  "project_id": "urn:notx:proj:01HZX3K8PROJID123456789"
}
```

### Query Parameter Encoding

When passing a URN as a query parameter, percent-encode the entire URN:

```
GET /api/v1/resolve?urn=urn%3Anotx%3Anote%3A01HZX3K8J9X2M4P7R8T1Y6ZQ
```

## Examples

### Basic URNs

```
urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ      ← A note
urn:notx:usr:01HZX3K8ABCDEF1234567890        ← A user
urn:notx:usr:anon                             ← Anonymous author (global sentinel)
urn:notx:proj:01HZX3K8PROJID123456789        ← A project
urn:notx:org:01HZX3K8ORGID12345678901        ← An organization
urn:notx:folder:01HZX3K8FOLDERID1234567      ← A folder
urn:notx:event:01HZX3K8EVENTID123456789      ← A history event
urn:notx:device:01HZX3K8DEVICEID12345678     ← A user device (E2EE)
urn:notx:srv:01HZX3K8SERVERID123456789       ← A server instance
```

### Object With Authority and Namespace

A note created on a server whose URN is `urn:notx:srv:01HZSERVER123456789ABCDEF`, belonging to the logical group `"acme"`:

```json
{
  "id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZSERVER123456789ABCDEF",
  "namespace": "acme",
  "name": "Vendor Integration Plan",
  "content": "...",
  "head_sequence": 14,
  "project_id": "urn:notx:proj:01HZX3K8PROJID123456789",
  "folder_id": null,
  "parent_id": null,
  "node_links": {},
  "deleted": false,
  "created_at": "2025-01-15T10:23:00Z",
  "updated_at": "2025-06-01T14:55:00Z"
}
```

The same note may be cached on a different server. The `id` never changes. The `authority` points back to the owning server.

### Cross-Server References

Because URNs are globally unique, a note on one server can reference a note on another server using the same URN format — no special cross-instance syntax is needed:

```json
{
  "id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZSERVER123456789ABCDEF",
  "namespace": "acme",
  "name": "Vendor Integration Plan",
  "content": "...",
  "node_links": {
    "requirements": "urn:notx:note:01HZX3K8REMOTENOTE123456",
    "api_docs": "urn:notx:note:01HZX3K8ANOTHERNOTE12345"
  }
}
```

The two notes referenced in `node_links` may live on entirely different servers. The resolver follows the standard algorithm for each: check local → look up authority → fetch remote → cache.

### Event With Anonymous Author

```json
{
  "id": "urn:notx:event:01HZX3K8EVENTID123456789",
  "authority": "urn:notx:srv:01HZSERVER123456789ABCDEF",
  "note_id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "sequence": 1,
  "author_id": "urn:notx:usr:anon",
  "label": "Initial content",
  "payload": "1|Hello, world.",
  "created_at": "2025-01-15T10:23:00Z"
}
```

### Resolution Example

Two servers: `srv-a` (`urn:notx:srv:01HZSERVERA1234567890`) and `srv-b` (`urn:notx:srv:01HZSERVERB1234567890`).

```
On srv-a:

  resolve("urn:notx:note:01HZX3K8LOCALNOTE1234567")
    → found in local storage (authority = urn:notx:srv:01HZSERVERA1234567890)
    → return full local metadata

  resolve("urn:notx:note:01HZX3K8REMOTENOTE123456")
    → not in local storage
    → routing table: authority = urn:notx:srv:01HZSERVERB1234567890
    → endpoint: https://notes.partner.io/api/v1
    → GET https://notes.partner.io/api/v1/objects/urn%3Anotx%3Anote%3A01HZX3K8REMOTENOTE123456
    → receive { id, authority, namespace, name, author_id, created_at }
    → cache for 1 hour
    → return metadata (no content, no events)

  resolve("urn:notx:usr:anon")
    → hardcoded sentinel: { id: "urn:notx:usr:anon", name: "Anonymous", profile_pic: null }
    → no storage lookup performed
```

No IDs are rewritten at any point in this exchange.

### Device URN in Encrypted Event

```
Encrypted event block:

  key[urn:notx:device:01HZX3K8DEVICEID12345678]: <base64-wrapped-cek>
  key[urn:notx:device:01HZX3K8DEVICEID98765432]: <base64-wrapped-cek>
  ciphertext: <base64-encrypted-payload>
```

The device URNs identify which devices can decrypt the content. The same globally unique URN format is used as everywhere else.

## Conformance

A conformant notx implementation must:

- [ ] Parse URNs in the `urn:notx:<type>:<id>` format
- [ ] Reject URNs that do not begin with the literal prefix `urn:notx:`
- [ ] Reject URNs with an `<id>` that does not match ULID format, UUID format, or a recognized sentinel
- [ ] Treat URN strings as case-sensitive (ULID segments are uppercase; the `urn:notx:` prefix is lowercase)
- [ ] Never encode namespace, server name, or any location/ownership information in a URN
- [ ] Store `authority` (as a `urn:notx:srv:<id>`) and `namespace` (as a string) as separate metadata fields on every object
- [ ] Resolve `urn:notx:usr:anon` as a hardcoded sentinel without any storage or network lookup
- [ ] Implement the standard resolution algorithm: local → routing table → remote fetch → cache
- [ ] Never rewrite object URNs when importing, exporting, caching, or transferring objects between servers
- [ ] Percent-encode URNs when embedding them in URLs; percent-decode before parsing
- [ ] Generate a single, permanent server URN (`urn:notx:srv:<id>`) at initialization and use it as the `authority` for all locally owned objects
- [ ] Preserve all URN references faithfully across import/export without transformation
- [ ] For cross-server resolution: return only safe metadata fields; never transmit note content or event payloads in response to a metadata resolution request
- [ ] Forward-compatible: treat unrecognized `<type>` values as opaque identifiers rather than errors, to allow new types to be introduced without breaking older implementations
