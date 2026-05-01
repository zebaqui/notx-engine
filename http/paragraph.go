package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types
// ─────────────────────────────────────────────────────────────────────────────

type paragraphJSON struct {
	ID                 string    `json:"id"`
	NoteURN            string    `json:"note_urn"`
	ProjectURN         string    `json:"project_urn"`
	FolderURN          string    `json:"folder_urn"`
	Sequence           int       `json:"sequence"`
	Position           int       `json:"position"`
	LineStart          int       `json:"line_start"`
	LineEnd            int       `json:"line_end"`
	Text               string    `json:"text"`
	Role               string    `json:"role"`
	MainConcepts       []string  `json:"main_concepts"`
	SupportingConcepts []string  `json:"supporting_concepts"`
	ConceptFamilies    []string  `json:"concept_families"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type paragraphRelationJSON struct {
	ID                string     `json:"id"`
	SourceParagraphID string     `json:"source_paragraph_id"`
	TargetParagraphID string     `json:"target_paragraph_id"`
	NoteURNSource     string     `json:"note_urn_source"`
	NoteURNTarget     string     `json:"note_urn_target"`
	ProjectURNSource  string     `json:"project_urn_source"`
	ProjectURNTarget  string     `json:"project_urn_target"`
	FolderURNSource   string     `json:"folder_urn_source"`
	FolderURNTarget   string     `json:"folder_urn_target"`
	ProximityTier     string     `json:"proximity_tier"`
	RelationType      string     `json:"relation_type"`
	Score             float64    `json:"score"`
	ReasonSignals     []string   `json:"reason_signals"`
	PatternHash       string     `json:"pattern_hash"`
	Version           string     `json:"version"`
	FeedbackVote      *string    `json:"feedback_vote"`
	FeedbackAt        *time.Time `json:"feedback_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

type paragraphWeightsJSON struct {
	WProximityTier  float64   `json:"w_proximity_tier"`
	WRolePair       float64   `json:"w_role_pair"`
	WOverlap        float64   `json:"w_overlap"`
	WCue            float64   `json:"w_cue"`
	WPattern        float64   `json:"w_pattern"`
	TierSameDoc     float64   `json:"tier_same_doc"`
	TierSameFolder  float64   `json:"tier_same_folder"`
	TierSameProject float64   `json:"tier_same_project"`
	TierGlobal      float64   `json:"tier_global"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Converters
// ─────────────────────────────────────────────────────────────────────────────

func paragraphToJSON(p repo.ParagraphRecord) paragraphJSON {
	mc := p.MainConcepts
	if mc == nil {
		mc = []string{}
	}
	sc := p.SupportingConcepts
	if sc == nil {
		sc = []string{}
	}
	cf := p.ConceptFamilies
	if cf == nil {
		cf = []string{}
	}
	return paragraphJSON{
		ID: p.ID, NoteURN: p.NoteURN, ProjectURN: p.ProjectURN, FolderURN: p.FolderURN,
		Sequence: p.Sequence, Position: p.Position, LineStart: p.LineStart, LineEnd: p.LineEnd,
		Text: p.Text, Role: p.Role,
		MainConcepts: mc, SupportingConcepts: sc, ConceptFamilies: cf,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func relationToJSON(r repo.ParagraphRelationRecord) paragraphRelationJSON {
	sigs := r.ReasonSignals
	if sigs == nil {
		sigs = []string{}
	}
	return paragraphRelationJSON{
		ID: r.ID, SourceParagraphID: r.SourceParagraphID, TargetParagraphID: r.TargetParagraphID,
		NoteURNSource: r.NoteURNSource, NoteURNTarget: r.NoteURNTarget,
		ProjectURNSource: r.ProjectURNSource, ProjectURNTarget: r.ProjectURNTarget,
		FolderURNSource: r.FolderURNSource, FolderURNTarget: r.FolderURNTarget,
		ProximityTier: r.ProximityTier, RelationType: r.RelationType,
		Score: r.Score, ReasonSignals: sigs, PatternHash: r.PatternHash, Version: r.Version,
		FeedbackVote: r.FeedbackVote, FeedbackAt: r.FeedbackAt, CreatedAt: r.CreatedAt,
	}
}

func weightsToJSON(w repo.ParagraphWeights) paragraphWeightsJSON {
	return paragraphWeightsJSON{
		WProximityTier: w.WProximityTier, WRolePair: w.WRolePair,
		WOverlap: w.WOverlap, WCue: w.WCue, WPattern: w.WPattern,
		TierSameDoc: w.TierSameDoc, TierSameFolder: w.TierSameFolder,
		TierSameProject: w.TierSameProject, TierGlobal: w.TierGlobal,
		UpdatedAt: w.UpdatedAt,
	}
}

func weightsFromJSON(j paragraphWeightsJSON) repo.ParagraphWeights {
	return repo.ParagraphWeights{
		WProximityTier: j.WProximityTier, WRolePair: j.WRolePair,
		WOverlap: j.WOverlap, WCue: j.WCue, WPattern: j.WPattern,
		TierSameDoc: j.TierSameDoc, TierSameFolder: j.TierSameFolder,
		TierSameProject: j.TierSameProject, TierGlobal: j.TierGlobal,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Paragraphs
// ─────────────────────────────────────────────────────────────────────────────

type listParagraphsResponse struct {
	Paragraphs    []paragraphJSON `json:"paragraphs"`
	NextPageToken string          `json:"next_page_token,omitempty"`
}

func (h *Handler) routeParagraphs(w http.ResponseWriter, r *http.Request) {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusNotFound, "paragraph service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListParagraphs(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeParagraph(w http.ResponseWriter, r *http.Request) {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusNotFound, "paragraph service not available")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/paragraphs/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "paragraph id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetParagraph(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleListParagraphs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := repo.ParagraphListOptions{
		NoteURN:   q.Get("noteURN"),
		FolderURN: q.Get("folderURN"),
		PageToken: q.Get("pageToken"),
	}
	if ps := q.Get("pageSize"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil {
			opts.PageSize = n
		}
	}
	paragraphs, nextToken, err := h.paragraphSvc.ListParagraphs(r.Context(), opts)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "ListParagraphs")
		return
	}
	out := make([]paragraphJSON, len(paragraphs))
	for i, p := range paragraphs {
		out[i] = paragraphToJSON(p)
	}
	writeJSON(w, http.StatusOK, listParagraphsResponse{Paragraphs: out, NextPageToken: nextToken})
}

func (h *Handler) handleGetParagraph(w http.ResponseWriter, r *http.Request, id string) {
	p, err := h.paragraphSvc.GetParagraph(r.Context(), id)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "GetParagraph")
		return
	}
	writeJSON(w, http.StatusOK, paragraphToJSON(p))
}

// ─────────────────────────────────────────────────────────────────────────────
// Relations
// ─────────────────────────────────────────────────────────────────────────────

type listRelationsResponse struct {
	Relations     []paragraphRelationJSON `json:"relations"`
	NextPageToken string                  `json:"next_page_token,omitempty"`
}

type feedbackRequest struct {
	Vote string `json:"vote"`
}

func (h *Handler) routeParagraphRelations(w http.ResponseWriter, r *http.Request) {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusNotFound, "paragraph service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListRelations(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeParagraphRelation(w http.ResponseWriter, r *http.Request) {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusNotFound, "paragraph service not available")
		return
	}
	// Path: /v1/paragraph-relations/{id} or /v1/paragraph-relations/{id}/feedback
	path := strings.TrimPrefix(r.URL.Path, "/v1/paragraph-relations/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "relation id is required")
		return
	}

	if strings.HasSuffix(path, "/feedback") {
		id := strings.TrimSuffix(path, "/feedback")
		switch r.Method {
		case http.MethodPost:
			h.handleRecordFeedback(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetRelation(w, r, path)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleListRelations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := repo.ParagraphRelationListOptions{
		NoteURN:           q.Get("noteURN"),
		FolderURN:         q.Get("folderURN"),
		SourceParagraphID: q.Get("sourceParagraphId"),
		PageToken:         q.Get("pageToken"),
	}
	if ms := q.Get("minScore"); ms != "" {
		if f, err := strconv.ParseFloat(ms, 64); err == nil {
			opts.MinScore = f
		}
	}
	if ps := q.Get("pageSize"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil {
			opts.PageSize = n
		}
	}
	relations, nextToken, err := h.paragraphSvc.ListRelations(r.Context(), opts)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "ListRelations")
		return
	}
	out := make([]paragraphRelationJSON, len(relations))
	for i, rel := range relations {
		out[i] = relationToJSON(rel)
	}
	writeJSON(w, http.StatusOK, listRelationsResponse{Relations: out, NextPageToken: nextToken})
}

func (h *Handler) handleGetRelation(w http.ResponseWriter, r *http.Request, id string) {
	rel, err := h.paragraphSvc.GetRelation(r.Context(), id)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "GetRelation")
		return
	}
	writeJSON(w, http.StatusOK, relationToJSON(rel))
}

func (h *Handler) handleRecordFeedback(w http.ResponseWriter, r *http.Request, id string) {
	var req feedbackRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.paragraphSvc.RecordFeedback(r.Context(), id, req.Vote); err != nil {
		svcErrToHTTP(w, r, h, err, "RecordFeedback")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Weights
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeParagraphWeights(w http.ResponseWriter, r *http.Request) {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusNotFound, "paragraph service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetParagraphWeights(w, r)
	case http.MethodPut:
		h.handleSetParagraphWeights(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) handleGetParagraphWeights(w http.ResponseWriter, r *http.Request) {
	weights, err := h.paragraphSvc.GetWeights(r.Context())
	if err != nil {
		svcErrToHTTP(w, r, h, err, "GetWeights")
		return
	}
	writeJSON(w, http.StatusOK, weightsToJSON(weights))
}

func (h *Handler) handleSetParagraphWeights(w http.ResponseWriter, r *http.Request) {
	var req paragraphWeightsJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.paragraphSvc.SetWeights(r.Context(), weightsFromJSON(req))
	if err != nil {
		svcErrToHTTP(w, r, h, err, "SetWeights")
		return
	}
	writeJSON(w, http.StatusOK, weightsToJSON(updated))
}

// ─────────────────────────────────────────────────────────────────────────────
// Rebuild
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleParagraphRebuild(w http.ResponseWriter, r *http.Request) {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusNotFound, "paragraph service not available")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := h.paragraphSvc.RebuildGraph(r.Context()); err != nil {
		svcErrToHTTP(w, r, h, err, "RebuildGraph")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rebuild_queued"})
}

// paragraphSvcGuard returns true and writes a 503 if the paragraph service
// is not wired. Callers should return immediately when this returns true.
func (h *Handler) paragraphSvcGuard(w http.ResponseWriter) bool {
	if h.paragraphSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "paragraph service not available")
		return true
	}
	return false
}
