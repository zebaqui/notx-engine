package smoke

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/repo/sqlite"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestContextGraph_ThreeNotes_BurstsAndCandidates
//
// Scenario
// --------
// Three notes are created inside the same project and each receives one event
// with thematically overlapping content about "authentication" and "gateway"
// start-of-day initialisation. Because all three notes share a significant
// token overlap the Jaccard gate (default 0.12) should fire and produce at
// least one candidate_relation row.
//
// The third note is the validator: after writing its event we wait up to
// 2 seconds for the background BM25 scorer goroutine to enrich the pending
// candidates, then assert:
//
//  1. At least 3 burst rows were stored (one per event across the three notes).
//  2. At least one candidate_relation row exists between different notes.
//  3. At least one candidate has bm25_score > 0  (scorer has run).
//  4. All enriched candidates are in "pending" status (they have not been
//     promoted or dismissed by anyone).
//
// ─────────────────────────────────────────────────────────────────────────────
func TestContextGraph_ThreeNotes_BurstsAndCandidates(t *testing.T) {
	ctx := context.Background()

	// ── Provider ─────────────────────────────────────────────────────────────
	// We need the SQLite provider: it owns the burst-extraction hook, the
	// context_bursts table, the candidate_relations table, and the background
	// BM25 scorer goroutine. The in-memory provider has none of those.
	dir := t.TempDir()
	p, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	// ── Shared identifiers ────────────────────────────────────────────────────
	projURN := core.MustParseURN("urn:notx:proj:11111111-1111-7111-8111-111111111111")
	authorURN := core.MustParseURN("urn:notx:usr:aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa")

	note1URN := core.MustParseURN("urn:notx:note:11111111-1111-7111-8111-000000000001")
	note2URN := core.MustParseURN("urn:notx:note:11111111-1111-7111-8111-000000000002")
	note3URN := core.MustParseURN("urn:notx:note:11111111-1111-7111-8111-000000000003")

	now := time.Now().UTC()

	// ── Helpers ───────────────────────────────────────────────────────────────

	// createNote persists a note header associated with the shared project.
	createNote := func(urn core.URN, name string) {
		t.Helper()
		n := core.NewNote(urn, name, now)
		n.ProjectURN = &projURN
		if err := p.Create(ctx, n); err != nil {
			t.Fatalf("create note %q: %v", name, err)
		}
	}

	// appendEvent appends a single event whose content is the given lines.
	appendEvent := func(noteURN core.URN, seq int, lines []string) {
		t.Helper()
		entries := make([]core.LineEntry, len(lines))
		for i, l := range lines {
			entries[i] = core.LineEntry{
				Op:         core.LineOpSet,
				LineNumber: i + 1,
				Content:    l,
			}
		}
		ev := &core.Event{
			URN:       core.NewURN(core.ObjectTypeEvent),
			NoteURN:   noteURN,
			Sequence:  seq,
			AuthorURN: authorURN,
			CreatedAt: now.Add(time.Duration(seq) * time.Millisecond),
			Entries:   entries,
		}
		if err := p.AppendEvent(ctx, ev, repo.AppendEventOptions{}); err != nil {
			t.Fatalf("AppendEvent note=%s seq=%d: %v", noteURN, seq, err)
		}
	}

	// ── Step 1: create the project ────────────────────────────────────────────
	proj := &core.Project{
		URN:       projURN,
		Name:      "Context Graph Smoke Project",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := p.CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// ── Step 2: create the three notes ───────────────────────────────────────
	createNote(note1URN, "API Gateway Design")
	createNote(note2URN, "Daily Standup Sprint 14")
	createNote(note3URN, "Authentication Service Overview")

	// ── Step 3: append events with thematically overlapping content ───────────
	//
	// All three blobs talk about authentication, gateway, and start-of-day
	// initialisation. The shared vocabulary ("authentication", "gateway",
	// "initialize", "request", "service", "process") is enough to push the
	// Jaccard score above the default 0.12 gate on every pair.

	note1Content := []string{
		"The SOD (Start of Day) process initializes all gateway state",
		"before the first request is accepted by the authentication service.",
		"All downstream services must wait for the SOD signal.",
		"Gateway authentication must complete before request forwarding.",
		"The authentication module validates every incoming request token.",
	}

	note2Content := []string{
		"SOD jobs failed again on staging: the gateway did not receive",
		"the authentication initialization signal before traffic was routed.",
		"Need to fix the gateway startup sequence and authentication handshake.",
		"The authentication service initialize call is timing out on cold start.",
		"Gateway request routing depends on authentication service readiness.",
	}

	note3Content := []string{
		"Authentication service responsibilities: validate request tokens,",
		"initialize session state, and signal gateway readiness on SOD.",
		"The gateway forwards each request to the authentication module.",
		"Authentication must complete initialization before any request is processed.",
		"SOD sequence: authentication service starts, signals gateway, gateway opens.",
	}

	appendEvent(note1URN, 1, note1Content)
	appendEvent(note2URN, 1, note2Content)
	appendEvent(note3URN, 1, note3Content)

	// ── Step 4: assert burst rows were created ────────────────────────────────
	// Each AppendEvent call should have produced at least one context_burst row.
	// We poll with a short retry because the burst insertion is synchronous
	// (inside the write goroutine) but we want to give the DB a moment to flush.

	var totalBursts int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b1, _, err1 := p.ListBursts(ctx, note1URN.String(), 0, 100)
		b2, _, err2 := p.ListBursts(ctx, note2URN.String(), 0, 100)
		b3, _, err3 := p.ListBursts(ctx, note3URN.String(), 0, 100)
		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatalf("ListBursts errors: %v / %v / %v", err1, err2, err3)
		}
		totalBursts = len(b1) + len(b2) + len(b3)
		if totalBursts >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if totalBursts < 3 {
		t.Fatalf("expected at least 3 burst rows (one per note), got %d", totalBursts)
	}
	t.Logf("burst rows: %d", totalBursts)

	// ── Step 5: assert candidate rows exist between different notes ───────────
	// Candidates are inserted synchronously inside the write goroutine, so they
	// should be visible immediately after AppendEvent returns.

	allCandidates, _, err := p.ListCandidates(ctx, repo.CandidateListOptions{
		ProjectURN: projURN.String(),
		PageSize:   100,
	})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}

	if len(allCandidates) == 0 {
		t.Fatal("expected at least one candidate_relation between the three notes, got 0")
	}
	t.Logf("candidate rows: %d", len(allCandidates))

	// Verify every candidate links two *different* notes.
	for _, c := range allCandidates {
		if c.NoteURN_A == c.NoteURN_B {
			t.Errorf("candidate %s has same note on both sides: %s", c.ID, c.NoteURN_A)
		}
		if c.OverlapScore <= 0 {
			t.Errorf("candidate %s has non-positive overlap_score: %f", c.ID, c.OverlapScore)
		}
		if c.Status != "pending" {
			t.Errorf("candidate %s: expected status=pending, got %q", c.ID, c.Status)
		}
	}

	// ── Step 6: wait for the background BM25 scorer to enrich candidates ──────
	// The scorer goroutine runs asynchronously. We poll for up to 5 seconds
	// for at least one candidate to have bm25_score > 0.

	var enrichedCount int
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		enrichedCount = 0
		cands, _, err := p.ListCandidates(ctx, repo.CandidateListOptions{
			ProjectURN: projURN.String(),
			PageSize:   100,
		})
		if err != nil {
			t.Fatalf("ListCandidates (scoring poll): %v", err)
		}
		for _, c := range cands {
			if c.BM25Score > 0 {
				enrichedCount++
			}
		}
		if enrichedCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if enrichedCount == 0 {
		t.Fatal("expected at least one candidate with bm25_score > 0 after BM25 scorer ran, got 0")
	}
	t.Logf("candidates with bm25_score > 0: %d / %d", enrichedCount, len(allCandidates))

	// ── Step 7: spot-check burst content for note 3 ───────────────────────────
	// Verify the stored burst text actually contains recognisable words from
	// the event payload so we know extraction is working end-to-end.

	bursts3, _, err := p.ListBursts(ctx, note3URN.String(), 0, 100)
	if err != nil {
		t.Fatalf("ListBursts note3: %v", err)
	}
	if len(bursts3) == 0 {
		t.Fatal("note3: expected at least one burst row")
	}

	combined := strings.Builder{}
	for _, b := range bursts3 {
		combined.WriteString(b.Text)
		combined.WriteString(" ")
		combined.WriteString(b.Tokens)
		combined.WriteString(" ")
	}
	burstText := strings.ToLower(combined.String())

	for _, keyword := range []string{"authentication", "gateway", "request"} {
		if !strings.Contains(burstText, keyword) {
			t.Errorf("note3 burst content/tokens does not contain keyword %q; got: %s",
				keyword, burstText)
		}
	}

	// ── Step 8: verify stats endpoint returns sane numbers ────────────────────
	stats, err := p.GetContextStats(ctx, projURN.String())
	if err != nil {
		t.Fatalf("GetContextStats: %v", err)
	}

	if stats.BurstsTotal < 3 {
		t.Errorf("stats.BurstsTotal = %d, want >= 3", stats.BurstsTotal)
	}
	if stats.CandidatesPending < 1 {
		t.Errorf("stats.CandidatesPending = %d, want >= 1", stats.CandidatesPending)
	}
	if stats.CandidatesPendingUnenriched < 0 {
		t.Errorf("stats.CandidatesPendingUnenriched = %d, should be >= 0", stats.CandidatesPendingUnenriched)
	}

	t.Logf("stats: bursts_total=%d candidates_pending=%d unenriched=%d",
		stats.BurstsTotal,
		stats.CandidatesPending,
		stats.CandidatesPendingUnenriched,
	)
}
