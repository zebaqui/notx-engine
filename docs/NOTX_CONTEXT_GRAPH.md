# Contextual Graph Layer for Living Documents

## Overview

The link spec (`NOTX_LINK_SPEC.md`) gives notx a stable addressing system: every
meaningful span of content can be named with an anchor ID and referenced by
`notx:lnk:id` tokens that survive edits. That is the foundation — the permanent
roads of the knowledge graph.

But roads have to be planned. As a collection of notes grows and changes rapidly
through event sourcing, the manual work of declaring anchors and writing link
tokens does not scale. Authors miss connections. Concepts drift across notes.
A new definition appears in one project document while five others already use
the same term without any formal link.

The **Contextual Graph Layer** is the discovery engine that sits underneath
stable links. It observes every edit event, extracts small bursts of local
context, and quietly detects when two pieces of content across the system look
like they might be talking about the same thing. It never makes a final judgment.
It produces **candidates** — lightweight, reviewable signals that say:
"these two spans may be related." Users and AI agents decide what to do next.

### What this layer is not

- It is not a full RAG (Retrieval-Augmented Generation) pipeline.
- It is not a vector embedding system (though it is designed to be a clean
  attachment point for one in the future).
- It does not rewrite notes, create links, or modify anchors autonomously.
- It does not block the write path. Its work on each edit must complete
  under **10 milliseconds** on the hot path.

---

## How It Fits the Architecture

```
                    ┌────────────────────────────────────────┐
                    │           notx write path              │
                    │                                        │
  AppendEvent ──►  │  1. validate event                     │
                    │  2. apply to materialized content      │
                    │  3. update FTS5 index (notes_fts)      │
                    │  4. update anchor position hints       │  ← link spec
                    │  5. ── CONTEXT BURST EXTRACTION ──     │  ← this doc
                    │     a. check rate limits               │
                    │     b. split event into burst windows  │
                    │     c. similarity skip check           │
                    │     d. tokenize each burst             │
                    │     e. Jaccard scoring (in-memory)     │
                    │     f. insert bursts + candidates      │
                    │  6. commit                             │
                    └────────────────────────────────────────┘
                                       │
                        ┌──────────────┴──────────────────────┐
                        │                                     │
                  context_bursts                   candidate_relations
                   (SQLite table)                    (SQLite table)
                        │                   bm25_score=0 on insert │
                        │                          │               │
                        │               ┌──────────┘               │
                        │               │                          │
                        │    Background BM25 scorer            Review API
                        │    (async goroutine, updates      (human / AI)
                        │     bm25_score after insert)
                        │                          │
                        │              ┌───────────┤
                        │              │           │
                        │          Dismiss    Promote to
                        │                    notx:lnk:id link
                        │                    + anchor declaration
```

The contextual graph layer is a **pure addition**. It does not modify the event
stream, does not change the `.notx` file format, and does not affect the
immutability guarantees of the event log. Every artifact it produces lives in
two new SQLite tables that can be dropped and rebuilt from the event log at any
time without data loss.

---

## Part 1 — Context Bursts

### Definition

A **context burst** is a small, self-contained excerpt of note content generated
at the moment an event is appended. It captures just enough surrounding text to
convey the meaning of what changed — not the whole note, not just the changed
line, but a narrow window of surrounding context.

A burst is **not** a diff. It is a snapshot of the changed region in its
readable form: what the content looks like _after_ the event is applied, with
a few lines of surrounding context to establish meaning.

A single event may produce **multiple bursts** if it touches several
non-contiguous regions of the document. Each burst is an independent unit with
its own line range, token set, and candidacy surface.

### Extraction Algorithm

When `AppendEvent` is called, after the event is applied to the materialized
content, the engine runs burst extraction inside the same write transaction.

**Step 1 — Check rate limits (fast exit)**

Before any other work, check whether the note or project has hit its daily burst
cap (see [Burst Rate Limiting](#burst-rate-limiting)). If either cap is exceeded,
skip all remaining steps and return. The write still succeeds.

**Step 2 — Identify affected lines**

Collect the set of line numbers touched by the event's `LineEntry` operations:

```
affected = { entry.LineNumber for entry in event.Entries
             where entry.Op in (LineOpSet, LineOpSetEmpty, LineOpDelete) }
```

**Step 3 — Group into contiguous windows**

Sort the affected line numbers. Walk the sorted list and group them into
contiguous runs — a new group starts whenever two adjacent line numbers are more
than `burst_window_lines * 2 + 1` apart (i.e., their context windows would not
overlap). Expand each group by `±burst_window_lines`, clamped to `[1, totalLines]`.

The result is an ordered list of `(line_start, line_end)` ranges. Each range is
an independent burst candidate.

**Step 4 — Split oversized windows**

If any single range spans more than `burst_chunk_size` lines (default: 20),
split it into sequential sub-windows of `burst_chunk_size` lines with a 2-line
overlap between adjacent sub-windows to preserve context at boundaries.

```
Example: a contiguous range covering lines 10–70 with burst_chunk_size=20
  → sub-window 1: lines 10–29
  → sub-window 2: lines 28–47   (2-line overlap)
  → sub-window 3: lines 46–65   (2-line overlap)
  → sub-window 4: lines 64–70
```

If an event would produce more than `burst_max_windows` (default: 10) total
sub-windows after splitting, only the first 10 are processed. Each resulting
burst is marked `truncated: true`. This is an extreme edge case; under normal
use `truncated` will never be set.

**Step 5 — Extract text**

For each `(line_start, line_end)` range, read the materialized content lines
from the post-event document state. Concatenate them preserving newlines. Strip
leading and trailing blank lines from the window.

**Step 6 — Consecutive similarity skip**

Before treating each window as a new burst, run a single read-only query to
check whether the window is redundant:

```sql
SELECT tokens, created_at
FROM   context_bursts
WHERE  note_urn = ?
ORDER  BY created_at DESC
LIMIT  1
```

If the most recent burst for this note satisfies **all** of the following:

- It was created within the last `burst_skip_window_seconds` (default: 300 — five minutes)
- Its line range overlaps or is directly adjacent to the current window's range
- Its Jaccard similarity against the current window's token set is ≥
  `burst_skip_threshold` (default: 0.80)

Then **skip** inserting the new burst. No new row is created. The existing burst
row is **not modified in any way**. This is a pure read → decision step with
zero writes to existing data.

This prevents rapid re-edits of the same passage (e.g., typo corrections over
several events) from producing a cascade of near-identical burst rows, without
introducing any mutation of immutable records or transaction complexity.

**Step 7 — Skip trivial bursts**

For each remaining window, discard it if:

- The extracted text is blank or contains only whitespace.
- The total token count after normalization (Step 8) is fewer than 3 tokens.
- The event author is `urn:notx:usr:anon` and the note belongs to an
  auto-generated sequence (e.g., a snapshot compaction event).

**Step 8 — Tokenize**

Tokenize each burst's extracted text using the same algorithm as the FTS5 index
(reusing `tokenise` from `repo/index/index.go`): lowercase, split on
non-alphanumeric boundaries, discard single-character tokens, discard the
engine's built-in global stop-word list. Store the result as a space-separated
normalized token string.

**Step 9 — Enrich with context**

Every burst carries the full contextual envelope from the event and note:

| Field         | Source                                                              |
| ------------- | ------------------------------------------------------------------- |
| `note_urn`    | `event.NoteURN`                                                     |
| `project_urn` | `note.ProjectURN` (may be empty)                                    |
| `folder_urn`  | `note.FolderURN` (may be empty)                                     |
| `author_urn`  | `event.AuthorURN`                                                   |
| `sequence`    | `event.Sequence`                                                    |
| `line_start`  | First line of the burst window                                      |
| `line_end`    | Last line of the burst window                                       |
| `truncated`   | `true` only if this burst is one of > 10 sub-windows from one event |
| `created_at`  | `event.CreatedAt`                                                   |

### Burst Rate Limiting

High-velocity projects (frequent large pastes, automated writes) could generate
hundreds of bursts rapidly, degrading candidate detection quality and query
performance. Two independent caps prevent this.

**Per-note daily cap** (`burst_max_per_note_per_day`, default: 50)

Before extraction, query:

```sql
SELECT COUNT(*) FROM context_bursts
WHERE note_urn = ? AND created_at >= <start_of_today_ms>
```

If the result is ≥ the cap, skip burst extraction for this event entirely.

**Per-project daily cap** (`burst_max_per_project_per_day`, default: 500)

Same pattern scoped to `project_urn`. If the project cap is hit, no further
bursts are generated for any note in the project until the next UTC day.

Both caps are checked against indexed columns (`idx_bursts_note`,
`idx_bursts_project`) and add < 0.5 ms to the write path on a warm cache.

When a cap is hit the event's contextual contribution is simply absent. The
write succeeds, the event is durable, and no warning is surfaced to the caller.
Silent omission is always correct for advisory infrastructure.

**Per-project overrides**

The global defaults suit most projects, but automated workflows or high-velocity
projects may need higher caps. Per-project overrides are stored in
`project_context_config` (see [Part 3 — SQLite Schema](#part-3--sqlite-schema))
and take precedence over the global config. A `NULL` value in the project config
means "use the global default."

The rate limit check order is:

1. Look up `project_context_config` for the note's `project_urn` (cheap PRIMARY
   KEY lookup; adds < 0.2 ms to the rate limit step).
2. If a project-level cap is set (`NOT NULL`), use it; otherwise fall back to
   the global config value.

### Burst Scope

Bursts are scoped to the **project** they belong to. Candidate detection only
runs against other bursts in the same project. Cross-project candidates are not
generated automatically — the project boundary is treated as an intentional
scope wall.

For notes not in any project, bursts are scoped globally (they may match any
other project-less note on the same server instance).

### Burst Lifetime

Bursts are not permanent records. They are working material for candidate
detection. The engine retains bursts for a configurable window (default:
**90 days** from `created_at`). Older bursts are swept by a background
maintenance job. Candidate relations that reference swept bursts are not
deleted — only the burst text is removed; the relation record is preserved
with the burst marked `expired`.

---

## Part 2 — Candidate Relations

### Definition

A **candidate relation** is a lightweight record that says: "burst A and burst B
share enough overlapping context that they might be meaningfully related." It
carries no assertion about _how_ they are related. That judgment belongs to the
author or an AI agent.

### Detection Algorithm

Candidate detection runs after each burst is stored, inside the same write
transaction. It is a single-stage Jaccard pass. BM25 enrichment happens
asynchronously after the transaction commits (see
[Background BM25 Scorer](#background-bm25-scorer)).

**Step 1 — Query recent bursts in scope**

Fetch up to `candidate_lookback_n` (default: 100) bursts from
`context_bursts` that:

- Share the same `project_urn` as the new burst (or are also project-less)
- Were created within the last `candidate_lookback_days` (default: 30)
- Belong to a **different note** than the new burst
  (`burst.note_urn != new_burst.note_urn`)

Ordered by `created_at DESC` so the most recently active notes are evaluated
first within the budget.

**Step 2 — Compute raw Jaccard in Go**

For each fetched burst, compute Jaccard similarity over the raw normalized token
sets (the space-separated `tokens` strings already stored in the database):

```
jaccard(A, B) = |tokens(A) ∩ tokens(B)| / |tokens(A) ∪ tokens(B)|
```

This is pure in-memory set math in Go — no SQL, no re-parsing, no network.

**Step 3 — Apply threshold gate**

Discard pairs where `jaccard < overlap_threshold` (default: 0.12). The
threshold is intentionally permissive: false positives are cheap to dismiss;
false negatives are invisible.

**Step 4 — Deduplicate**

Before inserting, check whether a candidate relation between these two bursts
(in either direction) already exists with status `pending` or `promoted`. Skip
if so. This prevents re-edits of the same content from flooding the queue.

**Step 5 — Insert candidate**

Insert into `candidate_relations` with `status='pending'`,
`overlap_score = jaccard`, and `bm25_score = 0.0`. The `bm25_score` column is
populated asynchronously by the background BM25 scorer after the write
transaction commits.

### Candidate Status Lifecycle

```
pending ──► promoted ──► (creates real notx:lnk:id link + anchor)
   │
   └──► dismissed ──► (soft-deleted, never surfaced again for this burst pair)
```

| Status      | Meaning                                                                      |
| ----------- | ---------------------------------------------------------------------------- |
| `pending`   | Detected but not yet reviewed                                                |
| `promoted`  | Reviewed and converted into a real `notx:lnk:id` link by a user or AI agent  |
| `dismissed` | Reviewed and determined not to be a meaningful connection                    |
| `expired`   | One or both source bursts have been swept; preserved for audit, not surfaced |

Dismissed candidates are **never** re-surfaced for the same burst pair. If the
same two notes later develop stronger overlapping content in a new edit, a new
candidate relation is created for the new burst pair — the old dismissal does
not block it.

### What a Candidate Is Not

A candidate relation is between **bursts**, not between notes as a whole. Two
notes may have multiple independent candidate relations across different bursts
(different sections, different concepts). This is intentional: the same two
notes may share several distinct connection points, and each deserves independent
review.

When a candidate is promoted, the resulting `notx:lnk:id` link is anchored at
the specific span (burst window lines) where the connection was detected — not
at the note level.

---

## Part 3 — SQLite Schema

The contextual graph layer adds two new tables and one FTS5 virtual table.
These are appended as a single migration to the existing schema in
`repo/sqlite/schema.go`.

### Migration (v3)

```sql
-- v3: contextual graph layer
-- Adds: context_bursts, context_bursts_fts, candidate_relations

-- Context bursts: one or more per event, one per contiguous changed-line window.
-- Large events are split into multiple burst rows (one per chunk), not truncated.
-- Existing burst rows are NEVER modified after insertion.
CREATE TABLE IF NOT EXISTS context_bursts (
    id          TEXT    PRIMARY KEY,           -- UUIDv7 of the burst
    note_urn    TEXT    NOT NULL,
    project_urn TEXT    NOT NULL DEFAULT '',
    folder_urn  TEXT    NOT NULL DEFAULT '',
    author_urn  TEXT    NOT NULL DEFAULT '',
    sequence    INTEGER NOT NULL,              -- event sequence that produced this burst
    line_start  INTEGER NOT NULL,
    line_end    INTEGER NOT NULL,
    text        TEXT    NOT NULL,              -- raw extracted text (window content)
    tokens      TEXT    NOT NULL DEFAULT '',   -- space-separated normalized token set
    truncated   INTEGER NOT NULL DEFAULT 0,    -- 1 only when event produced > 10 sub-windows
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_bursts_note    ON context_bursts(note_urn, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bursts_project ON context_bursts(project_urn, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bursts_created ON context_bursts(created_at DESC);

-- FTS5 over burst tokens.
-- Used by the background BM25 scorer to enrich candidate_relations.bm25_score.
-- Not queried on the hot write path.
CREATE VIRTUAL TABLE IF NOT EXISTS context_bursts_fts USING fts5(
    id          UNINDEXED,
    note_urn    UNINDEXED,
    project_urn UNINDEXED,
    tokens,
    content='context_bursts',
    content_rowid='rowid'
);

-- Candidate relations: pairs of bursts from different notes that may be connected.
-- overlap_score = raw Jaccard at detection time (set on insert, never updated).
-- bm25_score    = FTS5 BM25 relevance score for queue ordering.
--                 Starts at 0.0; updated asynchronously by the background scorer.
CREATE TABLE IF NOT EXISTS candidate_relations (
    id            TEXT    PRIMARY KEY,         -- UUIDv7 of the relation
    burst_a_id    TEXT    NOT NULL,
    burst_b_id    TEXT    NOT NULL,
    note_urn_a    TEXT    NOT NULL,
    note_urn_b    TEXT    NOT NULL,
    project_urn   TEXT    NOT NULL DEFAULT '',
    overlap_score REAL    NOT NULL,            -- raw Jaccard at detection time
    bm25_score    REAL    NOT NULL DEFAULT 0,  -- stored as -bm25() so higher = more relevant;
                                               -- 0.0 = unenriched (sorts below all enriched rows)
    status        TEXT    NOT NULL DEFAULT 'pending',
    created_at    INTEGER NOT NULL,
    reviewed_at   INTEGER,                     -- NULL until reviewed
    reviewed_by   TEXT    NOT NULL DEFAULT '', -- URN of reviewing user or agent
    promoted_link TEXT    NOT NULL DEFAULT ''  -- the notx:lnk:id token if promoted
);

-- Per-project context graph configuration overrides.
-- NULL in any column means "fall back to the global server config default."
-- Allows high-velocity projects or automated workflows to raise their caps
-- without changing the server-wide configuration.
CREATE TABLE IF NOT EXISTS project_context_config (
    project_urn                   TEXT    PRIMARY KEY,
    burst_max_per_note_per_day    INTEGER,  -- NULL = use global default
    burst_max_per_project_per_day INTEGER,  -- NULL = use global default
    updated_at                    INTEGER NOT NULL
);

-- Primary retrieval index: project queue ordered by confidence then raw score.
CREATE INDEX IF NOT EXISTS idx_candidates_project_status
    ON candidate_relations(project_urn, status, bm25_score DESC, overlap_score DESC);
CREATE INDEX IF NOT EXISTS idx_candidates_notes
    ON candidate_relations(note_urn_a, note_urn_b, status);
CREATE INDEX IF NOT EXISTS idx_candidates_burst_a ON candidate_relations(burst_a_id);
CREATE INDEX IF NOT EXISTS idx_candidates_burst_b ON candidate_relations(burst_b_id);

-- Deep connection pairs: materialized aggregate of all candidates between a note pair.
-- Canonical ordering: note_urn_a < note_urn_b (lexicographic, enforced by caller).
-- Updated atomically with each candidate insert / status change.
-- connection_score is recalculated on every upsert (not cached separately).
CREATE TABLE IF NOT EXISTS note_pair_connections (
    note_urn_a          TEXT    NOT NULL,
    note_urn_b          TEXT    NOT NULL,
    project_urn         TEXT    NOT NULL DEFAULT '',
    candidate_count     INTEGER NOT NULL DEFAULT 0,    -- all statuses
    pending_count       INTEGER NOT NULL DEFAULT 0,
    promoted_count      INTEGER NOT NULL DEFAULT 0,
    dismissed_count     INTEGER NOT NULL DEFAULT 0,
    connection_score    REAL    NOT NULL DEFAULT 0.0,  -- weighted overlap sum (see Part 12)
    first_seen_at       INTEGER NOT NULL,
    last_activity_at    INTEGER NOT NULL,
    is_deep             INTEGER NOT NULL DEFAULT 0,    -- 1 once depth threshold crossed
    deep_flagged_at     INTEGER,                       -- NULL until is_deep first set to 1
    PRIMARY KEY (note_urn_a, note_urn_b)
);

CREATE INDEX IF NOT EXISTS idx_pairs_project_score
    ON note_pair_connections(project_urn, connection_score DESC);
CREATE INDEX IF NOT EXISTS idx_pairs_deep
    ON note_pair_connections(project_urn, is_deep, last_activity_at DESC);

-- Note metadata inferences: asynchronously derived title/project suggestions
-- for notes that were created without a title or project URN.
-- One active (pending) record per note at most.
-- Acceptance writes an AppendEvent to the note's event log; rejection records
-- a token hash so inference is not re-run until content changes substantially.
CREATE TABLE IF NOT EXISTS note_context_inferences (
    id                    TEXT    PRIMARY KEY,         -- UUIDv7
    note_urn              TEXT    NOT NULL,
    inferred_title        TEXT    NOT NULL DEFAULT '', -- '' if title inference inconclusive
    inferred_project_urn  TEXT    NOT NULL DEFAULT '', -- '' if project inference inconclusive
    title_confidence      REAL    NOT NULL DEFAULT 0.0,
    project_confidence    REAL    NOT NULL DEFAULT 0.0,
    project_evidence      TEXT    NOT NULL DEFAULT '', -- JSON: [{project_urn,candidate_count,fraction}]
    title_basis_burst_id  TEXT    NOT NULL DEFAULT '', -- burst ID from which title was derived
    status                TEXT    NOT NULL DEFAULT 'pending', -- pending|accepted|rejected
    created_at            INTEGER NOT NULL,
    reviewed_at           INTEGER,
    reviewed_by           TEXT    NOT NULL DEFAULT '',
    rejected_token_hash   TEXT    NOT NULL DEFAULT ''  -- for re-enable gate after rejection
);

-- Only one pending inference per note at a time.
CREATE UNIQUE INDEX IF NOT EXISTS idx_inferences_note_pending
    ON note_context_inferences(note_urn)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_inferences_status
    ON note_context_inferences(status, created_at DESC);
```

### Notes on Schema Design

**Why only two tables?**

Project-level stop phrases were considered but removed as premature complexity.
The global stop-word list (already used by the FTS5 tokenizer) eliminates
function-word noise. If per-project noise proves to be a real problem after
deployment, a `project_stop_phrases` table can be added in a future migration
without affecting the current design.

**Why `bm25_score` starts at 0.0?**

The hot write path inserts candidates with `bm25_score = 0.0` and returns
immediately. The background BM25 scorer picks up newly inserted candidates
within seconds and updates the score. Freshly inserted candidates will briefly
appear at the bottom of the queue before being enriched — this is acceptable
because the queue is reviewed by humans and agents on a timescale of minutes
or hours, not milliseconds.

**Why is `overlap_score` the raw Jaccard, not a cleaned score?**

Without project stop phrases, there is no cleaning step. The raw Jaccard over
the globally-tokenized (stop-word-stripped) token sets is already reasonably
clean. If candidates driven by structural boilerplate become a problem, the fix
is a future stop-phrase table — not a scoring change to this schema.

**Why `(burst_a_id, burst_b_id)` not `(note_urn_a, note_urn_b)`?**

Candidates are between specific changed regions, not between notes as a whole.
Two notes may legitimately share multiple independent candidate relations (one
per conceptually distinct overlap region). Note-level deduplication would
collapse these into a single undifferentiated signal.

**Why a separate `project_context_config` table instead of a column on `projects`?**

The existing `projects` table has no `extra` or JSON catch-all column, so
adding per-project context config there would require a migration that alters
that table. A dedicated table keeps the context graph's concerns isolated and
makes it trivially droppable if the feature is not needed. The table is queried
by PRIMARY KEY on the hot path, so there is no index overhead beyond the
implicit PK index.

**Why are burst rows immutable after insertion?**

The similarity skip (Part 1, Step 6) is a read → decision step that produces
no writes to existing rows. Keeping burst rows append-only means the table
is safe to rebuild, audit, and reason about — there is no "was this row
modified after initial insert?" ambiguity.

---

## Part 4 — Write Path Integration

### Where It Hooks In

Burst extraction and candidate detection hook into `NoteRepository.AppendEvent`
in `repo/sqlite/provider.go`, after the event is written and the FTS index is
updated, still inside the same write goroutine.

The single-writer goroutine model in the `Provider` guarantees no concurrency
risk: burst insertion and candidate scoring run sequentially with the event
write, and the database is never left in a state where an event exists without
its corresponding bursts.

### Performance Budget

All hot-path contextual work must complete in under **10 milliseconds** on
commodity hardware.

| Step                                        | Budget     |
| ------------------------------------------- | ---------- |
| Rate limit check (2× COUNT, indexed)        | < 0.5 ms   |
| Line grouping + split (Go, in-memory)       | < 0.5 ms   |
| Similarity skip check (1 SQL SELECT, 1 row) | < 0.5 ms   |
| Tokenization (Go, reuse `tokenise`)         | < 0.5 ms   |
| Fetch recent bursts (SQL, N=100)            | < 1.5 ms   |
| Jaccard scoring (Go, in-memory, N=100)      | < 1.5 ms   |
| Insert bursts + candidates (bm25_score=0)   | < 2 ms     |
| **Total**                                   | **< 7 ms** |

The 3 ms margin is buffer for I/O variance and events that produce multiple
burst windows. The `candidate_lookback_n=100` limit is the primary budget knob.

### Background BM25 Scorer

BM25 scoring against `context_bursts_fts` is **not** performed on the hot write
path. It runs in a dedicated background goroutine started at server startup.

**Mechanism:**

When candidate rows are inserted in the write transaction with `bm25_score=0`,
their IDs are sent (non-blocking) to a buffered channel owned by the scorer
goroutine:

```
scorerCh chan string   // buffered, capacity 512
```

The scorer goroutine reads IDs from the channel, runs a BM25 FTS5 query for
each, and updates the row via the normal write channel:

```sql
-- Scorer query per candidate.
-- SQLite's bm25() returns negative values (more negative = better match).
-- We negate it before storing so that higher stored score = more relevant,
-- and unenriched rows (bm25_score=0.0) sort below all enriched rows with
-- the existing ORDER BY bm25_score DESC index.
SELECT -bm25(context_bursts_fts) AS score
FROM   context_bursts_fts
WHERE  context_bursts_fts MATCH ?   -- significant token set from burst_a
AND    id = ?                       -- burst_b's id
```

```sql
-- Scorer update per candidate
UPDATE candidate_relations
SET    bm25_score = ?
WHERE  id = ?
```

**Conditions for BM25 enrichment:**

The scorer only enriches candidates where `overlap_score >= bm25_min_overlap`
(default: 0.20). Candidates below this threshold are valid but lower-priority
connections that are unlikely to benefit from reranking. This keeps the scorer
goroutine's workload proportional to the most interesting candidates.

**If the channel is full:**

The write path drops the ID silently. The candidate row remains with
`bm25_score=0` and will still appear in the review queue, sorted below
enriched candidates. The write is never blocked.

**If the scorer goroutine fails:**

The scorer logs a WARN and continues. A failed BM25 update leaves the candidate
at `bm25_score=0`, which is a graceful degradation — the candidate is still
discoverable, just not optimally ordered.

**Scorer lag and the unenriched state:**

When the scorer falls behind — for example, after a bulk import generates
hundreds of candidates faster than the scorer can process them — affected
candidates remain at `bm25_score=0.0`. This is the **unenriched state**.

Consequences of scorer lag:

- The review queue is ordered by `bm25_score DESC, overlap_score DESC`. Since
  enriched candidates have `bm25_score > 0` (stored as `-bm25()`, which is
  positive for any real match), unenriched candidates at `bm25_score=0.0` sort
  below all enriched ones. They appear at the bottom of the queue and are
  reviewed last — a natural backpressure: the most confident connections surface
  first regardless of lag.
- `GET /v1/context/stats` exposes `candidates_pending_unenriched` (count of
  `pending` rows with `bm25_score=0`). Operators and agents can use this to
  gauge scorer health. A count that stays persistently high and does not decrease
  indicates the scorer is stalled or `scorerCh` is overflowing.

**Scorer restart on error:**

If the scorer goroutine exits due to an unhandled error (not a per-item failure),
the server logs an ERROR and restarts the goroutine after a 5-second backoff.
The scorer has no persistent state beyond the `candidate_relations` table, so a
restart safely re-drains any IDs already in `scorerCh`.

**Overflow reconciliation:**

When `scorerCh` overflows, dropped IDs are permanently lost from the channel —
those candidates stay unenriched indefinitely. A background reconciliation job
(runs every hour) finds all `pending` candidates where `bm25_score=0` and
`created_at < NOW() - 5 minutes`, and re-enqueues their IDs into `scorerCh`
(non-blocking send, drops again if still full). This catches overflow victims
without a full table scan on the hot write path.

### Graceful Degradation

If burst extraction or candidate detection fails for any reason (schema
migration in progress, FTS rebuild, query timeout), the error is **logged at
WARN level and not returned to the caller**. The `AppendEvent` response is
determined solely by whether the event itself was written successfully.

The contextual layer is advisory infrastructure — it must never block a write.

### Configuration

```yaml
context_graph:
  enabled: true

  # Hot-path candidate detection
  overlap_threshold: 0.12 # raw Jaccard gate for candidate insertion
  candidate_lookback_days: 30 # how far back to query for candidate matches
  candidate_lookback_n: 100 # max bursts fetched per event for scoring

  # Burst extraction
  burst_window_lines: 2 # ±N context lines around each changed line
  burst_chunk_size: 20 # max lines per window before splitting
  burst_max_windows: 10 # max sub-windows produced by one event

  # Consecutive similarity skip (read-only; does NOT mutate existing rows)
  burst_skip_window_seconds: 300 # look-back window for skip check (5 minutes)
  burst_skip_threshold: 0.80 # Jaccard threshold to trigger skip

  # Rate limiting
  burst_max_per_note_per_day: 50
  burst_max_per_project_per_day: 500

  # Burst lifecycle
  burst_retention_days: 90 # bursts older than this are swept

  # Background BM25 scorer (async goroutine)
  bm25_min_overlap_to_score: 0.20 # only enrich candidates above this overlap_score
  bm25_scorer_buffer: 512 # scorerCh capacity; drop silently if full

  # Deep connection detection (see Part 12)
  deep_connection_candidate_threshold: 5    # min total candidates to flag pair as deep
  deep_connection_promoted_threshold: 2     # min promoted candidates to flag pair as deep
  deep_connection_score_threshold: 1.5      # min connection_score to flag pair as deep

  # Note metadata inference (see Part 11)
  inference_project_min_fraction: 0.60      # min candidate fraction for project inference
  inference_project_min_candidates: 3       # min candidates before project inference runs
  inference_max_reruns_per_day: 5           # max inference re-runs per note per UTC day
```

These values live in the server configuration file alongside the existing
`server`, `storage`, and `tls` sections.

---

## Part 5 — Promotion: Candidate to Real Link

Promotion is the act of converting a `pending` candidate relation into a stable,
first-class `notx:lnk:id` link that both notes will carry permanently.

### What Promotion Does

When a candidate is promoted (by a user action, a CLI command, or an AI agent
call), the engine performs the following steps atomically:

**Step 1 — Generate human-readable anchor IDs**

For each note in the candidate pair, check whether the burst's line range
already has a named anchor in the `# anchor:` header table. If so, reuse that
anchor's existing ID and skip creation.

Otherwise, derive an anchor ID from the burst text using the **slug algorithm**:

```
1. Lowercase + split the burst's extracted text on non-alphanumeric boundaries.
2. Remove single-character tokens.
3. Remove tokens in the engine's global function-word list:
   { a, an, the, and, or, but, is, are, was, were, be, been,
     for, to, in, on, at, by, of, it, its, this, that, with,
     from, not, do, did, has, have, will, can }
   Note: bursts heavy on acronyms or domain terms (e.g., `JWT`, `SOD`,
   `FTS5`, `API`) will naturally produce short slugs like `jwt-validate`
   or `sod-initialize`. This is acceptable — a short domain slug is still
   meaningful and far preferable to a random hash. Do not try to expand
   or spell out acronyms.
4. Take the first 2–3 remaining tokens (minimum 2, maximum 3).
   Always include the first 2 tokens that survive step 3, even if they
   are slightly common — do NOT apply a second stricter filter.
5. Join with hyphens. Truncate to 40 characters.
6. If the result collides with an existing anchor ID in the note,
   append -2, -3, ... until unique.

Fallback (fewer than 2 tokens survive step 3):
   a. Take the first token of length ≥ 2 found anywhere in the raw text,
      without applying the stop-word filter.
   b. Append a hyphen and the first 6 characters of the hex-encoded
      SHA-256 hash of the first 30 characters of the raw burst text.
   c. Truncate to 40 characters.
   d. Apply collision suffix if needed.
```

**Examples:**

| Burst text (first line)                                       | Generated anchor ID               |
| ------------------------------------------------------------- | --------------------------------- |
| `The SOD process initializes all gateway state`               | `sod-process-initializes`         |
| `Authentication MUST complete before request forwarding`      | `authentication-complete-request` |
| `TODO: fix retry logic in the upstream connector`             | `retry-logic-upstream`            |
| `See RFC 9110 §4.2 for the definition`                        | `rfc-definition`                  |
| `## Sprint 14` (only function words + number after stripping) | `sprint-3f8a1b` (fallback)        |
| `—` (single em-dash, nothing else)                            | `burst-a1b2c3` (fallback)         |

The slug is intentionally short and meaningful. An author reading
`# anchor: retry-logic-upstream` in a `.notx` header immediately understands
what it refers to. An author reading `# anchor: a3f8b2c1d4e5f678` does not.

**Step 2 — Write anchor entries**

Write `# anchor:` lines to the `.notx` file headers of both notes:

```
# anchor: retry-logic-upstream  line:<burst.line_start>  char:0-0
```

**Step 3 — Emit link tokens**

Construct both directions of the link:

```
A→B: notx:lnk:id:urn:notx:note:<note_b_urn>:<anchor_b_id>
B→A: notx:lnk:id:urn:notx:note:<note_a_urn>:<anchor_a_id>
```

**Step 4 — Update `node_links`**

Add the new link tokens to the `node_links` map in both notes. The map key
defaults to the `label` supplied by the reviewer, or the target's anchor ID
if no label was given.

**Step 5 — Update backlink index**

Insert two rows into the `backlinks` table (defined in `NOTX_LINK_SPEC.md`) —
one for each direction.

**Step 6 — Mark candidate promoted**

```sql
UPDATE candidate_relations
SET status='promoted', reviewed_at=?, reviewed_by=?, promoted_link=?
WHERE id=?
```

`promoted_link` stores the A→B `notx:lnk:id` token.

### Partial Promotion

A caller may promote with `direction: a_to_b` or `direction: b_to_a` to create
only one side of the link. Useful when the relationship is asymmetric — e.g.,
note B references a concept defined in note A, but note A does not need a
back-reference.

### Promotion by an AI Agent

The promotion API is designed to be called by an LLM agent. The agent:

1. Fetches pending candidates via
   `GET /v1/context/candidates?status=pending&project_urn=<urn>` — response is
   ordered by `bm25_score DESC, overlap_score DESC` (most confident first, once
   the background scorer has run).
2. For each candidate, reads `burst_a.text` and `burst_b.text`.
3. Reasons about whether the connection is meaningful and optionally proposes a
   `label` for the `node_links` key.
4. Calls `POST /v1/context/candidates/:id/promote` with the label, or
   `POST /v1/context/candidates/:id/dismiss`.

The agent never needs to read full note content — the burst text is exactly
the relevant excerpt, purpose-sized for LLM context windows.

### Anchor Rename After Promotion

Auto-generated slug IDs may not always be perfect. After promotion, a reviewer
or agent may rename an anchor without breaking any links:

```
notx anchor move <note-urn> <auto-slug-id> --rename <better-name>
```

The engine updates the `# anchor:` entry in the file header, updates the
`anchors` SQLite table, and rewrites all `notx:lnk:id` tokens that reference
the old ID (located via the backlink index). All references remain valid.

---

## Part 6 — API Surface

```
# Context bursts
GET  /v1/context/bursts
     ?note_urn=<urn>
     &since_sequence=<seq>
     &page_size=<n>
     → list bursts for a note, ordered by sequence ASC

GET  /v1/context/bursts/:id
     → single burst record with full text and tokens

# Candidate relations
GET  /v1/context/candidates
     ?project_urn=<urn>
     &status=<pending|promoted|dismissed|expired>
     &note_urn=<urn>          (filter to candidates involving this note)
     &min_score=<float>       (filter by overlap_score floor)
     &page_size=<n>
     → paginated list ordered by bm25_score DESC, overlap_score DESC

GET  /v1/context/candidates/:id
     → single candidate with both burst previews embedded

POST /v1/context/candidates/:id/promote
     body: {
       "label":     "optional map key for node_links",
       "direction": "both|a_to_b|b_to_a",
       "reviewer":  "urn:notx:usr:<id>"
     }
     → promotes candidate; creates slug anchors + link tokens; returns
       { anchor_a_id, anchor_b_id, link_a_to_b, link_b_to_a }

POST /v1/context/candidates/:id/dismiss
     body: { "reviewer": "urn:notx:usr:<id>" }
     → marks dismissed, removes from pending queue

# Graph neighbors (confirmed links + pending candidates for a note)
GET  /v1/notes/:urn/neighbors
     ?include=links,candidates,backlinks
     → { confirmed_links: [...], pending_candidates: [...], backlinks: [...] }

# Per-project rate limit configuration
GET /v1/projects/:urn/context-config
    → { burst_max_per_note_per_day: N|null, burst_max_per_project_per_day: N|null }

PUT /v1/projects/:urn/context-config
    body: {
      "burst_max_per_note_per_day":    100,   -- null to reset to global default
      "burst_max_per_project_per_day": 2000   -- null to reset to global default
    }
    → updated config object

# Health and stats
GET  /v1/context/stats
     ?project_urn=<urn>
     → {
         bursts_total: N,
         bursts_today: N,
         candidates_pending: N,
         candidates_pending_unenriched: N,
         candidates_promoted: N,
         candidates_dismissed: N,
         deep_connections_total:        N,
         deep_connections_unreviewed:   N,
         inferences_pending:            N,
         inferences_accepted:           N,
         oldest_pending_age_days: N
       }

# Note metadata inferences
GET  /v1/context/inferences
     ?status=<pending|accepted|rejected>
     &page_size=<n>
     → paginated list ordered by project_confidence DESC, title_confidence DESC

GET  /v1/context/inferences/:id
     → single inference with full project_evidence array

GET  /v1/notes/:urn/inference
     → the active pending inference for this note (404 if none)

POST /v1/context/inferences/:id/accept
     body: {
       "accept_title":   true,
       "accept_project": true,
       "reviewer":       "urn:notx:usr:<id>"
     }
     → applies accepted fields to the note header via AppendEvent;
       marks status='accepted'

POST /v1/context/inferences/:id/reject
     body: { "reviewer": "urn:notx:usr:<id>" }
     → marks status='rejected'; records rejection_token_hash to gate re-runs

# Deep connections
GET  /v1/context/deep-connections
     ?project_urn=<urn>
     &only_unreviewed=true      (default false; filters to is_deep=1 AND pending_count>0)
     &page_size=<n>
     → list of note pairs ordered by connection_score DESC

GET  /v1/context/deep-connections/<note_urn_a>/<note_urn_b>
     → full detail: connection aggregate + all candidates for this pair
       candidates ordered by bm25_score DESC, overlap_score DESC

POST /v1/context/deep-connections/<note_urn_a>/<note_urn_b>/clear-deep
     body: { "reviewer": "urn:notx:usr:<id>" }
     → manually clears is_deep=0 when connection is determined to be noise
```

### Response Shape — Candidate List

```json
{
  "candidates": [
    {
      "id": "019063a5-1f67-7a42-afd3-000000000001",
      "status": "pending",
      "overlap_score": 0.31,
      "bm25_score": 4.21,
      "created_at": "2025-06-01T14:55:00Z",
      "connection_depth": {
        "candidate_count": 7,
        "promoted_count": 2,
        "connection_score": 2.14,
        "is_deep": true
      },
      "burst_a": {
        "id": "019063a5-1f67-7a42-afd3-aaa000000001",
        "note_urn": "urn:notx:note:019063a5-1f67-7a42-afd3-111111111111",
        "note_name": "API Gateway Design",
        "sequence": 7,
        "line_start": 12,
        "line_end": 16,
        "text": "The SOD (Start of Day) process initializes all gateway state\nbefore the first request is accepted.\nAll downstream services must wait for the SOD signal."
      },
      "burst_b": {
        "id": "019063a5-1f67-7a42-afd3-bbb000000002",
        "note_urn": "urn:notx:note:019063a5-1f67-7a42-afd3-222222222222",
        "note_name": "Daily Standup Notes — Sprint 14",
        "sequence": 3,
        "line_start": 4,
        "line_end": 6,
        "text": "SOD jobs failed again on staging — the gateway did not receive\nthe initialization signal before traffic was routed."
      }
    }
  ],
  "next_page_token": "..."
}
```

A `bm25_score` of `0.0` indicates the background scorer has not yet processed
this candidate. Clients and agents may want to filter or deprioritize
`bm25_score=0` candidates when the scorer is known to be running.

---

## Part 7 — CLI Interface

```
# Burst inspection
notx context bursts <note-urn>                       # list all bursts for a note
notx context bursts <note-urn> --since <seq>         # bursts from a given sequence

# Candidate review
notx context candidates                              # pending candidates (all projects)
notx context candidates --project <urn>              # scoped to a project
notx context candidates --note <urn>                 # involving a specific note
notx context candidates --status promoted            # view promoted relations
notx context candidates --min-score 0.25             # filter by overlap_score floor

notx context promote <candidate-id>                  # interactive promotion
notx context promote <candidate-id> --label <key>    # non-interactive
notx context promote <candidate-id> --dir a_to_b     # one-directional
notx context dismiss <candidate-id>                  # dismiss a candidate

# Graph view
notx context neighbors <note-urn>                    # confirmed links + candidates
notx context stats --project <urn>                   # queue health stats

# Per-project rate limit configuration
notx context config get --project <urn>              # show project-level caps (null = global)
notx context config set --project <urn> \
    --note-cap <N> --project-cap <N>                 # set per-project overrides
notx context config reset --project <urn>            # clear overrides (revert to global)

# Maintenance
notx context sweep                                   # trigger burst expiry sweep
notx context rebuild --project <urn>                 # drop and rebuild bursts +
                                                     # candidates from event log

# Deep connections
notx context deep-connections                        # list all deep connection pairs
notx context deep-connections --project <urn>        # scoped to a project
notx context deep-connections <note-a> <note-b>      # inspect a specific pair
notx context deep-connections <note-a> <note-b> \
    --clear-deep                                     # clear the deep flag manually

# Note metadata inference
notx context inferences                              # list pending inferences
notx context inferences --status accepted            # view accepted inferences
notx context inferences accept <id>                  # accept title and/or project
notx context inferences accept <id> --title-only     # accept title only
notx context inferences accept <id> --project-only   # accept project only
notx context inferences reject <id>                  # reject inference
notx notes inference <note-urn>                      # show active inference for a note
```

All commands support `--json` for machine-readable output.

---

## Part 8 — LLM Agent Interaction Model

The contextual graph layer is designed with LLM agents as first-class consumers.
The entire review loop can be driven by an agent using only the HTTP API and the
`notx tool` CLI.

### Recommended Agent Workflow

```
1. Wait for background BM25 scorer to enrich pending candidates.
   Check: GET /v1/context/stats?project_urn=<urn>
   → candidates_pending_unenriched should be near 0 before batch review.

2. Fetch candidates ordered by confidence:
   notx tool project-candidates --project <urn> --status pending --json
   → list ordered by bm25_score DESC, overlap_score DESC

3. For each candidate:
   a. Read burst_a.text and burst_b.text (the relevant excerpts only)
   b. Read burst_a.note_name and burst_b.note_name for framing
   c. Reason: "Is this connection meaningful? What is the relationship?"
   d. If yes → POST /v1/context/candidates/:id/promote
                with a descriptive label (e.g. "sod-initialization-reference")
   e. If no  → POST /v1/context/candidates/:id/dismiss

4. After a promotion batch:
   notx tool links <note-urn> --json
   → verify the new notx:lnk:id links were created with expected anchor IDs

5. If auto-generated anchor IDs need improvement:
   notx anchor move <note-urn> <auto-id> --rename <better-name>
   → engine rewrites all references to the old ID automatically
```

### What the Agent Sees

The agent never needs to:

- Read full note content to evaluate a candidate (burst text is sufficient)
- Understand the `.notx` file format
- Manage anchor IDs manually (engine generates readable slugs on promotion)
- Track which server owns which note (URN resolution handles this)
- Know internal scoring details (queue ordering handles surfacing quality)

The burst text is the atomic unit of agent reasoning. It is purposefully small:
5–10 lines of real content, rich enough to understand meaning, narrow enough to
stay well within context window budgets even when batching 20+ candidates.

### Agent Batching

A typical batch prompt includes 10–20 candidates (with their burst previews)
and asks the agent to classify each as `promote`, `dismiss`, or `defer`. The
`defer` path is implemented by simply not acting on the candidate — it stays
`pending` and will appear in the next batch.

Large-scale periodic runs (e.g., a nightly cron job) can process all pending
candidates in the project and flush the queue, acting as an automatic link
discovery pass.

---

## Part 9 — Relationship to Other Notx Subsystems

### With the Link Spec (`NOTX_LINK_SPEC.md`)

The contextual graph layer is the **discovery feeder** for the link system.
Every `notx:lnk:id` link created via promotion started as a candidate relation.
The anchor table in the `.notx` header is the stable record; the
`context_bursts` and `candidate_relations` tables are the working scratchpad
that feeds it.

| Link spec layer     | Context graph layer role                      |
| ------------------- | --------------------------------------------- |
| `# anchor:` header  | Written on promotion using the slug algorithm |
| `notx:lnk:id` token | Created on promotion, stored in note content  |
| `backlinks` table   | Updated on promotion                          |
| `node_links` map    | Updated on promotion                          |
| Break detection     | Independent; runs on all edits regardless     |

### With the Event Log

The event log is the source of truth. The context graph layer reads events but
never modifies them. If `context_bursts` or `candidate_relations` are dropped,
the engine rebuilds them by replaying the event log via `notx context rebuild`.
This is identical in principle to how the FTS5 index is rebuilt on a schema
version bump.

### With the FTS5 Index

| Index                | Scope                              | Best for                                |
| -------------------- | ---------------------------------- | --------------------------------------- |
| `notes_fts`          | Full note content (latest state)   | "Find notes that mention X anywhere"    |
| `context_bursts_fts` | Changed regions with event context | "Find specific edits that introduced X" |

An agent can combine both: use `notes_fts` to identify relevant notes, then use
`context_bursts_fts` to find the specific edit events that introduced the topic.

### With Federation and Cross-Server Notes

Burst extraction and candidate detection run only on locally-owned notes (notes
where `authority == this server's URN`). For notes resolved from remote servers,
no bursts are generated locally. This keeps the candidate queue scoped to
content the local server is authoritative over.

Cross-server candidate detection is a future concern outside the scope of this
specification.

---

## Part 10 — Implementation Checklist

### Core Engine (`core/context.go` — new file)

- [ ] `GroupAffectedLines(entries []LineEntry, windowLines int) []LineRange`
      Groups affected line numbers into contiguous ranges with context padding
- [ ] `SplitRange(r LineRange, chunkSize int, overlap int) []LineRange`
      Splits an oversized range into overlapping sub-windows
- [ ] `SimilaritySkip(existing, candidate []string, threshold float64) bool`
      Pure Jaccard check used for the consecutive skip decision (Step 6)
- [ ] `ExtractBursts(note *core.Note, event *core.Event, cfg BurstConfig) []Burst`
      Orchestrates Steps 1–9: rate check, grouping, splitting, skip, trivial
      filter, tokenization, enrichment
- [ ] `TokenizeBurst(text string) []string`
      Reuses `tokenise` from `repo/index/index.go`
- [ ] `JaccardScore(a, b []string) float64`
      In-memory set intersection / union
- [ ] `SlugFromBurst(text string) string`
      Implements the slug algorithm; max 40 chars; fallback to hash suffix
- [ ] `DetectCandidates(newBurst Burst, recent []Burst, threshold float64) []CandidatePair`
      Single-stage raw Jaccard filter returning pairs with score

### SQLite Schema

- [ ] Append v3 migration to `migrations` slice in `repo/sqlite/schema.go`
- [ ] Update `ddl` constant to include `context_bursts`, `context_bursts_fts`,
      and `candidate_relations` (for fresh installs)
- [ ] Bump `currentSchemaVersion` to 3

### Write Path (`repo/sqlite/provider.go`)

- [ ] Rate limit check before burst extraction (two COUNT queries)
- [ ] Burst extraction call after FTS update, inside the write goroutine
- [ ] Consecutive similarity skip check (one SELECT, no writes to existing rows)
- [ ] In-memory Jaccard scoring (Go, N ≤ 100 recent bursts)
- [ ] Insert new burst rows and candidate rows with `bm25_score=0`
- [ ] Send newly inserted candidate IDs to `scorerCh` (non-blocking send)
- [ ] Graceful degradation: log WARN on any error, do not propagate to caller
- [ ] Load `ContextGraphConfig` from server config struct

### Background BM25 Scorer (`repo/sqlite/scorer.go` — new file)

- [ ] `StartScorer(ctx, db, writeCh, scorerCh chan string, cfg ScoreConfig)`
      Goroutine started at server startup; reads IDs from `scorerCh`
- [ ] For each ID: fetch `overlap_score`; skip if below `bm25_min_overlap`
- [ ] Run BM25 FTS5 query against `context_bursts_fts` for the candidate pair
- [ ] Update `candidate_relations.bm25_score` via the existing write channel
- [ ] On channel close (server shutdown), drain and flush remaining items

### Repository Interface (`repo/repo.go`)

- [ ] Define `ContextRepository` interface:
  - `BurstCountToday(ctx, noteURN, projectURN string) (noteCount, projectCount int, error)`
  - `MostRecentBurst(ctx, noteURN string) (Burst, bool, error)`
  - `RecentBurstsInProject(ctx, projectURN string, days, limit int) ([]Burst, error)`
  - `StoreBurst(ctx, Burst) error`
  - `ListBursts(ctx, noteURN string, sinceSeq, pageSize int) ([]Burst, string, error)`
  - `GetBurst(ctx, id string) (Burst, error)`
  - `SweepBursts(ctx, olderThan time.Time) (int, error)`
  - `StoreCandidates(ctx, []CandidateRelation) error`
  - `UpdateCandidateBM25(ctx, id string, score float64) error`
  - `ListCandidates(ctx, CandidateListOptions) ([]CandidateRelation, string, error)`
  - `GetCandidate(ctx, id string) (CandidateRelation, error)`
  - `PromoteCandidate(ctx, id string, opts PromoteOptions) (PromoteResult, error)`
  - `DismissCandidate(ctx, id, reviewerURN string) error`
  - `RebuildBursts(ctx, projectURN string) error`
- [ ] Implement on `*Provider` in `repo/sqlite/`

### HTTP API (`http/context.go` — new file)

- [ ] `GET  /v1/context/bursts` handler
- [ ] `GET  /v1/context/bursts/:id` handler
- [ ] `GET  /v1/context/candidates` handler
- [ ] `GET  /v1/context/candidates/:id` handler
- [ ] `POST /v1/context/candidates/:id/promote` handler
- [ ] `POST /v1/context/candidates/:id/dismiss` handler
- [ ] `GET  /v1/notes/:urn/neighbors` handler (extend `http/note.go`)
- [ ] `GET  /v1/context/stats` handler
- [ ] `GET  /v1/projects/:urn/context-config` handler (extend `http/project.go`)
- [ ] `PUT  /v1/projects/:urn/context-config` handler
- [ ] Register all new routes in `http/handler.go`

### CLI

- [ ] `notx context bursts` with `--since`
- [ ] `notx context candidates` with `--status`, `--project`, `--note`, `--min-score`
- [ ] `notx context promote` with `--label`, `--dir`
- [ ] `notx context dismiss`
- [ ] `notx context neighbors`
- [ ] `notx context stats`
- [ ] `notx context config get/set/reset` subcommands
- [ ] `notx context sweep`
- [ ] `notx context rebuild`
- [ ] `--json` flag on all context subcommands

### Maintenance

- [ ] Background sweep goroutine in server startup (default interval: every 24h)
- [ ] Background reconciliation job: every hour, re-enqueue unenriched `pending`
      candidates (where `bm25_score=0` and `created_at < NOW() - 5 minutes`)
      into `scorerCh` (non-blocking)
- [ ] Scorer goroutine restart loop with 5-second backoff on unhandled error
- [ ] `notx context rebuild` drops and rebuilds `context_bursts` and
      `candidate_relations` for a project by replaying the event log

### Note Metadata Inference (`repo/sqlite/inference.go` — new file)

- [ ] `QueueInference(ctx, noteURN string) error`
      Called by write path when a note's first burst is stored and title/project is missing
- [ ] `RunTitleInference(ctx, noteURN string) (title string, confidence float64, burstID string, error)`
      Reads first non-blank content line, applies slug algorithm, scores confidence
- [ ] `RunProjectInference(ctx, noteURN string) (projectURN string, confidence float64, evidence []ProjectEvidence, error)`
      Aggregates candidate partner `project_urn` distribution; applies min thresholds
- [ ] `StoreInference(ctx, Inference) error`
      Upserts (replaces pending) inference row; enforces unique pending index
- [ ] `AcceptInference(ctx, id string, acceptTitle, acceptProject bool, reviewerURN string) error`
      Emits AppendEvent to write `# title:` / `# project:` to note header; marks accepted
- [ ] `RejectInference(ctx, id, reviewerURN string) error`
      Computes rejection token hash; marks rejected
- [ ] `ShouldReEnableInference(ctx, noteURN string, newBurstTokens []string) bool`
      Reads rejected token hash; returns true if Jaccard < 0.50
- [ ] `ListInferences(ctx, status string, pageSize int, pageToken string) ([]Inference, string, error)`
- [ ] `GetInference(ctx, id string) (Inference, error)`

### Deep Connection Tracking (`repo/sqlite/pairs.go` — new file)

- [ ] `UpsertNotePair(ctx, noteURN_A, noteURN_B, projectURN string, candidate CandidateRelation) error`
      Called after every candidate insert: upserts note_pair_connections, recalculates
      connection_score, sets is_deep if threshold crossed
- [ ] `RecalcPairScore(ctx, noteURN_A, noteURN_B string) error`
      Called after every promote/dismiss: queries all non-dismissed candidates for the
      pair, recalculates connection_score, updates counts, re-evaluates is_deep
- [ ] `ListDeepConnections(ctx, projectURN string, onlyUnreviewed bool, pageSize int, pageToken string) ([]NotePairConnection, string, error)`
- [ ] `GetNotePair(ctx, noteURN_A, noteURN_B string) (NotePairConnection, error)`
- [ ] `ClearDeep(ctx, noteURN_A, noteURN_B, reviewerURN string) error`

### HTTP API additions

- [ ] `GET  /v1/context/inferences` handler
- [ ] `GET  /v1/context/inferences/:id` handler
- [ ] `GET  /v1/notes/:urn/inference` handler
- [ ] `POST /v1/context/inferences/:id/accept` handler
- [ ] `POST /v1/context/inferences/:id/reject` handler
- [ ] `GET  /v1/context/deep-connections` handler
- [ ] `GET  /v1/context/deep-connections/:urn_a/:urn_b` handler
- [ ] `POST /v1/context/deep-connections/:urn_a/:urn_b/clear-deep` handler
- [ ] Update `GET /v1/context/stats` to include new inference and deep connection counts
- [ ] Update `GET /v1/context/candidates` response shape to include `connection_depth`

---

## Part 11 — Note Metadata Inference

### The Problem

Notes appended without a title or project URN are harder to surface and reason
about. A project-less note is scoped globally for candidate detection — it
competes with every other project-less note on the server, diluting the signal.
An untitled note forces reviewers to open full content to understand the subject.

The engine can derive both fields from the note's own burst content and from
the candidate relations it accumulates over time. Neither inference is mandatory:
they are advisory signals that a user or AI agent reviews and explicitly accepts.

---

### Title Inference

Title inference runs asynchronously in the same background goroutine as the
metadata scorer, triggered when the note's first burst is stored and
`note.Title == ""`.

**Algorithm**

1. Read the first non-blank line of the note's post-event materialized content.
2. Apply the slug algorithm (Part 5) to produce a token list. Strip stop words;
   take the first 2–4 remaining tokens. Join with spaces (not hyphens) and
   capitalize each word to form a human-readable title candidate.
3. Score confidence: `title_confidence = min(1.0, significant_token_count / 5.0)`
   where `significant_token_count` is the token count after stop-word removal.
4. If fewer than 2 significant tokens survive step 2, scan the next 5 non-blank
   lines and retry. If still fewer than 2 tokens, store the inference with
   `title_confidence < 0.4` flagged as low confidence. It will still be surfaced
   for human review but deprioritized in the queue.

**Examples**

| First content line                              | Inferred title             | Confidence |
| ----------------------------------------------- | -------------------------- | ---------- |
| `The SOD process initializes all gateway state` | `SOD Process Gateway`      | 0.80       |
| `## Sprint 14 Planning`                         | `Sprint Planning`          | 0.60       |
| `TODO: fix retry logic`                         | `Retry Logic`              | 0.60       |
| `---` (horizontal rule only)                    | _(scans next 5 lines)_     | —          |
| `a` (single character)                          | _(low confidence, stored)_ | 0.20       |

---

### Project Inference

Project inference runs after candidate detection, once the note has accumulated
at least `inference_project_min_candidates` (default: 3) candidates.

**Algorithm**

1. Query all candidates for this note's bursts.
2. Group partner bursts by their `project_urn`. Exclude candidates whose
   partner `project_urn` is empty — project-less partners contribute no signal.
3. Compute `fraction = count(candidates for project X) / total_candidates`.
4. If the top project's `fraction ≥ inference_project_min_fraction` (default:
   0.60) AND its candidate count ≥ `inference_project_min_candidates` (default:
   3), infer that project:
   - `inferred_project_urn = top_project_urn`
   - `project_confidence = fraction × min(1.0, candidate_count / 5.0)`
5. Store the full evidence array as JSON in `project_evidence` for auditing:

```json
[
  {
    "project_urn": "urn:notx:proj:alpha",
    "candidate_count": 7,
    "fraction": 0.78
  },
  {
    "project_urn": "urn:notx:proj:beta",
    "candidate_count": 2,
    "fraction": 0.22
  }
]
```

With `inference_project_min_fraction=0.60` and `inference_project_min_candidates=3`,
project `alpha` qualifies; project `beta` does not.

---

### When Inference Runs

Inference is a background operation. It never touches the hot write path.

| Trigger                                                                         | Action                                                        |
| ------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Note's first burst stored AND `title == ""` or `project_urn == ""`              | Queue for title inference immediately after write transaction |
| New candidate detected for a project-less note, crossing the min threshold      | Re-run project inference                                      |
| New burst for a note with a `rejected` inference has Jaccard < 0.50 vs rejected | Re-enable inference (content changed substantially); re-queue |

Re-runs are capped at `inference_max_reruns_per_day` (default: 5) per note per
UTC day. Beyond the cap, inference is silently skipped until the next day.

Inference does **not** run for:

- Notes that already have both `title` and `project_urn` set.
- Notes authored by `urn:notx:usr:anon`.
- Notes that are auto-generated sequence entries (snapshot compaction events).

---

### Lifecycle

```
pending ──► accepted ──► (AppendEvent written: sets # title: and/or # project: in header)
   │
   └──► rejected ──► (blocked until burst Jaccard vs rejected tokens < 0.50)
```

**Acceptance** emits a real `AppendEvent` that writes the inferred metadata to
the note's `.notx` header. This event enters the event log like any other, making
the metadata change auditable and rebuildable.

**Rejection** records the SHA-256 hash of the note's current burst token set
in `rejected_token_hash`. If a future burst for this note yields Jaccard
similarity < 0.50 against the rejected token hash, inference is re-enabled
automatically — the content diverged enough to warrant a fresh look.

---

### Review Queue Integration

Pending inferences are a separate queue from candidates. The `CheckQueue`
operation (Operation 1 in `NOTX_CONTEXT_CLIENT.md`) reports
`inferences_pending` in the stats response so sessions can also drain the
inference queue.

The `FetchBatch` operation does not include inferences — they are a distinct
concern reviewed via `InspectInferences` (Operation 9).

---

## Part 12 — Deep Connection Detection

### Definition

A **deep connection** is a note pair where the accumulated number and quality
of candidate relations signal a persistent, multi-faceted conceptual overlap.
Not one passing shared phrase — a structural co-dependency or ongoing shared
subject that has produced signal across multiple independent editing sessions.

A shallow connection: burst A and burst B share 40% of tokens once, in a single
edit event.

A deep connection: notes A and B have accumulated 7 candidates across 4
editing sessions, 3 of which have been promoted. Every time either note is
edited, new overlapping content appears.

---

### The note_pair_connections Table

After each candidate insert (or status change on promote/dismiss), the engine
upserts a row in `note_pair_connections`. Canonical ordering is enforced by the
caller: `note_urn_a` is always the lexicographically smaller of the two URNs.

The upsert recalculates `connection_score` in full each time (not incrementally),
because the decay formula depends on wall-clock time and could drift if stored
incrementally.

---

### Connection Score Formula

```
connection_score = Σ effective_score(c)
  for c in all_candidates(note_a, note_b)
  where c.status != 'dismissed'

effective_score(c) =
  max(c.bm25_score, c.overlap_score)      -- best available confidence signal
  × promotion_weight(c.status)            -- confirmed links count more
  × exp(−0.05 × age_days(c.created_at))  -- 14-day half-life decay

  promotion_weight('promoted') = 2.0
  promotion_weight('pending')  = 1.0
```

The score is unbounded and grows with each qualifying candidate. The exponential
decay means old, silent note pairs naturally lose score relative to actively
evolving ones. Dismissed candidates contribute 0 — noise is not rewarded.

**Example:** Two notes with 3 promoted candidates from 10 days ago and 2 pending
candidates from yesterday:

```
3 × (avg_overlap=0.35 × 2.0 × exp(-0.05×10)) = 3 × 0.35 × 2.0 × 0.607 = 1.27
2 × (avg_overlap=0.28 × 1.0 × exp(-0.05×1))  = 2 × 0.28 × 1.0 × 0.951 = 0.53
connection_score ≈ 1.80
```

With `deep_connection_score_threshold=1.5`, this pair is deep.

---

### Deep Connection Threshold

A note pair is flagged `is_deep = 1` on the next upsert when **any** of the
following conditions is met:

| Condition                                 | Default | Config key                            |
| ----------------------------------------- | ------- | ------------------------------------- |
| `candidate_count >= threshold_candidates` | 5       | `deep_connection_candidate_threshold` |
| `promoted_count >= threshold_promoted`    | 2       | `deep_connection_promoted_threshold`  |
| `connection_score >= threshold_score`     | 1.5     | `deep_connection_score_threshold`     |

Once set, `is_deep` is never cleared automatically. Reviewers or agents may
explicitly clear it via `POST .../clear-deep` when a pair turns out to be noise.

`deep_flagged_at` records the timestamp when `is_deep` was first set to 1.

---

### Review Queue Integration

Every candidate response in `ListCandidates` includes a `connection_depth` field
showing the current state of its note pair:

```json
{
  "connection_depth": {
    "candidate_count": 7,
    "promoted_count": 2,
    "connection_score": 2.14,
    "is_deep": true
  }
}
```

This gives reviewers immediate context: a candidate from a deeply connected pair
carries stronger prior toward promotion, even if its individual burst text is
ambiguous.

**Stats:** `GET /v1/context/stats` exposes:

| Field                         | Meaning                                             |
| ----------------------------- | --------------------------------------------------- |
| `deep_connections_total`      | Total note pairs with `is_deep=1`                   |
| `deep_connections_unreviewed` | Deep pairs where `pending_count > 0` (needs review) |

An "unreviewed" deep connection is one where `is_deep=1` AND `pending_count>0`.

---

### Scoring on Status Change

When a candidate is promoted or dismissed, the engine:

1. Recalculates `effective_score` for all non-dismissed candidates in the pair.
2. Updates `connection_score`, `pending_count`, `promoted_count`, `dismissed_count`.
3. Re-evaluates the three deep connection conditions. If newly satisfied, sets
   `is_deep=1` and `deep_flagged_at` (if not already set).

This keeps `note_pair_connections` accurate as reviewers drain the queue.

---

### Design Decisions

| Decision                                         | Rationale                                                                                                                                                                                                                            |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Recalculate score on every upsert**            | Storing an incremental score that depends on `exp(-λ × age_days)` would drift silently over time as days pass without new candidates. A full recalculation is cheap (typically < 10 candidates per pair) and always accurate.        |
| **Canonical URN ordering (`a < b`)**             | A single primary key per pair avoids duplicate rows and simplifies deduplication. The caller enforces ordering before the upsert. No trigger or unique constraint on an unordered pair is needed.                                    |
| **`is_deep` is never auto-cleared**              | A pair that earned deep status (via promotions or high candidate volume) remains notable even if the queue drains. An explicit human or agent action to clear it is the right signal that it was re-evaluated and found to be noise. |
| **`connection_depth` embedded in candidate API** | Reviewers should not need a separate API call to know they are looking at a candidate from a deeply connected pair. Embedding it in the candidate list response keeps the review loop self-contained.                                |

---

## Quick Reference

```
# The question the contextual graph layer answers:
"What content changed recently that might be related to what just changed here?"

# The unit of currency:
Context Burst — post-event materialized text for one contiguous changed region
                (±2 lines of context). One event may produce multiple bursts.
                Burst rows are immutable after insertion.

# The unit of output:
Candidate Relation — a Jaccard-scored, reviewable pair of bursts from different
                     notes in the same project.
                     overlap_score = raw Jaccard (set on insert, never changes).
                     bm25_score    = FTS5 confidence (async, used for ordering).

# The unit of outcome:
Promoted Link — a notx:lnk:id token + human-readable slug anchor declarations
                in both notes, created when a candidate is confirmed.

# The performance contract:
< 7 ms added to AppendEvent on the hot write path.
BM25 enrichment is async (background goroutine, seconds after insert).

# The safety contract:
Contextual layer failure never fails a write. It is advisory infrastructure.
Existing burst rows are never mutated after insertion.

# The format contract:
Zero modifications to the .notx event stream or file format.
All artifacts live in two SQLite tables, fully rebuildable from the event log.

# The new inference question:
"This note has no title or project. Based on its content and burst connections,
 what should it be called and where does it belong?"

# The inference unit:
Note Metadata Inference — async suggestion of title and/or project_urn for a
                          note created without them. Derived from first burst
                          text (title) and candidate project_urn distribution
                          (project). Reviewed and accepted by user or agent.

# The deep connection question:
"These two notes have generated N candidates across M editing sessions.
 Are they structurally related — co-authored, interdependent, or covering
 the same evolving subject?"

# The deep connection unit:
note_pair_connections — materialized aggregate of all candidates between two notes.
                        connection_score = weighted sum of overlap scores with
                        14-day half-life decay. is_deep=1 when any threshold crossed.
                        Surfaces in review queue via connection_depth field on
                        each candidate.
```

---

## Summary of Design Decisions

| Decision                                                 | Rationale                                                                                                                                                                                                                                                                                                                                                                   |
| -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Skip instead of merge for consecutive similar bursts** | Merging requires updating an existing row inside the write transaction — creating mutation of supposedly immutable records, complex rollback scenarios, and reasoning burden. A read → skip decision produces the same practical outcome (no flood of near-identical bursts) with zero writes to existing data and no transaction complexity.                               |
| **Single-stage raw Jaccard on the hot path**             | Two-stage filtering with stop-phrase loading adds per-project DB queries and re-scoring work on every event. The global stop-word list already strips function words during tokenization; additional per-project filtering is premature until noise is proven to be a real problem at scale. When it is, a `project_stop_phrases` table can be added as a future migration. |
| **BM25 scoring in a background goroutine**               | A BM25 FTS5 query per candidate pair is 1–3 ms per candidate. With N=100 lookback and multiple passing pairs per event, this would blow the 10 ms budget. Moving it to a background goroutine keeps the write path fast, provides the ordering benefit within seconds, and degrades gracefully if the scorer is behind.                                                     |
| **Slug algorithm: first 2-3 non-function-word tokens**   | A slug built from the first meaningful words in the burst text is self-documenting in `.notx` headers and `notx:lnk:id` tokens that authors read. The fallback to `word-hash8chars` handles degenerate bursts without producing pure random strings. The 40-character max keeps slugs concise in file headers.                                                              |
| **Split large events into multiple bursts**              | Truncating to the first N lines of a large event silently discards entire sections of changed content. Splitting preserves every changed region as an independent candidate surface. The `truncated` flag marks the rare extreme case (> 10 sub-windows from one event) as an operational anomaly rather than hiding it.                                                    |
| **Burst rate limiting with per-project overrides**       | Global daily caps protect the system from automated write floods. Per-project overrides in `project_context_config` let power users and automated workflows raise those caps without affecting the whole server. The check is a PK lookup and adds < 0.2 ms to the hot path.                                                                                                |
| **Store `-bm25()` not `bm25()`**                         | SQLite's `bm25()` function returns negative values where more negative = better match. Storing the raw value would cause unenriched candidates (`bm25_score=0`) to sort above all enriched ones with `ORDER BY bm25_score DESC`. Negating before storage makes `0.0` the lowest possible score, so unenriched candidates naturally fall to the bottom of the review queue.  |
| **Overflow reconciliation for the BM25 scorer**          | When `scorerCh` overflows, dropped IDs stay unenriched indefinitely. A lightweight hourly reconciliation job re-enqueues them without touching the hot write path. This makes the scorer eventually consistent even under burst traffic, without requiring persistent queuing infrastructure.                                                                               |
| **Candidates between bursts, not notes**                 | Two long notes may share many independent connection points across different sections. Coalescing candidates to the note level loses the specificity needed for precise anchor creation at promotion time — a note-level candidate cannot tell you _where_ in the note the anchor should go.                                                                                |
| **Rebuild from event log**                               | The event log is the source of truth. All context graph artifacts are derived indices. Making them rebuildable means the schema and scoring algorithm can evolve without data loss — just run `notx context rebuild`.                                                                                                                                                       |
| **Advisory degradation on write**                        | The write path's contract is event durability. Coupling a contextual indexing failure to a write failure would turn a low-priority background concern into a user-visible data loss risk.                                                                                                                                                                                   |
