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

type noteAnalysisJSON struct {
	ID             string         `json:"id"`
	NoteURN        string         `json:"note_urn"`
	ProjectURN     string         `json:"project_urn"`
	FolderURN      string         `json:"folder_urn"`
	AllConcepts    []string       `json:"all_concepts"`
	ThemeConcepts  []string       `json:"theme_concepts"`
	Families       []string       `json:"families"`
	DominantRole   string         `json:"dominant_role"`
	RoleCounts     map[string]int `json:"role_counts"`
	ParagraphCount int            `json:"paragraph_count"`
	HeadSeq        int            `json:"head_seq"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type noteRelationJSON struct {
	ID            string    `json:"id"`
	SourceNoteURN string    `json:"source_note_urn"`
	TargetNoteURN string    `json:"target_note_urn"`
	ProjectURN    string    `json:"project_urn"`
	FolderURN     string    `json:"folder_urn"`
	RelationType  string    `json:"relation_type"`
	Score         float64   `json:"score"`
	ReasonSignals []string  `json:"reason_signals"`
	Version       string    `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Converters
// ─────────────────────────────────────────────────────────────────────────────

func noteAnalysisToJSON(r repo.NoteAnalysisRecord) noteAnalysisJSON {
	return noteAnalysisJSON{
		ID:             r.ID,
		NoteURN:        r.NoteURN,
		ProjectURN:     r.ProjectURN,
		FolderURN:      r.FolderURN,
		AllConcepts:    r.AllConcepts,
		ThemeConcepts:  r.ThemeConcepts,
		Families:       r.Families,
		DominantRole:   r.DominantRole,
		RoleCounts:     r.RoleCounts,
		ParagraphCount: r.ParagraphCount,
		HeadSeq:        r.HeadSeq,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func noteRelationToJSON(r repo.NoteRelationRecord) noteRelationJSON {
	return noteRelationJSON{
		ID:            r.ID,
		SourceNoteURN: r.SourceNoteURN,
		TargetNoteURN: r.TargetNoteURN,
		ProjectURN:    r.ProjectURN,
		FolderURN:     r.FolderURN,
		RelationType:  r.RelationType,
		Score:         r.Score,
		ReasonSignals: r.ReasonSignals,
		Version:       r.Version,
		CreatedAt:     r.CreatedAt,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/note-analyses?project_urn=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleNoteAnalyses(w http.ResponseWriter, r *http.Request) {
	if h.noteAnalysisRepo == nil {
		writeError(w, http.StatusNotFound, "note analysis service not available")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	projectURN := r.URL.Query().Get("project_urn")
	if projectURN == "" {
		writeError(w, http.StatusBadRequest, "project_urn is required")
		return
	}
	records, err := h.noteAnalysisRepo.ListNoteAnalyses(r.Context(), projectURN)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list note analyses: "+err.Error())
		return
	}
	out := make([]noteAnalysisJSON, len(records))
	for i, rec := range records {
		out[i] = noteAnalysisToJSON(rec)
	}
	writeJSON(w, http.StatusOK, out)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/note-relations?project_urn=...
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleNoteRelations(w http.ResponseWriter, r *http.Request) {
	if h.noteAnalysisRepo == nil {
		writeError(w, http.StatusNotFound, "note analysis service not available")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	projectURN := r.URL.Query().Get("project_urn")
	if projectURN == "" {
		writeError(w, http.StatusBadRequest, "project_urn is required")
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}
	records, err := h.noteAnalysisRepo.ListNoteRelations(r.Context(), projectURN, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list note relations: "+err.Error())
		return
	}
	out := make([]noteRelationJSON, len(records))
	for i, rec := range records {
		out[i] = noteRelationToJSON(rec)
	}
	writeJSON(w, http.StatusOK, out)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/note-relations/{note_urn}
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleNoteRelationsForNote(w http.ResponseWriter, r *http.Request) {
	if h.noteAnalysisRepo == nil {
		writeError(w, http.StatusNotFound, "note analysis service not available")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	noteURN := strings.TrimPrefix(r.URL.Path, "/v1/note-relations/")
	if noteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required in path")
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}
	records, err := h.noteAnalysisRepo.ListNoteRelationsForNote(r.Context(), noteURN, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list note relations: "+err.Error())
		return
	}
	out := make([]noteRelationJSON, len(records))
	for i, rec := range records {
		out[i] = noteRelationToJSON(rec)
	}
	writeJSON(w, http.StatusOK, out)
}
