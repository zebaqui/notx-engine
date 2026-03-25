# notx File Semantics

## Overview

A `.notx` file is a complete, self-contained archive of a note's full history. It is the canonical source of truth for the document's content and all changes made to it. This document describes how notx files are structured, read, written, and managed.

## File Organization

### Logical Sections

Every notx file has three logical sections:

1. **Metadata Header** — Metadata lines prefixed with `#` that identify and describe the note
2. **Event Stream** — A sequence of events (header, separator, line entries) that form the complete history
3. **Snapshot Blocks** — Optional materialized content checkpoints interspersed throughout the event stream

### Physical Layout

```
# notx/1.0
# note_urn:      notx:note:<uuid>
# name:          <note name>
# project_urn:   notx:proj:<uuid>
# folder_urn:    notx:folder:<uuid>
# created_at:    <iso-8601>
# head_sequence: <N>

<event 1 header>
->
<event 1 line entries>

<event 2 header>
->
<event 2 line entries>

snapshot:10:<iso-8601>
=>
<snapshot line entries>

<event 11 header>
->
<event 11 line entries>

...
```

Events are separated from each other by blank lines (optional, improves readability). Snapshot blocks appear in-line at any point in the event stream.

## Metadata Header

The file header consists of lines prefixed with `# ` (hash and space). These lines provide the metadata necessary to identify and reconstruct the note without external references.

### Required Fields

| Field           | Format         | Purpose                                                   |
| --------------- | -------------- | --------------------------------------------------------- |
| `notx/1.0`      | Version string | Format version. Required as first header line.            |
| `note_urn`      | URN string     | Unique identifier: `<namespace>:note:<uuid>`              |
| `name`          | Plain text     | Display name / title of the note                          |
| `created_at`    | ISO-8601 UTC   | Timestamp of note creation (immutable)                    |
| `head_sequence` | Integer        | Sequence number of the last applied event (current state) |

### Optional Fields

| Field         | Format               | Purpose                                                                |
| ------------- | -------------------- | ---------------------------------------------------------------------- |
| `project_urn` | URN string or null   | URN of the containing project: `<namespace>:proj:<uuid>`               |
| `folder_urn`  | URN string or null   | URN of the containing folder: `<namespace>:folder:<uuid>`              |
| `parent_urn`  | URN string or null   | URN of parent note (for hierarchical notes): `<namespace>:note:<uuid>` |
| `deleted`     | Boolean (true/false) | Soft-delete flag                                                       |
| `updated_at`  | ISO-8601 UTC         | Timestamp of last change (derived from last event)                     |

### Header Parsing Rules

1. Lines starting with `#` followed by a space are metadata.
2. Key-value pairs are separated by `: ` (colon and space).
3. Unknown keys are ignored (forward compatibility).
4. Metadata lines are never consumed during event replay.
5. The `head_sequence` value is updated by writers whenever new events are appended.

### Header Immutability

The `note_urn` and `created_at` fields **must never change** once the file is created. These fields establish the identity of the note and its origin time.

Other fields (`name`, `project_urn`, `folder_urn`, `parent_urn`, `deleted`) may be updated by writers, but **only in the metadata header**, never stored as event data.

## Event Stream

The event stream is the core of the file—a chronological sequence of changes applied to the document.

### Event Header Format

```
<sequence>:<iso-timestamp>:<author-urn>
```

**Example:**

```
1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
```

- **Sequence** — Monotonically increasing integer starting at 1. No gaps; must be contiguous.
- **ISO-timestamp** — ISO-8601 format with UTC timezone (Z suffix). Timestamps do not need to be strictly increasing (two events can have the same timestamp), but should generally advance.
- **Author URN** — Full URN of the user who authored the change, or `notx:usr:anon` for unknown authors.

### Event Separator

Immediately after the event header is a line containing exactly `->`:

```
->
```

This separator marks the boundary between the event header and the line entries.

### Line Entries

Each line entry describes a change to a specific line in the document.

**Format:**

```
<line-number> | <content>
```

- **Line number** — 1-based integer. Must be a positive integer.
- **Pipe delimiter** — Exact character `|` (ASCII 0x7C).
- **Content** — Everything after the pipe, including leading/trailing spaces (if any).

**Deletion marker:**

```
<line-number> |-
```

The special sequence `|-` (pipe followed by hyphen with no space between) indicates deletion. This is distinct from setting a line to the string `-` (which would be `| -` with a space).

### Event Semantics

Within a single event, line entries are processed in the order they appear. However, **all line numbers are interpreted relative to the document state before the event began**.

**Critical rule:** When processing deletions within an event, implementations have two valid approaches:

1. **Index-shifting approach** — Apply each entry in order, adjusting subsequent line numbers as deletions occur within the same event.
2. **Batch approach** — Parse all entries first, then apply deletions in reverse order (highest line number first) to avoid index shifting within the event.

Both approaches produce the same result if implemented correctly. Choose the approach that is simplest for your implementation.

### Event Sequencing

- Sequence numbers must be **contiguous** and **strictly increasing**. No gaps (e.g., sequence 1, 2, 3, not 1, 2, 4).
- The first event has sequence 1. Sequence 0 is implicit (empty document before any events).
- The `head_sequence` in the metadata header must equal the sequence of the last event in the file.

### Invalid Events

An event with a sequence number that doesn't match the expected next sequence is malformed. A parser may:

- Reject the entire file
- Stop parsing at the first invalid event
- Skip the invalid event and continue (lenient parsing)

The recommended behavior is **rejection** to catch file corruption early.

## Snapshot Blocks

Snapshot blocks are **optional optimizations** that appear inline in the event stream. They contain the materialized (fully replayed) content of the document at a specific sequence number.

### Snapshot Header Format

```
snapshot:<sequence>:<iso-timestamp>
```

**Example:**

```
snapshot:10:2025-01-15T11:30:00Z
```

- **Sequence** — The sequence number at which this snapshot was taken. Must correspond to an event that exists in the stream.
- **ISO-timestamp** — When the snapshot was written (informational, not used during replay).

### Snapshot Separator

After the snapshot header comes a line containing exactly `=>`:

```
=>
```

### Snapshot Content

Following the separator are line entries in the exact same format as event entries:

```
1 | # Meeting Notes
2 | Attendees: Alice, Bob, Carol
3 |
4 | ## Action Items
5 | - Alice: send recap
```

**Important:** Snapshot content is **never** expressed as deltas. It is the **complete materialized state** of the document at that sequence number. Every line of the document at that sequence is listed explicitly.

### Snapshot Validity

A snapshot at sequence N should contain exactly the same content as would result from replaying all events 1 through N. If the snapshot content does not match the replayed content, the **events are the source of truth**. Discard the invalid snapshot.

### Snapshot Placement

Snapshots typically appear after every N events (e.g., every 10 events). However, there is no requirement that they be regularly spaced:

- Snapshots can be sparse (e.g., only after major milestones)
- Snapshots can be dense (e.g., after every event)
- Snapshots can be absent entirely (rely on full replay)

The only rule is: a snapshot at sequence M must not appear after an event with sequence > M.

### Snapshot Optimization Strategy

A reader seeking the state at sequence 100 in a 1000-event document would:

1. Scan for the nearest snapshot with sequence <= 100
2. If found (e.g., sequence 90), load that snapshot as the base state
3. Replay events 91–100 on top
4. Return the result

If no snapshot exists, replay from event 1.

This strategy bounds replay cost to at most `SNAPSHOT_INTERVAL` events, regardless of document size.

## State Materialization

### Current State

The **current state** of a note is always reconstructed by:

1. Starting with an empty document (zero lines)
2. Replaying all events in sequence order up to the event at `head_sequence`
3. Returning the resulting text

The current state may also include URN references to other notes (cross-instance or same-instance), but only metadata is resolved from remote instances—never content.

The current state is **derived**, never stored directly in the event stream. However, systems often cache the materialized content for performance (e.g., in a database column) as a **read optimization only**.

### Accessing Past States

To access the state at sequence N (where N < head_sequence):

1. Find the nearest snapshot with sequence <= N
2. Load that snapshot as the base (or start empty if no snapshot exists)
3. Replay events from after the snapshot up to and including sequence N
4. Return the result

### Line Ending Semantics

notx treats the document as a sequence of newline-separated lines. The final line of the document **never** has a trailing newline in the canonical representation (matching Unix text file convention).

When materializing:

```
lines = [
  "# Meeting Notes",
  "Attendees: Alice, Bob",
  "",
  "## Action Items"
]
return lines.join('\n')
// Result: "# Meeting Notes\nAttendees: Alice, Bob\n\n## Action Items"
```

## Writing and Append Operations

### Atomic Appends

When a new event is appended to a file:

1. Generate the new event header, separator, and line entries
2. Append the new event block to the end of the file
3. Update the `# head_sequence:` metadata line (in-place rewrite or append new value)

The most reliable strategy is:

```
Append the complete event block (header, separator, entries, blank line)
Append or update the # head_sequence: <N> metadata line
Flush to disk (fsync)
```

Updating the metadata in-place (seeking back to an earlier position) is risky if the new sequence number has a different character width (e.g., 9 → 10).

### Safe Update Strategy

The safest approach is:

1. **Append-only writes** — Always append new events and snapshots to the end of the file
2. **Update `head_sequence` metadata** — Append a new `# head_sequence: <N>` line; the parser uses the last occurrence
3. **Fsync before confirming** — Ensure the append is durable before returning success to the caller

This approach never requires in-place file modification and is safe for concurrent readers (read-only operations see either the old or new sequence, never torn state).

### Snapshot Appending

When a snapshot is written:

```
Append blank line
Append snapshot header (snapshot:<seq>:<ts>)
Append snapshot separator (=>)
Append snapshot line entries
Append blank line
```

Snapshots are additive only—they are never modified or deleted.

## Concurrent Access

### Reading

Multiple processes can safely read a notx file concurrently, even while it is being written. The safest guarantee is that a reader will see either:

- The old state (sequences 1–N)
- The new state (sequences 1–N+1)

Never a partial or torn state, because the writer appends atomically and updates metadata last.

### Writing

A notx file should have **at most one writer at a time**. Multiple concurrent writers can corrupt the file.

Recommended locking strategies:

1. **Advisory file lock** (`flock` on Unix, `LockFileEx` on Windows) — acquired by the writer before appending, held until fsync completes
2. **Write-ahead temp file** — write the new event to a temp file, fsync, then atomic rename over the original
3. **Exclusive process lock** — a mutex or database row if the file is managed by a backend server

### Durability

After appending events, the file must be explicitly flushed to disk (fsync or equivalent) before the write is considered committed. This is critical for:

- Recovering from power loss or OS crash
- Ensuring readers see durable state
- Preventing torn writes

## File Recovery

### Detecting Corruption

A notx file is corrupt if:

1. **Sequence gap** — Events jump from sequence 5 to sequence 7 (missing sequence 6)
2. **Duplicate sequence** — Two events claim the same sequence number
3. **Out-of-order sequence** — Events are not in strictly increasing order
4. **Invalid header** — First line is not `# notx/1.0`
5. **Missing URN** — No `# note_urn:` line in the header
6. **Mismatched head_sequence** — The metadata `head_sequence` does not match the last event

### Recovery Strategies

**For transient corruption** (e.g., incomplete append due to crash):

1. Truncate the file to the last complete event (end at the last blank line before an incomplete event)
2. Update `head_sequence` metadata to reflect the truncated state
3. Fsync and continue

**For validation failure:**

1. Reject the file and alert the operator
2. Restore from a backup (if available)
3. Manually edit the file to correct sequence numbers or remove invalid events

A robust system should maintain backups or use a database backend (not file-only) for production use.

## Import and Export

### Exporting to notx

When exporting a note from a database or other system to a notx file:

1. Collect all events for the note in sequence order
2. Write the metadata header (note_urn, name, project_urn, etc.)
3. Write all events in order
4. Include snapshots at regular intervals (optional but recommended)
5. Write the final `# head_sequence:` value
6. Fsync and close

The exported file is immediately readable and importable into any other system.

### Importing from notx

When importing a notx file:

1. Parse the metadata header to extract note_urn, name, and other fields
2. Validate that the sequence numbers are contiguous and start at 1
3. Replay all events to compute the materialized current state
4. Store the events (and optionally snapshots) in the target system
5. Set the note's current state to the replayed content and head_sequence

If a note with the same `note_urn` already exists in the target system, the import must either:

- **Merge** — append the imported events after the existing head_sequence (requires that imported events start after the local head_sequence)
- **Replace** — delete all existing events and snapshots, replace with imported data
- **Reject** — fail with a conflict error and ask the user to choose merge or replace

### Cross-System Compatibility

The key to notx portability is that **all identity is encoded in the file itself** (note_urn, project_urn, etc.). A file can be imported into any notx system without loss of information or identity. The URN scheme ensures that the imported note retains its identity across systems and can be linked to its original.

## Version Numbers and Sequencing

### Sequence as Version Identifier

Each event has a sequence number (1, 2, 3, ...). A **version** of the document is identified by the sequence number of the last applied event.

- Version 0: empty document (before any events)
- Version 1: state after applying event 1
- Version 5: state after applying events 1–5
- Version N: state after applying events 1–N

There is a 1:1 mapping between sequence numbers and versions.

### head_sequence Semantic

The `head_sequence` in the metadata header indicates the **current version** of the note. It must always equal the sequence of the last event in the file.

After appending event 10 (on any instance with any namespace):

```
head_sequence: 10
```

The namespace does not affect sequencing—each note on each instance has its own independent sequence counter.

### Version Queries

A reader can ask for the state at any version:

```
state_at_version(noteURN, 3)
  → replay events 1–3, return content

state_at_version(noteURN, head_sequence)
  → fully materialized current state

state_at_version(noteURN, 50)  # where head_sequence = 30
  → error or return current state (version 50 does not exist)
```

## Garbage Collection and Maintenance

### Snapshot Compaction

Over time, a file accumulates snapshots. To compact:

1. Keep the most recent snapshots (e.g., keep snapshots at multiples of 100)
2. Remove older, redundant snapshots
3. Rewrite the file or mark snapshots as obsolete

Snapshots are purely optional, so removing them is always safe—the events remain.

### Event Immutability

Events **must never be modified or deleted** from the file. The event log is the permanent, immutable record. If a correction is needed, it must be expressed as a new event, not by modifying the past.

### Cleanup and Archival

For very long-lived notes (thousands of events), consider:

1. **Periodic snapshots** at major milestones (every 100 events instead of 10)
2. **External archival** — store older events in a separate archive file, keep recent events inline
3. **Format upgrade** — if a new version of notx is released, migrate to it explicitly (this is future scope)

## Conformance and Tooling

### Valid notx File Checklist

A valid notx file must have:

- [ ] First line is `# notx/1.0`
- [ ] Metadata header with at least `note_urn`, `name`, `created_at`, `head_sequence`
- [ ] Event headers with contiguous, strictly increasing sequence numbers starting at 1
- [ ] Each event header followed by `->` separator
- [ ] Each event header followed by one or more line entries or zero entries (empty event)
- [ ] Each line entry matches the regex `^(\d+) \|(.*)$` (with deletion variant `|-`)
- [ ] Snapshots (if present) have valid headers and `=>` separators
- [ ] Snapshot sequence numbers correspond to events in the stream
- [ ] UTF-8 encoding throughout
- [ ] `head_sequence` value matches the last event's sequence

### Reference Implementations

A conformant parser must:

1. Parse the metadata header correctly
2. Extract contiguous, strictly increasing sequence numbers
3. Correctly apply set, empty-set, and delete operations
4. Produce the same materialized content as any other conformant parser
5. Support the snapshot optimization (or ignore snapshots and always replay from event 1)

Round-trip testing (file → parse → materialize → compare against re-parse) is the best validation.
