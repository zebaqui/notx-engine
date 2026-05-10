# notx Link Specification

## Overview

Links in notx are built on a single foundational idea:

> **IDs are the only currency. You never link to a line. You link to an ID that was declared at a position.**

A position (line and character range) is how you _stamp_ an ID onto a specific piece of content. After that, every reference — within the same note, across notes, across servers — uses that ID. The engine tracks where that ID currently lives; callers never need to remember the original position.

This document defines:

1. How IDs are declared inside a `.notx` file (the frontmatter anchor table)
2. The two internal link tokens: `notx:lnk:id` (cross-note) and `notx:lnk:id::` (same-note)
3. The external link token: `world:lnk:uri`
4. How named outbound links (`links:`) are declared in frontmatter
5. How the engine detects when an edit may break an existing ID reference
6. Resolution semantics, validation, and the implementation checklist

---

## Design Principles

1. **IDs only, no positional links exposed** — `notx:lnk:line` is not a reference type authors use. Line and character information belongs to the _declaration_ of an ID, not to links between notes.
2. **Declarations live in frontmatter** — Every `.notx` file (or Markdown note) declares its anchors and named outbound links in YAML frontmatter at the top of the file, between `---` delimiters. This makes IDs discoverable without replaying the full event stream.
3. **Positions are tracked, not assumed** — When content is edited, the engine updates anchor positions in the frontmatter. A position is a best-effort hint for UI scroll and highlight; the ID itself is what is stored in links.
4. **Break detection is a first-class concern** — When an edit touches a region covered by an ID, the engine warns the author that existing references may be affected, and offers resolution paths.
5. **Federated by default** — Cross-note links use the full note URN, so remote references work identically to local ones.
6. **Graceful degradation** — If an ID cannot be found (note moved, anchor deleted), the renderer shows the raw token rather than crashing.

---

## Part 1 — ID Declaration

### The `anchors:` Block in Frontmatter

Every `.notx` file (or Markdown note) may declare an **anchor table** in its YAML frontmatter. Frontmatter is the block at the very top of the file delimited by `---` lines. Alongside the standard metadata fields (`id`, `title`, etc.), the frontmatter contains an `anchors:` list — one item per declared ID — and an optional `links:` list of named outbound links.

```
---
id: post-001
title: API Gateway Design
anchors:
  - intro line:1 char:0-0
  - auth-flow line:6 char:5-7
  - req-001 line:14 char:0-0
  - node-reject line:22 char:0-43
links:
  - auth-decision=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-decision
  - rejection-path=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
  - external-rfc=world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110
---
```

The frontmatter block is the **canonical, mutable metadata zone** for a note. Anchors are declared and updated here. The note body (everything after the closing `---`) is referred to as the **materialized body**, and all `line:` numbers in anchor items are 1-based line numbers within that body.

### Anchor Item Format

Each item in the `anchors:` list is a single string with space-separated fields:

```
<id>  line:<L>  char:<S>-<E>  [status:<status>]  [preview:<text>]
```

#### Field Reference

| Field             | Description                                                                                                                                                                  |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `<id>`            | The anchor identifier. Lowercase alphanumeric, hyphens, underscores. Pattern: `[a-z0-9_-]+`. Max 128 characters. Must be unique within the note.                             |
| `line:<L>`        | 1-based line number in the note's **materialized body** (the content after the closing `---` of the frontmatter block) at the time of last update.                           |
| `char:<S>-<E>`    | 0-based inclusive character range on that line. `0-0` means the entire line (no specific sub-line range). A single character at position 5 is `5-5`. Must satisfy `S <= E`.  |
| `status:<status>` | Optional. Omitted when the anchor is nominal (`ok`). Present only when `broken` or `deprecated`. See the status table in Part 4.                                             |
| `preview:<text>`  | Optional. The text of the anchored span at last index time. Extends to the end of the item string (may contain spaces). Used by renderers as a tooltip and by the lint tool. |

### What "char range" means

The character range lets you pin an ID to a sub-line span. This is useful for:

- A specific word in a paragraph (e.g., a term definition)
- A node label inside a flowchart line like `[Start] --> [Auth]`
- A requirement identifier embedded mid-line

When `char:0-0` is used, the ID covers the whole line. This is the default for section headings, flowchart node lines, and any case where the full line is the logical unit.

### How to Declare an Anchor

An anchor is declared by adding an item to the `anchors:` list in frontmatter. This is done:

- **By the editor/UI** — when the user selects a range of text and assigns it an ID via a "Create anchor" action. The editor appends an item to the `anchors:` list and saves the frontmatter.
- **By the CLI** — `notx anchor add <note-urn> <id> --line <L> --char <S>-<E>`
- **By the engine automatically** — for structured note types that have well-known anchor positions (e.g., every node in a flowchart note is anchored automatically on creation)

The act of adding an anchor item to the frontmatter does **not** modify the note body. The frontmatter is a mutable metadata zone; anchors are updated in-place there. The note body and any underlying event stream remain unaffected.

### Example: Flowchart Note

Given a flowchart note whose materialized body (after the frontmatter block) looks like this:

```
1  | [Start]
2  | [Start] --> [Authenticate]
3  | [Authenticate] --> [Authorized?]
4  | [Authorized?] --> |Yes| [Process Request]
5  | [Authorized?] --> |No|  [Reject]
6  | [Process Request] --> [Return 200]
7  | [Reject] --> [Return 401]
```

The complete frontmatter for this note would be:

```
---
id: flowchart-001
title: Auth Flow
anchors:
  - node-start line:1 char:0-0
  - node-auth line:2 char:0-0
  - node-decision line:3 char:0-0
  - node-process line:4 char:0-0
  - node-reject line:5 char:0-0
  - node-ok line:6 char:0-0
  - node-fail line:7 char:0-0
---
```

Any other note can now link to `node-reject` in this flowchart with a stable ID that will not break when lines are inserted above or below it — because the engine updates the `line:` hint in the frontmatter on each edit.

### Example: Sub-line ID in a Paragraph

Given a note body where line 14 reads:

```
14 | The system MUST authenticate all requests (see RFC 9110 §4.2) before forwarding.
```

To pin the anchor `req-auth` to just the word "authenticate" (characters 18–28), the frontmatter anchor item is:

```
- req-auth line:14 char:18-28 preview:authenticate
```

The `preview:` field is optional but recommended — it helps the lint tool verify that the anchored text has not silently changed.

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

There is no `notx:lnk:line` token for use in links. Line numbers are only written inside the frontmatter `anchors:` list by the engine. Authors and agents always use IDs.

---

### Token 1 — Cross-Note ID Link (`notx:lnk:id:<note-urn>:<anchor-id>`)

#### Syntax

```
notx:lnk:id:<note-urn>:<anchor-id>
```

| Segment       | Description                                                             |
| ------------- | ----------------------------------------------------------------------- |
| `<note-urn>`  | Full URN of the target note: `urn:notx:note:<uuid-v7>`                  |
| `<anchor-id>` | The declared anchor ID in the target note's frontmatter `anchors:` list |

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

1. Split on `:` to extract `<note-urn>` (four segments: `urn`, `notx`, `note`, `<uuid>`) and `<anchor-id>` (everything after the fifth `:`).
2. Resolve the note using the standard URN resolution algorithm: local storage → authority field → routing table → remote fetch.
3. Parse the resolved note's frontmatter. Extract the `anchors:` list.
4. Find the item whose `<id>` field equals `<anchor-id>` (case-insensitive comparison).
5. Return the anchor record: `{ id, line, char_start, char_end, preview, status }`.
   - `line` and `char` are used by the renderer for scroll and highlight; they are not part of the link's identity.
6. If the anchor is not found in the frontmatter, materialize note content and do a full scan as a fallback (for notes written before the frontmatter anchor format existed).

---

### Token 2 — Same-Note ID Link (`notx:lnk:id::<anchor-id>`)

#### Syntax

```
notx:lnk:id::<anchor-id>
```

The double colon (`::`) signals a self-reference. The resolver fills in the current note's URN automatically. This is syntactic sugar for `notx:lnk:id:<current-note-urn>:<anchor-id>` — it is expanded to the full form before storage in the frontmatter `links:` block or backlink records.

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

## Part 3 — Named Outbound Links (`links:` in Frontmatter)

The `links:` list in frontmatter is a **named map of outbound links** for the note. It is the frontmatter-based equivalent of the legacy `node_links` field on the note object. Each item assigns a human-readable label to a fully resolved link token.

### Format

```
<label>=<token>
```

| Segment   | Description                                                                        |
| --------- | ---------------------------------------------------------------------------------- |
| `<label>` | A short alphanumeric label for this link. Pattern: `[a-z0-9_-]+`. Unique per note. |
| `<token>` | A fully expanded link token: one of the three token types defined in Part 2.       |

### Example

```yaml
links:
  - auth-decision=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-decision
  - rejection-path=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
  - external-rfc=world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110
```

### Self-Reference Expansion

Self-reference tokens (`notx:lnk:id::`) are **expanded to their full form** before storage in the `links:` list. The engine replaces the `::` shorthand with the current note's URN at the time the link is written. This means the `links:` block always contains portable, fully qualified tokens — a reader of the file never needs to know which note they are looking at to resolve a link in the `links:` block.

**Before storage (what the author types):**

```yaml
links:
  - self-ref=notx:lnk:id::node-decision
```

**After engine expansion (what is written to frontmatter):**

```yaml
links:
  - self-ref=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-decision
```

The expanded form is also what gets stored in the server-side backlink index (see Part 5).

---

## Part 4 — Break Detection and Editor Semantics

This is the most critical operational concern for a link system built on IDs that track positions.

### When a Break Can Occur

An edit **may break** an existing ID reference when:

1. The edited content **deletes or replaces** a line that is currently pointed to by one or more `anchors:` items.
2. The edited content **modifies a line** and the change falls inside a sub-line character range (`char:<S>-<E>`) declared for an anchor on that line.
3. The edited content **inserts or deletes lines above** an anchored line (this does not break the reference — the engine updates the `line:` hint in frontmatter — but it does mean the hint is stale until the frontmatter is refreshed).

Cases 1 and 2 are **hard breaks** — the content the ID was pointing to is gone or changed beyond recognition. Case 3 is a **soft drift** — the ID is still valid, just at a different line number.

### Engine Behavior on Edit

When the body of a `.notx` file is modified, the engine runs **anchor impact analysis** before confirming the write:

#### Step 1 — Compute the delta

From the incoming edit, compute:

- Which lines are being deleted
- Which lines are being set to new content
- How many lines are being inserted (net change in line count)

#### Step 2 — Check each anchor

For every item in the current `anchors:` list in frontmatter:

| Condition                                                                                     | Classification  | Action                                                                  |
| --------------------------------------------------------------------------------------------- | --------------- | ----------------------------------------------------------------------- |
| Anchor's line is deleted                                                                      | **Hard break**  | Emit a break warning; block or flag the write                           |
| Anchor's line content changes AND the char range is `0-0` (whole line)                        | **Hard break**  | Emit a break warning                                                    |
| Anchor's line content changes AND the new content still contains the original char range text | **Soft change** | Update the anchor's char range if the text shifted; update `line:` hint |
| Anchor's line content changes AND the char range text is no longer present                    | **Hard break**  | Emit a break warning                                                    |
| Lines are inserted or deleted above the anchor                                                | **Drift**       | Update `line:` hint in frontmatter; no warning emitted                  |

#### Step 3 — Handle hard breaks

When a hard break is detected, the engine does **not silently proceed**. It returns a structured response to the caller describing which anchors are affected:

```json
{
  "status": "anchor_break_detected",
  "breaks": [
    {
      "anchor_id": "node-reject",
      "note_urn": "urn:notx:note:01HZFLOWCHART123456",
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

The engine updates the matching item in the `anchors:` list in frontmatter and applies the original edit.

#### `delete`

Remove the anchor entirely. All existing `notx:lnk:id` references that point to this anchor become **broken links** in the referrer notes. The engine records a deprecation event so renderers can distinguish "anchor intentionally deleted" from "anchor never existed."

```json
{
  "action": "delete",
  "anchor_id": "node-reject"
}
```

The engine removes the item from the `anchors:` list in frontmatter, applies the original edit, and adds a tombstone record to the backlink index.

#### `force`

Apply the edit unconditionally without resolving the break. The anchor item is left in frontmatter but is now stale. The `line:` and `char:` values will no longer match live content. The engine marks the anchor as `status:broken` in the item:

```yaml
anchors:
  - node-reject line:5 char:0-0 status:broken
```

Renderers and the lint tool will surface this as a broken anchor.

```json
{
  "action": "force"
}
```

### Frontmatter Anchor Update on Drift

When lines are inserted or deleted **above** an anchored line (no content change to the anchored line itself), the engine automatically updates the `line:` hints in the `anchors:` list in frontmatter as part of the write. This is transparent to the caller — no warning is emitted, and no resolution is required.

**Before edit (inserting 2 lines above line 5):**

```yaml
anchors:
  - node-reject line:5 char:0-0
```

**After edit:**

```yaml
anchors:
  - node-reject line:7 char:0-0
```

The anchor ID is unchanged; only the position hint is updated.

---

## Part 5 — Storage and Indexing

### Anchor Table in Frontmatter (Per-File)

The canonical record of all anchors for a note lives in that note's frontmatter `anchors:` list. The engine keeps this list up to date on every write.

```yaml
anchors:
  - <id> line:<L> char:<S>-<E> [status:<status>] [preview:<text>]
```

The optional `status:` field is only present when non-nominal:

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
| `line`       | Current 1-based line number in the materialized note body                                                 |
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

## Part 6 — Inline Syntax in Note Content

Link tokens appear verbatim in the note body (the content after the closing `---` of the frontmatter block). No special escaping is required — the scheme prefixes (`notx:lnk:`, `world:lnk:`) are unambiguous in plain text.

### Plain token

```
The rejection path is handled by notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
```

### Display-text wrapper

```
[Rejection Node](notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject)
[RFC 9110](world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110)
```

### Full example note with frontmatter and body links

```
---
id: overview-001
title: Architecture Overview
anchors:
  - intro line:1 char:0-0
links:
  - gateway=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-start
  - rejection=notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
  - rfc9110=world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110
---
Architecture Overview

Requests flow through the [API Gateway](notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-start).
Failed authentication ends at notx:lnk:id:urn:notx:note:01HZFLOWCHART123456:node-reject
Protocol reference: [RFC 9110](world:lnk:uri:https://www.rfc-editor.org/rfc/rfc9110)
```

---

## Part 7 — Parsing Rules

### Token Boundary Detection

A link token begins at the first character of its scheme (`n` in `notx:lnk:` or `w` in `world:lnk:`) and ends at:

- The first unescaped whitespace character
- End of line
- A closing `)` when inside a `[label](token)` wrapper

Tokens appearing in the frontmatter `links:` list are delimited by the `=` separator on the left and end of line on the right.

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

### Frontmatter Parsing Rules

1. The frontmatter block must begin on line 1 of the file with exactly `---`.
2. The block ends at the next line containing exactly `---`.
3. The content between the delimiters is parsed as YAML.
4. The `anchors:` key maps to a YAML sequence of strings. Each string is parsed according to the anchor item format in Part 1.
5. The `links:` key maps to a YAML sequence of strings. Each string is split on the first `=` character; everything before `=` is the label, everything after is the token.
6. Frontmatter parsing errors (malformed YAML, duplicate anchor IDs, invalid token syntax) are reported as file-level diagnostics. A parsing error in frontmatter does not prevent the note body from being rendered.
7. The note body begins on the line immediately following the closing `---`. All `line:` numbers in anchor items are 1-based relative to this body start line.

---

## Part 8 — Validation Rules

| Rule                                                               | Scope                       | On Violation                                                |
| ------------------------------------------------------------------ | --------------------------- | ----------------------------------------------------------- |
| `<anchor-id>` must match `[a-z0-9_-]+` (case-normalized)           | Declaration and link tokens | Token marked malformed; raw text shown                      |
| `<anchor-id>` must be unique within a note                         | `anchors:` frontmatter list | Engine rejects duplicate; writer must use a different ID    |
| `<note-urn>` must be a valid `urn:notx:note:<uuid-v7>`             | `notx:lnk:id`               | Token marked malformed                                      |
| `<uri>` must be syntactically valid                                | `world:lnk:uri`             | Token marked malformed                                      |
| `line` must be a positive integer                                  | `anchors:` frontmatter list | File is malformed; parser rejects the anchor item           |
| `char:<S>-<E>` must satisfy `S <= E` and both must be non-negative | `anchors:` frontmatter list | File is malformed; parser rejects the anchor item           |
| `<label>` in `links:` must match `[a-z0-9_-]+`                     | `links:` frontmatter list   | Item marked malformed; skipped by index                     |
| `<label>` must be unique within the `links:` list                  | `links:` frontmatter list   | Engine rejects duplicate; writer must use a different label |
| Self-reference tokens in `links:` must be expanded before storage  | `links:` frontmatter list   | Engine expands on write; un-expanded form is not stored     |
| An anchor with `status:broken` must not silently resolve           | Resolution                  | Renderer shows broken-link indicator; lint reports it       |
| Frontmatter must be valid YAML                                     | File level                  | File-level diagnostic; note body still rendered             |

---

## Part 9 — Renderer Behavior

### Resolution States

| State          | Display                                                                             |
| -------------- | ----------------------------------------------------------------------------------- |
| `ok`           | Active hyperlink / clickable element                                                |
| `drift`        | Link is valid; position hint was updated — no visual indicator needed               |
| `broken`       | Broken-link indicator (strikethrough or ⚠ icon); tooltip shows last known position  |
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
# Anchor management (note-centric)
GET    /v1/notes/:urn/anchors                → list all anchors for this note
POST   /v1/notes/:urn/anchors                → declare a new anchor
PUT    /v1/notes/:urn/anchors/:anchor-id     → reassign anchor position
DELETE /v1/notes/:urn/anchors/:anchor-id     → delete anchor (tombstone)
GET    /v1/notes/:urn/anchors/:anchor-id     → get a single anchor

# Link graph (note-centric)
GET    /v1/notes/:urn/links                  → outbound links from this note
GET    /v1/notes/:urn/backlinks              → inbound backlinks to this note
GET    /v1/notes/:urn/backlinks/:anchor-id   → backlinks to a specific anchor

# Low-level link index endpoints
GET    /v1/links/anchors?note_urn=...        → list anchors by note URN
PUT    /v1/links/anchors                     → upsert an anchor record
GET    /v1/links/anchors/:note_urn/:anchor_id → get one anchor
DELETE /v1/links/anchors/:note_urn/:anchor_id → delete anchor
GET    /v1/links/backlinks?target_urn=...    → list inbound backlinks
GET    /v1/links/backlinks/recent            → recently created backlinks
PUT    /v1/links/backlinks                   → upsert a backlink
DELETE /v1/links/backlinks                   → delete a backlink
GET    /v1/links/outbound?source_urn=...     → outbound links from a note
GET    /v1/links/referrers?target_urn=...    → referrers to an anchor
GET    /v1/links/external?source_urn=...     → external links from a note
PUT    /v1/links/external                    → upsert external link
DELETE /v1/links/external                    → delete external link

# Resolution
GET    /v1/resolve?token=<link-token>        → resolve any link token to JSON
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
# Frontmatter block (top of .notx or .md file)
---
id: <note-id>
title: <Note Title>
anchors:
  - <id> line:<L> char:<S>-<E>
  - <id> line:<L> char:<S>-<E> status:broken
  - <id> line:<L> char:<S>-<E> preview:<anchored text>
links:
  - <label>=notx:lnk:id:urn:notx:note:<note-id>:<anchor-id>
  - <label>=notx:lnk:id::<anchor-id>     (self-reference — expanded before storage)
  - <label>=world:lnk:uri:<uri>
---

# Link to an anchor in another note (in note body)
notx:lnk:id:urn:notx:note:<note-id>:<anchor-id>

# Link to an anchor in the same note (in note body)
notx:lnk:id::<anchor-id>

# Link to an external resource (in note body)
world:lnk:uri:<uri>

# Display-text wrapper (works for all token types)
[Display Text](<token>)
```

---

## Summary of Key Design Decisions

| Decision                                                 | Rationale                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **No `notx:lnk:line` reference type**                    | Line numbers are an internal position hint maintained by the engine. Exposing them as a link type would mean every edit that inserts a line above an anchored line would silently break every reference to it. IDs decouple the reference from the position entirely.                                                                                                                                                                                                                       |
| **Frontmatter as the anchor declaration zone**           | YAML frontmatter is a widely understood, tooling-friendly format supported by most Markdown editors and static site generators. Placing anchor declarations in frontmatter keeps them structurally separate from note content, makes them machine-readable without custom parsers, and allows standard YAML libraries to read and update them. This replaces the earlier `# anchor:` header-line approach, which required a custom parser and was specific to the `.notx` event-log format. |
| **Line and char as hints, not identity**                 | When the anchored content moves due to edits, the engine updates `line:` and `char:` in the frontmatter. These are navigation aids for the UI. The `<anchor-id>` is what links store, so the reference is never affected by position drift.                                                                                                                                                                                                                                                 |
| **Hard break detection before write**                    | Silently writing over anchored content would create a class of invisible link rot that is impossible to diagnose. Surfacing breaks at write time — before the event is committed — gives the author an actionable choice rather than a mystery later.                                                                                                                                                                                                                                       |
| **Three resolution options (reassign / delete / force)** | The author knows best. Reassign is the safe path. Delete is intentional cleanup. Force is an escape hatch for cases where the author knows no other notes actually use the anchor yet. Giving the author these options respects their intent while making the consequences explicit.                                                                                                                                                                                                        |
| **`world:lnk:uri` not resolved at write time**           | External resources are outside the engine's authority. Fetching them at write time adds network coupling, latency, privacy exposure, and side effects. Resolution is the caller's responsibility.                                                                                                                                                                                                                                                                                           |
| **Backlink index is server-side only**                   | The note file format stays pure — just the frontmatter and body. Derived structures like backlinks and the anchor index are rebuilt from replay. This means the file format itself is never corrupted by index state, and indexes can always be regenerated from scratch.                                                                                                                                                                                                                   |
| **Self-references expanded before storage**              | Storing `notx:lnk:id::` shorthand in the `links:` block or backlink index would require the reader to know the current note's URN to resolve the token. Expanding to the full form at write time makes stored tokens portable and self-contained.                                                                                                                                                                                                                                           |
