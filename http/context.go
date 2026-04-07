package http

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — bursts & candidates
// ─────────────────────────────────────────────────────────────────────────────

type burstRecordJSON struct {
	ID         string `json:"id"`
	NoteURN    string `json:"note_urn"`
	ProjectURN string `json:"project_urn,omitempty"`
	FolderURN  string `json:"folder_urn,omitempty"`
	AuthorURN  string `json:"author_urn,omitempty"`
	Sequence   int32  `json:"sequence"`
	LineStart  int32  `json:"line_start"`
	LineEnd    int32  `json:"line_end"`
	Text       string `json:"text"`
	Tokens     string `json:"tokens,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type candidateRecordJSON struct {
	ID           string           `json:"id"`
	BurstAID     string           `json:"burst_a_id"`
	BurstBID     string           `json:"burst_b_id"`
	NoteURN_A    string           `json:"note_urn_a"`
	NoteURN_B    string           `json:"note_urn_b"`
	ProjectURN   string           `json:"project_urn,omitempty"`
	OverlapScore float64          `json:"overlap_score"`
	BM25Score    float64          `json:"bm25_score"`
	Status       string           `json:"status"`
	CreatedAt    string           `json:"created_at,omitempty"`
	ReviewedAt   string           `json:"reviewed_at,omitempty"`
	ReviewedBy   string           `json:"reviewed_by,omitempty"`
	PromotedLink string           `json:"promoted_link,omitempty"`
	BurstA       *burstRecordJSON `json:"burst_a,omitempty"`
	BurstB       *burstRecordJSON `json:"burst_b,omitempty"`
}

type contextStatsJSON struct {
	BurstsTotal                 int64   `json:"bursts_total"`
	BurstsToday                 int64   `json:"bursts_today"`
	CandidatesPending           int64   `json:"candidates_pending"`
	CandidatesPendingUnenriched int64   `json:"candidates_pending_unenriched"`
	CandidatesPromoted          int64   `json:"candidates_promoted"`
	CandidatesDismissed         int64   `json:"candidates_dismissed"`
	OldestPendingAgeDays        float64 `json:"oldest_pending_age_days"`
}

type projectContextConfigJSON struct {
	ProjectURN               string `json:"project_urn"`
	BurstMaxPerNotePerDay    int32  `json:"burst_max_per_note_per_day"`
	BurstMaxPerProjectPerDay int32  `json:"burst_max_per_project_per_day"`
	UpdatedAt                string `json:"updated_at,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON request types
// ─────────────────────────────────────────────────────────────────────────────

type promoteRequest struct {
	Label       string `json:"label"`
	Direction   string `json:"direction"`    // "both" | "a_to_b" | "b_to_a"; default "both"
	ReviewerURN string `json:"reviewer_urn"` // default "urn:notx:usr:anon"
}

type dismissRequest struct {
	ReviewerURN string `json:"reviewer_urn"` // default "urn:notx:usr:anon"
}

type setProjectConfigRequest struct {
	BurstMaxPerNotePerDay    int32 `json:"burst_max_per_note_per_day"`
	BurstMaxPerProjectPerDay int32 `json:"burst_max_per_project_per_day"`
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON response types
// ─────────────────────────────────────────────────────────────────────────────

type listCandidatesResponse struct {
	Candidates    []*candidateRecordJSON `json:"candidates"`
	NextPageToken string                 `json:"next_page_token,omitempty"`
}

type listBurstsResponse struct {
	Bursts        []*burstRecordJSON `json:"bursts"`
	NextPageToken string             `json:"next_page_token,omitempty"`
}

type promoteResponse struct {
	AnchorAID string               `json:"anchor_a_id"`
	AnchorBID string               `json:"anchor_b_id"`
	LinkAToB  string               `json:"link_a_to_b,omitempty"`
	LinkBToA  string               `json:"link_b_to_a,omitempty"`
	Candidate *candidateRecordJSON `json:"candidate,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Proto → JSON conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func burstProtoToJSON(b *pb.BurstRecord) *burstRecordJSON {
	if b == nil {
		return nil
	}
	j := &burstRecordJSON{
		ID:         b.Id,
		NoteURN:    b.NoteUrn,
		ProjectURN: b.ProjectUrn,
		FolderURN:  b.FolderUrn,
		AuthorURN:  b.AuthorUrn,
		Sequence:   b.Sequence,
		LineStart:  b.LineStart,
		LineEnd:    b.LineEnd,
		Text:       b.Text,
		Tokens:     b.Tokens,
		Truncated:  b.Truncated,
	}
	if b.CreatedAt != nil {
		j.CreatedAt = b.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

func candidateProtoToJSON(c *pb.CandidateRecord) *candidateRecordJSON {
	if c == nil {
		return nil
	}
	j := &candidateRecordJSON{
		ID:           c.Id,
		BurstAID:     c.BurstAId,
		BurstBID:     c.BurstBId,
		NoteURN_A:    c.NoteUrnA,
		NoteURN_B:    c.NoteUrnB,
		ProjectURN:   c.ProjectUrn,
		OverlapScore: c.OverlapScore,
		BM25Score:    c.Bm25Score,
		Status:       c.Status,
		ReviewedBy:   c.ReviewedBy,
		PromotedLink: c.PromotedLink,
	}
	if c.CreatedAt != nil {
		j.CreatedAt = c.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if c.ReviewedAt != nil {
		j.ReviewedAt = c.ReviewedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if c.BurstA != nil {
		j.BurstA = burstProtoToJSON(c.BurstA)
	}
	if c.BurstB != nil {
		j.BurstB = burstProtoToJSON(c.BurstB)
	}
	return j
}

func statsProtoToJSON(s *pb.ContextStats) *contextStatsJSON {
	if s == nil {
		return nil
	}
	return &contextStatsJSON{
		BurstsTotal:                 s.BurstsTotal,
		BurstsToday:                 s.BurstsToday,
		CandidatesPending:           s.CandidatesPending,
		CandidatesPendingUnenriched: s.CandidatesPendingUnenriched,
		CandidatesPromoted:          s.CandidatesPromoted,
		CandidatesDismissed:         s.CandidatesDismissed,
		OldestPendingAgeDays:        s.OldestPendingAgeDays,
	}
}

func projectConfigProtoToJSON(c *pb.ProjectContextConfig) *projectContextConfigJSON {
	if c == nil {
		return nil
	}
	j := &projectContextConfigJSON{
		ProjectURN:               c.ProjectUrn,
		BurstMaxPerNotePerDay:    c.BurstMaxPerNotePerDay,
		BurstMaxPerProjectPerDay: c.BurstMaxPerProjectPerDay,
	}
	if c.UpdatedAt != nil {
		j.UpdatedAt = c.UpdatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

// ─────────────────────────────────────────────────────────────────────────────
// Route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

// routeContextStats — GET /v1/context/stats
func (h *Handler) routeContextStats(w http.ResponseWriter, r *http.Request) {
	if h.contextSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "context service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetContextStats(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeContextCandidates — GET /v1/context/candidates
func (h *Handler) routeContextCandidates(w http.ResponseWriter, r *http.Request) {
	if h.contextSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "context service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListCandidates(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeContextCandidate handles:
//
//	GET  /v1/context/candidates/{id}
//	POST /v1/context/candidates/{id}/promote
//	POST /v1/context/candidates/{id}/dismiss
func (h *Handler) routeContextCandidate(w http.ResponseWriter, r *http.Request) {
	if h.contextSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "context service not available")
		return
	}

	// Strip prefix to get "{id}" or "{id}/promote" or "{id}/dismiss".
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/context/candidates/")
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "candidate id is required")
		return
	}

	id, sub, hasSub := strings.Cut(trimmed, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "candidate id is required")
		return
	}

	if hasSub {
		switch sub {
		case "promote":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handlePromoteCandidate(w, r, id)
		case "dismiss":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleDismissCandidate(w, r, id)
		default:
			writeError(w, http.StatusNotFound, "unknown candidate sub-resource: "+sub)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetCandidate(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeContextBursts — GET /v1/context/bursts
func (h *Handler) routeContextBursts(w http.ResponseWriter, r *http.Request) {
	if h.contextSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "context service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListBursts(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeContextBurst — GET /v1/context/bursts/{id}
func (h *Handler) routeContextBurst(w http.ResponseWriter, r *http.Request) {
	if h.contextSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "context service not available")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/v1/context/bursts/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "burst id is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetBurst(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeContextConfig — GET/PUT /v1/context/config/{project_urn}
func (h *Handler) routeContextConfig(w http.ResponseWriter, r *http.Request) {
	if h.contextSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "context service not available")
		return
	}

	projectURN := strings.TrimPrefix(r.URL.Path, "/v1/context/config/")
	if projectURN == "" {
		writeError(w, http.StatusBadRequest, "project_urn is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetProjectConfig(w, r, projectURN)
	case http.MethodPut:
		h.handleSetProjectConfig(w, r, projectURN)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/context/stats
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetContextStats(w http.ResponseWriter, r *http.Request) {
	projectURN := r.URL.Query().Get("project_urn")

	resp, err := h.contextSvc.GetStats(r.Context(), &pb.GetStatsRequest{
		ProjectUrn: projectURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get context stats")
		return
	}

	writeJSON(w, http.StatusOK, statsProtoToJSON(resp.Stats))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/context/candidates
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListCandidates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectURN := q.Get("project_urn")
	noteURN := q.Get("note_urn")
	statusStr := q.Get("status")
	minScore := 0.0
	if s := q.Get("min_score"); s != "" {
		fmt.Sscanf(s, "%f", &minScore)
	}
	includeBursts := q.Get("include_bursts") == "true"
	var pageSize int32
	if ps := q.Get("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	pageToken := q.Get("page_token")

	resp, err := h.contextSvc.ListCandidates(r.Context(), &pb.ListCandidatesRequest{
		ProjectUrn:    projectURN,
		NoteUrn:       noteURN,
		Status:        statusStr,
		MinScore:      minScore,
		IncludeBursts: includeBursts,
		PageSize:      pageSize,
		PageToken:     pageToken,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list candidates")
		return
	}

	out := make([]*candidateRecordJSON, 0, len(resp.Candidates))
	for _, c := range resp.Candidates {
		out = append(out, candidateProtoToJSON(c))
	}
	writeJSON(w, http.StatusOK, &listCandidatesResponse{
		Candidates:    out,
		NextPageToken: resp.NextPageToken,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/context/candidates/{id}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetCandidate(w http.ResponseWriter, r *http.Request, id string) {
	// include_bursts defaults to true when not explicitly set to "false".
	includeBursts := r.URL.Query().Get("include_bursts") != "false"

	resp, err := h.contextSvc.GetCandidate(r.Context(), &pb.GetCandidateRequest{
		Id:            id,
		IncludeBursts: includeBursts,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get candidate")
		return
	}

	writeJSON(w, http.StatusOK, candidateProtoToJSON(resp.Candidate))
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/context/candidates/{id}/promote
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handlePromoteCandidate(w http.ResponseWriter, r *http.Request, id string) {
	var req promoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Direction == "" {
		req.Direction = "both"
	}
	if req.ReviewerURN == "" {
		req.ReviewerURN = "urn:notx:usr:anon"
	}

	resp, err := h.contextSvc.PromoteCandidate(r.Context(), &pb.PromoteCandidateRequest{
		Id:          id,
		Label:       req.Label,
		Direction:   req.Direction,
		ReviewerUrn: req.ReviewerURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "promote candidate")
		return
	}

	writeJSON(w, http.StatusOK, &promoteResponse{
		AnchorAID: resp.AnchorAId,
		AnchorBID: resp.AnchorBId,
		LinkAToB:  resp.LinkAToB,
		LinkBToA:  resp.LinkBToA,
		Candidate: candidateProtoToJSON(resp.Candidate),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/context/candidates/{id}/dismiss
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDismissCandidate(w http.ResponseWriter, r *http.Request, id string) {
	var req dismissRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.ReviewerURN == "" {
		req.ReviewerURN = "urn:notx:usr:anon"
	}

	resp, err := h.contextSvc.DismissCandidate(r.Context(), &pb.DismissCandidateRequest{
		Id:          id,
		ReviewerUrn: req.ReviewerURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "dismiss candidate")
		return
	}

	writeJSON(w, http.StatusOK, candidateProtoToJSON(resp.Candidate))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/context/bursts
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListBursts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	noteURN := q.Get("note_urn")
	if noteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}

	var sinceSequence int32
	if s := q.Get("since_sequence"); s != "" {
		fmt.Sscanf(s, "%d", &sinceSequence)
	}
	var pageSize int32
	if ps := q.Get("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	pageToken := q.Get("page_token")

	resp, err := h.contextSvc.ListBursts(r.Context(), &pb.ListBurstsRequest{
		NoteUrn:       noteURN,
		SinceSequence: sinceSequence,
		PageSize:      pageSize,
		PageToken:     pageToken,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list bursts")
		return
	}

	out := make([]*burstRecordJSON, 0, len(resp.Bursts))
	for _, b := range resp.Bursts {
		out = append(out, burstProtoToJSON(b))
	}
	writeJSON(w, http.StatusOK, &listBurstsResponse{
		Bursts:        out,
		NextPageToken: resp.NextPageToken,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/context/bursts/{id}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetBurst(w http.ResponseWriter, r *http.Request, id string) {
	resp, err := h.contextSvc.GetBurst(r.Context(), &pb.GetBurstRequest{
		Id: id,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get burst")
		return
	}

	writeJSON(w, http.StatusOK, burstProtoToJSON(resp.Burst))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/context/config/{project_urn}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetProjectConfig(w http.ResponseWriter, r *http.Request, projectURN string) {
	resp, err := h.contextSvc.GetProjectConfig(r.Context(), &pb.GetProjectConfigRequest{
		ProjectUrn: projectURN,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get project config")
		return
	}

	writeJSON(w, http.StatusOK, projectConfigProtoToJSON(resp.Config))
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /v1/context/config/{project_urn}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleSetProjectConfig(w http.ResponseWriter, r *http.Request, projectURN string) {
	var req setProjectConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.contextSvc.SetProjectConfig(r.Context(), &pb.SetProjectConfigRequest{
		ProjectUrn:               projectURN,
		BurstMaxPerNotePerDay:    req.BurstMaxPerNotePerDay,
		BurstMaxPerProjectPerDay: req.BurstMaxPerProjectPerDay,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "set project config")
		return
	}

	writeJSON(w, http.StatusOK, projectConfigProtoToJSON(resp.Config))
}
