package http

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// Users — route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListUsers(w, r)
	case http.MethodPost:
		h.handleCreateUser(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeUser(w http.ResponseWriter, r *http.Request) {
	urn := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "user URN is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetUser(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateUser(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteUser(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — users
// ─────────────────────────────────────────────────────────────────────────────

type userJSON struct {
	URN         string `json:"urn"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type createUserRequest struct {
	URN         string `json:"urn"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
}

type updateUserRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Deleted     *bool   `json:"deleted,omitempty"`
}

type listUsersResponse struct {
	Users         []*userJSON `json:"users"`
	NextPageToken string      `json:"next_page_token,omitempty"`
}

func userProtoToJSON(u *pb.User) *userJSON {
	if u == nil {
		return nil
	}
	j := &userJSON{
		URN:         u.Urn,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Deleted:     u.Deleted,
	}
	if u.CreatedAt != nil {
		j.CreatedAt = u.CreatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if u.UpdatedAt != nil {
		j.UpdatedAt = u.UpdatedAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/users
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeDeleted := q.Get("include_deleted") == "true"
	var pageSize int32
	if s := q.Get("page_size"); s != "" {
		fmt.Sscan(s, &pageSize)
	}
	pageToken := q.Get("page_token")

	resp, err := h.userSvc.ListUsers(r.Context(), &pb.ListUsersRequest{
		IncludeDeleted: includeDeleted,
		PageSize:       pageSize,
		PageToken:      pageToken,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list users")
		return
	}

	out := make([]*userJSON, 0, len(resp.Users))
	for _, u := range resp.Users {
		out = append(out, userProtoToJSON(u))
	}
	writeJSON(w, http.StatusOK, &listUsersResponse{Users: out, NextPageToken: resp.NextPageToken})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/users
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	resp, err := h.userSvc.CreateUser(r.Context(), &pb.CreateUserRequest{
		Urn:         req.URN,
		DisplayName: req.DisplayName,
		Email:       req.Email,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "create user")
		return
	}
	writeJSON(w, http.StatusCreated, userProtoToJSON(resp.User))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/users/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetUser(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.userSvc.GetUser(r.Context(), &pb.GetUserRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get user")
		return
	}
	writeJSON(w, http.StatusOK, userProtoToJSON(resp.User))
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/users/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Fetch current state to apply partial patch semantics.
	current, err := h.userSvc.GetUser(r.Context(), &pb.GetUserRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get user for update")
		return
	}

	grpcReq := &pb.UpdateUserRequest{
		Urn: urn,
		User: &pb.User{
			Urn:         urn,
			DisplayName: current.User.DisplayName,
			Email:       current.User.Email,
			Deleted:     current.User.Deleted,
		},
	}
	if req.DisplayName != nil {
		grpcReq.User.DisplayName = *req.DisplayName
	}
	if req.Email != nil {
		grpcReq.User.Email = *req.Email
	}
	if req.Deleted != nil {
		grpcReq.User.Deleted = *req.Deleted
	}

	resp, err := h.userSvc.UpdateUser(r.Context(), grpcReq)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "update user")
		return
	}
	writeJSON(w, http.StatusOK, userProtoToJSON(resp.User))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/users/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request, urn string) {
	_, err := h.userSvc.DeleteUser(r.Context(), &pb.DeleteUserRequest{Urn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "delete user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
