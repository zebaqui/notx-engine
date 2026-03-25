# notx URN Specification

## Overview

A **URN** (Uniform Resource Name) is a standardized identifier for any entity in a notx ecosystem. Every identifiable object—notes, users, projects, organizations, folders, and events—has a URN that uniquely identifies it within its instance or deployment.

The URN scheme enables:

- **Instance identity** — Every notx instance (platform or self-hosted) has a unique namespace
- **Global federation** — Resources can be referenced across instances with unambiguous identity
- **Portability** — A resource exported from one instance carries its namespace and identity when imported into another
- **Self-description** — A URN alone tells you which instance owns a resource, its type, and its unique ID
- **Metadata resolution** — Instances can resolve user/org metadata across boundaries for UX purposes (read-only, restricted API keys)

## URN Syntax

All notx URNs follow a three-segment structure:

```
<namespace>:<object-type>:<uuid>
```

### Segments

| Segment       | Description                                                                                                | Example                                |
| ------------- | ---------------------------------------------------------------------------------------------------------- | -------------------------------------- |
| `namespace`   | Instance identifier. `notx` for the official platform; custom for self-hosted (e.g., `acme`, `mycompany`). | `notx`, `acme`, `mycompany`            |
| `object-type` | The entity class. Defined in the table below.                                                              | `note`, `usr`, `proj`                  |
| `uuid`        | A UUID v4 or v7. Unique within the object-type on that instance.                                           | `018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a` |

### Delimiters

The delimiter between segments is **colon** (`:`, ASCII 0x3A). There are no spaces, hyphens, or other characters.

Example:

```
notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
```

### UUID Format

The `uuid` segment is a standard UUID (36 characters, lowercase):

```
018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
```

Both UUID v4 (random) and UUID v7 (time-ordered) are acceptable. UUID v7 is preferred for new implementations because it provides better index locality in databases.

### Namespace Format

The `namespace` segment is a short, alphanumeric identifier unique to each notx instance:

| Namespace | Type                   | Example                                     | Notes                                                               |
| --------- | ---------------------- | ------------------------------------------- | ------------------------------------------------------------------- |
| `notx`    | Official SaaS platform | `notx:note:<uuid>`                          | Reserved for official notx platform only                            |
| Custom    | Self-hosted instance   | `acme:note:<uuid>`, `mycompany:note:<uuid>` | Must be registered for federation; lowercase, alphanumeric + hyphen |

**Namespace registration** — Self-hosted instances choose a unique namespace and register it in a namespace registry (if federation is desired). This registration is purely for **metadata resolution compatibility** and does **not** grant any data access. The registry allows instances to discover each other's metadata APIs.

## Instance Types

### Official notx Platform

- **Namespace**: `notx`
- **Deployment**: SaaS, multi-tenant, operated by notx
- **Scope**: All resources created on platform.notx.io use the `notx` namespace
- **Example URNs**:
  ```
  notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
  notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
  notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
  ```

### Self-Hosted Instance

- **Namespace**: Custom identifier (e.g., `acme`, `company-xyz`, `internal`)
- **Deployment**: Private, self-operated, behind organizational firewall or on-premises
- **Scope**: All resources on this instance use the custom namespace
- **Example URNs**:
  ```
  acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
  acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
  acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
  ```

## Defined Object Types

The following object types are defined in the notx specification:

### Core Types

| Type    | Description                          | Scope                                  | Example URNs                                                                                          |
| ------- | ------------------------------------ | -------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `note`  | A note document with content history | Per-instance (unique within namespace) | `notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a`<br/>`acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a` |
| `event` | A history event within a note        | Per-note                               | `notx:event:2d3e4f5a-6b7c-8d9e-0f1a-2b3c4d5e6f7a`                                                     |

### User and Organization Types

| Type  | Description                 | Scope        | Example URNs                                                                                        |
| ----- | --------------------------- | ------------ | --------------------------------------------------------------------------------------------------- |
| `usr` | A registered user or author | Per-instance | `notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b`<br/>`acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b` |
| `org` | An organization             | Per-instance | `notx:org:9e8d7c6b-5a4f-3e2d-1c0b-9a8f7e6d5c4b`                                                     |

### Project and Folder Types

| Type     | Description               | Scope        | Example URNs                                       |
| -------- | ------------------------- | ------------ | -------------------------------------------------- |
| `proj`   | A project container       | Per-instance | `notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d`   |
| `folder` | A folder within a project | Per-instance | `notx:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f` |

### Special Sentinel Values

The following are reserved URN values with special meaning:

| URN                    | Type        | Meaning                                               | Scope            |
| ---------------------- | ----------- | ----------------------------------------------------- | ---------------- |
| `notx:usr:anon`        | `notx` only | Anonymous or unknown author on the official platform. | Platform only    |
| `<namespace>:usr:anon` | Any         | Could be defined per-instance for consistency         | Self-hosted only |

When an edit is made by an unauthenticated user or the author is not known, the instance-specific anonymous sentinel is used:

- On the `notx` platform: `notx:usr:anon`
- On `acme` self-hosted: `acme:usr:anon`

A parser encountering a `<namespace>:usr:anon` should treat it as "author unknown on that instance" rather than attempting to resolve it as a user record.

## Cross-Instance URN References

When a note on instance A references a note on instance B, the URN includes the source namespace:

```
Instance A (acme):        acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
Instance B (mycompany):   mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
Cross-reference in A:     "Also see mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
```

### Important Rules

- **No data pull** — A URN to a foreign resource (different namespace) is a **reference only**. The instance does not automatically fetch or replicate that data. The note's content always lives on its home instance.
- **Metadata resolution only** — When instance A encounters `mycompany:note:<uuid>`, it may:
  1. Query the mycompany instance's **restricted metadata API** for the note's name, author info, and timestamp (UX display)
  2. Never fetch the note's content (`payload`, `events`, etc.)
  3. Use cached metadata with a TTL to avoid repeated queries
  4. Gracefully degrade if the remote is unreachable (show the raw URN)
- **No mutual data sharing** — Two instances can reference each other's notes, but neither pulls the other's event log or content. They remain independent sources of truth for their own data.

## Namespace Registry

Self-hosted instances can register their namespace in a public or private namespace registry to enable federation. The registry is **metadata-only**:

```
{
  "namespace": "acme",
  "instance_name": "Acme Corp notx",
  "metadata_api": "https://notes.acme.internal/api/v1/metadata",
  "public": false,
  "admin_email": "admin@acme.com"
}
```

**Registry purposes:**

- Discovery — Other instances can find this namespace and its metadata endpoint
- UX resolution — When showing `acme:usr:<uuid>`, an instance can query the metadata API to display the user's name and avatar
- Validation — Instances can verify that a namespace actually exists before accepting URNs from it

**Registry does NOT:**

- Grant data access
- Enable content replication
- Expose authentication credentials
- Replace local access control

The metadata API should use restrictive, read-only API keys that allow only:

- User metadata (name, email, avatar URL)
- Organization metadata (name, slug)
- Note basic metadata (name, created_at, author)
- Never full note content or event history

## Parsing and Validation

### Parsing Rules

To parse a URN string:

1. Split on `:` (colon) to get tokens
2. The first token is `namespace`
3. The second token is `object-type`
4. All remaining tokens joined with `:` form the `uuid`

```
Input: "notx:usr:anon"
Tokens: ["notx", "usr", "anon"]
Result: namespace="notx", type="usr", uuid="anon"

Input: "acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
Tokens: ["acme", "note", "018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"]
Result: namespace="acme", type="note", uuid="018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
```

### Validation

A valid notx URN must satisfy:

- [ ] Namespace matches `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$` (alphanumeric + hyphen, no leading/trailing hyphens)
- [ ] Object-type is one of the defined types (or an unknown type for forward compatibility)
- [ ] UUID segment matches `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` (standard UUID format) OR is `anon`
- [ ] Total length ≤ 256 characters (practical limit)

Invalid URNs should be rejected early to prevent silent misinterpretation.

## Entity Metadata Schemas

Every URN type has an associated **metadata schema**—the set of fields that must be present when the entity is resolved locally. For cross-instance queries, only a subset of fields is exposed via the restricted metadata API.

### `note` Entity

**Local metadata (full, stored on home instance):**

| Field           | Type           | Nullable | Description                                                    |
| --------------- | -------------- | -------- | -------------------------------------------------------------- |
| `urn`           | URN string     | No       | `<namespace>:note:<uuid>`                                      |
| `name`          | string         | No       | Display name or title of the note                              |
| `content`       | string         | No       | Current materialized content (empty string if no events)       |
| `head_sequence` | integer        | No       | Sequence number of the last applied event (0 if none)          |
| `project_urn`   | URN or null    | Yes      | `<namespace>:proj:<uuid>` — the note's containing project      |
| `folder_urn`    | URN or null    | Yes      | `<namespace>:folder:<uuid>` — the note's containing folder     |
| `parent_urn`    | URN or null    | Yes      | `<namespace>:note:<uuid>` — parent note (hierarchical)         |
| `node_links`    | map[string]URN | No       | Graph links: node ID → target note URN (can be cross-instance) |
| `deleted`       | boolean        | No       | Soft-delete flag (default: false)                              |
| `created_at`    | ISO-8601 UTC   | No       | Note creation timestamp (immutable)                            |
| `updated_at`    | ISO-8601 UTC   | No       | Timestamp of last change (derived from last event)             |

**Exposed via restricted metadata API (cross-instance):**

| Field        | Type         | Description                                |
| ------------ | ------------ | ------------------------------------------ |
| `urn`        | URN string   | `<namespace>:note:<uuid>`                  |
| `name`       | string       | Display name or title of the note          |
| `author_urn` | URN string   | The user URN of the note's original author |
| `created_at` | ISO-8601 UTC | Note creation timestamp                    |
| `updated_at` | ISO-8601 UTC | Timestamp of last change                   |

**Never exposed:**

- `content` — the note's actual text/data
- `head_sequence` — internal implementation detail
- `node_links` — relationship metadata
- Anything that constitutes the note's substance

### `usr` Entity

**Local metadata (full):**

| Field         | Type         | Nullable | Description                                       |
| ------------- | ------------ | -------- | ------------------------------------------------- |
| `urn`         | URN string   | No       | `<namespace>:usr:<uuid>`                          |
| `name`        | string       | No       | Display name (e.g., "Alice Smith", "Bob Johnson") |
| `email`       | string       | No       | Primary email address                             |
| `profile_pic` | URL or null  | Yes      | Avatar image URL (absolute, CDN or server)        |
| `org_urn`     | URN or null  | Yes      | `<namespace>:org:<uuid>` — primary organization   |
| `created_at`  | ISO-8601 UTC | No       | Account creation timestamp                        |

**Exposed via restricted metadata API (cross-instance):**

| Field         | Type        | Description                   |
| ------------- | ----------- | ----------------------------- |
| `urn`         | URN string  | `<namespace>:usr:<uuid>`      |
| `name`        | string      | Display name for UI rendering |
| `profile_pic` | URL or null | Avatar for UI display         |

**Never exposed:**

- `email` — private contact information
- `org_urn` — organizational membership

### `proj` Entity

**Local metadata (full):**

| Field        | Type         | Nullable | Description                           |
| ------------ | ------------ | -------- | ------------------------------------- |
| `urn`        | URN string   | No       | `<namespace>:proj:<uuid>`             |
| `name`       | string       | No       | Project display name                  |
| `org_urn`    | URN or null  | Yes      | `<namespace>:org:<uuid>` — owning org |
| `created_at` | ISO-8601 UTC | No       | Project creation timestamp            |

**Exposed via restricted metadata API:**

| Field  | Type       | Description               |
| ------ | ---------- | ------------------------- |
| `urn`  | URN string | `<namespace>:proj:<uuid>` |
| `name` | string     | Project display name      |

### `org` Entity

**Local metadata (full):**

| Field        | Type         | Nullable | Description                                                                        |
| ------------ | ------------ | -------- | ---------------------------------------------------------------------------------- |
| `urn`        | URN string   | No       | `<namespace>:org:<uuid>`                                                           |
| `name`       | string       | No       | Organization display name (e.g., "Acme Corp", "Startup Inc.")                      |
| `org_slug`   | string       | No       | Short, URL-safe identifier (e.g., `acme`, `startup`). Used for access and sharing. |
| `created_at` | ISO-8601 UTC | No       | Organization creation timestamp                                                    |

**Exposed via restricted metadata API:**

| Field  | Type       | Description               |
| ------ | ---------- | ------------------------- |
| `urn`  | URN string | `<namespace>:org:<uuid>`  |
| `name` | string     | Organization display name |

**`org_slug` vs `urn`:**

- The `urn` is the globally unique identity: `acme:org:9e8d7c6b-5a4f-3e2d-1c0b-9a8f7e6d5c4b`
- The `org_slug` is a human-friendly short name used in URLs, for grouping, and for shared resource access: `acme`

The `org_slug` is **not** part of any URN. Two different organizations cannot have the same `org_slug` within a single instance, but the slug itself is not globally unique across all instances.

### `folder` Entity

**Local metadata (full):**

| Field         | Type         | Nullable | Description                                    |
| ------------- | ------------ | -------- | ---------------------------------------------- |
| `urn`         | URN string   | No       | `<namespace>:folder:<uuid>`                    |
| `name`        | string       | No       | Folder display name                            |
| `project_urn` | URN or null  | Yes      | `<namespace>:proj:<uuid>` — containing project |
| `parent_urn`  | URN or null  | Yes      | `<namespace>:folder:<uuid>` — parent folder    |
| `created_at`  | ISO-8601 UTC | No       | Folder creation timestamp                      |

**Exposed via restricted metadata API:**

| Field  | Type       | Description                 |
| ------ | ---------- | --------------------------- |
| `urn`  | URN string | `<namespace>:folder:<uuid>` |
| `name` | string     | Folder display name         |

### `event` Entity

Events are never exposed via cross-instance APIs. They are internal to each instance.

**Local metadata (full):**

| Field        | Type         | Nullable | Description                                             |
| ------------ | ------------ | -------- | ------------------------------------------------------- | ------------ | -------- | --- |
| `urn`        | URN string   | No       | `<namespace>:event:<uuid>`                              |
| `note_urn`   | URN string   | No       | The note this event belongs to                          |
| `sequence`   | integer      | No       | Monotonically increasing position within the note (≥ 1) |
| `author_urn` | URN string   | No       | `<namespace>:usr:<uuid>` or `<namespace>:usr:anon`      |
| `label`      | string       | No       | Human-readable description (e.g., "Edit at 2:34 PM")    |
| `payload`    | string       | No       | Lane-format delta: `N                                   | content`, `N | `, or `N | -`  |
| `created_at` | ISO-8601 UTC | No       | When the event was recorded                             |

**Field Details:**

- **`sequence`** — The event's position within its note. Events are ordered by sequence. No gaps are allowed.
- **`label`** — A user-friendly description auto-generated by the client (can be edited post-hoc without changing the event's content).
- **`payload`** — The lane-format representation of line changes. See the NOTX_FORMAT.md specification for format details.

### `snapshot` Entity (Optimization)

Snapshots are not independently addressable; they have no URN. They are internal metadata associated with a note at a specific sequence:

| Field        | Type         | Description                                          |
| ------------ | ------------ | ---------------------------------------------------- |
| `note_urn`   | URN string   | The note this snapshot belongs to                    |
| `sequence`   | integer      | Sequence of the last event included in this snapshot |
| `content`    | string       | Full materialized content at `sequence`              |
| `created_at` | ISO-8601 UTC | When the snapshot was written                        |

Snapshots are purely an optimization layer. They are never exposed to other instances.

## URN Resolution

**Resolution** is the process of taking a URN string and returning the entity's metadata.

### Local Resolution

Within a single notx instance, a resolver must:

1. Validate the URN format (see Parsing and Validation, above)
2. Verify the namespace matches the local instance's namespace
3. Look up the entity in the local database by type and UUID
4. Return the full local metadata if found; return an error if not found

```
Instance: acme

resolve("acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
  → query notes table where urn = "acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
  → return { urn, name, content, head_sequence, ... }
  → or return "not found" if no row matches

resolve("mycompany:note:018e4f2a...")
  → error: foreign namespace, cannot resolve locally
```

### Cross-Instance Metadata Resolution

When a URN points to a resource on a different instance, the local instance can:

1. **Query the remote metadata API** — Contact the peer instance's metadata resolver service with the URN and a read-only API key
2. **Cache the result** — Store resolved metadata locally with a TTL (e.g., 1 hour)
3. **Return error or placeholder** — If the remote cannot be reached or the resource is not found, return an error or display the raw URN
4. **Reject foreign URNs** — If federation is not enabled, reject resources from unknown instances

Example:

```
Instance: acme

resolve("mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b")
  → query local cache for this URN
  → cache miss
  → query namespace registry for "mycompany" → get metadata_api = "https://notes.mycompany.io/api/v1/metadata"
  → POST /metadata with { urn: "mycompany:note:...", api_key: "<restricted-key>" }
  → get { name: "Quarterly Planning", author_urn: "mycompany:usr:...", created_at: "2025-01-15T..." }
  → cache for 1 hour
  → return metadata

Note: never fetches mycompany:note:...'s content or events
```

### Sentinel Resolution

The sentinel `<namespace>:usr:anon` always resolves to:

```json
{
  "urn": "<namespace>:usr:anon",
  "name": "Anonymous",
  "profile_pic": null
}
```

No database lookup or remote query is needed.

## URN Construction

### Generating New URNs

To create a new entity on an instance, generate:

1. A new UUID (v4 or v7)
2. The instance's namespace
3. The appropriate object-type
4. Construct the URN as `<namespace>:<type>:<uuid>`

```go
import "github.com/google/uuid"

// On the notx platform
func NewNoteURN() string {
  id := uuid.New().String()
  return fmt.Sprintf("notx:note:%s", id)
}

// On acme self-hosted instance
func NewNoteURN() string {
  id := uuid.New().String()
  return fmt.Sprintf("acme:note:%s", id)
}
```

### UUID Uniqueness

UUIDs are generated locally (not assigned by a central authority). The probability of collision is negligible for practical purposes (1 in 2^122 for v4). Each instance generates UUIDs independently; there is no registry or coordination needed.

## Encoding and Escaping

### URI/URL Encoding

When a URN appears in a URL or REST API path, it must be URI-encoded. Colons are reserved characters in URIs:

```
Literal URN:        notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
URI-encoded:        notx%3Anote%3A018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
In URL path:        /notes/notx%3Anote%3A018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
```

When extracting a URN from a URL, always URI-decode first.

### JSON Encoding

In JSON, URNs are plain strings. No special encoding is required:

```json
{
  "note_urn": "notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a",
  "name": "Meeting Notes",
  "project_urn": "acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
}
```

## Examples

### Official Platform URNs

```
notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a     ← Note on notx platform
notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b      ← User on notx platform
notx:usr:anon                                        ← Anonymous author on notx platform
notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d     ← Project on notx platform
notx:org:9e8d7c6b-5a4f-3e2d-1c0b-9a8f7e6d5c4b      ← Organization on notx platform
notx:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f   ← Folder on notx platform
notx:event:2d3e4f5a-6b7c-8d9e-0f1a-2b3c4d5e6f7a    ← Event on notx platform
```

### Self-Hosted Instance URNs

```
acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a     ← Note on acme self-hosted
acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b      ← User on acme self-hosted
acme:usr:anon                                        ← Anonymous author on acme self-hosted
acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d     ← Project on acme self-hosted

mycompany:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f ← Folder on mycompany self-hosted
```

### Cross-Instance References

```json
{
  "note_urn": "acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a",
  "name": "Vendor Integration",
  "content": "...",
  "node_links": {
    "requirements": "mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b",
    "api_docs": "notx:note:9c8d7e6f-5a4b-3c2d-1e0f-9a8b7c6d5e4f"
  }
}
```

The note on `acme` references notes on `mycompany` and the `notx` platform. These are read-only references.

### Metadata Resolution Example

Instance: `acme`
Restricted metadata API key registered with `mycompany`

```
Local query:   resolve("acme:note:018e4f2a...")
               → full local metadata { urn, name, content, head_sequence, ... }

Remote query:  resolve("mycompany:note:7c3e9f1a...")
               → restricted API { urn, name, author_urn, created_at }
               → cache for 1 hour
               → do NOT have content or events
```

## Conformance

A conformant notx implementation must:

- [ ] Recognize and parse all defined URN types
- [ ] Validate URN format (namespace, object-type, uuid segments)
- [ ] Reject invalid URNs with clear error messages
- [ ] Preserve URN case (treat as case-sensitive strings)
- [ ] Support the `<namespace>:usr:anon` sentinel for anonymous authors
- [ ] Implement local entity resolution (lookup by URN within the instance's namespace)
- [ ] Preserve URNs faithfully across import/export
- [ ] For self-hosted: choose a unique namespace and register it for federation (optional)
- [ ] For federation: implement restricted metadata API with read-only access to safe fields
- [ ] For federation: never expose content, events, or private user information to other instances
- [ ] Document the instance's namespace and metadata API endpoint (if federation-enabled)
