package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Pull / read response types  (mirrors pull.go's local types)
// ─────────────────────────────────────────────────────────────────────────────

// NoteHeader is the note metadata shape returned by the cloud engine.
type NoteHeader struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	ProjectURN string `json:"project_urn"`
	FolderURN  string `json:"folder_urn"`
	Deleted    bool   `json:"deleted"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// NoteGetResponse is GET /engine/v1/notes/:urn
type NoteGetResponse struct {
	Header  NoteHeader `json:"header"`
	Content string     `json:"content"`
}

// NoteListResponse is GET /engine/v1/notes
type NoteListResponse struct {
	Notes         []NoteHeader `json:"notes"`
	NextPageToken string       `json:"next_page_token"`
}

// ProjectItem is a single project record.
type ProjectItem struct {
	URN  string `json:"urn"`
	Name string `json:"name"`
}

// ProjectListResponse is GET /engine/v1/projects
type ProjectListResponse struct {
	Projects      []ProjectItem `json:"projects"`
	NextPageToken string        `json:"next_page_token"`
}

// FolderItem is a single folder record.
type FolderItem struct {
	URN        string `json:"urn"`
	ProjectURN string `json:"project_urn"`
	Name       string `json:"name"`
}

// FolderListResponse is GET /engine/v1/folders
type FolderListResponse struct {
	Folders       []FolderItem `json:"folders"`
	NextPageToken string       `json:"next_page_token"`
}

// LineEntry is one line operation inside an event.
type LineEntry struct {
	Op         string `json:"op"`
	LineNumber int    `json:"line_number"`
	Content    string `json:"content"`
}

// NoteEvent is a single event in the event stream.
type NoteEvent struct {
	URN       string      `json:"urn"`
	NoteURN   string      `json:"note_urn,omitempty"`
	Sequence  int         `json:"sequence"`
	AuthorURN string      `json:"author_urn"`
	CreatedAt string      `json:"created_at"`
	Entries   []LineEntry `json:"entries"`
}

// EventsResponse is GET /engine/v1/notes/:urn/events
type EventsResponse struct {
	NoteURN string      `json:"note_urn"`
	Events  []NoteEvent `json:"events"`
	Count   int         `json:"count"`
}

// NoteClient is an HTTP client for cloud note operations.
// It targets the notx cloud REST API and authenticates via a Bearer JWT.
type NoteClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewNoteClient returns a NoteClient that talks to CloudBaseURL() using the
// provided JWT bearer token.
func NewNoteClient(token string) *NoteClient {
	return &NoteClient{
		baseURL: CloudBaseURL(),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Request / Response types
// ─────────────────────────────────────────────────────────────────────────────

// createNoteRequest matches the engine's flat POST /v1/notes body exactly.
type createNoteRequest struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	ProjectURN string `json:"project_urn,omitempty"`
	FolderURN  string `json:"folder_urn,omitempty"`
}

// createNoteResponse matches the engine's {"note": {...}} response shape.
type createNoteResponse struct {
	Note *struct {
		URN string `json:"urn"`
	} `json:"note,omitempty"`
	Error string `json:"error,omitempty"`
}

type replaceContentRequest struct {
	Content   string `json:"content"`
	AuthorURN string `json:"author_urn,omitempty"`
}

type replaceContentResponse struct {
	Sequence int    `json:"sequence"`
	Changed  bool   `json:"changed"`
	NoteURN  string `json:"note_urn,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Methods
// ─────────────────────────────────────────────────────────────────────────────

// CreateNote creates a new note on the cloud backend.
// It returns the URN assigned by the server (which may differ from the locally
// generated URN if the server overrides it).
func (c *NoteClient) CreateNote(
	ctx context.Context,
	noteURN, name, noteType string,
	projectURN, folderURN string,
) (urn string, err error) {
	reqBody := createNoteRequest{
		URN:        noteURN,
		Name:       name,
		NoteType:   noteType,
		ProjectURN: projectURN,
		FolderURN:  folderURN,
	}

	var resp createNoteResponse
	if err := c.doJSON(ctx, http.MethodPost, "/engine/v1/notes", reqBody, &resp); err != nil {
		return "", fmt.Errorf("cloud create note: %w", err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("cloud create note: %s", resp.Error)
	}

	if resp.Note != nil && resp.Note.URN != "" {
		return resp.Note.URN, nil
	}
	// Fall back to the locally generated URN if the server didn't echo it back.
	return noteURN, nil
}

// ReplaceContent sends the full note content to the cloud backend.
// The server diffs against the current state and appends only the changed lines
// as a new event. It returns the new sequence number and whether any lines
// actually changed.
func (c *NoteClient) ReplaceContent(
	ctx context.Context,
	noteURN, content, authorURN string,
) (sequence int, changed bool, err error) {
	reqBody := replaceContentRequest{
		Content:   content,
		AuthorURN: authorURN,
	}

	path := fmt.Sprintf("/engine/v1/notes/%s/content", percentEncodeURN(noteURN))

	var resp replaceContentResponse
	if err := c.doJSON(ctx, http.MethodPost, path, reqBody, &resp); err != nil {
		return 0, false, fmt.Errorf("cloud replace content: %w", err)
	}
	if resp.Error != "" {
		return 0, false, fmt.Errorf("cloud replace content: %s", resp.Error)
	}

	return resp.Sequence, resp.Changed, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Pull / read methods
// ─────────────────────────────────────────────────────────────────────────────

// GetNote fetches GET /engine/v1/notes/:urn and returns the header + content.
func (c *NoteClient) GetNote(ctx context.Context, urn string) (*NoteGetResponse, error) {
	path := "/engine/v1/notes/" + percentEncodeURN(urn)
	var resp NoteGetResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("cloud get note: %w", err)
	}
	return &resp, nil
}

// ListNotes fetches GET /engine/v1/notes?page_size=500 and returns all headers.
func (c *NoteClient) ListNotes(ctx context.Context) ([]NoteHeader, error) {
	var resp NoteListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/engine/v1/notes?page_size=500", nil, &resp); err != nil {
		return nil, fmt.Errorf("cloud list notes: %w", err)
	}
	return resp.Notes, nil
}

// ListProjects fetches GET /engine/v1/projects?page_size=500.
func (c *NoteClient) ListProjects(ctx context.Context) ([]ProjectItem, error) {
	var resp ProjectListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/engine/v1/projects?page_size=500", nil, &resp); err != nil {
		return nil, fmt.Errorf("cloud list projects: %w", err)
	}
	return resp.Projects, nil
}

// ListFolders fetches GET /engine/v1/folders?page_size=500.
func (c *NoteClient) ListFolders(ctx context.Context) ([]FolderItem, error) {
	var resp FolderListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/engine/v1/folders?page_size=500", nil, &resp); err != nil {
		return nil, fmt.Errorf("cloud list folders: %w", err)
	}
	return resp.Folders, nil
}

// GetEvents fetches GET /engine/v1/notes/:urn/events.
func (c *NoteClient) GetEvents(ctx context.Context, urn string) (*EventsResponse, error) {
	path := "/engine/v1/notes/" + percentEncodeURN(urn) + "/events"
	var resp EventsResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("cloud get events: %w", err)
	}
	return &resp, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// doJSON marshals reqBody, sends the request with the Bearer token, reads the
// full response body as bytes, then either surfaces an error (non-2xx) or
// decodes into respDst (2xx). Reading bytes first lets us include the server's
// "error" field in the returned error message on failure.
func (c *NoteClient) doJSON(ctx context.Context, method, path string, reqBody, respDst any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	// Read the full body so we can use it for both error reporting and decoding.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response from %s %s: %w", method, url, err)
	}

	if resp.StatusCode >= 400 {
		// Try to extract a human-readable "error" field from the JSON body.
		var errResp struct {
			Error string `json:"error"`
		}
		if jsonErr := json.Unmarshal(raw, &errResp); jsonErr == nil && errResp.Error != "" {
			return fmt.Errorf("%s %s: %s (HTTP %d)", method, url, errResp.Error, resp.StatusCode)
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if respDst != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, respDst); err != nil {
			return fmt.Errorf("decode response from %s %s: %w", method, url, err)
		}
	}

	return nil
}

// percentEncodeURN replaces ':' with '%3A' for safe use in URL path segments.
func percentEncodeURN(urn string) string {
	result := make([]byte, 0, len(urn)+8)
	for i := 0; i < len(urn); i++ {
		if urn[i] == ':' {
			result = append(result, '%', '3', 'A')
		} else {
			result = append(result, urn[i])
		}
	}
	return string(result)
}

// ─────────────────────────────────────────────────────────────────────────────
// Receive (sync) types and method
// ─────────────────────────────────────────────────────────────────────────────

// receiveNoteRequest is the request body for POST /engine/v1/notes/:urn/receive.
type receiveNoteRequest struct {
	Header NoteHeader  `json:"header"`
	Events []NoteEvent `json:"events"`
}

// receiveNoteResponse is the response body from POST /engine/v1/notes/:urn/receive.
type receiveNoteResponse struct {
	NoteURN      string `json:"note_urn"`
	EventsStored int    `json:"events_stored"`
	Error        string `json:"error,omitempty"`
}

// ReceiveNote pushes a full event stream for a note to the cloud engine via
// POST /engine/v1/notes/:urn/receive.
// Returns the number of events stored by the server.
func (c *NoteClient) ReceiveNote(ctx context.Context, header NoteHeader, events []NoteEvent) (int, error) {
	reqBody := receiveNoteRequest{
		Header: header,
		Events: events,
	}

	path := "/engine/v1/notes/" + percentEncodeURN(header.URN) + "/receive"
	var resp receiveNoteResponse
	if err := c.doJSON(ctx, http.MethodPost, path, reqBody, &resp); err != nil {
		return 0, fmt.Errorf("cloud receive note: %w", err)
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("cloud receive note: %s", resp.Error)
	}
	return resp.EventsStored, nil
}
