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

## Standard review session — full sequence

This is the complete operation chain for a review session, whether run by a
human or an AI agent. Each step feeds directly into the next.

```
1. CheckQueue      → confirm candidates_pending > 0 and scorer is ready
         ↓
2. FetchBatch      → retrieve up to 20 candidates with burst previews
         ↓
3. For each candidate:
     read burst_a.text + burst_b.text
     decide PROMOTE or DISMISS
     (if ambiguous → InspectCandidate for full detail)
         ↓
4. ActOnEach       → call Promote or Dismiss for every decision
         ↓
5. If next_page_token is non-empty → back to step 2
         ↓
6. CheckQueue      → confirm pending count has dropped
                     report: N promoted, N dismissed, N remaining
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

Run the full review loop:
  1. Call GetStats. If candidates_pending == 0, report and stop.
  2. Call ListCandidates to fetch the first batch.
  3. For each candidate, read burst_a.text and burst_b.text and decide.
  4. Call PromoteCandidate or DismissCandidate for each decision.
  5. If next_page_token is non-empty, fetch the next batch.
  6. When all pages are exhausted, call GetStats and report the final counts.
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
