package grpc

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// UserServer implements pb.UserServiceServer backed by repo.UserRepository.
type UserServer struct {
	pb.UnimplementedUserServiceServer
	repo        repo.UserRepository
	defaultPage int
	maxPage     int
}

// NewUserServer returns a ready-to-register UserServer.
func NewUserServer(r repo.UserRepository, defaultPage, maxPage int) *UserServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &UserServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// CreateUser validates the request, builds a core.User, and persists it.
func (s *UserServer) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}
	if req.DisplayName == "" {
		return nil, status.Error(codes.InvalidArgument, "display_name is required")
	}

	urn, err := core.ParseURN(req.Urn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid urn: %v", err)
	}
	if urn.ObjectType != core.ObjectTypeUser {
		return nil, status.Errorf(codes.InvalidArgument, "urn object type must be %q, got %q", core.ObjectTypeUser, urn.ObjectType)
	}

	now := time.Now().UTC()
	u := &core.User{
		URN:         urn,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateUser(ctx, u); err != nil {
		return nil, userRepoErrToStatus(err, req.Urn)
	}

	return &pb.CreateUserResponse{User: coreUserToProto(u)}, nil
}

// GetUser fetches a single user by URN.
func (s *UserServer) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.GetUserResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	u, err := s.repo.GetUser(ctx, req.Urn)
	if err != nil {
		return nil, userRepoErrToStatus(err, req.Urn)
	}

	return &pb.GetUserResponse{User: coreUserToProto(u)}, nil
}

// ListUsers returns a paginated list of users.
func (s *UserServer) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	result, err := s.repo.ListUsers(ctx, repo.UserListOptions{
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       pageSize,
		PageToken:      req.PageToken,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list users: %v", err)
	}

	protos := make([]*pb.User, 0, len(result.Users))
	for _, u := range result.Users {
		protos = append(protos, coreUserToProto(u))
	}

	return &pb.ListUsersResponse{
		Users:         protos,
		NextPageToken: result.NextPageToken,
	}, nil
}

// UpdateUser fetches the existing user, applies the patch, and persists it.
func (s *UserServer) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.UpdateUserResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	u, err := s.repo.GetUser(ctx, req.Urn)
	if err != nil {
		return nil, userRepoErrToStatus(err, req.Urn)
	}

	if req.User != nil {
		if req.User.DisplayName != "" {
			u.DisplayName = req.User.DisplayName
		}
		u.Email = req.User.Email
		u.Deleted = req.User.Deleted
	}
	u.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateUser(ctx, u); err != nil {
		return nil, userRepoErrToStatus(err, req.Urn)
	}

	return &pb.UpdateUserResponse{User: coreUserToProto(u)}, nil
}

// DeleteUser removes a user by URN.
func (s *UserServer) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.DeleteUser(ctx, req.Urn); err != nil {
		return nil, userRepoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteUserResponse{Urn: req.Urn, Deleted: true}, nil
}

// ── Conversion helpers ────────────────────────────────────────────────────────

func coreUserToProto(u *core.User) *pb.User {
	return &pb.User{
		Urn:         u.URN.String(),
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Deleted:     u.Deleted,
		CreatedAt:   timestamppb.New(u.CreatedAt),
		UpdatedAt:   timestamppb.New(u.UpdatedAt),
	}
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func userRepoErrToStatus(err error, urn string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "%q not found", urn)
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%q already exists", urn)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}
