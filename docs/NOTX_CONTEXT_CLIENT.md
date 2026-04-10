# notx Context Client — Operations & Prompt Spec

## Overview

This document defines the **operations** a client uses to interact with the
notx context graph layer. An operation is a named unit of work: one or more
gRPC calls to `ContextService`, a clear input contract, a clear output contract,
and ready-to-use prompt text that either a human can follow step-by-step or an
LLM agent can execute automatically.

There is no CLI, no MCP, no middleware. A client is anything that can call
`ContextService` RPCs — a Go program, a Python script, a notebook, a UI, or
an AI agent loop. The operations defined here are the vocabulary that any of
those clients speak.

```
┌───────────────────────────────────────────────────────────────┐
│                         client                                │
│   (human following prompts, or AI agent running a loop)       │
└───────────────────────────┬───────────────────────────────────┘
                            │  named operations defined in this doc
                            │  each operation = 1–3 gRPC calls + prompt
                            ▼
┌───────────────────────────────────────────────────────────────┐
│              ContextService  (gRPC, localhost:50051)          │
│                                                               │
│  ListBursts      GetBurst                                     │
│  ListCandidates  GetCandidate                                 │
│  PromoteCandidate  DismissCandidate                           │
│  GetStats        SetProjectConfig  GetProjectConfig           │
└───────────────────────────────────────────────────────────────┘
```

### Two modes, one spec

Every operation has a **prompt block** — a short, imperative instruction.

- **Human mode** — the user reads the prompt, makes the call manually (via
  `grpcurl`, a UI button, or a script), reads the result, and decides what to
  do next.
- **Agent mode** — an LLM receives the prompt as its system or user message,
  calls the RPC via a tool/function wrapper, reads the JSON result, and decides
  what to do next. The operations chain: the output of one becomes the input of
  the next.

The prompts are intentionally terse. They describe _what to do_ and _what to
look at in the result_, not implementation details.

---

## Design constraints

1. **Burst text is the atomic unit of reasoning.** An operation never asks the
   client to read full note content. The burst excerpt (5–10 lines) is
   everything needed to make a decision about whether two spans are related.

2. **Every operation is self-contained.** It specifies exactly which RPC to
   call, with which fields, and what to extract from the response. No hidden
   state between operations except for IDs passed explicitly.

3. **Operations compose in sequence.** The standard review loop is:
   `CheckQueue → FetchBatch → ReviewEach → ActOnEach`. Each step feeds the
   next. An agent can run the whole chain; a human can stop after any step.

4. **Decisions are binary.** For each candidate the client chooses `promote`
   or `dismiss`. A third option — do nothing — leaves the candidate `pending`
   for the next session. There is no `defer` status in the engine; deferral is
   the absence of action.

5. **Promotion is the meaningful outcome.** A promoted candidate becomes a
   stable `notx:lnk:id` link with human-readable anchor slugs in both note
   headers. Dismissal is lightweight cleanup. The goal of every session is to
   drain the `pending` queue by converting signal into links or discarding
   noise.

---

## gRPC wire reference

All operations call `notx.v1.ContextService`. The proto is defined in
`proto/context.proto`. The Go client accessor is `conn.Context()` from
`internal/grpcclient`.

For non-Go clients, use `grpcurl` against `localhost:50051` (insecure by
default in development):

```
grpcurl -plaintext -d '{"project_urn":"urn:notx:proj:..."}' \
  localhost:50051 notx.v1.ContextService/GetStats
```

All timestamps in responses are `google.protobuf.Timestamp` (RFC 3339 when
rendered as JSON). All IDs are UUIDv7 strings.

---

## Operations

---

### Operation 1 — `CheckQueue`

**Purpose:** Determine whether the queue is in a reviewable state before
starting a session. The background BM25 scorer enriches candidates
asynchronously after they are inserted. Reviewing before it catches up means
seeing candidates ordered only by raw Jaccard, not by full BM25 relevance.

**RPC**

```
ContextService/GetStats
```

**Request**

```json
{
  "project_urn": "<urn:notx:proj:...>   // scope to one project, or omit for server-wide"
}
```

**Response fields to read**

| Field                                 | What it tells you                              |
| ------------------------------------- | ---------------------------------------------- |
| `stats.candidates_pending`            | Total candidates waiting for a decision        |
| `stats.candidates_pending_unenriched` | Candidates the BM25 scorer has not yet scored  |
| `stats.oldest_pending_age_days`       | How long the oldest candidate has been waiting |
| `stats.bursts_today`                  | How many bursts were extracted today           |

**Readiness rule**

The queue is ready to review when:

```
candidates_pending_unenriched == 0
```

OR when all unenriched candidates are older than 5 minutes (the scorer has had
enough time to run; they are simply low-priority and below the enrichment
threshold).

If `candidates_pending == 0` there is nothing to do.

---

**Prompt — human**

```
Call GetStats for the project.

Look at candidates_pending_unenriched.
  - If it is 0: the queue is ready. Note candidates_pending and move on.
  - If it is > 0: wait 30 seconds and call GetStats again.
    Repeat until unenriched reaches 0 or stops decreasing.
  - If candidates_pending is 0: the queue is empty. Session complete.
```

---

**Prompt — agent**

```
Call ContextService/GetStats with the project_urn.

If stats.candidates_pending == 0: respond "Queue is empty. No candidates to review."
If stats.candidates_pending_unenriched > 0: wait 5 seconds and retry up to
  6 times. If unenriched is still > 0 after retries, proceed anyway — those
  candidates will be ordered by overlap_score only.
Record stats.candidates_pending as total_to_review.
Proceed to FetchBatch.
```

---

### Operation 2 — `FetchBatch`

**Purpose:** Retrieve a page of pending candidates with their burst text
embedded, ordered by confidence (bm25_score DESC, overlap_score DESC). Most
confident connections surface first.

**RPC**

```
ContextService/ListCandidates
```

**Request**

```json
{
  "project_urn": "<urn:notx:proj:...>",
  "status": "pending",
  "min_score": 0.15,
  "include_bursts": true,
  "page_size": 20,
  "page_token": "<from previous FetchBatch, or empty for first page>"
}
```

**`min_score` guidance**

| Value  | Effect                                                |
| ------ | ----------------------------------------------------- |
| `0.0`  | All candidates, including very weak overlaps          |
| `0.15` | Recommended default — removes most incidental matches |
| `0.25` | Conservative — only strong overlaps                   |

**Response fields to read**

| Field                           | What it tells you                                 |
| ------------------------------- | ------------------------------------------------- |
| `candidates[].id`               | ID to pass to `Promote` or `Dismiss`              |
| `candidates[].overlap_score`    | Raw Jaccard at detection time                     |
| `candidates[].bm25_score`       | FTS5 relevance score (0.0 = not yet enriched)     |
| `candidates[].burst_a.text`     | Excerpt from note A                               |
| `candidates[].burst_a.note_urn` | URN of note A                                     |
| `candidates[].burst_b.text`     | Excerpt from note B                               |
| `candidates[].burst_b.note_urn` | URN of note B                                     |
| `next_page_token`               | Pass to next `FetchBatch` call; empty = last page |

---

**Prompt — human**

```
Call ListCandidates with status="pending", include_bursts=true, page_size=20.
Set min_score to 0.15.

For each candidate in the response, you will see:
  - burst_a.text: an excerpt from one note
  - burst_b.text: an excerpt from a different note
  - overlap_score and bm25_score: how strongly the engine thinks they match

Read the burst text for each candidate and decide:
  PROMOTE — the two excerpts discuss the same concept, system, or dependency
             in a way a reader would benefit from a direct link between the notes.
  DISMISS — the overlap is incidental: shared boilerplate, common vocabulary,
             or unrelated topics that happen to use similar words.

Record your decision and the candidate ID for each one.
Then proceed to ActOnEach.
```

---

**Prompt — agent**

```
Call ContextService/ListCandidates with:
  project_urn = <project_urn>
  status = "pending"
  min_score = 0.15
  include_bursts = true
  page_size = 20
  page_token = <current_page_token, empty if first call>

For each candidate in candidates[]:
  Read burst_a.text and burst_b.text.
  Reason: do these two excerpts discuss the same concept, system, or decision?
    YES → queue this candidate ID for Promote, and propose a label:
          a short lowercase-hyphenated phrase describing the relationship
          (e.g. "sod-gateway-dependency", "auth-retry-policy", "rate-limit-config").
    NO  → queue this candidate ID for Dismiss.

Collect all decisions. Record next_page_token.
Proceed to ActOnEach with the collected decisions.
After ActOnEach, if next_page_token is non-empty, call FetchBatch again with
that token to process the next page.
```

---

### Operation 3 — `InspectCandidate`

**Purpose:** Fetch a single candidate in full detail when a batch listing is
not enough context to make a decision. Use this when `burst_a.text` or
`burst_b.text` in the batch feels ambiguous and you need to see the exact line
range, sequence number, and full token set before deciding.

**RPC**

```
ContextService/GetCandidate
```

**Request**

```json
{
  "id": "<candidate-id>",
  "include_bursts": true
}
```

**Response fields to read**

All fields from `FetchBatch`, plus:

| Field                                       | What it tells you                         |
| ------------------------------------------- | ----------------------------------------- |
| `candidate.burst_a.line_start` / `line_end` | Which lines in note A this burst covers   |
| `candidate.burst_a.sequence`                | Which event produced this burst           |
| `candidate.burst_a.tokens`                  | The normalized token set used for scoring |
| `candidate.burst_b.*`                       | Same for note B                           |

---

**Prompt — human**

```
Call GetCandidate with the candidate ID and include_bursts=true.

Read:
  burst_a.text  — full excerpt from note A
  burst_b.text  — full excerpt from note B
  burst_a.line_start / line_end — where in the note this came from
  overlap_score / bm25_score — confidence

Decide: PROMOTE or DISMISS.
```

---

**Prompt — agent**

```
Call ContextService/GetCandidate with id=<candidate_id> and include_bursts=true.

Read candidate.burst_a.text and candidate.burst_b.text in full.
Also read burst_a.line_start, burst_a.line_end, burst_b.line_start, burst_b.line_end
to understand where in each note the connection was detected.

Make a final PROMOTE or DISMISS decision for this candidate.
If promoting, propose a label based on the content of both bursts.
```

---

### Operation 4 — `Promote`

**Purpose:** Convert a pending candidate into a stable `notx:lnk:id` link.
The engine generates human-readable anchor slugs from the burst text, writes
`# anchor:` entries to both note headers, and records the link in the
backlink index. This is the primary meaningful output of the entire pipeline.

**RPC**

```
ContextService/PromoteCandidate
```

**Request**

```json
{
  "id": "<candidate-id>",
  "label": "<lowercase-hyphenated-relationship-name>",
  "direction": "both",
  "reviewer_urn": "<urn:notx:usr:...>"
}
```

**Field guidance**

| Field          | Guidance                                                                                                                                                                                                                                                                |
| -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `label`        | A short phrase describing _what_ connects the two notes, not _what_ the notes are. Good: `"sod-gateway-dependency"`, `"auth-retry-policy"`. Bad: `"note-a-to-note-b"`, `"connection-1"`. If omitted the engine uses the auto-generated anchor slug from the burst text. |
| `direction`    | Use `"both"` unless the relationship is clearly asymmetric. Use `"a_to_b"` when note B defines something that note A references, but note B does not need to know about note A.                                                                                         |
| `reviewer_urn` | The URN of the user or agent authorising the promotion. Stamped on the candidate record for audit. Use `"urn:notx:usr:anon"` if no user identity is available.                                                                                                          |

**Response fields to read**

| Field              | What it tells you                            |
| ------------------ | -------------------------------------------- |
| `anchor_a_id`      | The anchor slug written into note A's header |
| `anchor_b_id`      | The anchor slug written into note B's header |
| `link_a_to_b`      | The `notx:lnk:id` token pointing from A to B |
| `link_b_to_a`      | The `notx:lnk:id` token pointing from B to A |
| `candidate.status` | Should be `"promoted"`                       |

---

**Prompt — human**

```
Call PromoteCandidate with the candidate ID.

Set label to a short lowercase-hyphenated phrase that describes the
relationship between the two excerpts you just read.
Set direction to "both" unless one note clearly defines something the
other only references.
Set reviewer_urn to your user URN, or "urn:notx:usr:anon".

On success, the response gives you:
  anchor_a_id — the anchor now declared in note A
  anchor_b_id — the anchor now declared in note B
  link_a_to_b — the link token embedded in note A
  link_b_to_a — the link token embedded in note B

These are now permanent entries in both notes' headers.
```

---

**Prompt — agent**

```
Call ContextService/PromoteCandidate with:
  id = <candidate_id>
  label = <your proposed label>
  direction = "both"
  reviewer_urn = <reviewer_urn>

On success, record:
  anchor_a_id, anchor_b_id, link_a_to_b, link_b_to_a

These anchors are now permanent. Log: "Promoted <candidate_id>:
  <note_urn_a> anchor=<anchor_a_id> ← → <note_urn_b> anchor=<anchor_b_id>"

If the RPC returns NOT_FOUND: the candidate was already acted on. Skip.
If the RPC returns any other error: log it and skip this candidate.
Continue with the next decision.
```

---

### Operation 5 — `Dismiss`

**Purpose:** Mark a candidate as dismissed. The specific burst pair will never
be re-surfaced. If the same two notes later develop new overlapping content in
a different region, a new candidate will be created independently.

**RPC**

```
ContextService/DismissCandidate
```

**Request**

```json
{
  "id": "<candidate-id>",
  "reviewer_urn": "<urn:notx:usr:...>"
}
```

**Response fields to read**

| Field                   | What it tells you       |
| ----------------------- | ----------------------- |
| `candidate.status`      | Should be `"dismissed"` |
| `candidate.reviewed_at` | Timestamp of dismissal  |

---

**Prompt — human**

```
Call DismissCandidate with the candidate ID and your reviewer URN.
No further action needed. The candidate is removed from the pending queue.
```

---

**Prompt — agent**

```
Call ContextService/DismissCandidate with:
  id = <candidate_id>
  reviewer_urn = <reviewer_urn>

Log: "Dismissed <candidate_id>"
If NOT_FOUND: already acted on. Skip.
Continue with the next decision.
```

---

### Operation 6 — `ActOnEach`

**Purpose:** This is not a single RPC — it is the execution loop over the
decisions collected during `FetchBatch`. For each candidate in the batch,
call either `Promote` or `Dismiss` based on the decision made while reading
the burst text.

**Sequence**

```
for each (candidate_id, decision) in decisions:
    if decision == PROMOTE:
        call PromoteCandidate(candidate_id, label, direction, reviewer_urn)
    if decision == DISMISS:
        call DismissCandidate(candidate_id, reviewer_urn)
```

Errors on individual candidates do not abort the loop. Log the error and
continue.

---

**Prompt — human**

```
Work through your list of decisions one at a time.
For each PROMOTE decision: call PromoteCandidate (Operation 4).
For each DISMISS decision: call DismissCandidate (Operation 5).
When the list is empty, call FetchBatch again if next_page_token is non-empty.
When both decisions and pages are exhausted, call CheckQueue to confirm the
pending count has dropped.
```

---

**Prompt — agent**

```
Execute the decisions collected from FetchBatch in order.

For each decision:
  PROMOTE → call PromoteCandidate as specified in Operation 4.
  DISMISS → call DismissCandidate as specified in Operation 5.
  Error from either → log the error, skip to next decision.

After all decisions in this batch are executed:
  If next_page_token is non-empty → call FetchBatch with that token.
  If next_page_token is empty → the queue is fully processed for this session.
    Call GetStats to confirm candidates_pending has decreased.
    Report: "Session complete. Promoted N. Dismissed N. Remaining pending: N."
```

---

### Operation 7 — `InspectBursts`

**Purpose:** Read what the engine extracted from a specific note's recent
events. Use this to understand what content is being indexed before candidates
appear, to debug why a connection was or was not detected, or to find the burst
ID that anchors a promoted link.

**RPC**

```
ContextService/ListBursts
```

**Request**

```json
{
  "note_urn": "<urn:notx:note:...>",
  "since_sequence": 0,
  "page_size": 50
}
```

Set `since_sequence` to a specific event sequence number to see only bursts
produced by recent events.

**Response fields to read**

| Field                              | What it tells you                                                   |
| ---------------------------------- | ------------------------------------------------------------------- |
| `bursts[].id`                      | Burst ID (used in candidate records as `burst_a_id` / `burst_b_id`) |
| `bursts[].sequence`                | Which event produced this burst                                     |
| `bursts[].line_start` / `line_end` | Which lines of the note this covers                                 |
| `bursts[].text`                    | The exact excerpt the engine captured                               |
| `bursts[].tokens`                  | Space-separated normalized token set used for Jaccard scoring       |

---

**Prompt — human**

```
Call ListBursts with the note URN and since_sequence=0.

For each burst:
  Read line_start, line_end, and text to understand which part of the note
  was captured and what the engine saw.
  Read tokens to see what was indexed for candidate matching.

If you expected a candidate to appear but it did not, check whether the
relevant tokens are present in the burst. Absent tokens mean the content
did not survive the stop-word filter or tokenizer.
```

---

**Prompt — agent**

```
Call ContextService/ListBursts with:
  note_urn = <note_urn>
  since_sequence = <seq, or 0 for all>
  page_size = 50

List each burst: id, sequence, lines line_start–line_end, token count.
If asked to diagnose why a connection was not detected: compare the token
sets of bursts from both notes. Report which tokens overlap and which are
absent.
```

---

### Operation 8 — `SetRateLimits`

**Purpose:** Adjust per-project burst extraction caps before a bulk import,
automated write session, or any scenario that would hit the default daily caps
and stop generating candidates. Reset to global defaults afterwards.

**RPC — read current config**

```
ContextService/GetProjectConfig
```

**Request**

```json
{
  "project_urn": "<urn:notx:proj:...>"
}
```

**RPC — write new config**

```
ContextService/SetProjectConfig
```

**Request**

```json
{
  "project_urn": "<urn:notx:proj:...>",
  "burst_max_per_note_per_day": 100,
  "burst_max_per_project_per_day": 2000
}
```

Pass `0` for either cap to reset it to the global server default
(50 per note / 500 per project).

**Response fields to read**

| Field                                  | What it tells you                       |
| -------------------------------------- | --------------------------------------- |
| `config.burst_max_per_note_per_day`    | Stored cap (0 = global default applies) |
| `config.burst_max_per_project_per_day` | Stored cap (0 = global default applies) |
| `config.updated_at`                    | When the override was last written      |

---

**Prompt — human**

```
Before a bulk import:
  Call GetProjectConfig to see the current overrides.
  Call SetProjectConfig with higher caps suited to the import volume.

After the import:
  Call SetProjectConfig with both caps set to 0 to restore global defaults.
  Call GetStats to confirm burst extraction resumed normally.
```

---

**Prompt — agent**

```
To raise limits before a bulk write session:
  Call ContextService/SetProjectConfig with:
    project_urn = <project_urn>
    burst_max_per_note_per_day = <desired cap>
    burst_max_per_project_per_day = <desired cap>

To restore defaults after:
  Call ContextService/SetProjectConfig with both caps = 0.

Confirm by calling GetProjectConfig and verifying the stored values.
```

---

### Operation 9 — `InspectInferences`

**Purpose:** Review metadata inferences the engine has generated for notes that
were created without a title or project URN. The engine derives these from burst
content (title) and from the distribution of candidate partner project URNs
(project). Accepting an inference emits a real event that writes the metadata
to the note header permanently.

**RPC**

```
ContextService/ListInferences
```

**Request**

```json
{
  "status": "pending",
  "page_size": 20
}
```

**Response fields to read**

| Field                               | What it tells you                                              |
| ----------------------------------- | -------------------------------------------------------------- |
| `inferences[].id`                   | ID to pass to `AcceptInference` or `RejectInference`           |
| `inferences[].note_urn`             | Which note this inference applies to                           |
| `inferences[].inferred_title`       | Proposed title (empty if title inference inconclusive)         |
| `inferences[].title_confidence`     | Float 0–1; < 0.4 = low confidence                              |
| `inferences[].inferred_project_urn` | Proposed project URN (empty if project inference inconclusive) |
| `inferences[].project_confidence`   | Float 0–1                                                      |
| `inferences[].project_evidence`     | Evidence array: `[{project_urn, candidate_count, fraction}]`   |
| `inferences[].title_basis_burst_id` | Which burst the title was derived from                         |
| `inferences[].created_at`           | When the inference was generated                               |

---

**Prompt — human**

```
Call ListInferences with status="pending".

For each inference:
  Read inferred_title and title_confidence.
    - If title looks right and confidence >= 0.6: accept title.
    - If title looks wrong or confidence < 0.4: reject or skip.

  Read inferred_project_urn and project_evidence.
    - Look at project_evidence[0].fraction and candidate_count.
    - If the project looks right: accept project.
    - If evidence is weak or the project is wrong: reject or skip.

Proceed to AcceptInference or RejectInference for each decision.
```

---

**Prompt — agent**

```
Call ContextService/ListInferences with status="pending", page_size=20.

For each inference:
  1. Read inferred_title and title_confidence.
     If title_confidence >= 0.6 and the title makes sense for a note in this
     project: set accept_title=true.
     If title_confidence < 0.4 or the title is nonsensical: set accept_title=false.

  2. Read inferred_project_urn and project_evidence.
     Look at project_evidence[0].fraction and candidate_count.
     If fraction >= 0.60 and candidate_count >= 3: set accept_project=true.
     Otherwise: set accept_project=false.

  3. If accept_title=false AND accept_project=false: call RejectInference.
     If accept_title=true OR accept_project=true: call AcceptInference with
       the appropriate flags.

Proceed through all pages before returning a summary.
```

---

### Operation 10 — `AcceptInference` / `RejectInference`

**Purpose:** Accept or reject the engine's metadata suggestion for a note.
Acceptance emits an `AppendEvent` that writes the accepted fields to the note's
`.notx` header permanently. Rejection suppresses re-inference until the note's
content changes substantially (burst Jaccard < 0.50 vs rejected state).

**RPC — accept**

```
ContextService/AcceptInference
```

**Request**

```json
{
  "id": "<inference-id>",
  "accept_title": true,
  "accept_project": true,
  "reviewer_urn": "<urn:notx:usr:...>"
}
```

Set `accept_title` or `accept_project` to `false` to accept only one of the two
fields. At least one must be `true`.

**Response fields to read**

| Field                | What it tells you                                               |
| -------------------- | --------------------------------------------------------------- |
| `inference.status`   | Should be `"accepted"`                                          |
| `inference.note_urn` | The note that was updated                                       |
| `written_fields`     | List of fields written: `["title"]`, `["project_urn"]`, or both |

---

**RPC — reject**

```
ContextService/RejectInference
```

**Request**

```json
{
  "id": "<inference-id>",
  "reviewer_urn": "<urn:notx:usr:...>"
}
```

**Response fields to read**

| Field              | What it tells you      |
| ------------------ | ---------------------- |
| `inference.status` | Should be `"rejected"` |

---

**Prompt — human**

```
To accept:
  Call AcceptInference with the inference ID.
  Set accept_title=true and/or accept_project=true depending on which fields
  you want to apply. Set reviewer_urn to your user URN.
  On success, the note header now contains the accepted metadata.

To reject:
  Call RejectInference with the inference ID and your reviewer URN.
  The engine will not re-infer for this note until its content changes
  substantially.
```

---

**Prompt — agent**

```
To accept:
  Call ContextService/AcceptInference with:
    id = <inference_id>
    accept_title   = <true|false>
    accept_project = <true|false>
    reviewer_urn   = <reviewer_urn>

  Log: "Accepted inference <id> for note <note_urn>: written_fields=<written_fields>"
  If NOT_FOUND: already acted on. Skip.

To reject:
  Call ContextService/RejectInference with:
    id = <inference_id>
    reviewer_urn = <reviewer_urn>

  Log: "Rejected inference <id> for note <note_urn>"
  If NOT_FOUND: already acted on. Skip.
```

---

### Operation 11 — `InspectDeepConnections`

**Purpose:** List note pairs the engine has flagged as deeply connected —
pairs where the accumulated number and quality of candidates signals a
structural, ongoing relationship rather than a single coincidental overlap.
Use this to identify notes that likely deserve a permanent synthesis link or
a dedicated connecting document.

A pair is flagged deep when any of the following thresholds is crossed:

- 5 or more total candidates detected (configurable)
- 2 or more promoted candidates (configurable)
- connection_score ≥ 1.5 (weighted overlap sum with 14-day decay; configurable)

**RPC**

```
ContextService/ListDeepConnections
```

**Request**

```json
{
  "project_urn": "<urn:notx:proj:...>",
  "only_unreviewed": true,
  "page_size": 10
}
```

Set `only_unreviewed=true` to see only pairs that still have pending candidates.
Set `only_unreviewed=false` to see all deep pairs regardless of queue state.

**Response fields to read**

| Field                            | What it tells you                                     |
| -------------------------------- | ----------------------------------------------------- |
| `connections[].note_urn_a`       | First note in the pair                                |
| `connections[].note_urn_b`       | Second note in the pair                               |
| `connections[].note_name_a`      | Human-readable name of note A                         |
| `connections[].note_name_b`      | Human-readable name of note B                         |
| `connections[].candidate_count`  | Total candidates ever generated for this pair         |
| `connections[].promoted_count`   | Candidates already promoted to permanent links        |
| `connections[].pending_count`    | Candidates still awaiting review                      |
| `connections[].dismissed_count`  | Candidates dismissed as noise                         |
| `connections[].connection_score` | Weighted overlap sum (higher = stronger, more recent) |
| `connections[].deep_flagged_at`  | When the pair first crossed a deep threshold          |

---

**Prompt — human**

```
Call ListDeepConnections with only_unreviewed=true.

For each pair:
  Read note_name_a and note_name_b.
  Read candidate_count, promoted_count, pending_count.
  Read connection_score.

  Ask: are these two notes covering the same ongoing subject or
  dependency in a way that warrants a structural link beyond individual
  burst connections?

  If yes → proceed to ReviewDeepConnection (Operation 12) to work through
    all pending candidates for this pair at once.
  If no  → note it for later review or call clear-deep on the pair via the CLI.
```

---

**Prompt — agent**

```
Call ContextService/ListDeepConnections with:
  project_urn = <project_urn>
  only_unreviewed = true
  page_size = 10

For each connection in connections[]:
  Read note_name_a, note_name_b, candidate_count, promoted_count,
  pending_count, connection_score.

  Reason: "Do these two notes cover the same ongoing concept or system
  that warrants a strong structural link between them?"

  If yes: proceed to ReviewDeepConnection for this pair.
  If no: log the pair for manual review. Do not clear-deep automatically.

Report: "Found N deep connections. Proceeding to review M pairs."
```

---

### Operation 12 — `ReviewDeepConnection`

**Purpose:** Fetch all pending candidates for a specific deep-connected note
pair in one call and decide on each holistically. This is the preferred way
to review a deeply connected pair — seeing all overlapping regions together
gives better context for whether to promote each one than reviewing them in
isolation through the normal `FetchBatch` flow.

**RPC**

```
ContextService/GetDeepConnection
```

**Request**

```json
{
  "note_urn_a": "<urn:notx:note:...>",
  "note_urn_b": "<urn:notx:note:...>",
  "include_bursts": true
}
```

**Response fields to read**

| Field                           | What it tells you                                               |
| ------------------------------- | --------------------------------------------------------------- |
| `connection.note_name_a`        | Human-readable name of note A                                   |
| `connection.note_name_b`        | Human-readable name of note B                                   |
| `connection.connection_score`   | Current weighted overlap score for the pair                     |
| `connection.promoted_count`     | How many connections from this pair are already permanent links |
| `connection.pending_candidates` | All pending candidates for this pair, with burst text embedded  |

Each item in `pending_candidates` has the same shape as a regular candidate
from `FetchBatch` (including `burst_a.text`, `burst_b.text`, `overlap_score`,
`bm25_score`, and `connection_depth`).

---

**Prompt — human**

```
Call GetDeepConnection with the two note URNs and include_bursts=true.

Read note_name_a and note_name_b to orient yourself.
Read connection.promoted_count — these are already confirmed links between
the pair; you can use them as context for what kind of relationship exists.

For each candidate in pending_candidates:
  Read burst_a.text and burst_b.text.
  Decide: PROMOTE or DISMISS — using the established relationship context
  from the already-promoted links to inform borderline cases.

Then call Promote or Dismiss (Operations 4 and 5) for each decision.
```

---

**Prompt — agent**

```
Call ContextService/GetDeepConnection with:
  note_urn_a = <note_urn_a>
  note_urn_b = <note_urn_b>
  include_bursts = true

Read connection.note_name_a, connection.note_name_b, connection.promoted_count.
Build context: "Notes '<note_name_a>' and '<note_name_b>' already have
<promoted_count> confirmed links. This is a deep connection with
connection_score=<connection_score>."

For each candidate in connection.pending_candidates:
  Read burst_a.text and burst_b.text.
  Use the established relationship context to inform your decision:
    - For a deeply connected pair, lean toward PROMOTE unless the individual
      burst is clearly noise (boilerplate, structural markup, unrelated section).
    - DISMISS only when the specific excerpt is demonstrably unrelated to the
      established relationship between the notes.

  Collect decisions. Then call PromoteCandidate or DismissCandidate for each.

After all decisions: call GetDeepConnection again to confirm pending_count
has dropped. Report: "Reviewed deep connection <note_name_a> ↔ <note_name_b>.
Promoted N. Dismissed N. Remaining pending: N."
```

---

## Standard review session — full sequence

This is the complete operation chain for a review session, whether run by a
human or an AI agent. Each step feeds directly into the next.

```
1. CheckQueue      → confirm candidates_pending > 0 and scorer is ready
                     also note inferences_pending for Step 8
         ↓
2. FetchBatch      → retrieve up to 20 candidates with burst previews
                     (candidates from deep connections show connection_depth.is_deep=true)
         ↓
3. For each candidate:
     read burst_a.text + burst_b.text
     if connection_depth.is_deep=true: use established relationship as context
     decide PROMOTE or DISMISS
     (if ambiguous → InspectCandidate for full detail)
         ↓
4. ActOnEach       → call Promote or Dismiss for every decision
         ↓
5. If next_page_token is non-empty → back to step 2
         ↓
6. CheckQueue      → confirm pending count has dropped
                     report: N promoted, N dismissed, N remaining
         ↓
7. InspectDeepConnections (if any unreviewed deep pairs remain)
   → ReviewDeepConnection for each unreviewed deep pair
         ↓
8. InspectInferences (if inferences_pending > 0 from step 1)
   → AcceptInference / RejectInference for each pending inference
         ↓
9. CheckQueue      → confirm all queues drained
                     report: N promoted, N dismissed, N inferences accepted,
                             N inferences rejected, N remaining
```

---

## Agent system prompt template

This block is the complete system message to give an LLM agent before it
starts running the review loop. Fill in the bracketed fields at runtime.

```
You are a knowledge graph assistant for the notx note-taking system.

Your job is to review candidate connections between pairs of note excerpts
and decide whether each connection is meaningful enough to become a permanent
link between the two notes.

Project: <project_name>
Project URN: <project_urn>
Reviewer URN: <reviewer_urn>
Min score threshold: <min_score, e.g. 0.15>
Page size per batch: <batch_size, e.g. 20>

You have access to the following operations:

  GetStats(project_urn)
    → returns queue health. Check candidates_pending_unenriched before starting.

  ListCandidates(project_urn, status, min_score, include_bursts, page_size, page_token)
    → returns a page of candidates with burst_a.text and burst_b.text embedded.

  GetCandidate(id, include_bursts=true)
    → returns a single candidate in full detail. Use when burst text is ambiguous.

  PromoteCandidate(id, label, direction, reviewer_urn)
    → creates a permanent notx:lnk:id link. Requires a descriptive label.

  DismissCandidate(id, reviewer_urn)
    → removes the candidate from the pending queue permanently.

  ListDeepConnections(project_urn, only_unreviewed, page_size)
    → returns note pairs flagged as deeply connected (multiple candidates across sessions).

  GetDeepConnection(note_urn_a, note_urn_b, include_bursts=true)
    → returns all pending candidates for a specific deep note pair.

  ListInferences(status, page_size)
    → returns pending metadata inferences (title/project) for untitled or unprojectd notes.

  AcceptInference(id, accept_title, accept_project, reviewer_urn)
    → writes accepted metadata to note header. At least one accept flag must be true.

  RejectInference(id, reviewer_urn)
    → suppresses re-inference until note content changes substantially.

Decision rules:
  PROMOTE when: the two excerpts discuss the same concept, system, decision,
    or dependency in a way that a reader navigating from one note to the other
    would find genuinely useful.
  DISMISS when: the overlap is incidental — shared function words, boilerplate
    structure, common domain vocabulary used in unrelated contexts, or topics
    that happen to use similar terms but are not actually about the same thing.

Label rules (for PROMOTE):
  - Lowercase, hyphen-separated, 2–4 words.
  - Describes the relationship, not the notes themselves.
  - Examples: "sod-gateway-dependency", "auth-retry-policy", "rate-limit-config",
    "schema-migration-reference", "api-error-handling".

Deep connection rules:
  When a candidate shows connection_depth.is_deep=true, that pair has exceeded
  the depth threshold. Use the already-promoted links from the pair as context.
  Lean toward PROMOTE for pending candidates in a deep pair unless the specific
  excerpt is demonstrably unrelated to the established relationship.
  After draining the candidate queue, call ListDeepConnections to find any
  unreviewed deep pairs. For each, call GetDeepConnection and review holistically.

Inference rules:
  After completing the candidate review, call ListInferences with status="pending".
  For each inference:
    Accept title if title_confidence >= 0.6 and the title makes sense.
    Accept project if project_evidence[0].fraction >= 0.60 and candidate_count >= 3.
    Reject if neither field is acceptable.

Run the full review loop:
  1. Call GetStats. If candidates_pending == 0, report and stop.
  2. Call ListCandidates to fetch the first batch.
  3. For each candidate, read burst_a.text and burst_b.text and decide.
  4. Call PromoteCandidate or DismissCandidate for each decision.
  5. If next_page_token is non-empty, fetch the next batch.
  6. When all pages are exhausted, call GetStats and report the final counts.
  7. Call ListDeepConnections with only_unreviewed=true. For each deep pair,
     call GetDeepConnection and review all pending candidates holistically.
  8. Call ListInferences with status="pending". For each inference, call
     AcceptInference or RejectInference based on confidence and evidence.
  9. Call GetStats and report: N promoted, N dismissed, N inferences accepted,
     N inferences rejected, N deep connections reviewed, N remaining.
```

---

## Error handling

Every operation must handle these gRPC status codes gracefully. Neither a
human workflow nor an agent loop should abort on a single-candidate error.

| Code                | Meaning                           | Correct response                                                              |
| ------------------- | --------------------------------- | ----------------------------------------------------------------------------- |
| `NOT_FOUND`         | Candidate or burst does not exist | Skip this item. It was already acted on by a concurrent session or was swept. |
| `INVALID_ARGUMENT`  | Malformed request field           | Log the field name and value. Fix the request. Do not retry as-is.            |
| `UNAVAILABLE`       | Engine is not reachable           | Stop the session. The engine is not running or is unreachable.                |
| `DEADLINE_EXCEEDED` | RPC took longer than 30 s         | Retry once with a 5 s delay. If it fails again, skip and continue.            |
| Any other           | Unexpected server error           | Log the full status message. Skip this item and continue.                     |

---

## Composition: manual + agent in the same session

A session does not have to be purely human or purely agent. The operations
compose freely:

```
Human:  calls CheckQueue and FetchBatch manually to review the first page.
        Reads burst text. Decides which candidates to promote.

Agent:  receives the list of decided candidate IDs + labels.
        Calls PromoteCandidate and DismissCandidate for each.
        Calls FetchBatch for the remaining pages and decides those autonomously.
        Reports final counts back to the human.
```

The operations are stateless. The only shared state is the set of candidate
IDs in the engine. Any combination of human and agent calls is safe because
`PromoteCandidate` and `DismissCandidate` are idempotent on the candidate
status — a second call on an already-promoted candidate returns `NOT_FOUND`
or the current status, and the caller skips it.
