package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/internal/repo/memory"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/internal/server/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// freePort asks the OS for an available TCP port and returns it.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// startServer spins up a notx server backed by an in-memory provider on two
// random free ports, waits until the HTTP listener is accepting connections,
// and returns the HTTP base URL together with a cancel function the caller must
// invoke to stop the server.
func startServer(t *testing.T) (baseURL string, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	cfg := config.Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = true
	cfg.HTTPPort = httpPort
	cfg.GRPCPort = grpcPort

	provider := memory.New()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError, // keep test output clean
	}))

	srv, err := server.New(cfg, provider, log)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- srv.RunWithContext(ctx)
	}()

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
	}

	return fmt.Sprintf("http://127.0.0.1:%d", httpPort), stop
}

// postJSON sends a POST request with a JSON body and returns the response.
func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("postJSON marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b)) //nolint:noctx
	if err != nil {
		t.Fatalf("postJSON %s: %v", url, err)
	}
	return resp
}

// decodeBody reads the response body into dst and closes it.
func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode body: %v\nraw: %s", err, raw)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Wire types (mirrors handler.go — duplicated here to keep the test self-contained)
// ─────────────────────────────────────────────────────────────────────────────

type noteHeaderJSON struct {
	URN       string `json:"urn"`
	Name      string `json:"name"`
	NoteType  string `json:"note_type"`
	Deleted   bool   `json:"deleted"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type createNoteRequest struct {
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NoteType string `json:"note_type"`
}

type createNoteResponse struct {
	Note *noteHeaderJSON `json:"note"`
}

type lineEntryJSON struct {
	Op         string `json:"op"`
	LineNumber int    `json:"line_number"`
	Content    string `json:"content,omitempty"`
}

type appendEventRequest struct {
	NoteURN   string          `json:"note_urn"`
	Sequence  int             `json:"sequence"`
	AuthorURN string          `json:"author_urn"`
	Entries   []lineEntryJSON `json:"entries"`
}

type appendEventResponse struct {
	Sequence int `json:"sequence"`
}

type listNotesResponse struct {
	Notes         []*noteHeaderJSON `json:"notes"`
	NextPageToken string            `json:"next_page_token"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Smoke test
// ─────────────────────────────────────────────────────────────────────────────

// TestGetNotes is the single smoke test that exercises the full happy path:
//
//  1. Start a notx server backed by the in-memory provider.
//  2. Create a normal note via POST /v1/notes.
//  3. Append one event with content via POST /v1/events.
//  4. Call GET /v1/notes and assert the note appears in the list with the
//     correct URN, name, and note_type.
func TestGetNotes(t *testing.T) {
	baseURL, stop := startServer(t)
	defer stop()

	const (
		noteURN   = "notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
		noteName  = "Smoke Test Note"
		authorURN = "notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
	)

	// ── Step 1: create the note ───────────────────────────────────────────────
	createResp := postJSON(t, baseURL+"/v1/notes", createNoteRequest{
		URN:      noteURN,
		Name:     noteName,
		NoteType: "normal",
	})
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("create note: expected 201, got %d — %s", createResp.StatusCode, body)
	}

	var created createNoteResponse
	decodeBody(t, createResp, &created)

	if created.Note == nil {
		t.Fatal("create note: response.note is nil")
	}
	if created.Note.URN != noteURN {
		t.Errorf("create note: URN = %q, want %q", created.Note.URN, noteURN)
	}
	if created.Note.Name != noteName {
		t.Errorf("create note: Name = %q, want %q", created.Note.Name, noteName)
	}
	if created.Note.NoteType != "normal" {
		t.Errorf("create note: NoteType = %q, want %q", created.Note.NoteType, "normal")
	}

	// ── Step 2: append an event ───────────────────────────────────────────────
	eventResp := postJSON(t, baseURL+"/v1/events", appendEventRequest{
		NoteURN:   noteURN,
		Sequence:  1,
		AuthorURN: authorURN,
		Entries: []lineEntryJSON{
			{Op: "set", LineNumber: 1, Content: "Hello from the smoke test"},
		},
	})
	if eventResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(eventResp.Body)
		eventResp.Body.Close()
		t.Fatalf("append event: expected 201, got %d — %s", eventResp.StatusCode, body)
	}

	var appended appendEventResponse
	decodeBody(t, eventResp, &appended)

	if appended.Sequence != 1 {
		t.Errorf("append event: Sequence = %d, want 1", appended.Sequence)
	}

	// ── Step 3: list notes and verify ─────────────────────────────────────────
	listResp, err := http.Get(baseURL + "/v1/notes") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /v1/notes: %v", err)
	}
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		listResp.Body.Close()
		t.Fatalf("GET /v1/notes: expected 200, got %d — %s", listResp.StatusCode, body)
	}

	var listed listNotesResponse
	decodeBody(t, listResp, &listed)

	if len(listed.Notes) != 1 {
		t.Fatalf("GET /v1/notes: expected 1 note, got %d", len(listed.Notes))
	}

	got := listed.Notes[0]

	if got.URN != noteURN {
		t.Errorf("list notes[0].URN = %q, want %q", got.URN, noteURN)
	}
	if got.Name != noteName {
		t.Errorf("list notes[0].Name = %q, want %q", got.Name, noteName)
	}
	if got.NoteType != "normal" {
		t.Errorf("list notes[0].NoteType = %q, want %q", got.NoteType, "normal")
	}
	if got.Deleted {
		t.Errorf("list notes[0].Deleted = true, want false")
	}
}
