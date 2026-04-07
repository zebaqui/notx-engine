# notx Link Specification

## Overview

Links in notx are built on a single foundational idea:

> **IDs are the only currency. You never link to a line. You link to an ID that was declared at a position.**

A position (line and character range) is how you _stamp_ an ID onto a specific piece of content. After that, every reference — within the same note, across notes, across servers — uses that ID. The engine tracks where that ID currently lives; callers never need to remember the original position.

This document defines:

1. How IDs are declared inside a `.notx` file (the header anchor table)
2. The two internal link tokens: `notx:lnk:id` (cross-note) and `notx:lnk:id::` (same-note)
3. The external link token: `world:lnk:uri`
4. How the engine detects when an edit may break an existing ID reference
5. Resolution semantics, validation, and the implementation checklist

---

## Design Principles

1. **IDs only, no positional links exposed** — `notx:lnk:line` is not a reference type authors use. Line and character information belongs to the _declaration_ of an ID, not to links between notes.
2. **Declarations live in the file header** — Every `.notx` file owns an anchor table in its metadata header. This makes IDs discoverable without replaying the full event stream.
3. **Positions are tracked, not assumed** — When content is edited, the engine updates anchor positions in the header. A position is a best-effort hint for UI scroll/highlight; the ID itself is what is stored in links.
4. **Break detection is a first-class concern** — When an edit touches a region covered by an ID, the engine warns the author that existing references may be affected, and offers resolution paths.
5. **Federated by default** — Cross-note links use the full note URN, so remote references work identically to local ones.
6. **Graceful degradation** — If an ID cannot be found (note moved, anchor deleted), the renderer shows the raw token rather than crashing.

---

## Part 1 — ID Declaration

### The Anchor Table

Every `.notx` file may contain an **anchor table** in its metadata header. The anchor table lives between the standard header fields and the event stream. It is a sequence of `# anchor:` lines, one per declared ID.

```
# notx/1.0
# note_urn:      urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
# name:          API Gateway Design
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 12
#
# anchor: intro        line:1  char:0-0
# anchor: auth-flow    line:6  char:5-7
# anchor: req-001      line:14 char:0-0
# anchor: node-reject  line:22 char:0-43
```

### Anchor Line Format

```
# anchor: <id>  line:<L>  char:<start>-<end>
```

| Field          | Description                                                                                                                                         |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `<id>`         | The anchor identifier. Lowercase alphanumeric, hyphens, underscores. `[a-z0-9_-]+`. Max 128 chars. Unique within the file.                          |
| `line:<L>`     | 1-based line number in the note's **current materialized state** at the time of last update.                                                        |
| `char:<S>-<E>` | 0-based inclusive character range on that line. `0-0` means the entire line (no specific range). A genuine single character at position 5 is `5-5`. |

### What "char range" means

The character range lets you pin an ID to a sub-line span. This is useful for:

- A specific word in a paragraph (e.g., a term definition)
- A node label inside a flowchart line like `[Start] --> [Auth]`
- A requirement identifier embedded mid-line

When `char:0-0` is used, the ID covers the whole line. This is the default for section headings, flowchart node lines, and any case where the full line is the logical unit.

### Declaring an ID

An ID is declared by adding an `# anchor:` line to the header. This is done:

- **By the editor/UI** — when the user selects a range of text and assigns it an ID (via a "Create anchor" action)
- **By the CLI** — `notx anchor add <note-urn> <id> --line <L> --char <S>-<E>`
- **By the engine automatically** — for structured note types that have well-known anchor positions (e.g., every node in a flowchart note)

The act of adding an anchor line to the header does **not** modify the event stream. The header is a mutable metadata zone; anchors are updated in-place there. The event stream remains append-only and unaffected.

### Example: Flowchart Note

Given a flowchart note whose materialized content looks like this:

```
1  | [Start]
2  | [Start] --> [Authenticate]
3  | [Authenticate] --> [Authorized?]
4  | [Authorized?] --> |Yes| [Process Request]
5  | [Authorized?] --> |No|  [Reject]
6  | [Process Request] --> [Return 200]
7  | [Reject] --> [Return 401]
```

The anchor table in the header would be:

```
# anchor: node-start     line:1  char:0-0
# anchor: node-auth      line:3  char:0-0
# anchor: node-decision  line:3  char:0-0
# anchor: node-process   line:4  char:0-0
# anchor: node-reject    line:5  char:0-0
# anchor: node-ok        line:6  char:0-0
# anchor: node-fail      line:7  char:0-0
```

Any other note can now link to `node-reject` in this flowchart with a stable ID that will not break when lines are inserted above or below it — because the engine updates the `line:` hint in the header on each edit.

### Example: Sub-line ID in a Paragraph

```
14 | The system MUST authenticate all requests (see RFC 9110 §4.2) before forwarding.
```

To pin the ID `req-auth` to just the word "authenticate" (characters 18–28):

```
# anchor: req-auth  line:14  char:18-28
```

---

## Part 2 — Link Tokens

There are three link token types. All follow the envelope:

```
<scheme>:lnk:<kind>:<address>
```

### Overview Table

| Token                                | Resolves to                                    |
| ------------------------------------ | ---------------------------------------------- |
| `notx:lnk:id:<note-urn>:<anchor-id>` | Named anchor in another note                   |
| `notx:lnk:id::<anchor-id>`           | Named anchor in the same note (self-reference) |
| `world:lnk:uri:<uri>`                | External resource at the given URI             |

There is no `notx:lnk:line` token for use in links. Line numbers are only written inside the header anchor table by the engine. Authors and agents always use IDs.

---

### Token 1 — Cross-Note ID Link (`notx:lnk:id:<note-urn>:<anchor-id>`)

#### Syntax

```
notx:lnk:id:<note-urn>:<anchor-id>
```

| Segment       | Description                                                     |
| ------------- | --------------------------------------------------------------- |
| `<note-urn>`  | Full URN of the target note: `urn:notx:note:<uuid-v7>`          |
| `<anchor-id>` | The declared anchor ID in the target note's header anchor table |

#### Examples

```
notx:lnk:id:urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ:intro
notx:lnk:id:urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ:node-reject
notx:lnk:id:urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ:req-auth
```

#### With display text

```
[Gateway Rejection Node](notx:lnk:id:urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ:node-reject)
[Authentication Requirement](notx:lnk:id:urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ:req-auth)
```

#### Resolution Algorithm

1. Split on `:` to extract `<note-urn>` (four segments: `urn`, `notx`, `note`, `<id>`) and `<anchor-id>` (everything after the fifth `:`).
2. Resolve the note using the standard URN resolution algorithm: local storage → authority field → routing table → remote fetch.
3. Parse the resolved note's metadata header anchor table.
4. Find the entry where `id == <anchor-id>` (case-insensitive).
5. Return the anchor record: `{ id, line, char_start, char_end, preview }`.
   - `line` and `char` are used by the renderer for scroll and highlight; they are not part of the link's identity.
6. If the anchor is not found in the header, materialize note content and do a full scan as a fallback (for notes written before the anchor table existed).

---

### Token 2 — Same-Note ID Link (`notx:lnk:id::<anchor-id>`)

#### Syntax

```
notx:lnk:id::<anchor-id>
```

The double colon (`::`) signals a self-reference. The resolver fills in the current note's URN automatically. This is syntactic sugar for `notx:lnk:id:<current-note-urn>:<anchor-id>` — it is expanded to the full form before storage in `node_links` or backlink records.

#### Examples

```
See notx:lnk:id::req-auth for the authentication requirement.
[Decision point](notx:lnk:id::node-decision)
```

---

### Token 3 — External URI Link (`world:lnk:uri:<uri>`)

#### Syntax

```
world:lnk:uri:<uri>
```

| Segment | Description                                                                                               |
| ------- | --------------------------------------------------------------------------------------------------------- |
| `<uri>` | A valid URI. HTTPS preferred. Any URI scheme is syntactically accepted (`doi:`, `mailto:`, `ftp:`, etc.). |

#### Examples

```
world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110
world:lnk:uri:https://api.github.com/repos/org/repo
world:lnk:uri:doi:10.1000/xyz123
world:lnk:uri:mailto:team@example.com
```

#### With display text

```
[RFC 9110](world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110)
[GitHub API](world:lnk:uri:https://api.github.com/repos/org/repo)
```

#### Resolution

`world:lnk:uri` links are **not resolved by the notx engine at storage or parse time**. Resolution is the caller's responsibility:

- **Renderer / UI** — opens the URI in the platform's default browser or external handler when activated.
- **CLI agent** — may fetch and inline the resource content when running a `notx tool resolve` command.
- **Ingest pipeline** — the future `notx fetch <uri>` command will accept a `world:lnk:uri` token as its source, clip the resource into a new note, and record a backlink from the new note to the original `world:lnk:uri` token's URI.

---

## Part 3 — Break Detection and Editor Semantics

This is the most critical operational concern for a link system built on IDs that track positions.

### When a Break Can Occur

An edit **may break** an existing ID reference when:

1. The edited event **deletes or replaces** a line that is currently pointed to by one or more `# anchor:` entries.
2. The edited event **modifies a line** and the change falls inside a sub-line character range (`char:<S>-<E>`) declared for an anchor on that line.
3. The edited event **inserts or deletes lines above** an anchored line (this does not break the reference — the engine updates the `line:` hint — but it does mean the hint is now stale until the header is refreshed).

Cases 1 and 2 are **hard breaks** — the content the ID was pointing to is gone or changed beyond recognition. Case 3 is a **soft drift** — the ID is still valid, just at a different line.

### Engine Behavior on Edit

When a new event is appended to a `.notx` file, the engine runs **anchor impact analysis** before confirming the write:

#### Step 1 — Compute the delta

From the incoming event's line entries, compute:

- Which lines are being deleted (`<L>|-`)
- Which lines are being set to new content (`<L>|<content>`)
- How many lines are being inserted (net change in line count)

#### Step 2 — Check each anchor

For every anchor in the current header anchor table:

| Condition                                                                                     | Classification  | Action                                                                  |
| --------------------------------------------------------------------------------------------- | --------------- | ----------------------------------------------------------------------- |
| Anchor's line is deleted                                                                      | **Hard break**  | Emit a break warning; block or flag the write                           |
| Anchor's line content changes AND the char range is `0-0` (whole line)                        | **Hard break**  | Emit a break warning                                                    |
| Anchor's line content changes AND the new content still contains the original char range text | **Soft change** | Update the anchor's char range if the text shifted; update `line:` hint |
| Anchor's line content changes AND the char range text is no longer present                    | **Hard break**  | Emit a break warning                                                    |
| Lines are inserted/deleted above the anchor                                                   | **Drift**       | Update `line:` hint in header; no warning                               |

#### Step 3 — Handle hard breaks

When a hard break is detected, the engine does **not silently proceed**. It returns a structured response to the caller describing which anchors are affected:

```json
{
  "status": "anchor_break_detected",
  "breaks": [
    {
      "anchor_id": "node-reject",
      "note_urn": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
      "line": 5,
      "char": "0-0",
      "referrers": [
        "urn:notx:note:01HZDESIGN111111111111",
        "urn:notx:note:01HZOVERVIEW22222222222"
      ],
      "resolution_options": ["reassign", "delete", "force"]
    }
  ]
}
```

| Field                | Description                                                                                             |
| -------------------- | ------------------------------------------------------------------------------------------------------- |
| `anchor_id`          | The ID that would be broken                                                                             |
| `note_urn`           | The note that owns the anchor                                                                           |
| `line`               | The line that is being affected                                                                         |
| `referrers`          | URNs of all notes that currently hold a `notx:lnk:id` pointing to this anchor (from the backlink index) |
| `resolution_options` | What the caller can do next                                                                             |

### Resolution Options

The caller (editor, CLI, API client) must choose one of these paths to proceed:

#### `reassign`

Move the anchor to a different line and character range in the same note. The anchor ID is preserved; all existing `notx:lnk:id` references continue to resolve correctly because they use the ID, not the position.

```json
{
  "action": "reassign",
  "anchor_id": "node-reject",
  "new_line": 8,
  "new_char": "0-0"
}
```

The engine updates the `# anchor:` entry in the header and applies the original edit.

#### `delete`

Remove the anchor entirely. All existing `notx:lnk:id` references that point to this anchor become **broken links** in the referrer notes. The engine records a deprecation event so renderers can distinguish "anchor intentionally deleted" from "anchor never existed."

```json
{
  "action": "delete",
  "anchor_id": "node-reject"
}
```

The engine removes the `# anchor:` line from the header, applies the original edit, and adds a tombstone record to the backlink index.

#### `force`

Apply the edit unconditionally without resolving the break. The anchor entry is left in the header but is now stale. The `line:` and `char:` values will no longer match live content. The engine marks the anchor as `status:broken` in the header:

```
# anchor: node-reject  line:5  char:0-0  status:broken
```

Renderers and the lint tool will surface this as a broken anchor.

```json
{
  "action": "force"
}
```

### Header Anchor Update on Drift

When lines are inserted or deleted **above** an anchored line (no content change to the anchored line itself), the engine automatically updates the `line:` hints in the header as part of the event append. This is transparent to the caller — no warning is emitted, and no resolution is required.

```
Before edit (inserting 2 lines above line 5):
# anchor: node-reject  line:5  char:0-0

After edit:
# anchor: node-reject  line:7  char:0-0
```

The anchor ID is unchanged; only the position hint is updated.

---

## Part 4 — Storage and Indexing

### Anchor Table in the Header (Per-File)

The canonical record of all anchors for a note lives in that note's `.notx` file header. The engine keeps this table up to date on every write.

```
# anchor: <id>  line:<L>  char:<S>-<E>  [status:<status>]
```

The optional `status` field is only present when non-nominal:

| Status       | Meaning                                                                            |
| ------------ | ---------------------------------------------------------------------------------- |
| _(absent)_   | Anchor is valid and position is current                                            |
| `broken`     | Anchor was force-written over; position is stale                                   |
| `deprecated` | Anchor was intentionally deleted but a tombstone is kept for referrer notification |

### Server-Side Anchor Index (SQLite)

The engine materializes an anchor index table in SQLite for fast cross-note lookups:

```sql
CREATE TABLE anchors (
    note_urn    TEXT NOT NULL,
    anchor_id   TEXT NOT NULL,
    line        INTEGER NOT NULL,
    char_start  INTEGER NOT NULL DEFAULT 0,
    char_end    INTEGER NOT NULL DEFAULT 0,
    preview     TEXT,
    status      TEXT NOT NULL DEFAULT 'ok',
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (note_urn, anchor_id)
);
```

| Column       | Description                                                                                               |
| ------------ | --------------------------------------------------------------------------------------------------------- |
| `note_urn`   | The note that owns this anchor                                                                            |
| `anchor_id`  | The anchor identifier (normalized lowercase)                                                              |
| `line`       | Current 1-based line number in the materialized note                                                      |
| `char_start` | Start of sub-line character range (0-based, inclusive)                                                    |
| `char_end`   | End of sub-line character range (0-based, inclusive). Equal to `char_start` when covering the whole line. |
| `preview`    | The text content of the anchored span at last index time                                                  |
| `status`     | `ok`, `broken`, or `deprecated`                                                                           |
| `updated_at` | When this record was last updated by the engine                                                           |

### Server-Side Backlink Index (SQLite)

```sql
CREATE TABLE backlinks (
    source_urn     TEXT NOT NULL,
    target_urn     TEXT NOT NULL,
    target_anchor  TEXT NOT NULL,
    label          TEXT,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (source_urn, target_urn, target_anchor)
);
```

| Column          | Description                                                |
| --------------- | ---------------------------------------------------------- |
| `source_urn`    | Note containing the `notx:lnk:id` token                    |
| `target_urn`    | Note being pointed at                                      |
| `target_anchor` | The anchor ID being referenced                             |
| `label`         | Display text from the `[label](token)` wrapper, if present |
| `created_at`    | When this backlink was first indexed                       |

The backlink index is what the engine queries in Step 3 of break detection to produce the `referrers` list.

### External Links Index (SQLite)

External `world:lnk:uri` links are also indexed for lint and ingest purposes:

```sql
CREATE TABLE external_links (
    source_urn  TEXT NOT NULL,
    uri         TEXT NOT NULL,
    label       TEXT,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (source_urn, uri)
);
```

---

## Part 5 — The `node_links` Field on the Note Object

The note object's `node_links` field (from `NOTX_URN_SPEC.md`) is a **named map of outbound links** maintained by the engine. Its values are always fully expanded link tokens — never raw line numbers, never positional references.

```json
{
  "node_links": {
    "auth-decision": "notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-decision",
    "rejection-path": "notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject",
    "external-rfc": "world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110"
  }
}
```

Self-reference tokens (`notx:lnk:id::`) are expanded to their full form (`notx:lnk:id:<current-note-urn>:<anchor-id>`) before being stored in `node_links`.

---

## Part 6 — Inline Syntax in Note Content

Link tokens appear verbatim in line content inside the event stream. No special escaping is required — the scheme prefixes (`notx:lnk:`, `world:lnk:`) are unambiguous in plain text.

### Plain token

```
The rejection path is handled by notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
```

### Display-text wrapper

```
[Rejection Node](notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject)
[RFC 9110](world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110)
```

### Full example event with links

```
3:2025-06-01T14:55:00Z:urn:notx:usr:01932c7b-1b4a-7e3f-9abc-0123456789ab
->
1 | Architecture Overview
2 |
3 | Requests flow through the [API Gateway](notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-start).
4 | Failed authentication ends at notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
5 | Protocol reference: [RFC 9110](world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110)
```

---

## Part 7 — Parsing Rules

### Token Boundary Detection

A link token begins at the first character of its scheme (`n` in `notx:lnk:` or `w` in `world:lnk:`) and ends at:

- The first unescaped whitespace character
- End of line
- A closing `)` when inside a `[label](token)` wrapper

### Parsing `notx:lnk:id`

```
notx : lnk : id : urn : notx : note : <uuid> : <anchor-id>
 1      2     3    4     5      6       7           8
```

Split on `:` to get segments. Segments 4–7 form the `<note-urn>` (`urn:notx:note:<uuid>`). Segment 8 is the `<anchor-id>`.

Self-reference detection: if segment 4 is empty (the `::` shorthand), there is no `<note-urn>` and segment 5 is the `<anchor-id>`.

### Parsing `world:lnk:uri`

```
world : lnk : uri : <everything-else>
  1      2     3          4+
```

Everything from segment 4 onward (joined back with `:`) is the URI verbatim. The URI may contain colons (e.g., `https://`, `doi:`).

---

## Part 8 — Validation Rules

| Rule                                                               | Scope                       | On Violation                                             |
| ------------------------------------------------------------------ | --------------------------- | -------------------------------------------------------- |
| `<anchor-id>` must match `[a-z0-9_-]+` (case-normalized)           | Declaration and link tokens | Token marked malformed; raw text shown                   |
| `<anchor-id>` must be unique within a note                         | `# anchor:` header table    | Engine rejects duplicate; writer must use a different ID |
| `<note-urn>` must be a valid `urn:notx:note:<uuid-v7>`             | `notx:lnk:id`               | Token marked malformed                                   |
| `<uri>` must be syntactically valid                                | `world:lnk:uri`             | Token marked malformed                                   |
| `line` must be a positive integer                                  | `# anchor:` table           | File is malformed; parser rejects the anchor entry       |
| `char:<S>-<E>` must satisfy `S <= E` and both must be non-negative | `# anchor:` table           | File is malformed; parser rejects the anchor entry       |
| An anchor with `status:broken` must not silently resolve           | Resolution                  | Renderer shows broken-link indicator; lint reports it    |

---

## Part 9 — Renderer Behavior

### Resolution States

| State          | Display                                                                             |
| -------------- | ----------------------------------------------------------------------------------- |
| `ok`           | Active hyperlink / clickable element                                                |
| `drift`        | Link is valid; position hint was updated — no visual indicator needed               |
| `broken`       | Broken-link indicator (strikethrough or ⚠ icon); tooltip shows last known position |
| `deprecated`   | Intentionally deleted anchor; renderer shows "this anchor no longer exists" message |
| `not_found`    | Anchor ID not in target note at all; distinct from broken — may be a typo           |
| `malformed`    | Raw token text, no link behavior                                                    |
| `remote_error` | Offline indicator; raw token preserved for retry                                    |

### Click / Activation Behavior

| Token                        | Action                                                            |
| ---------------------------- | ----------------------------------------------------------------- |
| `notx:lnk:id:<urn>:<anchor>` | Navigate to target note, scroll to `line`, highlight `char` range |
| `notx:lnk:id::<anchor>`      | Scroll to `line`, highlight `char` range within the current note  |
| `world:lnk:uri:<uri>`        | Open URI in system default browser / external handler             |

---

## Part 10 — Machine-Readable JSON Format

For CLI agent tools and structured API responses, resolved links are returned as:

```json
{
  "token": "notx:lnk:id:urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ:node-reject",
  "link_type": "notx:lnk:id",
  "target_urn": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "target_anchor": "node-reject",
  "line": 5,
  "char_start": 0,
  "char_end": 0,
  "preview": "[Reject] --> [Return 401]",
  "status": "ok"
}
```

For an external link:

```json
{
  "token": "world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110",
  "link_type": "world:lnk:uri",
  "target_urn": null,
  "target_anchor": null,
  "uri": "https://www.rfc-editor.org/rfc/rfc9110",
  "status": "unresolved"
}
```

---

## Part 11 — API Surface

```
# Anchor management
GET  /v1/notes/:urn/anchors                  → list all anchors declared in this note
POST /v1/notes/:urn/anchors                  → declare a new anchor (id, line, char)
PUT  /v1/notes/:urn/anchors/:anchor-id       → reassign anchor to new line/char
DEL  /v1/notes/:urn/anchors/:anchor-id       → delete anchor (creates tombstone)
GET  /v1/notes/:urn/anchors/:anchor-id       → resolve a single anchor

# Link graph
GET  /v1/notes/:urn/links                    → outbound links from this note
GET  /v1/notes/:urn/backlinks                → inbound links to this note
GET  /v1/notes/:urn/backlinks/:anchor-id     → inbound links to a specific anchor
GET  /v1/notes/:urn/related                  → union of outbound + backlinks

# Resolution
GET  /v1/resolve?token=<link-token>          → resolve any link token to JSON
```

---

## Part 12 — CLI Interface

```
notx anchor add   <note-urn> <id> --line <L> --char <S>-<E>
notx anchor list  <note-urn>
notx anchor move  <note-urn> <id> --line <L> --char <S>-<E>
notx anchor rm    <note-urn> <id>

notx link resolve <token>                    # resolve a single link token
notx link list    <note-urn>                 # outbound links from a note
notx link back    <note-urn>                 # backlinks to a note
notx link back    <note-urn> <anchor-id>     # backlinks to a specific anchor

notx lint links   <note-urn>                 # broken/stale anchors in one note
notx lint links   --all                      # project-wide link health check
```

---

## Quick Reference

```
# Declare an anchor in a .notx file header
# anchor: <id>  line:<L>  char:<S>-<E>

# Link to an anchor in another note
notx:lnk:id:urn:notx:note:<note-id>:<anchor-id>

# Link to an anchor in the same note
notx:lnk:id::<anchor-id>

# Link to an external resource
world:lnk:uri:<uri>

# Display-text wrapper (works for all types)
[Display Text](<token>)
```

---

## Summary of Key Design Decisions

| Decision                                                 | Rationale                                                                                                                                                                                                                                                                            |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **No `notx:lnk:line` reference type**                    | Line numbers are an internal position hint maintained by the engine. Exposing them as a link type would mean every edit that inserts a line above an anchored line would silently break every reference to it. IDs decouple the reference from the position entirely.                |
| **Anchor table in the file header**                      | The `.notx` event stream is append-only and immutable. The header is the correct place for mutable, derived metadata like current positions. This keeps the event log clean while still making anchors discoverable without full replay.                                             |
| **Line and char as hints, not identity**                 | When the anchored content moves due to edits, the engine updates `line:` and `char:` in the header. These are navigation aids for the UI. The `<anchor-id>` is what links store, so the reference is never affected by position drift.                                               |
| **Hard break detection before write**                    | Silently writing over anchored content would create a class of invisible link rot that is impossible to diagnose. Surfacing breaks at write time — before the event is committed — gives the author an actionable choice rather than a mystery later.                                |
| **Three resolution options (reassign / delete / force)** | The author knows best. Reassign is the safe path. Delete is intentional cleanup. Force is an escape hatch for cases where the author knows no other notes actually use the anchor yet. Giving the author these options respects their intent while making the consequences explicit. |
| **`world:lnk:uri` not resolved at write time**           | External resources are outside the engine's authority. Fetching them at write time adds network coupling, latency, privacy exposure, and side effects. Resolution is the caller's responsibility.                                                                                    |
| **Backlink index is server-side only**                   | The `.notx` file format stays pure — just the event log and header. Derived structures like backlinks and the anchor index are rebuilt from replay. This means the file format itself is never corrupted by index state, and indexes can always be regenerated from scratch.         |
