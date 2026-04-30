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

// ProjectServer implements pb.ProjectServiceServer as a thin proto adapter,
// delegating all business logic to service.ProjectService.
type ProjectServer struct {
	pb.UnimplementedProjectServiceServer
	svc service.ProjectService
}

// NewProjectServer returns a ready-to-register ProjectServer.
func NewProjectServer(svc service.ProjectService) *ProjectServer {
	return &ProjectServer{svc: svc}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *ProjectServer) CreateProject(ctx context.Context, req *pb.CreateProjectRequest) (*pb.CreateProjectResponse, error) {
	urn, err := core.ParseURN(req.Urn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid urn: %v", err)
	}

	proj := &core.Project{
		URN:         urn,
		Name:        req.Name,
		Description: req.Description,
	}

	if err := s.svc.Create(ctx, proj); err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.CreateProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServer) GetProject(ctx context.Context, req *pb.GetProjectRequest) (*pb.GetProjectResponse, error) {
	proj, err := s.svc.Get(ctx, req.Urn)
	if err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.GetProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServer) ListProjects(ctx context.Context, req *pb.ListProjectsRequest) (*pb.ListProjectsResponse, error) {
	result, err := s.svc.List(ctx, repo.ProjectListOptions{
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       int(req.PageSize), // service clamps to its own defaults/max
		PageToken:      req.PageToken,
	})
	if err != nil {
		return nil, svcErrToStatus(err, "")
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
	var upd service.ProjectUpdate

	if req.Project != nil {
		// Name is only applied when non-empty (proto convention: empty = "no change").
		if req.Project.Name != "" {
			upd.Name = req.Project.Name
		}
		// Description and Deleted are ALWAYS applied when req.Project is present
		// so callers can explicitly clear the description or un-delete a project.
		desc := req.Project.Description
		upd.Description = &desc
		deleted := req.Project.Deleted
		upd.Deleted = &deleted
	}

	proj, err := s.svc.Update(ctx, req.Urn, upd)
	if err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.UpdateProjectResponse{Project: coreProjectToProto(proj)}, nil
}

func (s *ProjectServer) DeleteProject(ctx context.Context, req *pb.DeleteProjectRequest) (*pb.DeleteProjectResponse, error) {
	if err := s.svc.Delete(ctx, req.Urn); err != nil {
		return nil, svcErrToStatus(err, req.Urn)
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
