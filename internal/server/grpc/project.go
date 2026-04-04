package grpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// ProjectServer implements pb.ProjectServiceServer backed by a
// repo.ProjectRepository.
type ProjectServer struct {
	pb.UnimplementedProjectServiceServer
	repo        repo.ProjectRepository
	defaultPage int
	maxPage     int
}

// NewProjectServer returns a ready-to-register ProjectServer.
func NewProjectServer(r repo.ProjectRepository, defaultPage, maxPage int) *ProjectServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &ProjectServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// ── Projects ──────────────────────────────────────────────────────────────────

func (s *ProjectServer) CreateProject(ctx context.Context, req *pb.CreateProjectRequest) (*pb.CreateProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	urn, err := core.ParseURN(req.Urn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid urn: %v", err)
	}

	now := time.Now().UTC()
	proj := &core.Project{
		URN:         urn,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateProject(ctx, proj); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.CreateProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServer) GetProject(ctx context.Context, req *pb.GetProjectRequest) (*pb.GetProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	proj, err := s.repo.GetProject(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.GetProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServer) ListProjects(ctx context.Context, req *pb.ListProjectsRequest) (*pb.ListProjectsResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	result, err := s.repo.ListProjects(ctx, repo.ProjectListOptions{
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       pageSize,
		PageToken:      req.PageToken,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list projects: %v", err)
	}

	protos := make([]*pb.Project, 0, len(result.Projects))
	for _, p := range result.Projects {
		protos = append(protos, coreProjectToProto(p))
	}

	return &pb.ListProjectsResponse{
		Projects:      protos,
		NextPageToken: result.NextPageToken,
	}, nil
}

func (s *ProjectServer) UpdateProject(ctx context.Context, req *pb.UpdateProjectRequest) (*pb.UpdateProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	proj, err := s.repo.GetProject(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	if req.Project != nil && req.Project.Name != "" {
		proj.Name = req.Project.Name
	}
	if req.Project != nil {
		proj.Description = req.Project.Description
		proj.Deleted = req.Project.Deleted
	}
	proj.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateProject(ctx, proj); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.UpdateProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServer) DeleteProject(ctx context.Context, req *pb.DeleteProjectRequest) (*pb.DeleteProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.DeleteProject(ctx, req.Urn); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteProjectResponse{Urn: req.Urn, Deleted: true}, nil
}

// ── Conversion helpers ────────────────────────────────────────────────────────

func coreProjectToProto(p *core.Project) *pb.Project {
	return &pb.Project{
		Urn:         p.URN.String(),
		Name:        p.Name,
		Description: p.Description,
		Deleted:     p.Deleted,
		CreatedAt:   timestamppb.New(p.CreatedAt),
		UpdatedAt:   timestamppb.New(p.UpdatedAt),
	}
}

func projRepoErrToStatus(err error, urn string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "%q not found", urn)
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%q already exists", urn)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// suppress unused import warning if fmt is not used elsewhere
var _ = fmt.Sprintf
