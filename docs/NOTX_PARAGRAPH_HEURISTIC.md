# Paragraph Role Heuristic System

## Overview

The **Paragraph Role System** is a background intelligence layer that
automatically structures every note into annotated semantic units and discovers
scored relationships between them.

It answers a fundamentally different question than the rest of the engine:

> **Context Graph / Bursts** — "Which *notes* are related to each other?"
> *(used for cross-note search and link suggestion)*
>
> **Paragraph Role System** — "What does this *paragraph mean* and how does
> it connect to other ideas inside and across notes?"
> *(used for deep content understanding)*

The system operates entirely in the background. Users never manage paragraph
structure directly — they only optionally react to suggested connections via
thumbs up / thumbs down. All learning happens silently.

---

## How It Differs from the Context Graph

| Dimension | Context Graph (Bursts) | Paragraph Role System |
|---|---|---|
| **Unit of analysis** | Contiguous changed-line window | Semantic paragraph |
| **Scope** | Cross-note, project-scoped | **Global** — all notes, all projects |
| **Purpose** | Surface *which notes* are related | Understand *what paragraphs mean* |
| **Trigger** | On every `AppendEvent` | Background poll (30 s default) |
| **User action** | Promote / Dismiss | Thumbs up / Thumbs down |
| **Learning** | None | Anonymous pattern hash table |
| **Rebuild** | Incremental only | Full rebuild in one call |

---

## Core Concepts

### 1. Paragraph

A **paragraph** is a block of contiguous non-empty lines separated from its
neighbours by at least one blank line. Each paragraph is:

- Split from note content at processing time.
- Assigned a **role** that describes its rhetorical function.
- Annotated with **normalized concepts** extracted from its text.
- Stored with its source `note_urn`, `project_urn`, and `folder_urn` so future
  folder-level and project-level queries are possible without re-scanning notes.

### 2. Paragraph Role

The role classifies *what a paragraph is doing*, not what it is about.

| Role | Meaning | Trigger heuristics |
|---|---|---|
| `definition` | Declares what something is | "X is…", "X is defined as…", "X refers to…", "X means…" |
| `example` | Illustrates a concept concretely | "For example…", "For instance…", "Such as…", "e.g." |
| `contrast` | Introduces an opposition or exception | Starts with "However", "But", "Unlike", "Whereas", "In contrast" |
| `cause_effect` | Explains causation or consequence | "because…", "therefore…", "as a result…", "this leads to…", "thus…" |
| `question` | Poses a question or open problem | Contains "?", or starts with "What", "How", "Why", "When", "Where" |
| `claim` | General assertion or explanatory statement | **Default** — applied when no other pattern matches |

Role detection is performed by a priority-ordered list of regular expressions.
`question` is checked before `definition` to correctly handle "What is X?".
`claim` is the catch-all fallback.

### 3. Relation Type

A **relation** is a directed semantic edge from one paragraph to another. Six
relation types are supported:

| Type | Meaning | Typical signal |
|---|---|---|
| `elaborates` | Target expands on source | "furthermore", "in addition", "moreover" |
| `supports` | Target provides evidence for source | "supports", "demonstrates that", "proves that" |
| `contrasts_with` | Target opposes or qualifies source | "however", "but", "unlike", "whereas" |
| `answers` | Target responds to a question in source | Source is `question` role; target is `claim` |
| `causes` | Source leads to target | "because", "therefore", "as a result", "thus" |
| `illustrates` | Target gives a concrete example of source | "for example", "for instance", "such as" |

When a **lexical cue** is present at the start of the target paragraph, it
determines the relation type directly. When no cue is present, a default
relation type is inferred from the source/target role pair.

### 4. Proximity Tier

Every relation carries a **proximity tier** that records where the two
paragraphs live relative to each other in the document hierarchy at scoring
time:

| Tier | Condition |
|---|---|
| `same_doc` | Both paragraphs are in the same note |
| `same_folder` | Different notes, same folder |
| `same_project` | Different notes, same project, different folders |
| `global` | Different notes, different projects |

The tier is a scoring input (closer = higher base score) *and* a learning
input (feedback can independently tune how valuable each tier is).

---

## Concept Extraction and Normalization

### Why Normalize?

Text matching for concepts is already done by the full-text search (FTS5) layer.
The paragraph system needs a different kind of signal: *conceptual overlap*
between two paragraphs. Using raw text would couple the overlap score to
surface variation ("mammal" vs "mammals" vs "Mammal"). Normalization removes
that surface noise and makes overlap scores stable and consistent.

> **Design principle:** it is better to be *consistently wrong* in the same
> direction than to be inconsistently right. Systematic normalization creates
> valid indirect relationships even when the normalizer is imperfect, because
> the same bias applies equally to both paragraphs being compared.

### Normalization Steps

Applied by `core.NormalizeConcept(s string) string`:

1. **Lowercase** — "Lions" → "lions"
2. **Strip non-alphanumeric characters** — "mammal," → "mammal"
3. **Trim whitespace**
4. **Reject stop words** — a, an, the, is, are, was, were, be, been, have,
   has, had, do, does, did, will, would, could, should, may, might, in, on,
   at, to, for, of, with, by, from, this, that, it, its, i, we, you, he, she,
   they, their, our, your, as, if, so, then, when, which, who, what, how, why,
   not, no … (full list in `core/paragraph.go`)
5. **Reject single-character tokens**

### Concept Tiers

Extracted concepts are split into three tiers based on frequency and seed
membership:

| Tier | Rule | Example |
|---|---|---|
| `main_concepts` | Appears ≥ 2 times in the paragraph **or** is in the concept family seed | `lion`, `mammal` |
| `supporting_concepts` | Appears exactly once, not in seed | `carnivorous`, `apex`, `predators` |
| `concept_families` | Derived from seed — the named category of a main concept | `learning`, `cognition` |

The **concept family seed** (`core/paragraph.go`) is a hand-maintained map
from normalized tokens to family names. It grows over time:

```
"learning"    → "learning"
"memory"      → "learning"
"cognition"   → "cognition"
"schema"      → "cognition"
"algorithm"   → "software"
"system"      → "software"
```

Only `main_concepts` are used in the overlap scoring signal.

---

## Scoring Model

Every candidate pair `(a, b)` is scored by a weighted sum of five signals:

```
score =
  w_proximity_tier * proximityTierScore(a, b) +
  w_role_pair      * rolePairScore(a.role, b.role) +
  w_overlap        * conceptOverlapScore(a.mainConcepts, b.mainConcepts) +
  w_cue            * cueScore(b.text) +
  w_pattern        * patternSignal(patternHash(a, b))
```

The final score is clamped to `[0, 1]`.

### Signal 1 — Proximity Tier Score

```
proximityTierScore(a, b) = tierMultiplier(tier) × distanceScale(a, b)
```

**Tier multipliers** (initial values, adjustable via feedback):

| Tier | Default multiplier |
|---|---|
| `same_doc` | 1.00 |
| `same_folder` | 0.75 |
| `same_project` | 0.50 |
| `global` | 0.25 |

For `same_doc` only, the multiplier is further scaled by **paragraph distance**:

| Positions apart | Distance scale |
|---|---|
| 1 (adjacent) | 1.00 |
| 2 | 0.70 |
| 3 | 0.40 |
| > 3 | 0.10 |

For cross-note tiers (`same_folder`, `same_project`, `global`) the distance
scale is always 1.0 — physical position in different documents has no meaning.

> **Why separate multipliers from the dimension weight?**
> `w_proximity_tier` controls how much *proximity in general* matters.
> The four multipliers control the *relative value* of each tier within
> that dimension. Feedback can nudge them independently: e.g. you might
> learn that `same_folder` relations are reliably useful while `global`
> ones rarely are, without changing the overall importance of proximity.

### Signal 2 — Role Pair Score

A lookup table scores how well two roles relate rhetorically:

| Source role | Target role | Score |
|---|---|---|
| `definition` | `example` | 0.90 |
| `question` | `claim` | 0.90 |
| `question` | `example` | 0.85 |
| `contrast` | `contrast` | 0.85 |
| `cause_effect` | `claim` | 0.80 |
| `cause_effect` | `example` | 0.75 |
| `claim` | `example` | 0.75 |
| `definition` | `claim` | 0.70 |
| `claim` | `definition` | 0.65 |
| `claim` | `contrast` | 0.60 |
| `example` | `claim` | 0.55 |
| `contrast` | `claim` | 0.55 |
| `claim` | `cause_effect` | 0.55 |
| `claim` | `claim` | 0.45 |
| `example` | `example` | 0.40 |
| *(any unlisted pair)* | | 0.30 |

### Signal 3 — Concept Overlap Score

Jaccard similarity over the **normalized** `main_concepts` sets of `a` and `b`:

```
overlap = |conceptsA ∩ conceptsB| / |conceptsA ∪ conceptsB|
```

Because both sets are pre-normalized at extraction time, this comparison
never touches raw text and is stable across note edits that don't change
meaning.

### Signal 4 — Cue Score

Binary: `1.0` if the target paragraph's text contains a recognized lexical
cue phrase (see Relation Types above), `0.0` otherwise.

### Signal 5 — Pattern Signal

Looks up the anonymous `pattern_hash` in the pattern scores table and returns
`net_score` mapped from `[-1, 1]` to `[0, 1]`:

```
patternSignal = (net_score + 1) / 2
```

- `net_score = 0.0` (no feedback yet) → `patternSignal = 0.50` (neutral)
- `net_score = 1.0` (all thumbs up) → `patternSignal = 1.00` (full boost)
- `net_score = -1.0` (all thumbs down) → `patternSignal = 0.00` (penalty)

When the hash is not yet in the table, `0` is returned (neutral, does not
affect the score).

### Default Weights

```
w_proximity_tier  = 0.20
w_role_pair       = 0.25
w_overlap         = 0.20
w_cue             = 0.20
w_pattern         = 0.15
```

Weights adjust over time via the feedback loop (see below) and are stored
globally in a singleton `paragraph_weights` row.

### Candidate Selection

For each paragraph, the scorer considers the **±`SameDocWindowSize` adjacent
paragraphs** within the same note (default: 3). Each candidate pair is scored
and only the **top N** (default: 3) above the **minimum score threshold**
(default: 0.55) are kept per source paragraph.

---

## Pattern Hash and Anonymous Feedback

### The Problem with Named Feedback

Storing "user X liked relation between paragraph P and paragraph Q" creates
privacy exposure and does not generalize. If we stored content or IDs, the
system could never learn that "definition → example" is a universally useful
pattern — it would only record that *this specific pair* was useful.

### The Solution: Structural Fingerprinting

Every relation is tagged with a `pattern_hash` — a deterministic 16-hex-char
fingerprint derived **only from structural dimensions**:

```
PatternHash = SHA-256( roleA:roleB:relationType:proximityTier:cuePresent )[:16]
```

**What is encoded:**
- The role of the source paragraph (e.g. `definition`)
- The role of the target paragraph (e.g. `example`)
- The relation type (e.g. `illustrates`)
- The proximity tier (e.g. `same_doc`)
- Whether a lexical cue was present (`0` or `1`)

**What is never encoded:**
- Paragraph text
- Note, folder, or project identifiers
- User or device identifiers
- Timestamps

### Pattern Score Table

```sql
paragraph_pattern_scores (
    pattern_hash   TEXT PRIMARY KEY,  -- 16 hex chars
    role_a         TEXT,              -- debug info only
    role_b         TEXT,              -- debug info only
    relation_type  TEXT,              -- debug info only
    proximity_tier TEXT,              -- debug info only
    cue_present    INTEGER,           -- debug info only
    up_count       INTEGER,           -- accumulated thumbs up
    down_count     INTEGER,           -- accumulated thumbs down
    net_score      REAL               -- (up-down)/(up+down+2), clamped [-1,1]
)
```

The `role_*`, `relation_type`, `proximity_tier`, and `cue_present` columns
are stored for **debugging only** — the system never uses them to reconstruct
content. Only `pattern_hash` and `net_score` are used at scoring time.

### Net Score Formula

```
net_score = (up_count - down_count) / (up_count + down_count + smoothing)
```

The smoothing constant is **2** (Laplace / add-one smoothing). This prevents
a single early vote from pushing the score to ±1 before enough data exists.

---

## Feedback Loop

When a user votes thumbs up or thumbs down on a relation, three things happen
in sequence:

### Step 1: Persist the Vote

The `feedback_vote` and `feedback_at` columns on the `paragraph_relations`
row are updated immediately.

### Step 2: Update the Pattern Score

```
if vote == "up":   up_count++
if vote == "down": down_count++
net_score = (up - down) / (up + down + 2)   -- clamped to [-1, 1]
```

The updated `PatternScoreRecord` is upserted. The next time the scorer
encounters any relation with the same structural fingerprint, the
`patternSignal` sub-score will reflect this accumulated learning.

### Step 3: Nudge Global Weights

Each active `reason_signal` on the relation maps to a weight dimension:

| Signal | Dimension nudged |
|---|---|
| `proximity_tier:*` | `w_proximity_tier` |
| `role_pair` | `w_role_pair` |
| `concept_overlap` | `w_overlap` |
| `cue_phrase` | `w_cue` |
| `pattern_feedback` | `w_pattern` |

```
if vote == "up":   weight += 0.02
if vote == "down": weight -= 0.02
```

Additionally, the **tier multiplier** matching the relation's `proximity_tier`
is nudged by the same delta. For example, a thumbs down on a `global` relation
decreases `tier_global`.

**Clamp rules:**
- Signal dimension weights: `[0.05, 0.60]`
- Tier multipliers: `[0.10, 1.00]`

These bounds prevent any single dimension from dominating or collapsing
to zero.

> **What gets learned over time:**
> - Which role pairings tend to produce useful connections
> - Which lexical cues reliably indicate real relations
> - Which proximity tier is most reliable for your specific content
> - Which structural patterns consistently produce good or bad relations

---

## Processing Queue

The processing queue is implemented as a single integer column on the `notes`
table:

```sql
paragraph_head_seq INTEGER NOT NULL DEFAULT -1
```

A note needs processing when `paragraph_head_seq < head_seq`. The background
runner sets `paragraph_head_seq = head_seq` when a note is processed.

This approach is free of any secondary table and is automatically consistent:
if a note is edited during processing, `head_seq` advances past the just-set
`paragraph_head_seq`, and the note is re-queued on the next poll.

---

## Background Runner

`sqlite.StartParagraphRunner(ctx, db, writeFn, cfg)` starts a goroutine that:

1. **Polls** `notes WHERE paragraph_head_seq < head_seq` every
   `PollInterval` (default: 30 s), fetching up to 20 notes per batch.
2. **Loads** global weights and the full pattern score map once per batch
   (shared across all notes in that batch).
3. **For each note:**
   a. Fetches content from `note_content`.
   b. Splits content into paragraphs via `core.SplitParagraphs`.
   c. Classifies each paragraph's role via `core.ClassifyRole`.
   d. Extracts normalized concepts via `core.ExtractConcepts`.
   e. Scores all candidate pairs within `±SameDocWindowSize` positions.
   f. Keeps the top `TopN` relations above `MinScore` per source paragraph.
   g. Deletes old paragraphs and relations for the note (atomic replacement).
   h. Upserts the new paragraphs and relations.
   i. Calls `MarkNoteProcessed`.
4. **Errors on individual notes** are logged and skipped — the runner never
   blocks on a single failure.

### Configuration

```go
type ParagraphRunnerConfig struct {
    PollInterval      time.Duration // default: 30s
    SameDocWindowSize int           // look-ahead/behind within same note; default: 3
    CrossDocEnabled   bool          // Phase 2 flag — false for MVP
    TopN              int           // max relations per paragraph to keep; default: 3
    MinScore          float64       // minimum score threshold; default: 0.55
}
```

### Cross-Note Scoring (Phase 2)

The `CrossDocEnabled` flag is reserved for a future expansion where paragraphs
from *different* notes are compared. When enabled, the `same_folder`,
`same_project`, and `global` proximity tiers become active scoring paths. The
data model already supports this — `paragraph_relations` stores
`folder_urn_source`, `folder_urn_target`, `project_urn_source`, and
`project_urn_target` for every relation regardless of tier.

---

## Full Graph Rebuild

`POST /v1/paragraph-graph/rebuild` (or `service.ParagraphService.RebuildGraph`)
executes `ResetGraph` in a single write transaction:

```sql
DELETE FROM paragraph_relations;
DELETE FROM note_paragraphs;
UPDATE notes SET paragraph_head_seq = -1;
```

After this returns, every note is marked as needing processing. The background
runner picks them all up on its next poll — no further action is needed.

**What is preserved across a rebuild:**
- `paragraph_weights` — learned global weights survive
- `paragraph_pattern_scores` — accumulated anonymous feedback survives
- All notes, events, bursts, candidates — nothing in the main system is touched

A rebuild is appropriate when:
- Heuristic rules change (new role patterns, updated cue phrases)
- The concept family seed is expanded
- Scoring weights are manually reset
- Data corruption is detected in the paragraph tables

---

## Database Schema

### `note_paragraphs`

```sql
CREATE TABLE IF NOT EXISTS note_paragraphs (
    id                  TEXT    PRIMARY KEY,        -- UUIDv7
    note_urn            TEXT    NOT NULL,
    project_urn         TEXT    NOT NULL DEFAULT '', -- metadata; not a boundary
    folder_urn          TEXT    NOT NULL DEFAULT '', -- metadata; for folder queries
    sequence            INTEGER NOT NULL,           -- note head_seq when processed
    position            INTEGER NOT NULL,           -- 0-based paragraph index
    line_start          INTEGER NOT NULL,
    line_end            INTEGER NOT NULL,
    text                TEXT    NOT NULL,
    role                TEXT    NOT NULL DEFAULT 'claim',
    main_concepts       TEXT    NOT NULL DEFAULT '[]', -- JSON; normalized
    supporting_concepts TEXT    NOT NULL DEFAULT '[]', -- JSON; normalized
    concept_families    TEXT    NOT NULL DEFAULT '[]', -- JSON
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
```

### `paragraph_relations`

```sql
CREATE TABLE IF NOT EXISTS paragraph_relations (
    id                  TEXT    PRIMARY KEY,
    source_paragraph_id TEXT    NOT NULL,
    target_paragraph_id TEXT    NOT NULL,
    note_urn_source     TEXT    NOT NULL,
    note_urn_target     TEXT    NOT NULL,
    project_urn_source  TEXT    NOT NULL DEFAULT '',
    project_urn_target  TEXT    NOT NULL DEFAULT '',
    folder_urn_source   TEXT    NOT NULL DEFAULT '',
    folder_urn_target   TEXT    NOT NULL DEFAULT '',
    proximity_tier      TEXT    NOT NULL DEFAULT 'same_doc',
    relation_type       TEXT    NOT NULL,
    score               REAL    NOT NULL DEFAULT 0,
    reason_signals      TEXT    NOT NULL DEFAULT '[]', -- JSON
    pattern_hash        TEXT    NOT NULL DEFAULT '',
    version             TEXT    NOT NULL DEFAULT 'heuristic_v1',
    feedback_vote       TEXT,                          -- 'up' | 'down' | NULL
    feedback_at         INTEGER,
    created_at          INTEGER NOT NULL
);
```

### `paragraph_weights`

```sql
CREATE TABLE IF NOT EXISTS paragraph_weights (
    id                  INTEGER PRIMARY KEY CHECK (id = 1), -- singleton
    w_proximity_tier    REAL    NOT NULL DEFAULT 0.20,
    w_role_pair         REAL    NOT NULL DEFAULT 0.25,
    w_overlap           REAL    NOT NULL DEFAULT 0.20,
    w_cue               REAL    NOT NULL DEFAULT 0.20,
    w_pattern           REAL    NOT NULL DEFAULT 0.15,
    tier_same_doc       REAL    NOT NULL DEFAULT 1.00,
    tier_same_folder    REAL    NOT NULL DEFAULT 0.75,
    tier_same_project   REAL    NOT NULL DEFAULT 0.50,
    tier_global         REAL    NOT NULL DEFAULT 0.25,
    updated_at          INTEGER NOT NULL
);
```

### `paragraph_pattern_scores`

```sql
CREATE TABLE IF NOT EXISTS paragraph_pattern_scores (
    pattern_hash    TEXT    PRIMARY KEY,
    role_a          TEXT    NOT NULL DEFAULT '',
    role_b          TEXT    NOT NULL DEFAULT '',
    relation_type   TEXT    NOT NULL DEFAULT '',
    proximity_tier  TEXT    NOT NULL DEFAULT '',
    cue_present     INTEGER NOT NULL DEFAULT 0,
    up_count        INTEGER NOT NULL DEFAULT 0,
    down_count      INTEGER NOT NULL DEFAULT 0,
    net_score       REAL    NOT NULL DEFAULT 0.0,
    updated_at      INTEGER NOT NULL
);
```

---

## API Reference

### Paragraphs

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/paragraphs` | List paragraphs. Filter: `?noteURN=`, `?folderURN=`, `?pageSize=`, `?pageToken=` |
| `GET` | `/v1/paragraphs/{id}` | Get a single paragraph by ID |

**Paragraph response shape:**

```json
{
  "id": "2f84e81c-...",
  "note_urn": "urn:notx:note:...",
  "project_urn": "urn:notx:proj:...",
  "folder_urn": "urn:notx:folder:...",
  "sequence": 1,
  "position": 0,
  "line_start": 0,
  "line_end": 2,
  "text": "A lion is a large carnivorous mammal...",
  "role": "definition",
  "main_concepts": ["lion"],
  "supporting_concepts": ["carnivorous", "mammal", "felidae"],
  "concept_families": ["cognition"],
  "created_at": "2026-05-01T12:00:00Z",
  "updated_at": "2026-05-01T12:00:00Z"
}
```

### Relations

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/paragraph-relations` | List relations. Filter: `?noteURN=`, `?folderURN=`, `?minScore=`, `?sourceParagraphId=`, `?pageSize=`, `?pageToken=` |
| `GET` | `/v1/paragraph-relations/{id}` | Get a single relation by ID |
| `POST` | `/v1/paragraph-relations/{id}/feedback` | Submit thumbs up or down |

**Feedback request:**

```json
{ "vote": "up" }
```

or

```json
{ "vote": "down" }
```

**Relation response shape:**

```json
{
  "id": "r-...",
  "source_paragraph_id": "p-...",
  "target_paragraph_id": "p-...",
  "note_urn_source": "urn:notx:note:...",
  "note_urn_target": "urn:notx:note:...",
  "project_urn_source": "urn:notx:proj:...",
  "project_urn_target": "urn:notx:proj:...",
  "folder_urn_source": "urn:notx:folder:...",
  "folder_urn_target": "urn:notx:folder:...",
  "proximity_tier": "same_doc",
  "relation_type": "illustrates",
  "score": 0.6750,
  "reason_signals": ["proximity_tier:same_doc", "concept_overlap", "cue_phrase"],
  "pattern_hash": "343434eb7cf7523e",
  "version": "heuristic_v1",
  "feedback_vote": null,
  "feedback_at": null,
  "created_at": "2026-05-01T12:00:00Z"
}
```

### Weights

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/paragraph-weights` | Get global scoring weights |
| `PUT` | `/v1/paragraph-weights` | Replace global scoring weights |

**Weights response / request shape:**

```json
{
  "w_proximity_tier":  0.20,
  "w_role_pair":       0.25,
  "w_overlap":         0.20,
  "w_cue":             0.20,
  "w_pattern":         0.15,
  "tier_same_doc":     1.00,
  "tier_same_folder":  0.75,
  "tier_same_project": 0.50,
  "tier_global":       0.25,
  "updated_at":        "2026-05-01T12:00:00Z"
}
```

### Graph Rebuild

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/paragraph-graph/rebuild` | Wipe all paragraphs + relations, reset processing queue |

**Response:**

```json
{ "status": "rebuild_queued" }
```

---

## Code Map

| File | Responsibility |
|---|---|
| `core/paragraph.go` | Role constants, relation type constants, proximity tier constants, `SplitParagraphs`, `ClassifyRole`, `NormalizeConcept`, `ExtractConcepts`, `CueRelationType`, `PatternHash` |
| `core/paragraph_scorer.go` | `ScorerWeights`, `DefaultWeights`, `AnnotatedParagraph`, `ScoredRelation`, `ScoreCandidate`, `ResolveTier`, `RolePairScore`, `ConceptOverlapScore`, `DistanceScore` |
| `repo/repo.go` | `ParagraphRecord`, `ParagraphRelationRecord`, `ParagraphWeights`, `PatternScoreRecord`, `ParagraphListOptions`, `ParagraphRelationListOptions`, `ParagraphRepository` interface |
| `repo/sqlite/paragraph.go` | Full `ParagraphRepository` SQLite implementation + `StartParagraphRunner` + `ParagraphRunnerConfig` |
| `repo/sqlite/schema.go` | DDL for the four paragraph tables + v14 migration + `ensureParagraphQueueColumn` |
| `service/paragraph.go` | `ParagraphService` interface, `paragraphService` implementation, feedback loop, weight nudging |
| `service/engine.go` | `Engine.Paragraphs ParagraphService` field |
| `http/paragraph.go` | HTTP handlers for all paragraph endpoints |
| `http/handler.go` | Route registration |

---

## Design Decisions

### Why global scope, not project-scoped?

The burst/candidate system is project-scoped because its job is to surface
related notes *within a workspace*. The paragraph system's job is different:
to build deep understanding of *all content you have ever written*, regardless
of how it is organized. A definition written in one project should be able to
connect to an example written in another. Forcing project boundaries would
fragment that understanding artificially.

Projects and folders are stored as metadata on each paragraph so that
*optional* scoped queries remain possible — but the relation generation engine
never uses them as hard boundaries.

### Why a singleton weights row instead of per-note or per-project weights?

Per-project weights would mean a project with little feedback trains slowly
and produces noisy weights. Per-note weights are effectively untrained (each
note has too few relations to converge). A single global row accumulates
feedback from all notes and all users, converging faster and generalizing
better. Folder- or project-specific weight tuning is a natural future
extension once the global baseline is stable.

### Why hash feedback patterns instead of storing them raw?

Three reasons:

1. **Privacy** — no paragraph content, note IDs, or user identifiers are
   stored in the feedback table.
2. **Generalization** — a single vote on the pattern
   `definition→example / illustrates / same_doc / cue_present` improves
   scoring for *every* future relation with that fingerprint, across all
   notes, across all time.
3. **Stability** — the hash is stable across note edits. Even if a paragraph
   is rewritten, the new paragraphs produce the same fingerprint if they have
   the same structural characteristics.

### Why `paragraph_head_seq` instead of a separate queue table?

A separate queue table requires insert/delete coordination and can diverge from
the true note state. The `paragraph_head_seq < head_seq` invariant on the
`notes` row is always accurate: it is impossible for a note to be processed
"in the future" or to have an unprocessed change that `paragraph_head_seq`
does not reveal. It also makes `ResetGraph` trivially correct — a single
`UPDATE notes SET paragraph_head_seq = -1` re-queues everything atomically.

### Why is `CrossDocEnabled` false for MVP?

Within-note relations are the highest-signal source: adjacent paragraphs in
the same note have the strongest spatial proximity and the most coherent
thematic context. Cross-note scoring requires a strategy for candidate
selection (you cannot score every note against every other note), which adds
complexity that is not yet needed. The data model already supports cross-note
relations — adding the candidate selection strategy is a focused Phase 2 task.

---

## Quick Reference

```
Roles:          definition | example | contrast | cause_effect | question | claim
Relation types: elaborates | supports | contrasts_with | answers | causes | illustrates
Proximity tiers: same_doc | same_folder | same_project | global
Score range:    [0.0, 1.0]
Default MinScore: 0.55
Default TopN:   3 relations per paragraph
Default window: ±3 paragraphs (same doc)
Poll interval:  30 s
Schema version: 14
```
