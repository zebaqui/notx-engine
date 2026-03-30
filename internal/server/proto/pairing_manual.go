package notxpb

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// ─────────────────────────────────────────────────────────────────────────────
// ServerPairingService messages
//
// These types implement the protoiface.MessageV1 interface (Reset/String/
// ProtoMessage), which is sufficient for gRPC's codec to handle them via the
// protoadapt.MessageV2Of → legacyWrapMessage → aberrant-message path.
// The protobuf struct tags drive binary encoding/decoding.
// ─────────────────────────────────────────────────────────────────────────────

type RegisterServerRequest struct {
	ServerUrn     string `protobuf:"bytes,1,opt,name=server_urn,json=serverUrn,proto3" json:"server_urn,omitempty"`
	Csr           []byte `protobuf:"bytes,2,opt,name=csr,proto3" json:"csr,omitempty"`
	PairingSecret string `protobuf:"bytes,3,opt,name=pairing_secret,json=pairingSecret,proto3" json:"pairing_secret,omitempty"`
	ServerName    string `protobuf:"bytes,4,opt,name=server_name,json=serverName,proto3" json:"server_name,omitempty"`
	Endpoint      string `protobuf:"bytes,5,opt,name=endpoint,proto3" json:"endpoint,omitempty"`
}

func (x *RegisterServerRequest) Reset()         { *x = RegisterServerRequest{} }
func (x *RegisterServerRequest) String() string { return x.ServerUrn }
func (x *RegisterServerRequest) ProtoMessage()  {}

type RegisterServerResponse struct {
	ServerUrn     string                 `protobuf:"bytes,1,opt,name=server_urn,json=serverUrn,proto3" json:"server_urn,omitempty"`
	Certificate   []byte                 `protobuf:"bytes,2,opt,name=certificate,proto3" json:"certificate,omitempty"`
	CaCertificate []byte                 `protobuf:"bytes,3,opt,name=ca_certificate,json=caCertificate,proto3" json:"ca_certificate,omitempty"`
	ExpiresAt     *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
	RegisteredAt  *timestamppb.Timestamp `protobuf:"bytes,5,opt,name=registered_at,json=registeredAt,proto3" json:"registered_at,omitempty"`
}

func (x *RegisterServerResponse) Reset()         { *x = RegisterServerResponse{} }
func (x *RegisterServerResponse) String() string { return x.ServerUrn }
func (x *RegisterServerResponse) ProtoMessage()  {}

type RenewCertificateRequest struct {
	ServerUrn string `protobuf:"bytes,1,opt,name=server_urn,json=serverUrn,proto3" json:"server_urn,omitempty"`
	Csr       []byte `protobuf:"bytes,2,opt,name=csr,proto3" json:"csr,omitempty"`
}

func (x *RenewCertificateRequest) Reset()         { *x = RenewCertificateRequest{} }
func (x *RenewCertificateRequest) String() string { return x.ServerUrn }
func (x *RenewCertificateRequest) ProtoMessage()  {}

type RenewCertificateResponse struct {
	Certificate []byte                 `protobuf:"bytes,1,opt,name=certificate,proto3" json:"certificate,omitempty"`
	ExpiresAt   *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
}

func (x *RenewCertificateResponse) Reset()         { *x = RenewCertificateResponse{} }
func (x *RenewCertificateResponse) String() string { return "" }
func (x *RenewCertificateResponse) ProtoMessage()  {}

type GetCACertificateRequest struct{}

func (x *GetCACertificateRequest) Reset()         { *x = GetCACertificateRequest{} }
func (x *GetCACertificateRequest) String() string { return "" }
func (x *GetCACertificateRequest) ProtoMessage()  {}

type GetCACertificateResponse struct {
	CaCertificate []byte `protobuf:"bytes,1,opt,name=ca_certificate,json=caCertificate,proto3" json:"ca_certificate,omitempty"`
}

func (x *GetCACertificateResponse) Reset()         { *x = GetCACertificateResponse{} }
func (x *GetCACertificateResponse) String() string { return "" }
func (x *GetCACertificateResponse) ProtoMessage()  {}

type ListServersRequest struct {
	IncludeRevoked bool `protobuf:"varint,1,opt,name=include_revoked,json=includeRevoked,proto3" json:"include_revoked,omitempty"`
}

func (x *ListServersRequest) Reset()         { *x = ListServersRequest{} }
func (x *ListServersRequest) String() string { return "" }
func (x *ListServersRequest) ProtoMessage()  {}

type ListServersResponse struct {
	Servers []*ServerInfo `protobuf:"bytes,1,rep,name=servers,proto3" json:"servers,omitempty"`
}

func (x *ListServersResponse) Reset()         { *x = ListServersResponse{} }
func (x *ListServersResponse) String() string { return "" }
func (x *ListServersResponse) ProtoMessage()  {}

type ServerInfo struct {
	ServerUrn    string                 `protobuf:"bytes,1,opt,name=server_urn,json=serverUrn,proto3" json:"server_urn,omitempty"`
	ServerName   string                 `protobuf:"bytes,2,opt,name=server_name,json=serverName,proto3" json:"server_name,omitempty"`
	Endpoint     string                 `protobuf:"bytes,3,opt,name=endpoint,proto3" json:"endpoint,omitempty"`
	Revoked      bool                   `protobuf:"varint,4,opt,name=revoked,proto3" json:"revoked,omitempty"`
	RegisteredAt *timestamppb.Timestamp `protobuf:"bytes,5,opt,name=registered_at,json=registeredAt,proto3" json:"registered_at,omitempty"`
	ExpiresAt    *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`
	LastSeenAt   *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=last_seen_at,json=lastSeenAt,proto3" json:"last_seen_at,omitempty"`
}

func (x *ServerInfo) Reset()         { *x = ServerInfo{} }
func (x *ServerInfo) String() string { return x.ServerUrn }
func (x *ServerInfo) ProtoMessage()  {}

type RevokeServerRequest struct {
	ServerUrn string `protobuf:"bytes,1,opt,name=server_urn,json=serverUrn,proto3" json:"server_urn,omitempty"`
}

func (x *RevokeServerRequest) Reset()         { *x = RevokeServerRequest{} }
func (x *RevokeServerRequest) String() string { return x.ServerUrn }
func (x *RevokeServerRequest) ProtoMessage()  {}

type RevokeServerResponse struct {
	Revoked bool `protobuf:"varint,1,opt,name=revoked,proto3" json:"revoked,omitempty"`
}

func (x *RevokeServerResponse) Reset()         { *x = RevokeServerResponse{} }
func (x *RevokeServerResponse) String() string { return "" }
func (x *RevokeServerResponse) ProtoMessage()  {}

// ─────────────────────────────────────────────────────────────────────────────
// ServerPairingService gRPC interface — server side
// ─────────────────────────────────────────────────────────────────────────────

const (
	ServerPairingService_RegisterServer_FullMethodName   = "/notx.v1.ServerPairingService/RegisterServer"
	ServerPairingService_RenewCertificate_FullMethodName = "/notx.v1.ServerPairingService/RenewCertificate"
	ServerPairingService_GetCACertificate_FullMethodName = "/notx.v1.ServerPairingService/GetCACertificate"
	ServerPairingService_ListServers_FullMethodName      = "/notx.v1.ServerPairingService/ListServers"
	ServerPairingService_RevokeServer_FullMethodName     = "/notx.v1.ServerPairingService/RevokeServer"
)

// ServerPairingServiceServer is the server-side interface.
type ServerPairingServiceServer interface {
	RegisterServer(context.Context, *RegisterServerRequest) (*RegisterServerResponse, error)
	RenewCertificate(context.Context, *RenewCertificateRequest) (*RenewCertificateResponse, error)
	GetCACertificate(context.Context, *GetCACertificateRequest) (*GetCACertificateResponse, error)
	ListServers(context.Context, *ListServersRequest) (*ListServersResponse, error)
	RevokeServer(context.Context, *RevokeServerRequest) (*RevokeServerResponse, error)
	mustEmbedUnimplementedServerPairingServiceServer()
}

// UnimplementedServerPairingServiceServer provides default unimplemented
// implementations so that concrete servers only override what they need.
type UnimplementedServerPairingServiceServer struct{}

func (UnimplementedServerPairingServiceServer) RegisterServer(context.Context, *RegisterServerRequest) (*RegisterServerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RegisterServer not implemented")
}
func (UnimplementedServerPairingServiceServer) RenewCertificate(context.Context, *RenewCertificateRequest) (*RenewCertificateResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RenewCertificate not implemented")
}
func (UnimplementedServerPairingServiceServer) GetCACertificate(context.Context, *GetCACertificateRequest) (*GetCACertificateResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetCACertificate not implemented")
}
func (UnimplementedServerPairingServiceServer) ListServers(context.Context, *ListServersRequest) (*ListServersResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListServers not implemented")
}
func (UnimplementedServerPairingServiceServer) RevokeServer(context.Context, *RevokeServerRequest) (*RevokeServerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RevokeServer not implemented")
}
func (UnimplementedServerPairingServiceServer) mustEmbedUnimplementedServerPairingServiceServer() {}

// UnsafeServerPairingServiceServer may be embedded to opt out of forward
// compatibility guarantees for this service.
type UnsafeServerPairingServiceServer interface {
	mustEmbedUnimplementedServerPairingServiceServer()
}

// RegisterServerPairingServiceServer registers the service with the gRPC server.
func RegisterServerPairingServiceServer(s grpc.ServiceRegistrar, srv ServerPairingServiceServer) {
	s.RegisterService(&ServerPairingService_ServiceDesc, srv)
}

// ServerPairingService_ServiceDesc is the grpc.ServiceDesc for ServerPairingService.
var ServerPairingService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "notx.v1.ServerPairingService",
	HandlerType: (*ServerPairingServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "RegisterServer",
			Handler:    _ServerPairingService_RegisterServer_Handler,
		},
		{
			MethodName: "RenewCertificate",
			Handler:    _ServerPairingService_RenewCertificate_Handler,
		},
		{
			MethodName: "GetCACertificate",
			Handler:    _ServerPairingService_GetCACertificate_Handler,
		},
		{
			MethodName: "ListServers",
			Handler:    _ServerPairingService_ListServers_Handler,
		},
		{
			MethodName: "RevokeServer",
			Handler:    _ServerPairingService_RevokeServer_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "notx.proto",
}

func _ServerPairingService_RegisterServer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RegisterServerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServerPairingServiceServer).RegisterServer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: ServerPairingService_RegisterServer_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServerPairingServiceServer).RegisterServer(ctx, req.(*RegisterServerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ServerPairingService_RenewCertificate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RenewCertificateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServerPairingServiceServer).RenewCertificate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: ServerPairingService_RenewCertificate_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServerPairingServiceServer).RenewCertificate(ctx, req.(*RenewCertificateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ServerPairingService_GetCACertificate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetCACertificateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServerPairingServiceServer).GetCACertificate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: ServerPairingService_GetCACertificate_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServerPairingServiceServer).GetCACertificate(ctx, req.(*GetCACertificateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ServerPairingService_ListServers_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListServersRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServerPairingServiceServer).ListServers(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: ServerPairingService_ListServers_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServerPairingServiceServer).ListServers(ctx, req.(*ListServersRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ServerPairingService_RevokeServer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RevokeServerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServerPairingServiceServer).RevokeServer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: ServerPairingService_RevokeServer_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServerPairingServiceServer).RevokeServer(ctx, req.(*RevokeServerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// ─────────────────────────────────────────────────────────────────────────────
// ServerPairingService gRPC interface — client side
// ─────────────────────────────────────────────────────────────────────────────

// ServerPairingServiceClient is the client-side interface.
type ServerPairingServiceClient interface {
	RegisterServer(ctx context.Context, in *RegisterServerRequest, opts ...grpc.CallOption) (*RegisterServerResponse, error)
	RenewCertificate(ctx context.Context, in *RenewCertificateRequest, opts ...grpc.CallOption) (*RenewCertificateResponse, error)
	GetCACertificate(ctx context.Context, in *GetCACertificateRequest, opts ...grpc.CallOption) (*GetCACertificateResponse, error)
	ListServers(ctx context.Context, in *ListServersRequest, opts ...grpc.CallOption) (*ListServersResponse, error)
	RevokeServer(ctx context.Context, in *RevokeServerRequest, opts ...grpc.CallOption) (*RevokeServerResponse, error)
}

type serverPairingServiceClient struct {
	cc grpc.ClientConnInterface
}

// NewServerPairingServiceClient creates a new ServerPairingServiceClient.
func NewServerPairingServiceClient(cc grpc.ClientConnInterface) ServerPairingServiceClient {
	return &serverPairingServiceClient{cc}
}

func (c *serverPairingServiceClient) RegisterServer(ctx context.Context, in *RegisterServerRequest, opts ...grpc.CallOption) (*RegisterServerResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RegisterServerResponse)
	if err := c.cc.Invoke(ctx, ServerPairingService_RegisterServer_FullMethodName, in, out, cOpts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serverPairingServiceClient) RenewCertificate(ctx context.Context, in *RenewCertificateRequest, opts ...grpc.CallOption) (*RenewCertificateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RenewCertificateResponse)
	if err := c.cc.Invoke(ctx, ServerPairingService_RenewCertificate_FullMethodName, in, out, cOpts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serverPairingServiceClient) GetCACertificate(ctx context.Context, in *GetCACertificateRequest, opts ...grpc.CallOption) (*GetCACertificateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetCACertificateResponse)
	if err := c.cc.Invoke(ctx, ServerPairingService_GetCACertificate_FullMethodName, in, out, cOpts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serverPairingServiceClient) ListServers(ctx context.Context, in *ListServersRequest, opts ...grpc.CallOption) (*ListServersResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListServersResponse)
	if err := c.cc.Invoke(ctx, ServerPairingService_ListServers_FullMethodName, in, out, cOpts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serverPairingServiceClient) RevokeServer(ctx context.Context, in *RevokeServerRequest, opts ...grpc.CallOption) (*RevokeServerResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RevokeServerResponse)
	if err := c.cc.Invoke(ctx, ServerPairingService_RevokeServer_FullMethodName, in, out, cOpts...); err != nil {
		return nil, err
	}
	return out, nil
}
