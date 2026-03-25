# notx Format Specification

## What is notx?

**notx** is a plain-text, event-sourced format for storing notes and documents. A `.notx` file is a complete, portable, human-readable archive of a document's full history—every change ever made to it—stored as a sequence of line-level edits.

Instead of storing snapshots (full document copies), notx stores **what changed**:

- Line deletions
- Line insertions
- Line updates

The current state of any document is reconstructed by **replaying all events from the beginning** in order. This approach has several advantages:

- **Portable** — A `.notx` file is completely self-contained. No database, no external references (except via URNs to other notx documents).
- **Versionable** — The entire history is plain text. It can be committed to version control, diffed, and merged like any source file.
- **Auditable** — Every change is attributed to an author (via URN) and timestamped.
- **Efficient** — Storage cost is proportional to the size of changes, not the document. A large note with small edits uses little space.
- **Human-readable** — The format uses simple line-oriented syntax. You can read the history without special tools.

## notx URN Scheme

All identifiable entities in notx use a three-segment URN (Uniform Resource Name):

```
<namespace>:<object-type>:<uuid>
```

### URN Components

| Component     | Description                                                                                          |
| ------------- | ---------------------------------------------------------------------------------------------------- |
| `namespace`   | Instance identifier. `notx` for official platform; custom for self-hosted (e.g., `acme`, `company`). |
| `object-type` | The kind of entity (see table below).                                                                |
| `uuid`        | A UUID v4 (or v7). Unique within the object-type on that instance.                                   |

### Standard Object Types

| Type     | Description                 | Example URN (official platform)                    | Example URN (self-hosted)                          |
| -------- | --------------------------- | -------------------------------------------------- | -------------------------------------------------- |
| `note`   | A note document             | `notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a`   | `acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a`   |
| `usr`    | A registered user or author | `notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b`    | `acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b`    |
| `proj`   | A project                   | `notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d`   | `acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d`   |
| `folder` | A folder within a project   | `notx:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f` | `acme:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f` |
| `org`    | An organization             | `notx:org:9e8d7c6b-5a4f-3e2d-1c0b-9a8f7e6d5c4b`    | `acme:org:9e8d7c6b-5a4f-3e2d-1c0b-9a8f7e6d5c4b`    |
| `event`  | A history event             | `notx:event:2d3e4f5a-6b7c-8d9e-0f1a-2b3c4d5e6f7a`  | `acme:event:2d3e4f5a-6b7c-8d9e-0f1a-2b3c4d5e6f7a`  |

### The `anon` Sentinel

Unauthenticated or unknown authors use an instance-specific sentinel:

- Official platform: `notx:usr:anon`
- Self-hosted `acme`: `acme:usr:anon`

This is not a real UUID. It is a special value interpreted by parsers as "unknown author on that instance".

## File Structure

A `.notx` file consists of three sections:

1. **File header** — metadata about the note (lines starting with `#`)
2. **Event stream** — the complete history of changes
3. **Snapshot blocks** (optional) — optimization checkpoints for fast replay

### File Header

The file begins with metadata lines, each prefixed with `#`:

```
# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          My Meeting Notes
# project_urn:   notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
# folder_urn:    notx:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4
```

| Field           | Purpose                                                                         |
| --------------- | ------------------------------------------------------------------------------- |
| `notx/1.0`      | Format version. Always `notx/1.0` for this specification.                       |
| `note_urn`      | The unique identity of this note (`<namespace>:note:<uuid>`).                   |
| `name`          | Human-readable name / title of the note.                                        |
| `project_urn`   | URN of the project this note belongs to (`<namespace>:proj:<uuid>`) (optional). |
| `folder_urn`    | URN of the folder this note is in (`<namespace>:folder:<uuid>`) (optional).     |
| `created_at`    | ISO-8601 UTC timestamp of when the note was created.                            |
| `head_sequence` | The sequence number of the last applied event (current state).                  |

The `head_sequence` field is updated whenever new events are appended, allowing the file header to always reflect the current state without rewriting the entire file.

### Event Stream

After the header, the file contains a sequence of **events**. Each event describes a set of line changes.

#### Event Header

An event header is a single line with three colon-delimited fields:

```
<sequence>:<iso-timestamp>:<author-urn>
```

Example:

```
1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
```

| Field           | Type         | Description                                |
| --------------- | ------------ | ------------------------------------------ |
| `sequence`      | integer      | Monotonically increasing, starts at 1      |
| `iso-timestamp` | ISO-8601 UTC | When the event was recorded                |
| `author-urn`    | URN string   | Full URN of the author, or `notx:usr:anon` |

**Parser note:** The author URN contains colons, so the header has variable token count. The parser must treat everything after the second colon as the author URN.

#### Event Separator

A line containing exactly `->` signals the start of event entries:

```
->
```

#### Line Entries

Each line in an event describes a change to a specific line number. Format:

```
<line-number> | <content>
```

The line number is 1-based. The pipe `|` separates the line number from its content.

| Syntax       | Meaning                                                  |
| ------------ | -------------------------------------------------------- |
| `3 \| hello` | Set line 3 to `hello`                                    |
| `4 \|`       | Set line 4 to an empty string (line exists but is blank) |
| `5 \|-`      | **Delete** line 5; all subsequent lines shift up         |

#### Event Separator (Snapshot Block)

Optionally, after every 10 events (or at any interval), a **snapshot block** appears. It has a distinct header and separator:

```
snapshot:10:2025-01-15T11:00:00Z
=>
1 | # Meeting Notes
2 | Attendees: Alice, Bob
3 |
4 | ## Action Items
```

The snapshot header format is:

```
snapshot:<sequence>:<iso-timestamp>
```

The `=>` separator marks the start of the snapshot's line entries (in the same `N | content` format as events).

A snapshot is optional—it is only an optimization. It contains the **complete materialized content** of the note at a specific sequence number. The parser can jump to the nearest snapshot and replay only the remaining events rather than replaying from event 1.

## Event Format (Lane Format)

The core of notx is the **lane format**—a simple, line-oriented language for expressing document changes.

### Anatomy of an Event

```
1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | # Meeting Notes
2 |
3 | Attendees: Alice, Bob
```

This event:

1. Is event #1, created on 2025-01-15 at 09:00:00 UTC by user `7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b`
2. Creates three lines:
   - Line 1: `# Meeting Notes`
   - Line 2: empty string
   - Line 3: `Attendees: Alice, Bob`

### Change Semantics

**Set a line** (update or insert):

```
3 | updated content here
```

If line 3 exists, replace it. If line 3 is beyond the current document end, append.

**Set a line to empty:**

```
4 |
```

Line 4 is now an empty string. The line still exists; it is not deleted.

**Delete a line:**

```
5 |-
```

Remove line 5 entirely. All subsequent line numbers shift down by 1.

### Multi-Line Event

An event can change multiple lines:

```
2:2025-01-15T09:15:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
3 | Attendees: Alice, Bob, Carol
4 |
5 | ## Action Items
6 | - Alice: send recap
```

This event updates line 3 and adds lines 4–6.

### Event Ordering and Sequencing

Events are processed in order, one after another. Line numbers are always interpreted relative to the **current document state** after all previous events have been applied.

Example sequence:

**Event 1:**

```
1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | # Meeting Notes
2 |
3 | Attendees: Alice, Bob
```

Document after event 1:

```
# Meeting Notes

Attendees: Alice, Bob
```

**Event 2:**

```
2:2025-01-15T09:15:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
3 | Attendees: Alice, Bob, Carol
```

Document after event 2:

```
# Meeting Notes

Attendees: Alice, Bob, Carol
```

**Event 3:**

```
3:2025-01-15T09:30:00Z:notx:usr:3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f
->
4 |
5 | ## Action Items
6 | - Alice: send recap
```

Document after event 3:

```
# Meeting Notes

Attendees: Alice, Bob, Carol

## Action Items
- Alice: send recap
```

**Event 4:**

```
4:2025-01-15T10:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
2 |-
```

Document after event 4 (line 2 deleted, lines shift up):

```
# Meeting Notes
Attendees: Alice, Bob, Carol

## Action Items
- Alice: send recap
```

## Parsing Rules

A notx file parser must follow these rules:

1. **Comment lines** — Lines starting with `#` are metadata. Skip them during replay; extract key-value pairs for file header metadata.

2. **Blank lines** — Empty lines are whitespace. Skip them.

3. **Event header** — A line matching the regex `^(\d+):(\S+):(.+)$` is an event header.
   - Capture group 1: sequence number (integer)
   - Capture group 2: ISO-8601 timestamp
   - Capture group 3: author URN (remainder of line, including colons)

4. **Snapshot header** — A line matching `^snapshot:(\d+):(\S+)$` is a snapshot header.
   - Capture group 1: sequence number
   - Capture group 2: ISO-8601 timestamp

5. **Event separator** — A line that is exactly `->` marks the start of event entries.

6. **Snapshot separator** — A line that is exactly `=>` marks the start of snapshot entries.

7. **Line entry** — A line matching `^(\d+) \|(.*)$` is a line entry.
   - Capture group 1: line number (1-based integer)
   - Capture group 2: the content after the pipe
     - If the captured content is exactly `-` (after trimming), this is a deletion
     - If empty, the line is set to an empty string
     - Otherwise, the line is set to the captured content

8. **Unknown lines** — All other lines are ignored (forward-compatibility tolerance).

## Replay Algorithm

To reconstruct the document at any sequence number, apply events in order:

```
Initialize: lines = []

For each event with sequence <= targetSequence:
  For each line entry in the event:
    lineNumber = the entry's line number (1-based)

    If deletion (N |-):
      If lineNumber <= length(lines):
        Remove lines[lineNumber - 1]
        Decrement lineNumber for all subsequent entries in this event
    Else:
      If lineNumber > length(lines):
        Append content to lines
      Else:
        Replace lines[lineNumber - 1] with content

Return: lines.join('\n')
```

### Important Properties

- **Idempotent deletions** — Deleting a line that doesn't exist is a no-op (does not error).
- **Auto-append** — Setting a line beyond the document end automatically appends intermediate empty lines if needed.
- **Line shift awareness** — Within a single event, when a line is deleted, all subsequent line numbers in that event must be adjusted accordingly.

## Snapshot Optimization

For documents with many events, replaying from event 1 is inefficient. Snapshots are an optimization:

1. Find the nearest snapshot with `sequence <= targetSequence`
2. Start with the snapshot's materialized content
3. Replay only the events after the snapshot's sequence
4. Return the final state

This bounds replay cost to at most `SNAPSHOT_INTERVAL` events (default: 10).

Snapshots are **never the source of truth**. Events always are. A snapshot is a derived optimization; removing all snapshots does not change the behavior of a correct parser.

## Complete Example

```
# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Meeting Notes
# project_urn:   notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4
```

Or on a self-hosted instance:

```
# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Meeting Notes
# project_urn:   notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | # Meeting Notes
2 |
3 | Attendees: Alice, Bob

2:2025-01-15T09:15:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
3 | Attendees: Alice, Bob, Carol

3:2025-01-15T09:30:00Z:notx:usr:3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f
->
4 |
5 | ## Action Items
6 | - Alice: send recap

4:2025-01-15T10:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
2 |-
```

**Reading this file (on notx platform):**

1. Parse header → note `018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a`, named "Meeting Notes", in project `3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d`, created 2025-01-15 09:00:00 UTC, currently at sequence 4, on the `notx` platform.

**Same file on self-hosted instance (acme):**

The only differences would be the namespace in all URNs:

```
# notx/1.0
# note_urn:      acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Meeting Notes
# project_urn:   acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4

1:2025-01-15T09:00:00Z:acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | # Meeting Notes
...
```

2. Replay event 1 → 3 lines created
3. Replay event 2 → line 3 updated
4. Replay event 3 → lines 4–6 added
5. Replay event 4 → line 2 deleted

**Final document:**

```
# Meeting Notes
Attendees: Alice, Bob, Carol

## Action Items
- Alice: send recap
```

## Content Type

The MIME type for notx files is:

```
application/vnd.notx+plain
```

File extension: `.notx`

## Unicode and Encoding

notx files are UTF-8 encoded. All timestamps use ISO-8601 format with UTC timezone (Z suffix). Line content may contain any valid UTF-8 character.
