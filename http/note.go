package http

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
)

// ─────────────────────────────────────────────────────────────────────────────
// Notes — route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeNotes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListNotes(w, r)
	case http.MethodPost:
		h.handleCreateNote(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeNote(w http.ResponseWriter, r *http.Request) {
	// Strip /v1/notes/ prefix.
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/notes/")
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "note URN is required")
		return
	}

	// Sub-resource check: <urn>/events, <urn>/content
	if idx := strings.Index(trimmed, "/"); idx != -1 {
		urn := trimmed[:idx]
		sub := trimmed[idx+1:]
		if urn == "" {
			writeError(w, http.StatusBadRequest, "note URN is required")
			return
		}
		switch {
		case sub == "events":
			h.handleStreamEvents(w, r, urn)
		case sub == "content":
			h.handleReplaceContent(w, r, urn)
		case sub == "anchors":
			h.handleNoteAnchors(w, r, urn)
		case strings.HasPrefix(sub, "anchors/"):
			anchorID := strings.TrimPrefix(sub, "anchors/")
			h.handleNoteAnchor(w, r, urn, anchorID)
		case sub == "links":
			h.handleNoteLinks(w, r, urn)
		case sub == "backlinks":
			h.handleNoteBacklinks(w, r, urn, "")
		case strings.HasPrefix(sub, "backlinks/"):
			anchorID := strings.TrimPrefix(sub, "backlinks/")
			h.handleNoteBacklinks(w, r, urn, anchorID)
		default:
			writeError(w, http.StatusNotFound, "unknown note sub-resource: "+sub)
		}
		return
	}

	urn := trimmed
	switch r.Method {
	case http.MethodGet:
		h.handleGetNote(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateNote(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteNote(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeAppendEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleAppendEvent(w, r)
}

func (h *Handler) routeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleSearchNotes(w, r)
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — notes
// ─────────────────────────────────────────────────────────────────────────────

type noteHeaderJSON struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	ProjectURN string `json:"project_urn,omitempty"`
	FolderURN  string `json:"folder_urn,omitempty"`
	Deleted    bool   `json:"deleted"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type eventJSON struct {
	URN       string          `json:"urn,omitempty"`
	NoteURN   string          `json:"note_urn"`
	Sequence  int             `json:"sequence"`
	AuthorURN string          `json:"author_urn"`
	CreatedAt string          `json:"created_at"`
	Entries   []lineEntryJSON `json:"entries,omitempty"`
}

type lineEntryJSON struct {
	Op         string `json:"op"`
	LineNumber int    `json:"line_number"`
	Content    string `json:"content,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// core.* → JSON conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func coreNoteToJSON(n *core.Note) *noteHeaderJSON {
	if n == nil {
		return nil
	}
	j := &noteHeaderJSON{
		URN:       n.URN.String(),
		Name:      n.Name,
		NoteType:  n.NoteType.String(),
		Deleted:   n.Deleted,
		CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.ProjectURN != nil {
		j.ProjectURN = n.ProjectURN.String()
	}
	if n.FolderURN != nil {
		j.FolderURN = n.FolderURN.String()
	}
	return j
}

func coreEventToJSON(ev *core.Event) *eventJSON {
	if ev == nil {
		return nil
	}
	j := &eventJSON{
		URN:       ev.URN.String(),
		NoteURN:   ev.NoteURN.String(),
		Sequence:  ev.Sequence,
		AuthorURN: ev.AuthorURN.String(),
		CreatedAt: ev.CreatedAt.UTC().Format(time.RFC3339),
		Entries:   make([]lineEntryJSON, 0, len(ev.Entries)),
	}
	for _, e := range ev.Entries {
		j.Entries = append(j.Entries, coreLineEntryToJSON(e))
	}
	return j
}

func coreLineEntryToJSON(e core.LineEntry) lineEntryJSON {
	j := lineEntryJSON{LineNumber: e.LineNumber, Content: e.Content}
	switch e.Op {
	case core.LineOpSetEmpty:
		j.Op = "set_empty"
	case core.LineOpDelete:
		j.Op = "delete"
	case core.LineOpInsert:
		j.Op = "insert"
	default:
		j.Op = "set"
	}
	return j
}

func lineEntryFromJSON(j lineEntryJSON) (core.LineEntry, error) {
	if j.LineNumber < 1 {
		return core.LineEntry{}, fmt.Errorf("line_number must be >= 1")
	}
	var op core.LineOp
	switch j.Op {
	case "set", "":
		op = core.LineOpSet
	case "set_empty":
		op = core.LineOpSetEmpty
	case "delete":
		op = core.LineOpDelete
	case "insert":
		op = core.LineOpInsert
	default:
		return core.LineEntry{}, fmt.Errorf("unknown op %q: must be set, set_empty, delete, or insert", j.Op)
	}
	return core.LineEntry{LineNumber: j.LineNumber, Op: op, Content: j.Content}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes
// ─────────────────────────────────────────────────────────────────────────────

type listNotesResponse struct {
	Notes         []*noteHeaderJSON `json:"notes"`
	NextPageToken string            `json:"next_page_token,omitempty"`
}

func (h *Handler) handleListNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	opts := repo.ListOptions{
		ProjectURN:     q.Get("project_urn"),
		FolderURN:      q.Get("folder_urn"),
		PageToken:      q.Get("page_token"),
		IncludeDeleted: q.Get("include_deleted") == "true",
	}

	if ps := q.Get("page_size"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "page_size must be a positive integer")
			return
		}
		opts.PageSize = n
	}

	if nt := q.Get("note_type"); nt != "" {
		switch nt {
		case "normal":
			opts.FilterByType = true
			opts.NoteTypeFilter = core.NoteTypeNormal
		case "secure":
			opts.FilterByType = true
			opts.NoteTypeFilter = core.NoteTypeSecure
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_type %q: must be normal or secure", nt))
			return
		}
	}

	result, err := h.noteSvc.List(r.Context(), opts)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list notes")
		return
	}

	out := &listNotesResponse{
		Notes:         make([]*noteHeaderJSON, 0, len(result.Notes)),
		NextPageToken: result.NextPageToken,
	}
	for _, n := range result.Notes {
		out.Notes = append(out.Notes, coreNoteToJSON(n))
	}

	writeJSON(w, http.StatusOK, out)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/notes
// ─────────────────────────────────────────────────────────────────────────────

type createNoteRequest struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	SnipType   string `json:"snip_type,omitempty"`
	ProjectURN string `json:"project_urn,omitempty"`
	FolderURN  string `json:"folder_urn,omitempty"`
}

type createNoteResponse struct {
	Note *noteHeaderJSON `json:"note"`
}

func (h *Handler) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var req createNoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	noteURN, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid urn: %v", err))
		return
	}

	noteType, err := core.ParseNoteType(req.NoteType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	var note *core.Note
	if noteType == core.NoteTypeSecure {
		note = core.NewSecureNote(noteURN, req.Name, now)
	} else {
		note = core.NewNote(noteURN, req.Name, now)
	}

	if req.SnipType != "" {
		st := req.SnipType
		note.SnipType = &st
	}

	if req.ProjectURN != "" {
		projURN, err := core.ParseURN(req.ProjectURN)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid project_urn: %v", err))
			return
		}
		note.ProjectURN = &projURN
	}

	if req.FolderURN != "" {
		folderURN, err := core.ParseURN(req.FolderURN)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid folder_urn: %v", err))
			return
		}
		note.FolderURN = &folderURN
	}

	if err := h.noteSvc.Create(r.Context(), note); err != nil {
		svcErrToHTTP(w, r, h, err, "create note")
		return
	}

	writeJSON(w, http.StatusCreated, &createNoteResponse{Note: coreNoteToJSON(note)})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type getNoteResponse struct {
	Header  *noteHeaderJSON `json:"header"`
	Content string          `json:"content"`
}

func (h *Handler) handleGetNote(w http.ResponseWriter, r *http.Request, urn string) {
	note, _, err := h.noteSvc.Get(r.Context(), urn)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "get note")
		return
	}

	content := ""
	// Only reconstruct content for non-secure notes.
	if note.NoteType != core.NoteTypeSecure {
		content = note.Content()
	}

	writeJSON(w, http.StatusOK, &getNoteResponse{
		Header:  coreNoteToJSON(note),
		Content: content,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type updateNoteRequest struct {
	Name       *string `json:"name,omitempty"`
	ProjectURN *string `json:"project_urn,omitempty"`
	FolderURN  *string `json:"folder_urn,omitempty"`
	Deleted    *bool   `json:"deleted,omitempty"`
}

func (h *Handler) handleUpdateNote(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateNoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var upd service.NoteUpdate

	if req.Name != nil {
		if *req.Name == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		upd.Name = *req.Name
	}

	if req.ProjectURN != nil {
		if *req.ProjectURN == "" {
			upd.ClearProjectURN = true
		} else {
			upd.SetProjectURN = *req.ProjectURN
		}
	}

	if req.FolderURN != nil {
		if *req.FolderURN == "" {
			upd.ClearFolderURN = true
		} else {
			upd.SetFolderURN = *req.FolderURN
		}
	}

	if req.Deleted != nil {
		upd.Deleted = req.Deleted
	}

	note, err := h.noteSvc.Update(r.Context(), urn, upd)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "update note")
		return
	}

	writeJSON(w, http.StatusOK, &createNoteResponse{Note: coreNoteToJSON(note)})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type deleteNoteResponse struct {
	Deleted bool `json:"deleted"`
}

func (h *Handler) handleDeleteNote(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.noteSvc.Delete(r.Context(), urn); err != nil {
		svcErrToHTTP(w, r, h, err, "delete note")
		return
	}
	writeJSON(w, http.StatusOK, &deleteNoteResponse{Deleted: true})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/events
// ─────────────────────────────────────────────────────────────────────────────

type appendEventRequest struct {
	NoteURN        string          `json:"note_urn"`
	Sequence       int             `json:"sequence"`
	AuthorURN      string          `json:"author_urn"`
	CreatedAt      string          `json:"created_at,omitempty"`
	Entries        []lineEntryJSON `json:"entries,omitempty"`
	ExpectSequence int             `json:"expect_sequence,omitempty"`
}

type appendEventResponse struct {
	Sequence int `json:"sequence"`
}

func (h *Handler) handleAppendEvent(w http.ResponseWriter, r *http.Request) {
	var req appendEventRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.NoteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}
	if req.Sequence < 1 {
		writeError(w, http.StatusBadRequest, "sequence must be >= 1")
		return
	}
	// Fall back to the JWT-derived URN stamped by DeviceBridgeMiddleware
	// so callers do not need to echo the author back in the request body.
	if req.AuthorURN == "" {
		req.AuthorURN = r.Header.Get("X-Author-URN")
	}
	if req.AuthorURN == "" {
		writeError(w, http.StatusBadRequest, "author_urn is required")
		return
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries must not be empty")
		return
	}

	noteURN, err := core.ParseURN(req.NoteURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_urn: %v", err))
		return
	}

	authorURN, err := core.ParseURN(req.AuthorURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid author_urn: %v", err))
		return
	}

	entries := make([]core.LineEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		entry, err := lineEntryFromJSON(e)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid line entry: %v", err))
			return
		}
		entries = append(entries, entry)
	}

	createdAt := time.Now().UTC()
	if req.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, req.CreatedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid created_at: %v", err))
			return
		}
		createdAt = t.UTC()
	}

	ev := &core.Event{
		URN:       core.NewURN(core.ObjectTypeEvent),
		NoteURN:   noteURN,
		Sequence:  req.Sequence,
		AuthorURN: authorURN,
		CreatedAt: createdAt,
		Entries:   entries,
	}

	if err := h.noteSvc.AppendEvent(r.Context(), ev, repo.AppendEventOptions{ExpectSequence: req.Sequence}); err != nil {
		svcErrToHTTP(w, r, h, err, "append event")
		return
	}

	writeJSON(w, http.StatusCreated, &appendEventResponse{Sequence: req.Sequence})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes/:urn/events
// ─────────────────────────────────────────────────────────────────────────────

type streamEventsResponse struct {
	NoteURN string       `json:"note_urn"`
	Events  []*eventJSON `json:"events"`
	Count   int          `json:"count"`
}

func (h *Handler) handleStreamEvents(w http.ResponseWriter, r *http.Request, noteURN string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	fromSeq := 1
	if fs := r.URL.Query().Get("from"); fs != "" {
		n, err := strconv.Atoi(fs)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "from must be a positive integer")
			return
		}
		fromSeq = n
	}

	events, err := h.noteSvc.Events(r.Context(), noteURN, fromSeq)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "stream events")
		return
	}

	resp := &streamEventsResponse{
		NoteURN: noteURN,
		Events:  make([]*eventJSON, 0, len(events)),
		Count:   len(events),
	}
	for _, ev := range events {
		resp.Events = append(resp.Events, coreEventToJSON(ev))
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/notes/:urn/content
// ─────────────────────────────────────────────────────────────────────────────

type replaceContentRequest struct {
	Content   string `json:"content"`
	AuthorURN string `json:"author_urn,omitempty"`
}

type replaceContentResponse struct {
	Sequence int    `json:"sequence"`
	Changed  bool   `json:"changed"`
	NoteURN  string `json:"note_urn"`
}

func (h *Handler) handleReplaceContent(w http.ResponseWriter, r *http.Request, noteURN string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req replaceContentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// If the body did not carry an author_urn, fall back to the value that
	// DeviceBridgeMiddleware stamped from the JWT so bursts are always
	// attributed to a real user rather than the anonymous sentinel.
	authorURN := req.AuthorURN
	if authorURN == "" {
		authorURN = r.Header.Get("X-Author-URN")
	}

	result, err := h.noteSvc.ReplaceContent(r.Context(), service.ReplaceContentInput{
		NoteURN:   noteURN,
		Content:   req.Content,
		AuthorURN: authorURN,
	})
	if err != nil {
		svcErrToHTTP(w, r, h, err, "replace content")
		return
	}

	statusCode := http.StatusOK
	if result.Changed {
		statusCode = http.StatusCreated
	}

	writeJSON(w, statusCode, &replaceContentResponse{
		Sequence: result.Sequence,
		Changed:  result.Changed,
		NoteURN:  result.NoteURN,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/search
// ─────────────────────────────────────────────────────────────────────────────

type searchNotesResponse struct {
	Results       []*searchResultJSON `json:"results"`
	NextPageToken string              `json:"next_page_token,omitempty"`
}

type searchResultJSON struct {
	Note    *noteHeaderJSON `json:"note"`
	Excerpt string          `json:"excerpt,omitempty"`
}

func (h *Handler) handleSearchNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := q.Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "q (query) parameter is required")
		return
	}

	pageSize := h.cfg.DefaultPageSize
	if ps := q.Get("page_size"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "page_size must be a positive integer")
			return
		}
		if n > h.cfg.MaxPageSize {
			n = h.cfg.MaxPageSize
		}
		pageSize = n
	}

	results, err := h.noteSvc.Search(r.Context(), repo.SearchOptions{
		Query:     query,
		PageSize:  pageSize,
		PageToken: q.Get("page_token"),
	})
	if err != nil {
		svcErrToHTTP(w, r, h, err, "search notes")
		return
	}

	out := &searchNotesResponse{
		Results:       make([]*searchResultJSON, 0, len(results.Results)),
		NextPageToken: results.NextPageToken,
	}
	for _, sr := range results.Results {
		out.Results = append(out.Results, &searchResultJSON{
			Note:    coreNoteToJSON(sr.Note),
			Excerpt: sr.Excerpt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}
