package notxpb

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// ── Project messages ──────────────────────────────────────────────────────────

type ProjectProto struct {
	Urn         string                 `protobuf:"bytes,1,opt,name=urn,proto3" json:"urn,omitempty"`
	Name        string                 `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Description string                 `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
	Deleted     bool                   `protobuf:"varint,4,opt,name=deleted,proto3" json:"deleted,omitempty"`
	CreatedAt   *timestamppb.Timestamp `protobuf:"bytes,5,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	UpdatedAt   *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`
}

func (x *ProjectProto) Reset()         {}
func (x *ProjectProto) String() string { return x.Urn }
func (x *ProjectProto) ProtoMessage()  {}

type FolderProto struct {
	Urn         string                 `protobuf:"bytes,1,opt,name=urn,proto3" json:"urn,omitempty"`
	ProjectUrn  string                 `protobuf:"bytes,2,opt,name=project_urn,json=projectUrn,proto3" json:"project_urn,omitempty"`
	Name        string                 `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	Description string                 `protobuf:"bytes,4,opt,name=description,proto3" json:"description,omitempty"`
	Deleted     bool                   `protobuf:"varint,5,opt,name=deleted,proto3" json:"deleted,omitempty"`
	CreatedAt   *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	UpdatedAt   *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`
}

func (x *FolderProto) Reset()         {}
func (x *FolderProto) String() string { return x.Urn }
func (x *FolderProto) ProtoMessage()  {}

// ── Project request/response messages ────────────────────────────────────────

type CreateProjectRequest struct {
	Urn         string `protobuf:"bytes,1,opt,name=urn,proto3" json:"urn,omitempty"`
	Name        string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Description string `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
}

func (x *CreateProjectRequest) Reset()         {}
func (x *CreateProjectRequest) String() string { return x.Urn }
func (x *CreateProjectRequest) ProtoMessage()  {}

type CreateProjectResponse struct{ Project *ProjectProto }

func (x *CreateProjectResponse) Reset()         {}
func (x *CreateProjectResponse) String() string { return "" }
func (x *CreateProjectResponse) ProtoMessage()  {}

type GetProjectRequest struct{ Urn string }

func (x *GetProjectRequest) Reset()         {}
func (x *GetProjectRequest) String() string { return x.Urn }
func (x *GetProjectRequest) ProtoMessage()  {}

type GetProjectResponse struct{ Project *ProjectProto }

func (x *GetProjectResponse) Reset()         {}
func (x *GetProjectResponse) String() string { return "" }
func (x *GetProjectResponse) ProtoMessage()  {}

type ListProjectsRequest struct {
	IncludeDeleted bool   `protobuf:"varint,1,opt,name=include_deleted,json=includeDeleted,proto3" json:"include_deleted,omitempty"`
	PageSize       int32  `protobuf:"varint,2,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	PageToken      string `protobuf:"bytes,3,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
}

func (x *ListProjectsRequest) Reset()         {}
func (x *ListProjectsRequest) String() string { return "" }
func (x *ListProjectsRequest) ProtoMessage()  {}

type ListProjectsResponse struct {
	Projects      []*ProjectProto `protobuf:"bytes,1,rep,name=projects,proto3" json:"projects,omitempty"`
	NextPageToken string          `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
}

func (x *ListProjectsResponse) Reset()         {}
func (x *ListProjectsResponse) String() string { return "" }
func (x *ListProjectsResponse) ProtoMessage()  {}

type UpdateProjectRequest struct {
	Urn         string `protobuf:"bytes,1,opt,name=urn,proto3" json:"urn,omitempty"`
	Name        string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Description string `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
	Deleted     bool   `protobuf:"varint,4,opt,name=deleted,proto3" json:"deleted,omitempty"`
}

func (x *UpdateProjectRequest) Reset()         {}
func (x *UpdateProjectRequest) String() string { return x.Urn }
func (x *UpdateProjectRequest) ProtoMessage()  {}

type UpdateProjectResponse struct{ Project *ProjectProto }

func (x *UpdateProjectResponse) Reset()         {}
func (x *UpdateProjectResponse) String() string { return "" }
func (x *UpdateProjectResponse) ProtoMessage()  {}

type DeleteProjectRequest struct{ Urn string }

func (x *DeleteProjectRequest) Reset()         {}
func (x *DeleteProjectRequest) String() string { return x.Urn }
func (x *DeleteProjectRequest) ProtoMessage()  {}

type DeleteProjectResponse struct{ Deleted bool }

func (x *DeleteProjectResponse) Reset()         {}
func (x *DeleteProjectResponse) String() string { return "" }
func (x *DeleteProjectResponse) ProtoMessage()  {}

// ── Folder request/response messages ─────────────────────────────────────────

type CreateFolderRequest struct {
	Urn         string `protobuf:"bytes,1,opt,name=urn,proto3" json:"urn,omitempty"`
	ProjectUrn  string `protobuf:"bytes,2,opt,name=project_urn,json=projectUrn,proto3" json:"project_urn,omitempty"`
	Name        string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	Description string `protobuf:"bytes,4,opt,name=description,proto3" json:"description,omitempty"`
}

func (x *CreateFolderRequest) Reset()         {}
func (x *CreateFolderRequest) String() string { return x.Urn }
func (x *CreateFolderRequest) ProtoMessage()  {}

type CreateFolderResponse struct{ Folder *FolderProto }

func (x *CreateFolderResponse) Reset()         {}
func (x *CreateFolderResponse) String() string { return "" }
func (x *CreateFolderResponse) ProtoMessage()  {}

type GetFolderRequest struct{ Urn string }

func (x *GetFolderRequest) Reset()         {}
func (x *GetFolderRequest) String() string { return x.Urn }
func (x *GetFolderRequest) ProtoMessage()  {}

type GetFolderResponse struct{ Folder *FolderProto }

func (x *GetFolderResponse) Reset()         {}
func (x *GetFolderResponse) String() string { return "" }
func (x *GetFolderResponse) ProtoMessage()  {}

type ListFoldersRequest struct {
	ProjectUrn     string `protobuf:"bytes,1,opt,name=project_urn,json=projectUrn,proto3" json:"project_urn,omitempty"`
	IncludeDeleted bool   `protobuf:"varint,2,opt,name=include_deleted,json=includeDeleted,proto3" json:"include_deleted,omitempty"`
	PageSize       int32  `protobuf:"varint,3,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	PageToken      string `protobuf:"bytes,4,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
}

func (x *ListFoldersRequest) Reset()         {}
func (x *ListFoldersRequest) String() string { return "" }
func (x *ListFoldersRequest) ProtoMessage()  {}

type ListFoldersResponse struct {
	Folders       []*FolderProto `protobuf:"bytes,1,rep,name=folders,proto3" json:"folders,omitempty"`
	NextPageToken string         `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
}

func (x *ListFoldersResponse) Reset()         {}
func (x *ListFoldersResponse) String() string { return "" }
func (x *ListFoldersResponse) ProtoMessage()  {}

type UpdateFolderRequest struct {
	Urn         string `protobuf:"bytes,1,opt,name=urn,proto3" json:"urn,omitempty"`
	Name        string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Description string `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
	Deleted     bool   `protobuf:"varint,4,opt,name=deleted,proto3" json:"deleted,omitempty"`
}

func (x *UpdateFolderRequest) Reset()         {}
func (x *UpdateFolderRequest) String() string { return x.Urn }
func (x *UpdateFolderRequest) ProtoMessage()  {}

type UpdateFolderResponse struct{ Folder *FolderProto }

func (x *UpdateFolderResponse) Reset()         {}
func (x *UpdateFolderResponse) String() string { return "" }
func (x *UpdateFolderResponse) ProtoMessage()  {}

type DeleteFolderRequest struct{ Urn string }

func (x *DeleteFolderRequest) Reset()         {}
func (x *DeleteFolderRequest) String() string { return x.Urn }
func (x *DeleteFolderRequest) ProtoMessage()  {}

type DeleteFolderResponse struct{ Deleted bool }

func (x *DeleteFolderResponse) Reset()         {}
func (x *DeleteFolderResponse) String() string { return "" }
func (x *DeleteFolderResponse) ProtoMessage()  {}

// ── Service interface and registration ───────────────────────────────────────

// ProjectServiceServer is the server-side interface for the ProjectService.
type ProjectServiceServer interface {
	CreateProject(context.Context, *CreateProjectRequest) (*CreateProjectResponse, error)
	GetProject(context.Context, *GetProjectRequest) (*GetProjectResponse, error)
	ListProjects(context.Context, *ListProjectsRequest) (*ListProjectsResponse, error)
	UpdateProject(context.Context, *UpdateProjectRequest) (*UpdateProjectResponse, error)
	DeleteProject(context.Context, *DeleteProjectRequest) (*DeleteProjectResponse, error)

	CreateFolder(context.Context, *CreateFolderRequest) (*CreateFolderResponse, error)
	GetFolder(context.Context, *GetFolderRequest) (*GetFolderResponse, error)
	ListFolders(context.Context, *ListFoldersRequest) (*ListFoldersResponse, error)
	UpdateFolder(context.Context, *UpdateFolderRequest) (*UpdateFolderResponse, error)
	DeleteFolder(context.Context, *DeleteFolderRequest) (*DeleteFolderResponse, error)

	mustEmbedUnimplementedProjectServiceServer()
}

// UnimplementedProjectServiceServer must be embedded to forward compatibility.
type UnimplementedProjectServiceServer struct{}

func (UnimplementedProjectServiceServer) CreateProject(context.Context, *CreateProjectRequest) (*CreateProjectResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "CreateProject not implemented")
}
func (UnimplementedProjectServiceServer) GetProject(context.Context, *GetProjectRequest) (*GetProjectResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "GetProject not implemented")
}
func (UnimplementedProjectServiceServer) ListProjects(context.Context, *ListProjectsRequest) (*ListProjectsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ListProjects not implemented")
}
func (UnimplementedProjectServiceServer) UpdateProject(context.Context, *UpdateProjectRequest) (*UpdateProjectResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "UpdateProject not implemented")
}
func (UnimplementedProjectServiceServer) DeleteProject(context.Context, *DeleteProjectRequest) (*DeleteProjectResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "DeleteProject not implemented")
}
func (UnimplementedProjectServiceServer) CreateFolder(context.Context, *CreateFolderRequest) (*CreateFolderResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "CreateFolder not implemented")
}
func (UnimplementedProjectServiceServer) GetFolder(context.Context, *GetFolderRequest) (*GetFolderResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "GetFolder not implemented")
}
func (UnimplementedProjectServiceServer) ListFolders(context.Context, *ListFoldersRequest) (*ListFoldersResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ListFolders not implemented")
}
func (UnimplementedProjectServiceServer) UpdateFolder(context.Context, *UpdateFolderRequest) (*UpdateFolderResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "UpdateFolder not implemented")
}
func (UnimplementedProjectServiceServer) DeleteFolder(context.Context, *DeleteFolderRequest) (*DeleteFolderResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "DeleteFolder not implemented")
}
func (UnimplementedProjectServiceServer) mustEmbedUnimplementedProjectServiceServer() {}

// RegisterProjectServiceServer registers the ProjectServiceServer with the gRPC server.
// Since we're not using generated gRPC stubs for this service, we implement
// a simple passthrough using grpc.ServiceDesc.
func RegisterProjectServiceServer(s grpcServer, srv ProjectServiceServer) {
	s.RegisterService(&_ProjectService_serviceDesc, srv)
}

type grpcServer interface {
	RegisterService(*grpc.ServiceDesc, interface{})
}

var _ProjectService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "notx.v1.ProjectService",
	HandlerType: (*ProjectServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateProject",
			Handler:    _ProjectService_CreateProject_Handler,
		},
		{
			MethodName: "GetProject",
			Handler:    _ProjectService_GetProject_Handler,
		},
		{
			MethodName: "ListProjects",
			Handler:    _ProjectService_ListProjects_Handler,
		},
		{
			MethodName: "UpdateProject",
			Handler:    _ProjectService_UpdateProject_Handler,
		},
		{
			MethodName: "DeleteProject",
			Handler:    _ProjectService_DeleteProject_Handler,
		},
		{
			MethodName: "CreateFolder",
			Handler:    _ProjectService_CreateFolder_Handler,
		},
		{
			MethodName: "GetFolder",
			Handler:    _ProjectService_GetFolder_Handler,
		},
		{
			MethodName: "ListFolders",
			Handler:    _ProjectService_ListFolders_Handler,
		},
		{
			MethodName: "UpdateFolder",
			Handler:    _ProjectService_UpdateFolder_Handler,
		},
		{
			MethodName: "DeleteFolder",
			Handler:    _ProjectService_DeleteFolder_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "notx.proto",
}

func _ProjectService_CreateProject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateProjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).CreateProject(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/CreateProject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).CreateProject(ctx, req.(*CreateProjectRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_GetProject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetProjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).GetProject(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/GetProject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).GetProject(ctx, req.(*GetProjectRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_ListProjects_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListProjectsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).ListProjects(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/ListProjects"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).ListProjects(ctx, req.(*ListProjectsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_UpdateProject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateProjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).UpdateProject(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/UpdateProject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).UpdateProject(ctx, req.(*UpdateProjectRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_DeleteProject_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteProjectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).DeleteProject(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/DeleteProject"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).DeleteProject(ctx, req.(*DeleteProjectRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_CreateFolder_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateFolderRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).CreateFolder(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/CreateFolder"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).CreateFolder(ctx, req.(*CreateFolderRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_GetFolder_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetFolderRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).GetFolder(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/GetFolder"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).GetFolder(ctx, req.(*GetFolderRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_ListFolders_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListFoldersRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).ListFolders(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/ListFolders"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).ListFolders(ctx, req.(*ListFoldersRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_UpdateFolder_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateFolderRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).UpdateFolder(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/UpdateFolder"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).UpdateFolder(ctx, req.(*UpdateFolderRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProjectService_DeleteFolder_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteFolderRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProjectServiceServer).DeleteFolder(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/notx.v1.ProjectService/DeleteFolder"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProjectServiceServer).DeleteFolder(ctx, req.(*DeleteFolderRequest))
	}
	return interceptor(ctx, in, info, handler)
}
