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
	"github.com/zebaqui/notx-engine/internal/repo"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// ProjectServiceServer implements pb.ProjectServiceServer backed by a
// repo.ProjectRepository.
type ProjectServiceServer struct {
	pb.UnimplementedProjectServiceServer
	repo        repo.ProjectRepository
	defaultPage int
	maxPage     int
}

// NewProjectServiceServer returns a ready-to-register ProjectServiceServer.
func NewProjectServiceServer(r repo.ProjectRepository, defaultPage, maxPage int) *ProjectServiceServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &ProjectServiceServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// ── Projects ─────────────────────────────────────────────────────────────────

func (s *ProjectServiceServer) CreateProject(ctx context.Context, req *pb.CreateProjectRequest) (*pb.CreateProjectResponse, error) {
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

func (s *ProjectServiceServer) GetProject(ctx context.Context, req *pb.GetProjectRequest) (*pb.GetProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	proj, err := s.repo.GetProject(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.GetProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServiceServer) ListProjects(ctx context.Context, req *pb.ListProjectsRequest) (*pb.ListProjectsResponse, error) {
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

	protos := make([]*pb.ProjectProto, 0, len(result.Projects))
	for _, p := range result.Projects {
		protos = append(protos, coreProjectToProto(p))
	}

	return &pb.ListProjectsResponse{
		Projects:      protos,
		NextPageToken: result.NextPageToken,
	}, nil
}

func (s *ProjectServiceServer) UpdateProject(ctx context.Context, req *pb.UpdateProjectRequest) (*pb.UpdateProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	proj, err := s.repo.GetProject(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	if req.Name != "" {
		proj.Name = req.Name
	}
	proj.Description = req.Description
	proj.Deleted = req.Deleted
	proj.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateProject(ctx, proj); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.UpdateProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServiceServer) DeleteProject(ctx context.Context, req *pb.DeleteProjectRequest) (*pb.DeleteProjectResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.DeleteProject(ctx, req.Urn); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteProjectResponse{Deleted: true}, nil
}

// ── Folders ──────────────────────────────────────────────────────────────────

func (s *ProjectServiceServer) CreateFolder(ctx context.Context, req *pb.CreateFolderRequest) (*pb.CreateFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}
	if req.ProjectUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "project_urn is required")
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	urn, err := core.ParseURN(req.Urn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid urn: %v", err)
	}
	projURN, err := core.ParseURN(req.ProjectUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid project_urn: %v", err)
	}

	now := time.Now().UTC()
	f := &core.Folder{
		URN:         urn,
		ProjectURN:  projURN,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateFolder(ctx, f); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.CreateFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *ProjectServiceServer) GetFolder(ctx context.Context, req *pb.GetFolderRequest) (*pb.GetFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	f, err := s.repo.GetFolder(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.GetFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *ProjectServiceServer) ListFolders(ctx context.Context, req *pb.ListFoldersRequest) (*pb.ListFoldersResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	result, err := s.repo.ListFolders(ctx, repo.FolderListOptions{
		ProjectURN:     req.ProjectUrn,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       pageSize,
		PageToken:      req.PageToken,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list folders: %v", err)
	}

	protos := make([]*pb.FolderProto, 0, len(result.Folders))
	for _, f := range result.Folders {
		protos = append(protos, coreFolderToProto(f))
	}

	return &pb.ListFoldersResponse{
		Folders:       protos,
		NextPageToken: result.NextPageToken,
	}, nil
}

func (s *ProjectServiceServer) UpdateFolder(ctx context.Context, req *pb.UpdateFolderRequest) (*pb.UpdateFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	f, err := s.repo.GetFolder(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	if req.Name != "" {
		f.Name = req.Name
	}
	f.Description = req.Description
	f.Deleted = req.Deleted
	f.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateFolder(ctx, f); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.UpdateFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *ProjectServiceServer) DeleteFolder(ctx context.Context, req *pb.DeleteFolderRequest) (*pb.DeleteFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.DeleteFolder(ctx, req.Urn); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteFolderResponse{Deleted: true}, nil
}

// ── Conversion helpers ────────────────────────────────────────────────────────

func coreProjectToProto(p *core.Project) *pb.ProjectProto {
	return &pb.ProjectProto{
		Urn:         p.URN.String(),
		Name:        p.Name,
		Description: p.Description,
		Deleted:     p.Deleted,
		CreatedAt:   timestamppb.New(p.CreatedAt),
		UpdatedAt:   timestamppb.New(p.UpdatedAt),
	}
}

func coreFolderToProto(f *core.Folder) *pb.FolderProto {
	return &pb.FolderProto{
		Urn:         f.URN.String(),
		ProjectUrn:  f.ProjectURN.String(),
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
		CreatedAt:   timestamppb.New(f.CreatedAt),
		UpdatedAt:   timestamppb.New(f.UpdatedAt),
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
