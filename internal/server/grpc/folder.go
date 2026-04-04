package grpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// FolderServer implements pb.FolderServiceServer backed by a
// repo.ProjectRepository.
type FolderServer struct {
	pb.UnimplementedFolderServiceServer
	repo        repo.ProjectRepository
	defaultPage int
	maxPage     int
}

// NewFolderServer returns a ready-to-register FolderServer.
func NewFolderServer(r repo.ProjectRepository, defaultPage, maxPage int) *FolderServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &FolderServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// ── Folders ───────────────────────────────────────────────────────────────────

func (s *FolderServer) CreateFolder(ctx context.Context, req *pb.CreateFolderRequest) (*pb.CreateFolderResponse, error) {
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

func (s *FolderServer) GetFolder(ctx context.Context, req *pb.GetFolderRequest) (*pb.GetFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	f, err := s.repo.GetFolder(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.GetFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *FolderServer) ListFolders(ctx context.Context, req *pb.ListFoldersRequest) (*pb.ListFoldersResponse, error) {
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

	protos := make([]*pb.Folder, 0, len(result.Folders))
	for _, f := range result.Folders {
		protos = append(protos, coreFolderToProto(f))
	}

	return &pb.ListFoldersResponse{
		Folders:       protos,
		NextPageToken: result.NextPageToken,
	}, nil
}

func (s *FolderServer) UpdateFolder(ctx context.Context, req *pb.UpdateFolderRequest) (*pb.UpdateFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	f, err := s.repo.GetFolder(ctx, req.Urn)
	if err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	if req.Folder != nil && req.Folder.Name != "" {
		f.Name = req.Folder.Name
	}
	if req.Folder != nil {
		f.Description = req.Folder.Description
		f.Deleted = req.Folder.Deleted
	}
	f.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateFolder(ctx, f); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.UpdateFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *FolderServer) DeleteFolder(ctx context.Context, req *pb.DeleteFolderRequest) (*pb.DeleteFolderResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.DeleteFolder(ctx, req.Urn); err != nil {
		return nil, projRepoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteFolderResponse{Urn: req.Urn, Deleted: true}, nil
}

// ── Conversion helper ─────────────────────────────────────────────────────────

func coreFolderToProto(f *core.Folder) *pb.Folder {
	return &pb.Folder{
		Urn:         f.URN.String(),
		ProjectUrn:  f.ProjectURN.String(),
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
		CreatedAt:   timestamppb.New(f.CreatedAt),
		UpdatedAt:   timestamppb.New(f.UpdatedAt),
	}
}
