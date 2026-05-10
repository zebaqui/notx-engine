package http

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — anchors
// ─────────────────────────────────────────────────────────────────────────────

type anchorJSON struct {
	NoteURN   string `json:"note_urn"`
	AnchorID  string `json:"anchor_id"`
	Line      int32  `json:"line"`
	CharStart int32  `json:"char_start"`
	CharEnd   int32  `json:"char_end,omitempty"`
	Preview   string `json:"preview,omitempty"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type backlinkJSON struct {
	SourceURN    string `json:"source_urn"`
	TargetURN    string `json:"target_urn"`
	TargetAnchor string `json:"target_anchor"`
	Label        string `json:"label,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type externalLinkJSON struct {
	SourceURN string `json:"source_urn"`
	URI       string `json:"uri"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON request types
// ─────────────────────────────────────────────────────────────────────────────

type upsertAnchorRequest struct {
	NoteURN   string `json:"note_urn"`
	AnchorID  string `json:"anchor_id"`
	Line      int32  `json:"line"`
	CharStart int32  `json:"char_start"`
	CharEnd   int32  `json:"char_end"`
	Preview   string `json:"preview"`
	Status    string `json:"status"` // defaults to "ok" when empty
}

type upsertBacklinkRequest struct {
	SourceURN    string `json:"source_urn"`
	TargetURN    string `json:"target_urn"`
	TargetAnchor string `json:"target_anchor"`
	Label        string `json:"label"`
}

type deleteBacklinkRequest struct {
	SourceURN    string `json:"source_urn"`
	TargetURN    string `json:"target_urn"`
	TargetAnchor string `json:"target_anchor"`
}

type upsertExternalLinkRequest struct {
	SourceURN string `json:"source_urn"`
	URI       string `json:"uri"`
	Label     string `json:"label"`
}

type deleteExternalLinkRequest struct {
	SourceURN string `json:"source_urn"`
	URI       string `json:"uri"`
}

type relabelLinksRequest struct {
	NoteURN  string `json:"note_urn"`
	OldLabel string `json:"old_label"`
	NewLabel string `json:"new_label"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Repo → JSON conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func anchorRepoToJSON(a repo.AnchorRecord) *anchorJSON {
	j := &anchorJSON{
		NoteURN:   a.NoteURN,
		AnchorID:  a.AnchorID,
		Line:      int32(a.Line),
		CharStart: int32(a.CharStart),
		CharEnd:   int32(a.CharEnd),
		Preview:   a.Preview,
		Status:    a.Status,
	}
	if !a.UpdatedAt.IsZero() {
		j.UpdatedAt = a.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return j
}

func backlinkRepoToJSON(b repo.BacklinkRecord) *backlinkJSON {
	j := &backlinkJSON{
		SourceURN:    b.SourceURN,
		TargetURN:    b.TargetURN,
		TargetAnchor: b.TargetAnchor,
		Label:        b.Label,
	}
	if !b.CreatedAt.IsZero() {
		j.CreatedAt = b.CreatedAt.UTC().Format(time.RFC3339)
	}
	return j
}

func externalLinkRepoToJSON(e repo.ExternalLinkRecord) *externalLinkJSON {
	j := &externalLinkJSON{
		SourceURN: e.SourceURN,
		URI:       e.URI,
		Label:     e.Label,
	}
	if !e.CreatedAt.IsZero() {
		j.CreatedAt = e.CreatedAt.UTC().Format(time.RFC3339)
	}
	return j
}

// ─────────────────────────────────────────────────────────────────────────────
// Route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

// routeLinkAnchors handles GET /v1/links/anchors (list) and PUT /v1/links/anchors (upsert).
func (h *Handler) routeLinkAnchors(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListAnchors(w, r)
	case http.MethodPut:
		h.handleUpsertAnchor(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeLinkAnchor handles GET and DELETE on /v1/links/anchors/{note_urn}/{anchor_id}.
// The note_urn may itself contain colons (URN format), so we split on the last slash.
func (h *Handler) routeLinkAnchor(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/v1/links/anchors/")
	lastSlash := strings.LastIndex(suffix, "/")
	if lastSlash < 0 {
		writeError(w, http.StatusBadRequest, "path must be /v1/links/anchors/{note_urn}/{anchor_id}")
		return
	}
	noteURN := suffix[:lastSlash]
	anchorID := suffix[lastSlash+1:]

	if noteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}
	if anchorID == "" {
		writeError(w, http.StatusBadRequest, "anchor_id is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetAnchor(w, r, noteURN, anchorID)
	case http.MethodDelete:
		h.handleDeleteAnchor(w, r, noteURN, anchorID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeLinkBacklinks handles GET, PUT, and DELETE on /v1/links/backlinks.
func (h *Handler) routeLinkBacklinks(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListBacklinks(w, r)
	case http.MethodPut:
		h.handleUpsertBacklink(w, r)
	case http.MethodDelete:
		h.handleDeleteBacklink(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeLinkOutbound handles GET /v1/links/outbound.
func (h *Handler) routeLinkOutbound(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListOutbound(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeLinkReferrers handles GET /v1/links/referrers.
func (h *Handler) routeLinkReferrers(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetReferrers(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeLinkExternal handles GET, PUT, and DELETE on /v1/links/external.
func (h *Handler) routeLinkExternal(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListExternalLinks(w, r)
	case http.MethodPut:
		h.handleUpsertExternalLink(w, r)
	case http.MethodDelete:
		h.handleDeleteExternalLink(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/anchors?note_urn=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListAnchors(w http.ResponseWriter, r *http.Request) {
	noteURN := r.URL.Query().Get("note_urn")
	if noteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}

	anchors, err := h.linkSvc.ListAnchors(r.Context(), noteURN)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list anchors")
		return
	}

	out := make([]*anchorJSON, 0, len(anchors))
	for _, a := range anchors {
		out = append(out, anchorRepoToJSON(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"anchors": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/anchors/{note_urn}/{anchor_id}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetAnchor(w http.ResponseWriter, r *http.Request, noteURN, anchorID string) {
	a, err := h.linkSvc.GetAnchor(r.Context(), noteURN, anchorID)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "get anchor")
		return
	}
	writeJSON(w, http.StatusOK, anchorRepoToJSON(a))
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /v1/links/anchors
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleUpsertAnchor(w http.ResponseWriter, r *http.Request) {
	var req upsertAnchorRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NoteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}
	if req.AnchorID == "" {
		writeError(w, http.StatusBadRequest, "anchor_id is required")
		return
	}

	record := repo.AnchorRecord{
		NoteURN:   req.NoteURN,
		AnchorID:  req.AnchorID,
		Line:      int(req.Line),
		CharStart: int(req.CharStart),
		CharEnd:   int(req.CharEnd),
		Preview:   req.Preview,
		Status:    req.Status,
	}

	stored, err := h.linkSvc.UpsertAnchor(r.Context(), record)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "upsert anchor")
		return
	}
	writeJSON(w, http.StatusOK, anchorRepoToJSON(stored))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/links/anchors/{note_urn}/{anchor_id}?tombstone=true
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDeleteAnchor(w http.ResponseWriter, r *http.Request, noteURN, anchorID string) {
	tombstone := r.URL.Query().Get("tombstone") == "true"

	if err := h.linkSvc.DeleteAnchor(r.Context(), noteURN, anchorID, tombstone); err != nil {
		svcErrToHTTP(w, r, h, err, "delete anchor")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/backlinks?target_urn=...&anchor_id=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListBacklinks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	targetURN := q.Get("target_urn")
	if targetURN == "" {
		writeError(w, http.StatusBadRequest, "target_urn is required")
		return
	}
	anchorID := q.Get("anchor_id")

	backlinks, err := h.linkSvc.ListBacklinks(r.Context(), targetURN, anchorID)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list backlinks")
		return
	}

	out := make([]*backlinkJSON, 0, len(backlinks))
	for _, b := range backlinks {
		out = append(out, backlinkRepoToJSON(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"backlinks": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/outbound?source_urn=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListOutbound(w http.ResponseWriter, r *http.Request) {
	sourceURN := r.URL.Query().Get("source_urn")
	if sourceURN == "" {
		writeError(w, http.StatusBadRequest, "source_urn is required")
		return
	}

	links, err := h.linkSvc.ListOutboundLinks(r.Context(), sourceURN)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list outbound links")
		return
	}

	out := make([]*backlinkJSON, 0, len(links))
	for _, b := range links {
		out = append(out, backlinkRepoToJSON(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/backlinks/recent?note_urn=&label=&limit=
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRecentBacklinks(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	q := r.URL.Query()
	noteURN := q.Get("note_urn")
	label := q.Get("label")
	var limit int32
	if l := q.Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	backlinks, err := h.linkSvc.RecentBacklinks(r.Context(), repo.RecentBacklinksOptions{
		NoteURN: noteURN,
		Label:   label,
		Limit:   int(limit),
	})
	if err != nil {
		svcErrToHTTP(w, r, h, err, "recent backlinks")
		return
	}

	out := make([]*backlinkJSON, 0, len(backlinks))
	for _, b := range backlinks {
		out = append(out, backlinkRepoToJSON(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"backlinks": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/referrers?target_urn=...&anchor_id=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetReferrers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	targetURN := q.Get("target_urn")
	if targetURN == "" {
		writeError(w, http.StatusBadRequest, "target_urn is required")
		return
	}
	anchorID := q.Get("anchor_id")
	if anchorID == "" {
		writeError(w, http.StatusBadRequest, "anchor_id is required")
		return
	}

	urns, err := h.linkSvc.GetReferrers(r.Context(), targetURN, anchorID)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "get referrers")
		return
	}

	if urns == nil {
		urns = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_urns": urns})
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /v1/links/backlinks
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleUpsertBacklink(w http.ResponseWriter, r *http.Request) {
	var req upsertBacklinkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SourceURN == "" {
		writeError(w, http.StatusBadRequest, "source_urn is required")
		return
	}
	if req.TargetURN == "" {
		writeError(w, http.StatusBadRequest, "target_urn is required")
		return
	}

	record := repo.BacklinkRecord{
		SourceURN:    req.SourceURN,
		TargetURN:    req.TargetURN,
		TargetAnchor: req.TargetAnchor,
		Label:        req.Label,
	}

	stored, err := h.linkSvc.UpsertBacklink(r.Context(), record)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "upsert backlink")
		return
	}
	writeJSON(w, http.StatusOK, backlinkRepoToJSON(stored))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/links/backlinks
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDeleteBacklink(w http.ResponseWriter, r *http.Request) {
	var req deleteBacklinkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SourceURN == "" {
		writeError(w, http.StatusBadRequest, "source_urn is required")
		return
	}
	if req.TargetURN == "" {
		writeError(w, http.StatusBadRequest, "target_urn is required")
		return
	}

	if err := h.linkSvc.DeleteBacklink(r.Context(), req.SourceURN, req.TargetURN, req.TargetAnchor); err != nil {
		svcErrToHTTP(w, r, h, err, "delete backlink")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/external?source_urn=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListExternalLinks(w http.ResponseWriter, r *http.Request) {
	sourceURN := r.URL.Query().Get("source_urn")
	if sourceURN == "" {
		writeError(w, http.StatusBadRequest, "source_urn is required")
		return
	}

	links, err := h.linkSvc.ListExternalLinks(r.Context(), sourceURN)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list external links")
		return
	}

	out := make([]*externalLinkJSON, 0, len(links))
	for _, e := range links {
		out = append(out, externalLinkRepoToJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /v1/links/external
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleUpsertExternalLink(w http.ResponseWriter, r *http.Request) {
	var req upsertExternalLinkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SourceURN == "" {
		writeError(w, http.StatusBadRequest, "source_urn is required")
		return
	}
	if req.URI == "" {
		writeError(w, http.StatusBadRequest, "uri is required")
		return
	}

	record := repo.ExternalLinkRecord{
		SourceURN: req.SourceURN,
		URI:       req.URI,
		Label:     req.Label,
	}

	stored, err := h.linkSvc.UpsertExternalLink(r.Context(), record)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "upsert external link")
		return
	}
	writeJSON(w, http.StatusOK, externalLinkRepoToJSON(stored))
}

// ─────────────────────────────────────────────────────────────────────────────
// ─────────────────────────────────────────────────────────────────────────────
// Note-centric link endpoints — /v1/notes/{note_urn}/anchors[/{anchor_id}]
//                               /v1/notes/{note_urn}/links
//                               /v1/notes/{note_urn}/backlinks[/{anchor_id}]
// ─────────────────────────────────────────────────────────────────────────────

// handleNoteAnchors handles GET /v1/notes/{note_urn}/anchors.
func (h *Handler) handleNoteAnchors(w http.ResponseWriter, r *http.Request, noteURN string) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		anchors, err := h.linkSvc.ListAnchors(r.Context(), noteURN)
		if err != nil {
			svcErrToHTTP(w, r, h, err, "list anchors")
			return
		}
		out := make([]*anchorJSON, 0, len(anchors))
		for _, a := range anchors {
			out = append(out, anchorRepoToJSON(a))
		}
		writeJSON(w, http.StatusOK, map[string]any{"anchors": out})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleNoteAnchor handles GET /v1/notes/{note_urn}/anchors/{anchor_id}.
func (h *Handler) handleNoteAnchor(w http.ResponseWriter, r *http.Request, noteURN, anchorID string) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	if anchorID == "" {
		writeError(w, http.StatusBadRequest, "anchor_id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetAnchor(w, r, noteURN, anchorID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleNoteLinks handles GET /v1/notes/{note_urn}/links.
func (h *Handler) handleNoteLinks(w http.ResponseWriter, r *http.Request, noteURN string) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		links, err := h.linkSvc.ListOutboundLinks(r.Context(), noteURN)
		if err != nil {
			svcErrToHTTP(w, r, h, err, "list outbound links")
			return
		}
		out := make([]*backlinkJSON, 0, len(links))
		for _, b := range links {
			out = append(out, backlinkRepoToJSON(b))
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": out})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleNoteBacklinks handles GET /v1/notes/{note_urn}/backlinks[/{anchor_id}].
// When anchorID is empty, all backlinks to the note are returned.
// When anchorID is non-empty, only backlinks targeting that specific anchor are returned.
func (h *Handler) handleNoteBacklinks(w http.ResponseWriter, r *http.Request, noteURN, anchorID string) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		backlinks, err := h.linkSvc.ListBacklinks(r.Context(), noteURN, anchorID)
		if err != nil {
			svcErrToHTTP(w, r, h, err, "list backlinks")
			return
		}
		out := make([]*backlinkJSON, 0, len(backlinks))
		for _, b := range backlinks {
			out = append(out, backlinkRepoToJSON(b))
		}
		writeJSON(w, http.StatusOK, map[string]any{"backlinks": out})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// DELETE /v1/links/external
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDeleteExternalLink(w http.ResponseWriter, r *http.Request) {
	var req deleteExternalLinkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SourceURN == "" {
		writeError(w, http.StatusBadRequest, "source_urn is required")
		return
	}
	if req.URI == "" {
		writeError(w, http.StatusBadRequest, "uri is required")
		return
	}

	if err := h.linkSvc.DeleteExternalLink(r.Context(), req.SourceURN, req.URI); err != nil {
		svcErrToHTTP(w, r, h, err, "delete external link")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
func (h *Handler) handleRelabelLinks(w http.ResponseWriter, r *http.Request) {
	if h.linkSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "link service not available")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req relabelLinksRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NoteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}
	if req.OldLabel == "" {
		writeError(w, http.StatusBadRequest, "old_label is required")
		return
	}
	if req.NewLabel == "" {
		writeError(w, http.StatusBadRequest, "new_label is required")
		return
	}

	updated, err := h.linkSvc.RelabelLinks(r.Context(), req.NoteURN, req.OldLabel, req.NewLabel)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "relabel links")
		return
	}
	if updated == nil {
		updated = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"updated":       updated,
		"updated_count": len(updated),
	})
}
