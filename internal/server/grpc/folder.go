package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
)

// FolderServer implements pb.FolderServiceServer by delegating all business
// logic to service.FolderService.
type FolderServer struct {
	pb.UnimplementedFolderServiceServer
	svc service.FolderService
}

// NewFolderServer returns a ready-to-register FolderServer.
func NewFolderServer(svc service.FolderService) *FolderServer {
	return &FolderServer{svc: svc}
}

// ── Folders ───────────────────────────────────────────────────────────────────

func (s *FolderServer) CreateFolder(ctx context.Context, req *pb.CreateFolderRequest) (*pb.CreateFolderResponse, error) {
	urn, err := core.ParseURN(req.Urn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid urn: %v", err)
	}
	projURN, err := core.ParseURN(req.ProjectUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid project_urn: %v", err)
	}

	folder := &core.Folder{
		URN:         urn,
		ProjectURN:  projURN,
		Name:        req.Name,
		Description: req.Description,
	}

	if err := s.svc.Create(ctx, folder); err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.CreateFolderResponse{Folder: coreFolderToProto(folder)}, nil
}

func (s *FolderServer) GetFolder(ctx context.Context, req *pb.GetFolderRequest) (*pb.GetFolderResponse, error) {
	f, err := s.svc.Get(ctx, req.Urn)
	if err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.GetFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *FolderServer) ListFolders(ctx context.Context, req *pb.ListFoldersRequest) (*pb.ListFoldersResponse, error) {
	result, err := s.svc.List(ctx, repo.FolderListOptions{
		ProjectURN:     req.ProjectUrn,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       int(req.PageSize),
		PageToken:      req.PageToken,
	})
	if err != nil {
		return nil, svcErrToStatus(err, "")
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
	var upd service.FolderUpdate
	if req.Folder != nil {
		if req.Folder.Name != "" {
			upd.Name = req.Folder.Name
		}
		desc := req.Folder.Description
		upd.Description = &desc
		deleted := req.Folder.Deleted
		upd.Deleted = &deleted
	}

	f, err := s.svc.Update(ctx, req.Urn, upd)
	if err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.UpdateFolderResponse{Folder: coreFolderToProto(f)}, nil
}

func (s *FolderServer) DeleteFolder(ctx context.Context, req *pb.DeleteFolderRequest) (*pb.DeleteFolderResponse, error) {
	if err := s.svc.Delete(ctx, req.Urn); err != nil {
		return nil, svcErrToStatus(err, req.Urn)
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
