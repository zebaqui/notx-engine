package http

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
)

// ─────────────────────────────────────────────────────────────────────────────
// Projects — route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListProjects(w, r)
	case http.MethodPost:
		h.handleCreateProject(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeProject(w http.ResponseWriter, r *http.Request) {
	urn := strings.TrimPrefix(r.URL.Path, "/v1/projects/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "project URN is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetProject(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateProject(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteProject(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeFolders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListFolders(w, r)
	case http.MethodPost:
		h.handleCreateFolder(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeFolder(w http.ResponseWriter, r *http.Request) {
	urn := strings.TrimPrefix(r.URL.Path, "/v1/folders/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "folder URN is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetFolder(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateFolder(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteFolder(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — projects & folders
// ─────────────────────────────────────────────────────────────────────────────

type projectJSON struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type createProjectRequest struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type updateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Deleted     *bool   `json:"deleted,omitempty"`
}

type listProjectsResponse struct {
	Projects      []*projectJSON `json:"projects"`
	NextPageToken string         `json:"next_page_token,omitempty"`
}

func coreProjectToJSON(p *core.Project) *projectJSON {
	if p == nil {
		return nil
	}
	return &projectJSON{
		URN:         p.URN.String(),
		Name:        p.Name,
		Description: p.Description,
		Deleted:     p.Deleted,
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type folderJSON struct {
	URN         string `json:"urn"`
	ProjectURN  string `json:"project_urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type createFolderRequest struct {
	URN         string `json:"urn"`
	ProjectURN  string `json:"project_urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type updateFolderRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Deleted     *bool   `json:"deleted,omitempty"`
}

type listFoldersResponse struct {
	Folders       []*folderJSON `json:"folders"`
	NextPageToken string        `json:"next_page_token,omitempty"`
}

func coreFolderToJSON(f *core.Folder) *folderJSON {
	if f == nil {
		return nil
	}
	return &folderJSON{
		URN:         f.URN.String(),
		ProjectURN:  f.ProjectURN.String(),
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
		CreatedAt:   f.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   f.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// derefOrEmpty dereferences a *string, returning "" when nil.
func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/projects
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeDeleted := q.Get("include_deleted") == "true"
	var pageSize int32
	if ps := q.Get("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	pageToken := q.Get("page_token")

	result, err := h.projSvc.List(r.Context(), repo.ProjectListOptions{
		IncludeDeleted: includeDeleted,
		PageSize:       int(pageSize),
		PageToken:      pageToken,
	})
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list projects")
		return
	}

	out := make([]*projectJSON, 0, len(result.Projects))
	for _, p := range result.Projects {
		out = append(out, coreProjectToJSON(p))
	}
	writeJSON(w, http.StatusOK, &listProjectsResponse{
		Projects:      out,
		NextPageToken: result.NextPageToken,
	})
}

// POST /v1/projects
func (h *Handler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
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

	urn, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proj := &core.Project{
		URN:         urn,
		Name:        req.Name,
		Description: req.Description,
	}
	if err := h.projSvc.Create(r.Context(), proj); err != nil {
		svcErrToHTTP(w, r, h, err, "create project")
		return
	}

	writeJSON(w, http.StatusCreated, coreProjectToJSON(proj))
}

// GET /v1/projects/<urn>
func (h *Handler) handleGetProject(w http.ResponseWriter, r *http.Request, urn string) {
	proj, err := h.projSvc.Get(r.Context(), urn)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "get project")
		return
	}
	writeJSON(w, http.StatusOK, coreProjectToJSON(proj))
}

// PATCH /v1/projects/<urn>
func (h *Handler) handleUpdateProject(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	upd := service.ProjectUpdate{
		Name:        derefOrEmpty(req.Name),
		Description: req.Description,
		Deleted:     req.Deleted,
	}

	proj, err := h.projSvc.Update(r.Context(), urn, upd)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "update project")
		return
	}
	writeJSON(w, http.StatusOK, coreProjectToJSON(proj))
}

// DELETE /v1/projects/<urn>
func (h *Handler) handleDeleteProject(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.projSvc.Delete(r.Context(), urn); err != nil {
		svcErrToHTTP(w, r, h, err, "delete project")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Folders
// ─────────────────────────────────────────────────────────────────────────────

// GET /v1/folders
func (h *Handler) handleListFolders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectURN := q.Get("project_urn")
	includeDeleted := q.Get("include_deleted") == "true"
	var pageSize int32
	if ps := q.Get("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	pageToken := q.Get("page_token")

	result, err := h.folderSvc.List(r.Context(), repo.FolderListOptions{
		ProjectURN:     projectURN,
		IncludeDeleted: includeDeleted,
		PageSize:       int(pageSize),
		PageToken:      pageToken,
	})
	if err != nil {
		svcErrToHTTP(w, r, h, err, "list folders")
		return
	}

	out := make([]*folderJSON, 0, len(result.Folders))
	for _, f := range result.Folders {
		out = append(out, coreFolderToJSON(f))
	}
	writeJSON(w, http.StatusOK, &listFoldersResponse{
		Folders:       out,
		NextPageToken: result.NextPageToken,
	})
}

// POST /v1/folders
func (h *Handler) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req createFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.ProjectURN == "" {
		writeError(w, http.StatusBadRequest, "project_urn is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	urn, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projURN, err := core.ParseURN(req.ProjectURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	folder := &core.Folder{
		URN:         urn,
		ProjectURN:  projURN,
		Name:        req.Name,
		Description: req.Description,
	}
	if err := h.folderSvc.Create(r.Context(), folder); err != nil {
		svcErrToHTTP(w, r, h, err, "create folder")
		return
	}

	writeJSON(w, http.StatusCreated, coreFolderToJSON(folder))
}

// GET /v1/folders/<urn>
func (h *Handler) handleGetFolder(w http.ResponseWriter, r *http.Request, urn string) {
	folder, err := h.folderSvc.Get(r.Context(), urn)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "get folder")
		return
	}
	writeJSON(w, http.StatusOK, coreFolderToJSON(folder))
}

// PATCH /v1/folders/<urn>
func (h *Handler) handleUpdateFolder(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	upd := service.FolderUpdate{
		Name:        derefOrEmpty(req.Name),
		Description: req.Description,
		Deleted:     req.Deleted,
	}

	folder, err := h.folderSvc.Update(r.Context(), urn, upd)
	if err != nil {
		svcErrToHTTP(w, r, h, err, "update folder")
		return
	}
	writeJSON(w, http.StatusOK, coreFolderToJSON(folder))
}

// DELETE /v1/folders/<urn>
func (h *Handler) handleDeleteFolder(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.folderSvc.Delete(r.Context(), urn); err != nil {
		svcErrToHTTP(w, r, h, err, "delete folder")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
