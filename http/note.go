package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/zebaqui/notx-engine/proto"
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
		switch sub {
		case "events":
			h.handleStreamEvents(w, r, urn)
		case "content":
			h.handleReplaceContent(w, r, urn)
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

// protoHeaderToJSON converts a pb.NoteHeader to the HTTP wire type.
func protoHeaderToJSON(h *pb.NoteHeader) *noteHeaderJSON {
	if h == nil {
		return nil
	}
	j := &noteHeaderJSON{
		URN:        h.Urn,
		Name:       h.Name,
		Deleted:    h.Deleted,
		ProjectURN: h.ProjectUrn,
		FolderURN:  h.FolderUrn,
	}
	switch h.NoteType {
	case pb.NoteType_NOTE_TYPE_SECURE:
		j.NoteType = "secure"
	default:
		j.NoteType = "normal"
	}
	if h.CreatedAt != nil {
		j.CreatedAt = h.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if h.UpdatedAt != nil {
		j.UpdatedAt = h.UpdatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

// protoEventToJSON converts a pb.Event to the HTTP wire type.
func protoEventToJSON(ev *pb.Event) *eventJSON {
	if ev == nil {
		return nil
	}
	j := &eventJSON{
		URN:       ev.Urn,
		NoteURN:   ev.NoteUrn,
		Sequence:  int(ev.Sequence),
		AuthorURN: ev.AuthorUrn,
		Entries:   make([]lineEntryJSON, 0, len(ev.Entries)),
	}
	if ev.CreatedAt != nil {
		j.CreatedAt = ev.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	for _, e := range ev.Entries {
		j.Entries = append(j.Entries, protoLineEntryToJSON(e))
	}
	return j
}

func protoLineEntryToJSON(e *pb.LineEntry) lineEntryJSON {
	j := lineEntryJSON{
		LineNumber: int(e.LineNumber),
		Content:    e.Content,
	}
	switch e.Op {
	case 1:
		j.Op = "set_empty"
	case 2:
		j.Op = "delete"
	case 3:
		j.Op = "insert"
	default:
		j.Op = "set"
	}
	return j
}

func lineEntryFromJSON(j lineEntryJSON) (lineEntryResult, error) {
	if j.LineNumber < 1 {
		return lineEntryResult{}, fmt.Errorf("line_number must be >= 1")
	}
	var op int32
	switch j.Op {
	case "set", "":
		op = 0
	case "set_empty":
		op = 1
	case "delete":
		op = 2
	case "insert":
		op = 3
	default:
		return lineEntryResult{}, fmt.Errorf("unknown op %q: must be set, set_empty, delete, or insert", j.Op)
	}
	return lineEntryResult{Op: op, LineNumber: int32(j.LineNumber), Content: j.Content}, nil
}

type lineEntryResult struct {
	Op         int32
	LineNumber int32
	Content    string
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

	req := &pb.ListNotesRequest{
		ProjectUrn:     q.Get("project_urn"),
		FolderUrn:      q.Get("folder_urn"),
		PageToken:      q.Get("page_token"),
		IncludeDeleted: q.Get("include_deleted") == "true",
	}

	if ps := q.Get("page_size"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "page_size must be a positive integer")
			return
		}
		req.PageSize = int32(n)
	}

	if nt := q.Get("note_type"); nt != "" {
		switch nt {
		case "normal":
			req.NoteType = pb.NoteType_NOTE_TYPE_NORMAL
		case "secure":
			req.NoteType = pb.NoteType_NOTE_TYPE_SECURE
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_type %q: must be normal or secure", nt))
			return
		}
	}

	resp, err := h.noteSvc.ListNotes(r.Context(), req)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list notes")
		return
	}

	out := &listNotesResponse{
		Notes:         make([]*noteHeaderJSON, 0, len(resp.Notes)),
		NextPageToken: resp.NextPageToken,
	}
	for _, hdr := range resp.Notes {
		out.Notes = append(out.Notes, protoHeaderToJSON(hdr))
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

	var noteType pb.NoteType
	switch req.NoteType {
	case "secure":
		noteType = pb.NoteType_NOTE_TYPE_SECURE
	case "normal", "":
		noteType = pb.NoteType_NOTE_TYPE_NORMAL
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_type %q: must be normal or secure", req.NoteType))
		return
	}

	pbHdr := &pb.NoteHeader{
		Urn:        req.URN,
		Name:       req.Name,
		NoteType:   noteType,
		SnipType:   req.SnipType,
		ProjectUrn: req.ProjectURN,
		FolderUrn:  req.FolderURN,
	}

	resp, err := h.noteSvc.CreateNote(r.Context(), &pb.CreateNoteRequest{Header: pbHdr})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "create note")
		return
	}

	writeJSON(w, http.StatusCreated, &createNoteResponse{Note: protoHeaderToJSON(resp.Header)})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type getNoteResponse struct {
	Header  *noteHeaderJSON `json:"header"`
	Content string          `json:"content"`
}

func (h *Handler) handleGetNote(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.noteSvc.GetNote(r.Context(), &pb.GetNoteRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get note")
		return
	}

	content := ""
	// Only reconstruct content for non-secure notes.
	if resp.Header.NoteType != pb.NoteType_NOTE_TYPE_SECURE {
		content = applyEventsToContent(resp.Events)
	}

	writeJSON(w, http.StatusOK, &getNoteResponse{
		Header:  protoHeaderToJSON(resp.Header),
		Content: content,
	})
}

// applyEventsToContent reconstructs the plaintext content of a note from its
// ordered list of EventProto by replaying line-entry operations in sequence.
func applyEventsToContent(events []*pb.Event) string {
	lines := make(map[int]string)
	for _, ev := range events {
		for _, e := range ev.Entries {
			switch e.Op {
			case 0: // SET
				lines[int(e.LineNumber)] = e.Content
			case 1: // SET_EMPTY
				lines[int(e.LineNumber)] = ""
			case 2: // DELETE
				delete(lines, int(e.LineNumber))
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	maxLine := 0
	for k := range lines {
		if k > maxLine {
			maxLine = k
		}
	}
	parts := make([]string, maxLine)
	for i := 1; i <= maxLine; i++ {
		parts[i-1] = lines[i]
	}
	return strings.Join(parts, "\n")
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

	// Fetch current state so we can carry forward unset fields.
	current, err := h.noteSvc.GetNote(r.Context(), &pb.GetNoteRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get note for update")
		return
	}

	header := &pb.NoteHeader{
		Urn:        urn,
		Name:       current.Header.Name,
		NoteType:   current.Header.NoteType,
		ProjectUrn: current.Header.ProjectUrn,
		FolderUrn:  current.Header.FolderUrn,
		Deleted:    current.Header.Deleted,
	}

	if req.Name != nil {
		if *req.Name == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		header.Name = *req.Name
	}

	if req.ProjectURN != nil {
		if *req.ProjectURN == "" {
			header.ProjectUrn = "CLEAR"
		} else {
			header.ProjectUrn = *req.ProjectURN
		}
	}

	if req.FolderURN != nil {
		if *req.FolderURN == "" {
			header.FolderUrn = "CLEAR"
		} else {
			header.FolderUrn = *req.FolderURN
		}
	}

	if req.Deleted != nil {
		header.Deleted = *req.Deleted
	}

	grpcReq := &pb.UpdateNoteRequest{Urn: urn, Header: header}

	resp, err := h.noteSvc.UpdateNote(r.Context(), grpcReq)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "update note")
		return
	}

	writeJSON(w, http.StatusOK, &createNoteResponse{Note: protoHeaderToJSON(resp.Header)})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type deleteNoteResponse struct {
	Deleted bool `json:"deleted"`
}

func (h *Handler) handleDeleteNote(w http.ResponseWriter, r *http.Request, urn string) {
	_, err := h.noteSvc.DeleteNote(r.Context(), &pb.DeleteNoteRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete note")
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
	if req.AuthorURN == "" {
		writeError(w, http.StatusBadRequest, "author_urn is required")
		return
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries must not be empty")
		return
	}

	pbEntries := make([]*pb.LineEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		entry, err := lineEntryFromJSON(e)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid line entry: %v", err))
			return
		}
		pbEntries = append(pbEntries, &pb.LineEntry{
			Op:         int32(entry.Op),
			LineNumber: int32(entry.LineNumber),
			Content:    entry.Content,
		})
	}

	ev := &pb.Event{
		NoteUrn:   req.NoteURN,
		Sequence:  int32(req.Sequence),
		AuthorUrn: req.AuthorURN,
		Entries:   pbEntries,
	}

	if req.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, req.CreatedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid created_at: %v", err))
			return
		}
		ev.CreatedAt = timestamppb.New(t.UTC())
	}

	_, err := h.noteSvc.AppendEvent(r.Context(), &pb.AppendEventRequest{Event: ev})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "append event")
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

	fromSeq := int32(1)
	if fs := r.URL.Query().Get("from"); fs != "" {
		n, err := strconv.Atoi(fs)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "from must be a positive integer")
			return
		}
		fromSeq = int32(n)
	}

	// Use an in-memory server stream adapter to collect all events.
	ms := &memoryEventStream{ctx: r.Context()}
	if err := h.noteSvc.StreamEvents(
		&pb.StreamEventsRequest{NoteUrn: noteURN, FromSequence: fromSeq},
		ms,
	); err != nil {
		grpcErrToHTTP(w, r, h, err, "stream events")
		return
	}

	resp := &streamEventsResponse{
		NoteURN: noteURN,
		Events:  make([]*eventJSON, 0, len(ms.events)),
		Count:   len(ms.events),
	}
	for _, ev := range ms.events {
		resp.Events = append(resp.Events, protoEventToJSON(ev))
	}

	writeJSON(w, http.StatusOK, resp)
}

// memoryEventStream is an in-process grpc.ServerStream adapter for
// NoteService_StreamEventsServer. It collects all sent events in memory so
// the HTTP handler can return them as a single JSON response.
type memoryEventStream struct {
	ctx    context.Context
	events []*pb.Event
	grpc.ServerStream
}

func (s *memoryEventStream) Context() context.Context { return s.ctx }
func (s *memoryEventStream) Send(resp *pb.StreamEventsResponse) error {
	s.events = append(s.events, resp.Event)
	return nil
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

	resp, err := h.noteSvc.ReplaceContent(r.Context(), &pb.ReplaceContentRequest{
		NoteUrn:   noteURN,
		Content:   req.Content,
		AuthorUrn: req.AuthorURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "replace content")
		return
	}

	statusCode := http.StatusOK
	if resp.Changed {
		statusCode = http.StatusCreated
	}

	writeJSON(w, statusCode, &replaceContentResponse{
		Sequence: int(resp.Sequence),
		Changed:  resp.Changed,
		NoteURN:  noteURN,
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

	pageSize := int32(h.cfg.DefaultPageSize)
	if ps := q.Get("page_size"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "page_size must be a positive integer")
			return
		}
		if n > h.cfg.MaxPageSize {
			n = h.cfg.MaxPageSize
		}
		pageSize = int32(n)
	}

	resp, err := h.noteSvc.SearchNotes(r.Context(), &pb.SearchNotesRequest{
		Query:     query,
		PageSize:  pageSize,
		PageToken: q.Get("page_token"),
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "search notes")
		return
	}

	out := &searchNotesResponse{
		Results:       make([]*searchResultJSON, 0, len(resp.Results)),
		NextPageToken: resp.NextPageToken,
	}
	for _, sr := range resp.Results {
		out.Results = append(out.Results, &searchResultJSON{
			Note:    protoHeaderToJSON(sr.Header),
			Excerpt: sr.Excerpt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}
