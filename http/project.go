package http

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "github.com/zebaqui/notx-engine/proto"
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

func projectProtoToJSON(p *pb.Project) *projectJSON {
	if p == nil {
		return nil
	}
	j := &projectJSON{
		URN:         p.Urn,
		Name:        p.Name,
		Description: p.Description,
		Deleted:     p.Deleted,
	}
	if p.CreatedAt != nil {
		j.CreatedAt = p.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if p.UpdatedAt != nil {
		j.UpdatedAt = p.UpdatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
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

func folderProtoToJSON(f *pb.Folder) *folderJSON {
	if f == nil {
		return nil
	}
	j := &folderJSON{
		URN:         f.Urn,
		ProjectURN:  f.ProjectUrn,
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
	}
	if f.CreatedAt != nil {
		j.CreatedAt = f.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if f.UpdatedAt != nil {
		j.UpdatedAt = f.UpdatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
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

	resp, err := h.projSvc.ListProjects(r.Context(), &pb.ListProjectsRequest{
		IncludeDeleted: includeDeleted,
		PageSize:       pageSize,
		PageToken:      pageToken,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list projects")
		return
	}

	out := make([]*projectJSON, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		out = append(out, projectProtoToJSON(p))
	}
	writeJSON(w, http.StatusOK, &listProjectsResponse{
		Projects:      out,
		NextPageToken: resp.NextPageToken,
	})
}

// POST /v1/projects
func (h *Handler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}

	resp, err := h.projSvc.CreateProject(r.Context(), &pb.CreateProjectRequest{
		Urn:         req.URN,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "create project")
		return
	}

	writeJSON(w, http.StatusCreated, projectProtoToJSON(resp.Project))
}

// GET /v1/projects/<urn>
func (h *Handler) handleGetProject(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.projSvc.GetProject(r.Context(), &pb.GetProjectRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get project")
		return
	}
	writeJSON(w, http.StatusOK, projectProtoToJSON(resp.Project))
}

// PATCH /v1/projects/<urn>
func (h *Handler) handleUpdateProject(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Fetch current state to apply partial patch.
	current, err := h.projSvc.GetProject(r.Context(), &pb.GetProjectRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get project for update")
		return
	}

	grpcReq := &pb.UpdateProjectRequest{
		Urn: urn,
		Project: &pb.Project{
			Urn:         urn,
			Name:        current.Project.Name,
			Description: current.Project.Description,
			Deleted:     current.Project.Deleted,
		},
	}
	if req.Name != nil {
		grpcReq.Project.Name = *req.Name
	}
	if req.Description != nil {
		grpcReq.Project.Description = *req.Description
	}
	if req.Deleted != nil {
		grpcReq.Project.Deleted = *req.Deleted
	}

	resp, err := h.projSvc.UpdateProject(r.Context(), grpcReq)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "update project")
		return
	}
	writeJSON(w, http.StatusOK, projectProtoToJSON(resp.Project))
}

// DELETE /v1/projects/<urn>
func (h *Handler) handleDeleteProject(w http.ResponseWriter, r *http.Request, urn string) {
	_, err := h.projSvc.DeleteProject(r.Context(), &pb.DeleteProjectRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete project")
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

	resp, err := h.folderSvc.ListFolders(r.Context(), &pb.ListFoldersRequest{
		ProjectUrn:     projectURN,
		IncludeDeleted: includeDeleted,
		PageSize:       pageSize,
		PageToken:      pageToken,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list folders")
		return
	}

	out := make([]*folderJSON, 0, len(resp.Folders))
	for _, f := range resp.Folders {
		out = append(out, folderProtoToJSON(f))
	}
	writeJSON(w, http.StatusOK, &listFoldersResponse{
		Folders:       out,
		NextPageToken: resp.NextPageToken,
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

	resp, err := h.folderSvc.CreateFolder(r.Context(), &pb.CreateFolderRequest{
		Urn:         req.URN,
		ProjectUrn:  req.ProjectURN,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "create folder")
		return
	}

	writeJSON(w, http.StatusCreated, folderProtoToJSON(resp.Folder))
}

// GET /v1/folders/<urn>
func (h *Handler) handleGetFolder(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.folderSvc.GetFolder(r.Context(), &pb.GetFolderRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get folder")
		return
	}
	writeJSON(w, http.StatusOK, folderProtoToJSON(resp.Folder))
}

// PATCH /v1/folders/<urn>
func (h *Handler) handleUpdateFolder(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Fetch current state to apply partial patch.
	current, err := h.folderSvc.GetFolder(r.Context(), &pb.GetFolderRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get folder for update")
		return
	}

	grpcReq := &pb.UpdateFolderRequest{
		Urn: urn,
		Folder: &pb.Folder{
			Urn:         urn,
			ProjectUrn:  current.Folder.ProjectUrn,
			Name:        current.Folder.Name,
			Description: current.Folder.Description,
			Deleted:     current.Folder.Deleted,
		},
	}
	if req.Name != nil {
		grpcReq.Folder.Name = *req.Name
	}
	if req.Description != nil {
		grpcReq.Folder.Description = *req.Description
	}
	if req.Deleted != nil {
		grpcReq.Folder.Deleted = *req.Deleted
	}

	resp, err := h.folderSvc.UpdateFolder(r.Context(), grpcReq)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "update folder")
		return
	}
	writeJSON(w, http.StatusOK, folderProtoToJSON(resp.Folder))
}

// DELETE /v1/folders/<urn>
func (h *Handler) handleDeleteFolder(w http.ResponseWriter, r *http.Request, urn string) {
	_, err := h.folderSvc.DeleteFolder(r.Context(), &pb.DeleteFolderRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete folder")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
