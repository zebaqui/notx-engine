package smoke

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/core"
	grpcclient "github.com/zebaqui/notx-engine/internal/grpcclient"
	"github.com/zebaqui/notx-engine/internal/server"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/repo/sqlite"
)

// ─────────────────────────────────────────────────────────────────────────────
// startSQLiteServer
//
// Spins up a full notx server backed by a temporary SQLite provider on two
// random free ports (HTTP + gRPC). The SQLite provider implements
// repo.ContextRepository so ContextService is fully registered and functional.
//
// Returns the gRPC address, the provider (for direct assertion), and a stop
// function the caller must defer.
// ─────────────────────────────────────────────────────────────────────────────

func startSQLiteServer(t *testing.T) (grpcAddr string, p *sqlite.Provider, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	dir := t.TempDir()
	provider, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("startSQLiteServer: sqlite.New: %v", err)
	}

	cfg := config.Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = true
	cfg.HTTPPort = httpPort
	cfg.GRPCPort = grpcPort
	cfg.DeviceOnboarding.AutoApprove = true

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	srv, err := server.New(
		cfg,
		provider, // NoteRepository
		provider, // ProjectRepository
		provider, // DeviceRepository
		provider, // UserRepository
		provider, // ServerRepository
		provider, // PairingSecretStore
		provider, // ContextRepository  ← enables ContextService
		provider, // LinkRepository     ← enables LinkService
		log,
		nil, // busRepo — sync bus not needed in smoke tests
	)
	if err != nil {
		provider.Close()
		t.Fatalf("startSQLiteServer: server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithContext(ctx) }()

	// Wait for gRPC port to accept connections (up to 3 s).
	grpcAddr = fmt.Sprintf("127.0.0.1:%d", grpcPort)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", grpcAddr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop = func() {
		cancel()
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
			t.Log("warning: server did not stop within 5 s")
		}
		provider.Close()
	}

	return grpcAddr, provider, stop
}

// dialGRPC opens an insecure gRPC client connection to the given address and
// registers a cleanup hook to close it when the test ends.
func dialGRPC(t *testing.T, addr string) *grpcclient.Conn {
	t.Helper()
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialGRPC: %v", err)
	}
	conn := grpcclient.WrapConn(cc)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// ─────────────────────────────────────────────────────────────────────────────
// TestContextGRPC_ThreeNotes_BurstsAndCandidates
//
// End-to-end scenario exercising the full stack:
//
//   SQLite provider (burst extraction + BM25 scorer)
//       → server.New (ContextService registered)
//           → gRPC wire
//               → grpcclient.Conn.Context() typed accessor
//                   → ContextService RPCs (ListBursts, ListCandidates,
//                      GetCandidate, GetStats, DismissCandidate,
//                      SetProjectConfig, GetProjectConfig)
//
// Three notes are written via NoteService + AppendEvent with thematically
// overlapping content about authentication and gateway initialisation. The
// test then exercises every ContextService RPC to validate that:
//
//  1. Bursts were extracted and are retrievable via ListBursts / GetBurst.
//  2. Candidate relations were detected and are retrievable via ListCandidates
//     / GetCandidate (with embedded burst previews).
//  3. The background BM25 scorer enriches at least one candidate.
//  4. GetStats returns sane counts scoped to the project.
//  5. DismissCandidate transitions a candidate to status="dismissed".
//  6. SetProjectConfig / GetProjectConfig round-trip per-project rate limits.
// ─────────────────────────────────────────────────────────────────────────────

func TestContextGRPC_ThreeNotes_BurstsAndCandidates(t *testing.T) {
	grpcAddr, _, stop := startSQLiteServer(t)
	defer stop()

	conn := dialGRPC(t, grpcAddr)

	ctx := context.Background()

	notes := conn.Notes()
	ctxSvc := conn.Context()
	projects := conn.Projects()

	// ── Shared identifiers ────────────────────────────────────────────────────

	const (
		projURNStr   = "urn:notx:proj:22222222-2222-7222-8222-222222222222"
		note1URNStr  = "urn:notx:note:22222222-2222-7222-8222-000000000001"
		note2URNStr  = "urn:notx:note:22222222-2222-7222-8222-000000000002"
		note3URNStr  = "urn:notx:note:22222222-2222-7222-8222-000000000003"
		authorURNStr = "urn:notx:usr:bbbbbbbb-bbbb-7bbb-8bbb-bbbbbbbbbbbb"
	)

	// ── Step 1: create the project ────────────────────────────────────────────

	_, err := projects.CreateProject(ctx, &pb.CreateProjectRequest{
		Urn:  projURNStr,
		Name: "gRPC Context Smoke Project",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// ── Step 2: create three notes via NoteService ────────────────────────────

	noteDefs := []struct {
		urn  string
		name string
	}{
		{note1URNStr, "API Gateway Design"},
		{note2URNStr, "Daily Standup Sprint 14"},
		{note3URNStr, "Authentication Service Overview"},
	}

	for _, nd := range noteDefs {
		_, err := notes.CreateNote(ctx, &pb.CreateNoteRequest{
			Header: &pb.NoteHeader{
				Urn:        nd.urn,
				Name:       nd.name,
				NoteType:   pb.NoteType_NOTE_TYPE_NORMAL,
				ProjectUrn: projURNStr,
			},
		})
		if err != nil {
			t.Fatalf("CreateNote %q: %v", nd.name, err)
		}
	}

	// ── Step 3: append events with overlapping thematic content ───────────────
	//
	// All three blobs share tokens: authentication, gateway, initialize,
	// request, service, sod — enough to pass the default Jaccard gate of 0.12
	// on every pair.

	noteContents := []struct {
		noteURN string
		lines   []string
	}{
		{
			note1URNStr,
			[]string{
				"The SOD (Start of Day) process initializes all gateway state",
				"before the first request is accepted by the authentication service.",
				"All downstream services must wait for the SOD signal.",
				"Gateway authentication must complete before request forwarding.",
				"The authentication module validates every incoming request token.",
			},
		},
		{
			note2URNStr,
			[]string{
				"SOD jobs failed again on staging: the gateway did not receive",
				"the authentication initialization signal before traffic was routed.",
				"Need to fix the gateway startup sequence and authentication handshake.",
				"The authentication service initialize call is timing out on cold start.",
				"Gateway request routing depends on authentication service readiness.",
			},
		},
		{
			note3URNStr,
			[]string{
				"Authentication service responsibilities: validate request tokens,",
				"initialize session state, and signal gateway readiness on SOD.",
				"The gateway forwards each request to the authentication module.",
				"Authentication must complete initialization before any request is processed.",
				"SOD sequence: authentication service starts, signals gateway, gateway opens.",
			},
		},
	}

	for _, nc := range noteContents {
		entries := make([]*pb.LineEntry, len(nc.lines))
		for i, l := range nc.lines {
			entries[i] = &pb.LineEntry{Op: 0, LineNumber: int32(i + 1), Content: l}
		}
		_, err := notes.AppendEvent(ctx, &pb.AppendEventRequest{
			Event: &pb.Event{
				NoteUrn:   nc.noteURN,
				Sequence:  1,
				AuthorUrn: authorURNStr,
				Entries:   entries,
			},
		})
		if err != nil {
			t.Fatalf("AppendEvent note=%s: %v", nc.noteURN, err)
		}
	}

	// ── Step 4: ListBursts — at least one burst per note ─────────────────────
	// Burst insertion is synchronous inside AppendEvent so results are
	// immediately visible.

	totalBursts := 0
	for _, nd := range noteDefs {
		resp, err := ctxSvc.ListBursts(ctx, &pb.ListBurstsRequest{
			NoteUrn:  nd.urn,
			PageSize: 100,
		})
		if err != nil {
			t.Fatalf("ListBursts %q: %v", nd.name, err)
		}
		if len(resp.Bursts) == 0 {
			t.Errorf("ListBursts %q: expected at least 1 burst, got 0", nd.name)
		}
		totalBursts += len(resp.Bursts)

		// Validate burst fields on the first burst.
		if len(resp.Bursts) > 0 {
			b := resp.Bursts[0]
			if b.Id == "" {
				t.Errorf("ListBursts %q: burst.id is empty", nd.name)
			}
			if b.NoteUrn != nd.urn {
				t.Errorf("ListBursts %q: burst.note_urn = %q, want %q", nd.name, b.NoteUrn, nd.urn)
			}
			if b.LineStart <= 0 {
				t.Errorf("ListBursts %q: burst.line_start = %d, want > 0", nd.name, b.LineStart)
			}
			if b.Text == "" {
				t.Errorf("ListBursts %q: burst.text is empty", nd.name)
			}
			if b.Tokens == "" {
				t.Errorf("ListBursts %q: burst.tokens is empty", nd.name)
			}
			if b.CreatedAt == nil {
				t.Errorf("ListBursts %q: burst.created_at is nil", nd.name)
			}
		}
	}
	t.Logf("total burst rows via ListBursts: %d", totalBursts)

	// ── Step 5: GetBurst — fetch a specific burst by ID ───────────────────────

	// Grab the ID of note1's first burst so we can fetch it individually.
	listResp1, err := ctxSvc.ListBursts(ctx, &pb.ListBurstsRequest{
		NoteUrn:  note1URNStr,
		PageSize: 1,
	})
	if err != nil || len(listResp1.Bursts) == 0 {
		t.Fatalf("ListBursts (note1 for GetBurst): err=%v, bursts=%d", err, len(listResp1.Bursts))
	}
	burstID := listResp1.Bursts[0].Id

	getBurstResp, err := ctxSvc.GetBurst(ctx, &pb.GetBurstRequest{Id: burstID})
	if err != nil {
		t.Fatalf("GetBurst %q: %v", burstID, err)
	}
	if getBurstResp.Burst == nil {
		t.Fatal("GetBurst: response.burst is nil")
	}
	if getBurstResp.Burst.Id != burstID {
		t.Errorf("GetBurst: id = %q, want %q", getBurstResp.Burst.Id, burstID)
	}

	// ── Step 6: ListCandidates — at least one cross-note candidate ────────────

	var candidateID string
	var listCandResp *pb.ListCandidatesResponse

	// Candidates are inserted synchronously; poll briefly just in case of any
	// scheduler delay on the writer goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		listCandResp, err = ctxSvc.ListCandidates(ctx, &pb.ListCandidatesRequest{
			ProjectUrn:    projURNStr,
			Status:        "pending",
			IncludeBursts: true,
			PageSize:      100,
		})
		if err != nil {
			t.Fatalf("ListCandidates: %v", err)
		}
		if len(listCandResp.Candidates) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(listCandResp.Candidates) == 0 {
		t.Fatal("ListCandidates: expected at least one candidate between the three notes, got 0")
	}
	t.Logf("candidate rows: %d", len(listCandResp.Candidates))

	for _, c := range listCandResp.Candidates {
		// Different notes on each side.
		if c.NoteUrnA == c.NoteUrnB {
			t.Errorf("candidate %s: note_urn_a == note_urn_b (%s)", c.Id, c.NoteUrnA)
		}
		// Valid overlap score.
		if c.OverlapScore <= 0 {
			t.Errorf("candidate %s: overlap_score = %f, want > 0", c.Id, c.OverlapScore)
		}
		// Status must be pending.
		if c.Status != "pending" {
			t.Errorf("candidate %s: status = %q, want pending", c.Id, c.Status)
		}
		// Burst previews embedded (include_bursts=true).
		if c.BurstA == nil {
			t.Errorf("candidate %s: burst_a is nil (include_bursts was true)", c.Id)
		} else {
			if c.BurstA.Text == "" {
				t.Errorf("candidate %s: burst_a.text is empty", c.Id)
			}
		}
		if c.BurstB == nil {
			t.Errorf("candidate %s: burst_b is nil (include_bursts was true)", c.Id)
		} else {
			if c.BurstB.Text == "" {
				t.Errorf("candidate %s: burst_b.text is empty", c.Id)
			}
		}
	}

	// Keep the first candidate ID for later RPC calls.
	candidateID = listCandResp.Candidates[0].Id

	// ── Step 7: GetCandidate — fetch the same candidate individually ──────────

	getCandResp, err := ctxSvc.GetCandidate(ctx, &pb.GetCandidateRequest{
		Id:            candidateID,
		IncludeBursts: true,
	})
	if err != nil {
		t.Fatalf("GetCandidate %q: %v", candidateID, err)
	}
	if getCandResp.Candidate == nil {
		t.Fatal("GetCandidate: response.candidate is nil")
	}
	if getCandResp.Candidate.Id != candidateID {
		t.Errorf("GetCandidate: id = %q, want %q", getCandResp.Candidate.Id, candidateID)
	}
	if getCandResp.Candidate.BurstA == nil || getCandResp.Candidate.BurstB == nil {
		t.Error("GetCandidate: burst previews missing even though include_bursts=true")
	}

	// ── Step 8: Wait for BM25 scorer — at least one enriched candidate ────────

	var enrichedCount int
	scorerDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(scorerDeadline) {
		enrichedCount = 0
		allCands, err := ctxSvc.ListCandidates(ctx, &pb.ListCandidatesRequest{
			ProjectUrn: projURNStr,
			PageSize:   100,
		})
		if err != nil {
			t.Fatalf("ListCandidates (scorer poll): %v", err)
		}
		for _, c := range allCands.Candidates {
			if c.Bm25Score > 0 {
				enrichedCount++
			}
		}
		if enrichedCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if enrichedCount == 0 {
		t.Fatal("no candidate has bm25_score > 0 after waiting for BM25 scorer")
	}
	t.Logf("candidates with bm25_score > 0: %d / %d", enrichedCount, len(listCandResp.Candidates))

	// ── Step 9: GetStats — validate server-wide and project-scoped counts ─────

	// Global stats.
	globalStats, err := ctxSvc.GetStats(ctx, &pb.GetStatsRequest{})
	if err != nil {
		t.Fatalf("GetStats (global): %v", err)
	}
	if globalStats.Stats == nil {
		t.Fatal("GetStats (global): stats is nil")
	}
	if globalStats.Stats.BurstsTotal < 3 {
		t.Errorf("GetStats (global): bursts_total = %d, want >= 3", globalStats.Stats.BurstsTotal)
	}
	if globalStats.Stats.CandidatesPending < 1 {
		t.Errorf("GetStats (global): candidates_pending = %d, want >= 1", globalStats.Stats.CandidatesPending)
	}
	t.Logf("global stats: bursts_total=%d candidates_pending=%d unenriched=%d",
		globalStats.Stats.BurstsTotal,
		globalStats.Stats.CandidatesPending,
		globalStats.Stats.CandidatesPendingUnenriched,
	)

	// Project-scoped stats.
	projStats, err := ctxSvc.GetStats(ctx, &pb.GetStatsRequest{ProjectUrn: projURNStr})
	if err != nil {
		t.Fatalf("GetStats (project): %v", err)
	}
	if projStats.Stats == nil {
		t.Fatal("GetStats (project): stats is nil")
	}
	if projStats.Stats.BurstsTotal < 3 {
		t.Errorf("GetStats (project): bursts_total = %d, want >= 3", projStats.Stats.BurstsTotal)
	}
	t.Logf("project stats: bursts_total=%d candidates_pending=%d",
		projStats.Stats.BurstsTotal,
		projStats.Stats.CandidatesPending,
	)

	// ── Step 10: DismissCandidate — transition a candidate to dismissed ────────

	dismissResp, err := ctxSvc.DismissCandidate(ctx, &pb.DismissCandidateRequest{
		Id:          candidateID,
		ReviewerUrn: authorURNStr,
	})
	if err != nil {
		t.Fatalf("DismissCandidate %q: %v", candidateID, err)
	}
	if dismissResp.Candidate == nil {
		t.Fatal("DismissCandidate: response.candidate is nil")
	}
	if dismissResp.Candidate.Status != "dismissed" {
		t.Errorf("DismissCandidate: status = %q, want dismissed", dismissResp.Candidate.Status)
	}
	if dismissResp.Candidate.ReviewedBy != authorURNStr {
		t.Errorf("DismissCandidate: reviewed_by = %q, want %q",
			dismissResp.Candidate.ReviewedBy, authorURNStr)
	}
	if dismissResp.Candidate.ReviewedAt == nil {
		t.Error("DismissCandidate: reviewed_at is nil, want a timestamp")
	}

	// Confirm the change is visible via GetCandidate.
	afterDismiss, err := ctxSvc.GetCandidate(ctx, &pb.GetCandidateRequest{Id: candidateID})
	if err != nil {
		t.Fatalf("GetCandidate after dismiss: %v", err)
	}
	if afterDismiss.Candidate.Status != "dismissed" {
		t.Errorf("GetCandidate after dismiss: status = %q, want dismissed",
			afterDismiss.Candidate.Status)
	}

	// ── Step 11: SetProjectConfig / GetProjectConfig round-trip ───────────────

	setResp, err := ctxSvc.SetProjectConfig(ctx, &pb.SetProjectConfigRequest{
		ProjectUrn:               projURNStr,
		BurstMaxPerNotePerDay:    75,
		BurstMaxPerProjectPerDay: 750,
	})
	if err != nil {
		t.Fatalf("SetProjectConfig: %v", err)
	}
	if setResp.Config == nil {
		t.Fatal("SetProjectConfig: response.config is nil")
	}
	if setResp.Config.BurstMaxPerNotePerDay != 75 {
		t.Errorf("SetProjectConfig: burst_max_per_note_per_day = %d, want 75",
			setResp.Config.BurstMaxPerNotePerDay)
	}
	if setResp.Config.BurstMaxPerProjectPerDay != 750 {
		t.Errorf("SetProjectConfig: burst_max_per_project_per_day = %d, want 750",
			setResp.Config.BurstMaxPerProjectPerDay)
	}

	getConfigResp, err := ctxSvc.GetProjectConfig(ctx, &pb.GetProjectConfigRequest{
		ProjectUrn: projURNStr,
	})
	if err != nil {
		t.Fatalf("GetProjectConfig: %v", err)
	}
	if getConfigResp.Config == nil {
		t.Fatal("GetProjectConfig: response.config is nil")
	}
	if getConfigResp.Config.BurstMaxPerNotePerDay != 75 {
		t.Errorf("GetProjectConfig: burst_max_per_note_per_day = %d, want 75",
			getConfigResp.Config.BurstMaxPerNotePerDay)
	}
	if getConfigResp.Config.BurstMaxPerProjectPerDay != 750 {
		t.Errorf("GetProjectConfig: burst_max_per_project_per_day = %d, want 750",
			getConfigResp.Config.BurstMaxPerProjectPerDay)
	}

	// ── Step 12: Note 3 burst content sanity check ────────────────────────────

	note3Bursts, err := ctxSvc.ListBursts(ctx, &pb.ListBurstsRequest{
		NoteUrn:  note3URNStr,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("ListBursts note3: %v", err)
	}
	if len(note3Bursts.Bursts) == 0 {
		t.Fatal("note3: expected at least one burst")
	}

	var combined strings.Builder
	for _, b := range note3Bursts.Bursts {
		combined.WriteString(b.Text)
		combined.WriteString(" ")
		combined.WriteString(b.Tokens)
		combined.WriteString(" ")
	}
	burstText := strings.ToLower(combined.String())

	for _, keyword := range []string{"authentication", "gateway", "request"} {
		if !strings.Contains(burstText, keyword) {
			t.Errorf("note3 burst content does not contain expected keyword %q", keyword)
		}
	}

	// ── Step 13: ListCandidates with note_urn filter ───────────────────────────

	note1Cands, err := ctxSvc.ListCandidates(ctx, &pb.ListCandidatesRequest{
		NoteUrn:  note1URNStr,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("ListCandidates (note_urn filter): %v", err)
	}
	for _, c := range note1Cands.Candidates {
		if c.NoteUrnA != note1URNStr && c.NoteUrnB != note1URNStr {
			t.Errorf("ListCandidates (note_urn filter): candidate %s does not involve note1", c.Id)
		}
	}

	t.Logf("candidates involving note1: %d", len(note1Cands.Candidates))

	// ── Step 14: PromoteCandidate — promote the second candidate (if present) ──
	// We already dismissed the first candidate. Find one that is still pending.

	var promotableID string
	for _, c := range listCandResp.Candidates {
		if c.Id != candidateID {
			promotableID = c.Id
			break
		}
	}

	if promotableID != "" {
		promoteResp, err := ctxSvc.PromoteCandidate(ctx, &pb.PromoteCandidateRequest{
			Id:          promotableID,
			Label:       "auth-gateway-relation",
			Direction:   "both",
			ReviewerUrn: authorURNStr,
		})
		if err != nil {
			t.Fatalf("PromoteCandidate %q: %v", promotableID, err)
		}
		if promoteResp.Candidate == nil {
			t.Fatal("PromoteCandidate: response.candidate is nil")
		}
		if promoteResp.Candidate.Status != "promoted" {
			t.Errorf("PromoteCandidate: status = %q, want promoted", promoteResp.Candidate.Status)
		}
		t.Logf("promoted candidate: anchor_a=%q anchor_b=%q link_a_to_b=%q",
			promoteResp.AnchorAId, promoteResp.AnchorBId, promoteResp.LinkAToB)

		// Verify it no longer appears in a pending-only query.
		pendingAfter, err := ctxSvc.ListCandidates(ctx, &pb.ListCandidatesRequest{
			ProjectUrn: projURNStr,
			Status:     "pending",
			PageSize:   100,
		})
		if err != nil {
			t.Fatalf("ListCandidates (pending after promote): %v", err)
		}
		for _, c := range pendingAfter.Candidates {
			if c.Id == promotableID {
				t.Errorf("promoted candidate %s still appears in pending list", promotableID)
			}
		}

		// Confirm it appears in a promoted-only query.
		promotedList, err := ctxSvc.ListCandidates(ctx, &pb.ListCandidatesRequest{
			ProjectUrn: projURNStr,
			Status:     "promoted",
			PageSize:   100,
		})
		if err != nil {
			t.Fatalf("ListCandidates (promoted): %v", err)
		}
		foundPromoted := false
		for _, c := range promotedList.Candidates {
			if c.Id == promotableID {
				foundPromoted = true
				break
			}
		}
		if !foundPromoted {
			t.Errorf("promoted candidate %s not found in promoted list", promotableID)
		}
	} else {
		t.Log("only one candidate detected — skipping PromoteCandidate step")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestContextGRPC_GetStats_Empty
//
// Validates that GetStats returns a well-formed zero response when no bursts
// or candidates exist (empty server, no events appended).
// ─────────────────────────────────────────────────────────────────────────────

func TestContextGRPC_GetStats_Empty(t *testing.T) {
	grpcAddr, _, stop := startSQLiteServer(t)
	defer stop()

	conn := dialGRPC(t, grpcAddr)
	ctxSvc := conn.Context()

	ctx := context.Background()

	resp, err := ctxSvc.GetStats(ctx, &pb.GetStatsRequest{})
	if err != nil {
		t.Fatalf("GetStats on empty server: %v", err)
	}
	if resp.Stats == nil {
		t.Fatal("GetStats: stats is nil on empty server")
	}
	if resp.Stats.BurstsTotal != 0 {
		t.Errorf("empty server: bursts_total = %d, want 0", resp.Stats.BurstsTotal)
	}
	if resp.Stats.CandidatesPending != 0 {
		t.Errorf("empty server: candidates_pending = %d, want 0", resp.Stats.CandidatesPending)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestContextGRPC_ListBursts_RequiresNoteURN
//
// Validates that ListBursts returns INVALID_ARGUMENT when note_urn is absent.
// ─────────────────────────────────────────────────────────────────────────────

func TestContextGRPC_ListBursts_RequiresNoteURN(t *testing.T) {
	grpcAddr, _, stop := startSQLiteServer(t)
	defer stop()

	conn := dialGRPC(t, grpcAddr)
	ctxSvc := conn.Context()

	_, err := ctxSvc.ListBursts(context.Background(), &pb.ListBurstsRequest{})
	if err == nil {
		t.Fatal("expected error for missing note_urn, got nil")
	}
	if !strings.Contains(err.Error(), "InvalidArgument") &&
		!strings.Contains(err.Error(), "note_urn") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestContextGRPC_GetBurst_NotFound
//
// Validates that GetBurst returns NOT_FOUND for a non-existent ID.
// ─────────────────────────────────────────────────────────────────────────────

func TestContextGRPC_GetBurst_NotFound(t *testing.T) {
	grpcAddr, _, stop := startSQLiteServer(t)
	defer stop()

	conn := dialGRPC(t, grpcAddr)
	ctxSvc := conn.Context()

	_, err := ctxSvc.GetBurst(context.Background(), &pb.GetBurstRequest{
		Id: "00000000-0000-7000-8000-000000000000",
	})
	if err == nil {
		t.Fatal("expected NOT_FOUND for non-existent burst, got nil")
	}
	if !strings.Contains(err.Error(), "NotFound") &&
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("expected NotFound error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestContextGRPC_SetProjectConfig_ResetToDefault
//
// Validates that passing 0 for a cap resets it to the global default (nil in
// the DB), and that GetProjectConfig reflects this as a 0 value in the proto
// (since proto3 has no null for scalars — 0 is the "use default" sentinel).
// ─────────────────────────────────────────────────────────────────────────────

func TestContextGRPC_SetProjectConfig_ResetToDefault(t *testing.T) {
	grpcAddr, _, stop := startSQLiteServer(t)
	defer stop()

	conn := dialGRPC(t, grpcAddr)
	ctxSvc := conn.Context()

	ctx := context.Background()
	const projURN = "urn:notx:proj:33333333-3333-7333-8333-333333333333"

	// Set a non-zero config first.
	_, err := ctxSvc.SetProjectConfig(ctx, &pb.SetProjectConfigRequest{
		ProjectUrn:               projURN,
		BurstMaxPerNotePerDay:    100,
		BurstMaxPerProjectPerDay: 1000,
	})
	if err != nil {
		t.Fatalf("SetProjectConfig (set): %v", err)
	}

	// Reset both caps to 0 (global default).
	resetResp, err := ctxSvc.SetProjectConfig(ctx, &pb.SetProjectConfigRequest{
		ProjectUrn:               projURN,
		BurstMaxPerNotePerDay:    0,
		BurstMaxPerProjectPerDay: 0,
	})
	if err != nil {
		t.Fatalf("SetProjectConfig (reset): %v", err)
	}
	if resetResp.Config.BurstMaxPerNotePerDay != 0 {
		t.Errorf("after reset: burst_max_per_note_per_day = %d, want 0",
			resetResp.Config.BurstMaxPerNotePerDay)
	}

	// GetProjectConfig should reflect the reset state.
	getResp, err := ctxSvc.GetProjectConfig(ctx, &pb.GetProjectConfigRequest{
		ProjectUrn: projURN,
	})
	if err != nil {
		t.Fatalf("GetProjectConfig after reset: %v", err)
	}
	if getResp.Config.BurstMaxPerNotePerDay != 0 {
		t.Errorf("GetProjectConfig after reset: burst_max_per_note_per_day = %d, want 0",
			getResp.Config.BurstMaxPerNotePerDay)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers used by tests in this file only
// ─────────────────────────────────────────────────────────────────────────────

// projectURNForContext builds a deterministic project URN for context tests.
// Avoids collisions with URNs used in other smoke tests.
func projectURNForContext(suffix string) core.URN {
	return core.MustParseURN("urn:notx:proj:cccccccc-cccc-7ccc-8ccc-" + suffix)
}

// noteURNForContext builds a deterministic note URN for context tests.
func noteURNForContext(suffix string) core.URN {
	return core.MustParseURN("urn:notx:note:cccccccc-cccc-7ccc-8ccc-" + suffix)
}

// appendTestEvent is a thin wrapper that appends a single event with the given
// text lines via NoteService and fails the test on error.
func appendTestEvent(
	t *testing.T,
	ctx context.Context,
	notes pb.NoteServiceClient,
	noteURN, authorURN string,
	seq int,
	lines []string,
) {
	t.Helper()
	entries := make([]*pb.LineEntry, len(lines))
	for i, l := range lines {
		entries[i] = &pb.LineEntry{Op: 0, LineNumber: int32(i + 1), Content: l}
	}
	_, err := notes.AppendEvent(ctx, &pb.AppendEventRequest{
		Event: &pb.Event{
			NoteUrn:   noteURN,
			Sequence:  int32(seq),
			AuthorUrn: authorURN,
			Entries:   entries,
		},
	})
	if err != nil {
		t.Fatalf("AppendEvent note=%s seq=%d: %v", noteURN, seq, err)
	}
}

// createTestNote creates a note via NoteService and fails the test on error.
func createTestNote(
	t *testing.T,
	ctx context.Context,
	notes pb.NoteServiceClient,
	urn, name, projectURN string,
) {
	t.Helper()
	_, err := notes.CreateNote(ctx, &pb.CreateNoteRequest{
		Header: &pb.NoteHeader{
			Urn:        urn,
			Name:       name,
			NoteType:   pb.NoteType_NOTE_TYPE_NORMAL,
			ProjectUrn: projectURN,
		},
	})
	if err != nil {
		t.Fatalf("CreateNote %q: %v", name, err)
	}
}

// pollCandidates polls ListCandidates until at least minCount candidates appear
// or the deadline is exceeded, then returns the final result.
func pollCandidates(
	t *testing.T,
	ctx context.Context,
	ctxSvc pb.ContextServiceClient,
	projectURN string,
	minCount int,
	timeout time.Duration,
) []*pb.CandidateRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []*pb.CandidateRecord
	for time.Now().Before(deadline) {
		resp, err := ctxSvc.ListCandidates(ctx, &pb.ListCandidatesRequest{
			ProjectUrn: projectURN,
			Status:     "pending",
			PageSize:   100,
		})
		if err != nil {
			t.Fatalf("pollCandidates ListCandidates: %v", err)
		}
		last = resp.Candidates
		if len(last) >= minCount {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return last
}

// Ensure unused imports from helpers don't cause compile errors.
var (
	_ = projectURNForContext
	_ = noteURNForContext
	_ = appendTestEvent
	_ = createTestNote
	_ = pollCandidates
	_ = repo.AppendEventOptions{}
)
