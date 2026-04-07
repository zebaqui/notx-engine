package http

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "github.com/zebaqui/notx-engine/proto"
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

// ─────────────────────────────────────────────────────────────────────────────
// Proto → JSON conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func anchorProtoToJSON(a *pb.AnchorRecord) *anchorJSON {
	if a == nil {
		return nil
	}
	j := &anchorJSON{
		NoteURN:   a.NoteUrn,
		AnchorID:  a.AnchorId,
		Line:      a.Line,
		CharStart: a.CharStart,
		CharEnd:   a.CharEnd,
		Preview:   a.Preview,
		Status:    a.Status,
	}
	if a.UpdatedAt != nil {
		j.UpdatedAt = a.UpdatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

func backlinkProtoToJSON(b *pb.BacklinkRecord) *backlinkJSON {
	if b == nil {
		return nil
	}
	j := &backlinkJSON{
		SourceURN:    b.SourceUrn,
		TargetURN:    b.TargetUrn,
		TargetAnchor: b.TargetAnchor,
		Label:        b.Label,
	}
	if b.CreatedAt != nil {
		j.CreatedAt = b.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

func externalLinkProtoToJSON(e *pb.ExternalLinkRecord) *externalLinkJSON {
	if e == nil {
		return nil
	}
	j := &externalLinkJSON{
		SourceURN: e.SourceUrn,
		URI:       e.Uri,
		Label:     e.Label,
	}
	if e.CreatedAt != nil {
		j.CreatedAt = e.CreatedAt.AsTime().UTC().Format(time.RFC3339)
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

	resp, err := h.linkSvc.ListAnchors(r.Context(), &pb.ListAnchorsRequest{
		NoteUrn: noteURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list anchors")
		return
	}

	out := make([]*anchorJSON, 0, len(resp.Anchors))
	for _, a := range resp.Anchors {
		out = append(out, anchorProtoToJSON(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"anchors": out})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/links/anchors/{note_urn}/{anchor_id}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetAnchor(w http.ResponseWriter, r *http.Request, noteURN, anchorID string) {
	resp, err := h.linkSvc.GetAnchor(r.Context(), &pb.GetAnchorRequest{
		NoteUrn:  noteURN,
		AnchorId: anchorID,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get anchor")
		return
	}
	writeJSON(w, http.StatusOK, anchorProtoToJSON(resp.Anchor))
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
	if req.Status == "" {
		req.Status = "ok"
	}

	resp, err := h.linkSvc.UpsertAnchor(r.Context(), &pb.UpsertAnchorRequest{
		NoteUrn:   req.NoteURN,
		AnchorId:  req.AnchorID,
		Line:      req.Line,
		CharStart: req.CharStart,
		CharEnd:   req.CharEnd,
		Preview:   req.Preview,
		Status:    req.Status,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "upsert anchor")
		return
	}
	writeJSON(w, http.StatusOK, anchorProtoToJSON(resp.Anchor))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/links/anchors/{note_urn}/{anchor_id}?tombstone=true
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDeleteAnchor(w http.ResponseWriter, r *http.Request, noteURN, anchorID string) {
	tombstone := r.URL.Query().Get("tombstone") == "true"

	_, err := h.linkSvc.DeleteAnchor(r.Context(), &pb.DeleteAnchorRequest{
		NoteUrn:   noteURN,
		AnchorId:  anchorID,
		Tombstone: tombstone,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete anchor")
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

	resp, err := h.linkSvc.ListBacklinks(r.Context(), &pb.ListBacklinksRequest{
		TargetUrn: targetURN,
		AnchorId:  anchorID,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list backlinks")
		return
	}

	out := make([]*backlinkJSON, 0, len(resp.Backlinks))
	for _, b := range resp.Backlinks {
		out = append(out, backlinkProtoToJSON(b))
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

	resp, err := h.linkSvc.ListOutboundLinks(r.Context(), &pb.ListOutboundLinksRequest{
		SourceUrn: sourceURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list outbound links")
		return
	}

	out := make([]*backlinkJSON, 0, len(resp.Links))
	for _, b := range resp.Links {
		out = append(out, backlinkProtoToJSON(b))
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

	resp, err := h.linkSvc.RecentBacklinks(r.Context(), &pb.RecentBacklinksRequest{
		NoteUrn: noteURN,
		Label:   label,
		Limit:   limit,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "recent backlinks")
		return
	}

	out := make([]*backlinkJSON, 0, len(resp.Backlinks))
	for _, b := range resp.Backlinks {
		out = append(out, backlinkProtoToJSON(b))
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

	resp, err := h.linkSvc.GetReferrers(r.Context(), &pb.GetReferrersRequest{
		TargetUrn: targetURN,
		AnchorId:  anchorID,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get referrers")
		return
	}

	urns := resp.SourceUrns
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

	resp, err := h.linkSvc.UpsertBacklink(r.Context(), &pb.UpsertBacklinkRequest{
		SourceUrn:    req.SourceURN,
		TargetUrn:    req.TargetURN,
		TargetAnchor: req.TargetAnchor,
		Label:        req.Label,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "upsert backlink")
		return
	}
	writeJSON(w, http.StatusOK, backlinkProtoToJSON(resp.Backlink))
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

	_, err := h.linkSvc.DeleteBacklink(r.Context(), &pb.DeleteBacklinkRequest{
		SourceUrn:    req.SourceURN,
		TargetUrn:    req.TargetURN,
		TargetAnchor: req.TargetAnchor,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete backlink")
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

	resp, err := h.linkSvc.ListExternalLinks(r.Context(), &pb.ListExternalLinksRequest{
		SourceUrn: sourceURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list external links")
		return
	}

	out := make([]*externalLinkJSON, 0, len(resp.Links))
	for _, e := range resp.Links {
		out = append(out, externalLinkProtoToJSON(e))
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

	resp, err := h.linkSvc.UpsertExternalLink(r.Context(), &pb.UpsertExternalLinkRequest{
		SourceUrn: req.SourceURN,
		Uri:       req.URI,
		Label:     req.Label,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "upsert external link")
		return
	}
	writeJSON(w, http.StatusOK, externalLinkProtoToJSON(resp.Link))
}

// ─────────────────────────────────────────────────────────────────────────────
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

	_, err := h.linkSvc.DeleteExternalLink(r.Context(), &pb.DeleteExternalLinkRequest{
		SourceUrn: req.SourceURN,
		Uri:       req.URI,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete external link")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
