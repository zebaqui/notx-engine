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

All identifiable entities in notx use a four-segment URN (Uniform Resource Name):

```
urn:notx:<object-type>:<id>
```

### URN Components

| Component     | Description                                                   |
| ------------- | ------------------------------------------------------------- |
| `urn`         | Fixed prefix. Marks this as a notx URN.                       |
| `notx`        | Fixed scheme identifier.                                      |
| `object-type` | The kind of entity (see table below).                         |
| `id`          | ULID (preferred) or UUID. Globally unique. Generated locally. |

### Standard Object Types

| Type     | Description                 | Example URN                              |
| -------- | --------------------------- | ---------------------------------------- |
| `note`   | A note document             | `urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ` |
| `usr`    | A registered user or author | `urn:notx:usr:01HZX3K8ABCDEF1234567890`  |
| `proj`   | A project                   | `urn:notx:proj:01HZX3K8PROJID123456789`  |
| `folder` | A folder within a project   | `urn:notx:folder:01HZX3K8FOLDERID12345`  |
| `org`    | An organization             | `urn:notx:org:01HZX3K8ORGID1234567890`   |
| `event`  | A history event             | `urn:notx:event:01HZX3K8EVENTID123456`   |
| `device` | A registered device (E2EE)  | `urn:notx:device:01HZX3K8DEVICEID12345`  |
| `srv`    | A notx server instance      | `urn:notx:srv:01HZX3K8SERVERID123456`    |

### The `anon` Sentinel

Unauthenticated or unknown authors use a single global sentinel value:

```
urn:notx:usr:anon
```

This is not a real ID. It is a special value interpreted by parsers as "unknown or unauthenticated author". There is no namespace prefix — it is universal regardless of which server or deployment created the event.

### Note on Namespace

Namespace is **separate metadata on the object** (`"namespace": "acme"`), not encoded in the URN. The `.notx` file format may include optional `# authority:` and `# namespace:` header fields for human context, but the `note_urn`, event author URNs, and device URNs all use the `urn:notx:...` format regardless of which server or namespace created them. There is no distinction in URN structure between the official platform and any self-hosted instance.

## File Structure

A `.notx` file consists of three sections:

1. **File header** — metadata about the note (lines starting with `#`)
2. **Event stream** — the complete history of changes
3. **Snapshot blocks** (optional) — optimization checkpoints for fast replay

### File Header

The file begins with metadata lines, each prefixed with `#`:

```
# notx/1.0
# note_urn:      urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
# authority:     urn:notx:srv:01HZSERVER1234567890123
# namespace:     acme
# note_type:     normal
# name:          My Meeting Notes
# project_urn:   urn:notx:proj:01HZX3K8PROJID123456789
# folder_urn:    urn:notx:folder:01HZX3K8FOLDERID12345
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4
```

| Field           | Purpose                                                                                                                |
| --------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `notx/1.0`      | Format version. Always `notx/1.0` for this specification.                                                              |
| `note_urn`      | The unique identity of this note (`urn:notx:note:<id>`).                                                               |
| `authority`     | URN of the server instance that created or owns this note (`urn:notx:srv:<id>`). Optional.                             |
| `namespace`     | Human-readable namespace label for the owning organization (e.g. `acme`). Optional context only — not part of any URN. |
| `note_type`     | Security classification: `normal` (default) or `secure`. Immutable after creation.                                     |
| `name`          | Human-readable name / title of the note.                                                                               |
| `project_urn`   | URN of the project this note belongs to (`urn:notx:proj:<id>`). Optional.                                              |
| `folder_urn`    | URN of the folder this note is in (`urn:notx:folder:<id>`). Optional.                                                  |
| `created_at`    | ISO-8601 UTC timestamp of when the note was created.                                                                   |
| `head_sequence` | The sequence number of the last applied event (current state).                                                         |

The `head_sequence` field is updated whenever new events are appended, allowing the file header to always reflect the current state without rewriting the entire file.

The `note_type` field determines the entire data pipeline for the note:

- `normal` — plaintext stored on the server, TLS + access control protection, automatic sync, server-searchable. Absence of the field is treated as `normal` for backward compatibility.
- `secure` — end-to-end encrypted, server stores ciphertext only, explicit sharing required, not server-searchable. See [Encrypted Event Format](#encrypted-event-format) below.

`note_type` is **immutable**. It is set at creation time and cannot be changed by any event or API call.

### Event Stream

After the header, the file contains a sequence of **events**. Each event describes a set of line changes.

#### Event Header

An event header is a single line with three colon-delimited fields:

```
<sequence>:<iso-timestamp>:<author-urn>
```

Example:

```
1:2025-01-15T09:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
```

| Field           | Type         | Description                                    |
| --------------- | ------------ | ---------------------------------------------- |
| `sequence`      | integer      | Monotonically increasing, starts at 1          |
| `iso-timestamp` | ISO-8601 UTC | When the event was recorded                    |
| `author-urn`    | URN string   | Full URN of the author, or `urn:notx:usr:anon` |

**Parser note:** The author URN contains colons (e.g. `urn:notx:usr:01HZX3K8ABCDEF1234567890`); everything after the second colon in the header line is the author URN, which itself starts with `urn:`. The parser must treat everything after the second colon as the author URN.

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

#### Encrypted Event Format

For notes with `note_type: secure`, the normal `N | content` line entries are replaced with an **encrypted block**. The `!encrypted` marker on the first line of the event body signals this:

```
1:2025-01-15T09:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
->
!encrypted
nonce:   <base64-nonce>
payload: <base64-ciphertext>
key[urn:notx:device:01HZX3K8DEVICEID12345]: <base64-wrapped-cek>
key[urn:notx:device:01HZX3K8DEVICE2ID1234]: <base64-wrapped-cek>
```

| Field               | Description                                                                                                                             |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `!encrypted`        | Marker indicating this event uses the encrypted format. Must be the first entry after `->`.                                             |
| `nonce`             | Base64-encoded AES-256-GCM nonce. Unique per event — never reused.                                                                      |
| `payload`           | Base64-encoded AES-256-GCM ciphertext of the serialized line entries.                                                                   |
| `key[<device-urn>]` | Per-device entry: the Content Encryption Key (CEK) wrapped with that device's public key (X25519 ECDH). One entry per recipient device. |

**Parser behavior for `!encrypted` events**:

- A server-side parser (no private key) **stores and relays the block verbatim** without error. It never attempts decryption.
- A client-side parser locates its own `key[<own-device-urn>]` entry, uses its private key to unwrap the CEK, then decrypts `payload` to recover the line entries, and replays them normally.
- A parser encountering `!encrypted` on a `note_type: normal` note **must reject the file** as malformed.

**Encryption scheme** (for implementors):

1. Generate a random 256-bit Content Encryption Key (CEK) per event.
2. Serialize the line entries as UTF-8 text in normal lane format.
3. Encrypt the serialized entries with AES-256-GCM using the CEK and a random 96-bit nonce.
4. For each recipient device, derive a shared secret via X25519 ECDH (sender private key + recipient public key), then derive a wrapping key with HKDF-SHA256, and wrap the CEK with it.
5. Store the nonce, ciphertext, and all per-device wrapped CEK entries.

See [NOTX_SECURITY_MODEL.md](./NOTX_SECURITY_MODEL.md) for the complete implementation plan.

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
1:2025-01-15T09:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
->
1 | # Meeting Notes
2 |
3 | Attendees: Alice, Bob
```

This event:

1. Is event #1, created on 2025-01-15 at 09:00:00 UTC by user `urn:notx:usr:01HZX3K8ABCDEF1234567890`
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
2:2025-01-15T09:15:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
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
1:2025-01-15T09:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
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
2:2025-01-15T09:15:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
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
3:2025-01-15T09:30:00Z:urn:notx:usr:01HZX3K8CDEF1234567890AB
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
4:2025-01-15T10:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
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

1. **Comment lines** — Lines starting with `#` are metadata. Skip them during replay; extract key-value pairs for file header metadata. The `note_type` field must be extracted and used to select the correct event processing path.

2. **Blank lines** — Empty lines are whitespace. Skip them.

3. **Event header** — A line matching the regex `^(\d+):(\S+):(.+)$` is an event header.
   - Capture group 1: sequence number (integer)
   - Capture group 2: ISO-8601 timestamp
   - Capture group 3: author URN (remainder of line, including colons). The author URN starts with `urn:` and contains additional colons as part of the `urn:notx:<type>:<id>` structure. Everything after the second colon in the header line is treated as the author URN.

4. **Snapshot header** — A line matching `^snapshot:(\d+):(\S+)$` is a snapshot header.
   - Capture group 1: sequence number
   - Capture group 2: ISO-8601 timestamp

5. **Event separator** — A line that is exactly `->` marks the start of event entries.

6. **Snapshot separator** — A line that is exactly `=>` marks the start of snapshot entries.

7. **Encrypted event marker** — A line that is exactly `!encrypted` after the `->` separator signals an encrypted event block (only valid when `note_type: secure`).
   - A server-side parser stores the entire block verbatim and does not attempt decryption.
   - A client-side parser unwraps the CEK using the device private key, then decrypts the payload and processes the recovered line entries normally.
   - Encountering `!encrypted` in a `note_type: normal` file is a parse error.

8. **Line entry** — A line matching `^(\d+) \|(.*)$` is a line entry.
   - Capture group 1: line number (1-based integer)
   - Capture group 2: the content after the pipe
     - If the captured content is exactly `-` (after trimming), this is a deletion
     - If empty, the line is set to an empty string
     - Otherwise, the line is set to the captured content

9. **Unknown lines** — All other lines are ignored (forward-compatibility tolerance).

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

The following is a complete `.notx` file. The `urn:notx:...` URN format is identical regardless of whether the file was created on the official notx platform or any self-hosted instance. The optional `authority` and `namespace` header fields provide server and organizational context, but are not part of any URN.

```
# notx/1.0
# note_urn:      urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
# authority:     urn:notx:srv:01HZSERVER1234567890123
# namespace:     acme
# name:          Meeting Notes
# project_urn:   urn:notx:proj:01HZX3K8PROJID123456789
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4

1:2025-01-15T09:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
->
1 | # Meeting Notes
2 |
3 | Attendees: Alice, Bob

2:2025-01-15T09:15:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
->
3 | Attendees: Alice, Bob, Carol

3:2025-01-15T09:30:00Z:urn:notx:usr:01HZX3K8CDEF1234567890AB
->
4 |
5 | ## Action Items
6 | - Alice: send recap

4:2025-01-15T10:00:00Z:urn:notx:usr:01HZX3K8ABCDEF1234567890
->
2 |-
```

**Reading this file:**

1. Parse header → note `urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ`, named "Meeting Notes", in project `urn:notx:proj:01HZX3K8PROJID123456789`, created 2025-01-15 09:00:00 UTC, currently at sequence 4, associated with the `acme` namespace on server `urn:notx:srv:01HZSERVER1234567890123`.
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
