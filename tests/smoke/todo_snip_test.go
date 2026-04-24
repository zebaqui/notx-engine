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

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/repo/sqlite"
	"github.com/zebaqui/notx-engine/snip"
	"github.com/zebaqui/notx-engine/snip/plugins/todo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Server helpers (SQLite + todo plugin)
// ─────────────────────────────────────────────────────────────────────────────

// startTodoServer spins up a full notx server backed by an in-process SQLite
// database with the todo snip plugin registered.  It returns the HTTP base URL
// and a stop function the caller must defer.
func startTodoServer(t *testing.T) (baseURL string, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	dir := t.TempDir()
	provider, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("startTodoServer: sqlite.New: %v", err)
	}

	cfg := config.Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = true
	cfg.HTTPPort = httpPort
	cfg.GRPCPort = grpcPort

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	plugins := []snip.SnipPlugin{todo.New()}

	srv, err := server.New(cfg, provider, provider, provider, provider, log, plugins)
	if err != nil {
		provider.Close()
		t.Fatalf("startTodoServer: server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithContext(ctx) }()

	// Wait for HTTP to accept connections (up to 3 s).
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
			t.Log("warning: todo server did not stop within 5 s")
		}
		provider.Close()
	}

	return fmt.Sprintf("http://127.0.0.1:%d", httpPort), stop
}

// ─────────────────────────────────────────────────────────────────────────────
// Wire types (local to this test file)
// ─────────────────────────────────────────────────────────────────────────────

type createTodoNoteRequest struct {
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NoteType string `json:"note_type"`
	SnipType string `json:"snip_type,omitempty"`
}

type todoItemJSON struct {
	NoteURN       string `json:"note_urn"`
	LineNumber    int    `json:"line_number"`
	Text          string `json:"text"`
	Status        string `json:"status"`
	CheckboxState string `json:"checkbox_state"`
}

type listTodosResponse struct {
	Todos []todoItemJSON `json:"todos"`
}

type updateTodoStatusRequest struct {
	NoteURN    string `json:"note_urn"`
	LineNumber int    `json:"line_number"`
	Status     string `json:"status"`
}

type updateTodoStatusResponse struct {
	Updated bool   `json:"updated"`
	Status  string `json:"status"`
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

func patchJSON(t *testing.T, client *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("patchJSON: marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("patchJSON: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("patchJSON %s: %v", url, err)
	}
	return resp
}

// ─────────────────────────────────────────────────────────────────────────────
// TestTodoSnipLifecycle
//
// Full end-to-end lifecycle for the todo snip plugin:
//
//  1. Start a notx server backed by SQLite with the todo plugin registered.
//  2. Create a note with snip_type="todo".
//  3. Append an event whose content contains two Markdown checkboxes.
//  4. GET /v1/snips/todo — verify both todos exist with status "backlog".
//  5. PATCH /v1/snips/todo — move the first todo to "doing" (in-progress).
//  6. GET /v1/snips/todo?status=doing — verify the todo appears in-progress.
//  7. PATCH /v1/snips/todo — move the first todo to "done".
//  8. GET /v1/snips/todo?status=done — verify the todo is done.
// ─────────────────────────────────────────────────────────────────────────────

func TestTodoSnipLifecycle(t *testing.T) {
	baseURL, stop := startTodoServer(t)
	defer stop()

	const (
		noteURN   = "urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-2c3d4e5f6a7b"
		noteName  = "Todo Lifecycle Note"
		authorURN = "urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
	)

	client := http.DefaultClient

	// ── Step 1: create a note with snip_type="todo" ───────────────────────────
	createResp := postJSON(t, baseURL+"/v1/notes", createTodoNoteRequest{
		URN:      noteURN,
		Name:     noteName,
		NoteType: "normal",
		SnipType: "todo",
	})
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("create note: expected 201, got %d — %s", createResp.StatusCode, body)
	}
	createResp.Body.Close()

	// ── Step 2: append an event with two Markdown checkboxes ──────────────────
	//
	// The content will be:
	//   line 1: "# My Todos"
	//   line 2: "- [ ] Write the integration test"
	//   line 3: "- [ ] Review the integration test"
	//
	// The todo plugin indexes line numbers 0-based from the raw content string,
	// so checkbox on content line 1 (0-indexed) → line_number=1,
	// and checkbox on content line 2 (0-indexed) → line_number=2.

	eventResp := postJSON(t, baseURL+"/v1/events", appendEventRequest{
		NoteURN:   noteURN,
		Sequence:  1,
		AuthorURN: authorURN,
		Entries: []lineEntryJSON{
			{Op: "set", LineNumber: 1, Content: "# My Todos"},
			{Op: "set", LineNumber: 2, Content: "- [ ] Write the integration test"},
			{Op: "set", LineNumber: 3, Content: "- [ ] Review the integration test"},
		},
	})
	if eventResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(eventResp.Body)
		eventResp.Body.Close()
		t.Fatalf("append event: expected 201, got %d — %s", eventResp.StatusCode, body)
	}
	eventResp.Body.Close()

	// Give the plugin's synchronous hook a moment to finish (it runs inline,
	// so this is just a safety margin for any internal write flushing).
	time.Sleep(50 * time.Millisecond)

	// ── Step 3: list todos — both should be in "backlog" ─────────────────────
	var listed listTodosResponse
	{
		resp := doGet(t, baseURL+"/v1/snips/todo")
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET /v1/snips/todo: expected 200, got %d — %s", resp.StatusCode, body)
		}
		decodeBody(t, resp, &listed)

		if len(listed.Todos) != 2 {
			t.Fatalf("expected 2 todos, got %d: %+v", len(listed.Todos), listed.Todos)
		}

		for _, item := range listed.Todos {
			if item.NoteURN != noteURN {
				t.Errorf("todo note_urn = %q, want %q", item.NoteURN, noteURN)
			}
			if item.Status != "backlog" {
				t.Errorf("todo %q status = %q, want backlog", item.Text, item.Status)
			}
			if item.CheckboxState != "open" {
				t.Errorf("todo %q checkbox_state = %q, want open", item.Text, item.CheckboxState)
			}
		}

		t.Logf("todos after creation: %+v", listed.Todos)
	}

	// Identify the line number of the first todo ("Write the integration test").
	// Read it from the listed todos rather than hardcoding, since the line number
	// in the DB matches the LineNumber from the appended event (1-based).
	var firstTodoLine int
	for _, item := range listed.Todos {
		if item.Text == "Write the integration test" {
			firstTodoLine = item.LineNumber
			break
		}
	}
	if firstTodoLine == 0 {
		t.Fatalf("could not find 'Write the integration test' in listed todos")
	}

	// ── Step 4: move the first todo to "doing" (in-progress) ─────────────────
	{
		patchResp := patchJSON(t, client, baseURL+"/v1/snips/todo", updateTodoStatusRequest{
			NoteURN:    noteURN,
			LineNumber: firstTodoLine,
			Status:     "doing",
		})
		if patchResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(patchResp.Body)
			patchResp.Body.Close()
			t.Fatalf("PATCH /v1/snips/todo (doing): expected 200, got %d — %s", patchResp.StatusCode, body)
		}
		var patched updateTodoStatusResponse
		decodeBody(t, patchResp, &patched)

		if !patched.Updated {
			t.Errorf("PATCH /v1/snips/todo: expected updated=true, got false")
		}
		if patched.Status != "doing" {
			t.Errorf("PATCH /v1/snips/todo: returned status = %q, want doing", patched.Status)
		}
	}

	// ── Step 5: verify in-progress via GET /v1/snips/todo?status=doing ────────
	{
		resp := doGet(t, baseURL+"/v1/snips/todo?status=doing")
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET /v1/snips/todo?status=doing: expected 200, got %d — %s", resp.StatusCode, body)
		}
		var listed listTodosResponse
		decodeBody(t, resp, &listed)

		if len(listed.Todos) != 1 {
			t.Fatalf("expected 1 doing todo, got %d: %+v", len(listed.Todos), listed.Todos)
		}

		got := listed.Todos[0]
		if got.Status != "doing" {
			t.Errorf("todo status = %q, want doing", got.Status)
		}
		if got.LineNumber != firstTodoLine {
			t.Errorf("todo line_number = %d, want %d", got.LineNumber, firstTodoLine)
		}

		t.Logf("in-progress todo: %+v", got)
	}

	// ── Step 6: mark the first todo as "done" ────────────────────────────────
	{
		patchResp := patchJSON(t, client, baseURL+"/v1/snips/todo", updateTodoStatusRequest{
			NoteURN:    noteURN,
			LineNumber: firstTodoLine,
			Status:     "done",
		})
		if patchResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(patchResp.Body)
			patchResp.Body.Close()
			t.Fatalf("PATCH /v1/snips/todo (done): expected 200, got %d — %s", patchResp.StatusCode, body)
		}
		var patched updateTodoStatusResponse
		decodeBody(t, patchResp, &patched)

		if !patched.Updated {
			t.Errorf("PATCH /v1/snips/todo: expected updated=true")
		}
		if patched.Status != "done" {
			t.Errorf("PATCH /v1/snips/todo: returned status = %q, want done", patched.Status)
		}
	}

	// ── Step 7: verify done via GET /v1/snips/todo?status=done ───────────────
	{
		resp := doGet(t, baseURL+"/v1/snips/todo?status=done")
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET /v1/snips/todo?status=done: expected 200, got %d — %s", resp.StatusCode, body)
		}
		var listed listTodosResponse
		decodeBody(t, resp, &listed)

		if len(listed.Todos) != 1 {
			t.Fatalf("expected 1 done todo, got %d: %+v", len(listed.Todos), listed.Todos)
		}

		got := listed.Todos[0]
		if got.Status != "done" {
			t.Errorf("todo status = %q, want done", got.Status)
		}
		if got.LineNumber != firstTodoLine {
			t.Errorf("todo line_number = %d, want %d", got.LineNumber, firstTodoLine)
		}
		if got.Text != "Write the integration test" {
			t.Errorf("todo text = %q, want %q", got.Text, "Write the integration test")
		}

		t.Logf("done todo: %+v", got)
	}

	// ── Step 8: sanity-check the second todo is still in backlog ──────────────
	{
		resp := doGet(t, baseURL+"/v1/snips/todo?status=backlog")
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET /v1/snips/todo?status=backlog: expected 200, got %d — %s", resp.StatusCode, body)
		}
		var listed listTodosResponse
		decodeBody(t, resp, &listed)

		if len(listed.Todos) != 1 {
			t.Fatalf("expected 1 backlog todo, got %d: %+v", len(listed.Todos), listed.Todos)
		}

		got := listed.Todos[0]
		if got.Status != "backlog" {
			t.Errorf("second todo status = %q, want backlog", got.Status)
		}
		if got.Text != "Review the integration test" {
			t.Errorf("second todo text = %q, want %q", got.Text, "Review the integration test")
		}

		t.Logf("remaining backlog todo: %+v", got)
	}
}
