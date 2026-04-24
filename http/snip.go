package http

import (
	"fmt"
	"net/http"

	pb "github.com/zebaqui/notx-engine/proto"
)

// routeSnips handles GET /v1/snips — list snips with optional filters.
func (h *Handler) routeSnips(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	req := &pb.ListSnipsRequest{
		SnipType:     q.Get("snip_type"),
		ProjectUrn:   q.Get("project_urn"),
		ParentUrn:    q.Get("parent_urn"),
		ParentAnchor: q.Get("parent_anchor"),
		PageToken:    q.Get("page_token"),
	}
	if v := q.Get("include_deleted"); v == "true" {
		req.IncludeDeleted = true
	}
	if v := q.Get("page_size"); v != "" {
		var ps int32
		if _, err := fmt.Sscanf(v, "%d", &ps); err == nil {
			req.PageSize = ps
		}
	}

	resp, err := h.noteSvc.ListSnips(r.Context(), req)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "ListSnips")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
