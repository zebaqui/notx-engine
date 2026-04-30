package http

import (
	"net/http"
	"strconv"

	"github.com/zebaqui/notx-engine/repo"
)

// routeSnips handles GET /v1/snips — list snips with optional filters.
func (h *Handler) routeSnips(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	opts := repo.ListSnipsOptions{
		SnipType:     q.Get("snip_type"),
		ProjectURN:   q.Get("project_urn"),
		ParentURN:    q.Get("parent_urn"),
		ParentAnchor: q.Get("parent_anchor"),
		PageToken:    q.Get("page_token"),
	}
	if q.Get("include_deleted") == "true" {
		opts.IncludeDeleted = true
	}
	if v := q.Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.PageSize = n
		}
	}

	result, err := h.noteSvc.ListSnips(r.Context(), opts)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list snips")
		return
	}

	out := make([]*noteHeaderJSON, 0, len(result.Notes))
	for _, n := range result.Notes {
		out = append(out, coreNoteToJSON(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snips":           out,
		"next_page_token": result.NextPageToken,
	})
}
