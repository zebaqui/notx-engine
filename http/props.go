package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Props ── route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routePropSchemas(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListPropSchemas(w, r)
	case http.MethodPost:
		h.handleCreatePropSchema(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routePropSchema(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/props/schemas/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "schema id is required")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		h.handleUpdatePropSchema(w, r, id)
	case http.MethodDelete:
		h.handleDeletePropSchema(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types
// ─────────────────────────────────────────────────────────────────────────────

type propSchemaJSON struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	Options   []string `json:"options"`
	Position  int      `json:"position"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func propSchemaToJSON(s repo.PropSchema) propSchemaJSON {
	opts := s.Options
	if opts == nil {
		opts = []string{}
	}
	return propSchemaJSON{
		ID:        s.ID,
		Name:      s.Name,
		Key:       s.Key,
		Type:      s.Type,
		Options:   opts,
		Position:  s.Position,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/props/schemas
// ─────────────────────────────────────────────────────────────────────────────

type listPropSchemasResponse struct {
	Schemas []propSchemaJSON `json:"schemas"`
}

func (h *Handler) handleListPropSchemas(w http.ResponseWriter, r *http.Request) {
	if h.propSvc == nil {
		writeJSON(w, http.StatusOK, &listPropSchemasResponse{Schemas: []propSchemaJSON{}})
		return
	}
	schemas, err := h.propSvc.List(r.Context())
	if err != nil {
		h.internalError(w, r, "list prop schemas", err)
		return
	}
	out := &listPropSchemasResponse{Schemas: make([]propSchemaJSON, 0, len(schemas))}
	for _, s := range schemas {
		out.Schemas = append(out.Schemas, propSchemaToJSON(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/props/schemas
// ─────────────────────────────────────────────────────────────────────────────

type createPropSchemaRequest struct {
	Name     string   `json:"name"`
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Options  []string `json:"options"`
	Position int      `json:"position"`
}

type propSchemaResponse struct {
	Schema propSchemaJSON `json:"schema"`
}

func (h *Handler) handleCreatePropSchema(w http.ResponseWriter, r *http.Request) {
	if h.propSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "prop schema service not available")
		return
	}
	var req createPropSchemaRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s := &repo.PropSchema{
		Name:     req.Name,
		Key:      req.Key,
		Type:     req.Type,
		Options:  req.Options,
		Position: req.Position,
	}
	if err := h.propSvc.Create(r.Context(), s); err != nil {
		svcErrToHTTP(w, r, h, err, "create prop schema")
		return
	}
	writeJSON(w, http.StatusCreated, &propSchemaResponse{Schema: propSchemaToJSON(*s)})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/props/schemas/:id
// ─────────────────────────────────────────────────────────────────────────────

type updatePropSchemaRequest struct {
	Name     *string  `json:"name,omitempty"`
	Key      *string  `json:"key,omitempty"`
	Type     *string  `json:"type,omitempty"`
	Options  []string `json:"options,omitempty"`
	Position *int     `json:"position,omitempty"`
}

func (h *Handler) handleUpdatePropSchema(w http.ResponseWriter, r *http.Request, id string) {
	if h.propSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "prop schema service not available")
		return
	}

	// Fetch existing to carry forward unchanged fields.
	existing, err := h.propSvc.Get(r.Context(), id)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "get prop schema for update")
		return
	}

	var req updatePropSchemaRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Key != nil {
		existing.Key = *req.Key
	}
	if req.Type != nil {
		existing.Type = *req.Type
	}
	if req.Options != nil {
		existing.Options = req.Options
	}
	if req.Position != nil {
		existing.Position = *req.Position
	}

	if err := h.propSvc.Update(r.Context(), &existing); err != nil {
		svcErrToHTTP(w, r, h, err, "update prop schema")
		return
	}
	writeJSON(w, http.StatusOK, &propSchemaResponse{Schema: propSchemaToJSON(existing)})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/props/schemas/:id
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDeletePropSchema(w http.ResponseWriter, r *http.Request, id string) {
	if h.propSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "prop schema service not available")
		return
	}
	if err := h.propSvc.Delete(r.Context(), id); err != nil {
		svcErrToHTTP(w, r, h, err, "delete prop schema")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
