package smoke

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/repo/sqlite"
)

// startSQLiteHTTPServer spins up a notx server backed by a temporary SQLite
// provider on two random free ports (HTTP + gRPC). Both HTTP and gRPC are
// enabled so the context service (burst extraction, search) is fully wired.
//
// Returns the HTTP base URL and a stop function the caller must invoke.
func startSQLiteHTTPServer(t *testing.T) (baseURL string, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	dir := t.TempDir()
	provider, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("startSQLiteHTTPServer: sqlite.New: %v", err)
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
		provider, // ContextRepository  ← enables burst extraction + search
		provider, // LinkRepository
		log,
	)
	if err != nil {
		provider.Close()
		t.Fatalf("startSQLiteHTTPServer: server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithContext(ctx) }()

	// Wait until the HTTP port is accepting connections (up to 3 s).
	addr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
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

	return fmt.Sprintf("http://127.0.0.1:%d", httpPort), stop
}

// ─────────────────────────────────────────────────────────────────────────────
// Wire types (local — kept self-contained)
// ─────────────────────────────────────────────────────────────────────────────

type matchLocation struct {
	Line      int `json:"line"`
	CharStart int `json:"char_start"`
	CharEnd   int `json:"char_end"`
}

type burstSearchResult struct {
	ID             string          `json:"id"`
	NoteURN        string          `json:"note_urn"`
	ProjectURN     string          `json:"project_urn,omitempty"`
	LineStart      int             `json:"line_start"`
	LineEnd        int             `json:"line_end"`
	Text           string          `json:"text"`
	Tokens         string          `json:"tokens,omitempty"`
	BM25Score      float32         `json:"bm25_score"`
	CreatedAt      string          `json:"created_at,omitempty"`
	MatchLocations []matchLocation `json:"match_locations,omitempty"`
}

type burstSearchResponse struct {
	Results []burstSearchResult `json:"results"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Smoke test
// ─────────────────────────────────────────────────────────────────────────────

// TestBurstSearch_CreateNoteAndFind is an end-to-end smoke test that verifies
// the full burst-search path including three matching modes:
//
//  1. Exact case   — "Challenges" matches the stored word exactly.
//  2. Lowercase    — "challenges" matches "Challenges" case-insensitively.
//  3. Prefix       — "Cha" matches "Challenges" via FTS5 prefix search.
//
// Setup:
//   - Start a SQLite-backed notx server (the memory provider does not implement
//     ContextRepository / burst extraction).
//   - Register a device (AutoApprove=true so it is immediately usable).
//   - Create a normal note and append one event containing the word "Challenges".
//     SQLite AppendEvent calls extractBurstsForEvent synchronously so bursts
//     are visible immediately after the call returns.
func TestBurstSearch_CreateNoteAndFind(t *testing.T) {
	baseURL, stop := startSQLiteHTTPServer(t)
	defer stop()

	const (
		noteURN   = "urn:notx:note:aabbccdd-0000-4000-8000-000000000001"
		noteName  = "Burst Search Smoke Note"
		authorURN = "urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
	)

	// ── Step 0: register a device ─────────────────────────────────────────────
	deviceID := registerTestDevice(t, http.DefaultClient, baseURL)

	// ── Step 1: create the note ───────────────────────────────────────────────
	createResp := postJSONWithDeviceID(t, http.DefaultClient, baseURL+"/v1/notes", deviceID, createNoteRequest{
		URN:      noteURN,
		Name:     noteName,
		NoteType: "normal",
	})
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("create note: expected 201, got %d — %s", createResp.StatusCode, body)
	}
	createResp.Body.Close()

	// ── Step 2: append an event whose content contains "Challenges" ───────────
	// The SQLite provider extracts bursts synchronously inside AppendEvent, so
	// the burst is immediately visible after this call returns.
	//
	// "Challenges" appears at the very start of line 1 (char 0..10).
	// We use this known position to assert MatchLocations in each sub-test.
	const (
		matchWord      = "Challenges"
		matchLine      = 1  // 1-based line within the burst text
		matchCharStart = 0  // byte offset in that line
		matchCharEnd   = 10 // len("Challenges")
	)
	eventResp := postJSONWithDeviceID(t, http.DefaultClient, baseURL+"/v1/events", deviceID, appendEventRequest{
		NoteURN:   noteURN,
		Sequence:  1,
		AuthorURN: authorURN,
		Entries: []lineEntryJSON{
			{Op: "set", LineNumber: 1, Content: "Challenges in distributed systems require careful planning."},
			{Op: "set", LineNumber: 2, Content: "A second line to give the burst some body and context."},
			{Op: "set", LineNumber: 3, Content: "Third line: more content so the burst extractor has enough material."},
		},
	})
	if eventResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(eventResp.Body)
		eventResp.Body.Close()
		t.Fatalf("append event: expected 201, got %d — %s", eventResp.StatusCode, body)
	}
	eventResp.Body.Close()

	// ── Step 3: run all three search variants ─────────────────────────────────
	//
	// Each sub-test issues a search request and asserts that the note we just
	// created appears in the results.  They share the same server and burst
	// data so they run as sub-tests of this function rather than separate
	// top-level tests.
	// Each case carries the query string and the expected match position within
	// the burst text.  For prefix queries ("Cha") the match location still
	// reports the full prefix span [0, 3) — not the full word — because
	// FindMatchLocations searches for the literal query string.
	searchCases := []struct {
		name          string
		query         string
		wantLine      int
		wantCharStart int
		wantCharEnd   int
	}{
		// "Challenges" at line 1, chars [0, 10)
		{"exact_case", "Challenges", matchLine, matchCharStart, matchCharEnd},
		// same position, queried in lowercase
		{"lowercase", "challenges", matchLine, matchCharStart, matchCharEnd},
		// prefix "Cha" is a substring of "Challenges" starting at char 0
		{"prefix", "Cha", matchLine, 0, 3},
	}

	for _, tc := range searchCases {
		tc := tc // capture
		t.Run(tc.name, func(t *testing.T) {
			searchURL := fmt.Sprintf("%s/v1/context/bursts/search?q=%s", baseURL, tc.query)
			searchReq, err := newGetRequestWithDeviceID(searchURL, deviceID)
			if err != nil {
				t.Fatalf("build search request: %v", err)
			}

			searchResp, err := http.DefaultClient.Do(searchReq)
			if err != nil {
				t.Fatalf("GET bursts/search: %v", err)
			}

			if searchResp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(searchResp.Body)
				searchResp.Body.Close()
				t.Fatalf("GET bursts/search: expected 200, got %d — %s", searchResp.StatusCode, body)
			}

			var result burstSearchResponse
			decodeBody(t, searchResp, &result)

			if len(result.Results) == 0 {
				t.Fatalf("q=%q: expected at least 1 result, got 0 — search may not support this match mode", tc.query)
			}

			found := false
			for _, r := range result.Results {
				if r.NoteURN != noteURN {
					continue
				}
				found = true

				if r.ID == "" {
					t.Errorf("q=%q: burst result id is empty", tc.query)
				}
				if r.Text == "" {
					t.Errorf("q=%q: burst result text is empty", tc.query)
				}

				// ── match_locations assertions ────────────────────────────
				if len(r.MatchLocations) == 0 {
					t.Errorf("q=%q: expected match_locations to be non-empty", tc.query)
				} else {
					// The first location must point at the known position.
					first := r.MatchLocations[0]
					if first.Line != tc.wantLine {
						t.Errorf("q=%q: match_locations[0].line = %d, want %d",
							tc.query, first.Line, tc.wantLine)
					}
					if first.CharStart != tc.wantCharStart {
						t.Errorf("q=%q: match_locations[0].char_start = %d, want %d",
							tc.query, first.CharStart, tc.wantCharStart)
					}
					if first.CharEnd != tc.wantCharEnd {
						t.Errorf("q=%q: match_locations[0].char_end = %d, want %d",
							tc.query, first.CharEnd, tc.wantCharEnd)
					}
				}

				t.Logf("q=%q hit: id=%s score=%.4f locations=%+v text=%q",
					tc.query, r.ID, r.BM25Score, r.MatchLocations, r.Text)
				break
			}

			if !found {
				t.Errorf("q=%q: no result with note_urn=%q — got %d results: %+v",
					tc.query, noteURN, len(result.Results), result.Results)
			}
		})
	}
}

// newGetRequestWithDeviceID builds a GET *http.Request with an X-Device-ID header.
func newGetRequestWithDeviceID(url, deviceID string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if err != nil {
		return nil, err
	}
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}
	return req, nil
}
