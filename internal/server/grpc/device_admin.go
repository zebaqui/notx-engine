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

// DeviceAdminServer implements pb.DeviceAdminServiceServer backed by
// the shared repo.DeviceRepository so the gRPC and HTTP admin layers read from
// and write to the same device store.
type DeviceAdminServer struct {
	pb.UnimplementedDeviceAdminServiceServer

	repo repo.DeviceRepository
}

// NewDeviceAdminServer returns a ready-to-register DeviceAdminServer
// backed by the supplied DeviceRepository.
func NewDeviceAdminServer(r repo.DeviceRepository) *DeviceAdminServer {
	return &DeviceAdminServer{repo: r}
}

// ── AdminRegisterDevice ──────────────────────────────────────────────────────
// AdminRegisterDevice is the HTTP-layer variant of RegisterDevice. The HTTP
// handler resolves role and approval_status (including admin-passphrase logic)
// before calling this method, so the gRPC service itself never needs to know
// about HTTP-layer configuration such as AdminPassphraseHash or AutoApprove.

func (s *DeviceAdminServer) AdminRegisterDevice(ctx context.Context, req *pb.AdminRegisterDeviceRequest) (*pb.AdminRegisterDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}
	if req.OwnerUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_urn is required")
	}

	deviceURN, err := core.ParseURN(req.DeviceUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid device_urn: %v", err)
	}
	if deviceURN.ObjectType != core.ObjectTypeDevice {
		return nil, status.Errorf(codes.InvalidArgument,
			"device_urn must be of type %q, got %q", core.ObjectTypeDevice, deviceURN.ObjectType)
	}

	ownerURN, err := core.ParseURN(req.OwnerUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid owner_urn: %v", err)
	}

	var role core.DeviceRole
	switch req.Role {
	case pb.DeviceRole_DEVICE_ROLE_ADMIN:
		role = core.DeviceRoleAdmin
	case pb.DeviceRole_DEVICE_ROLE_RELAY:
		role = core.DeviceRole("relay")
	case pb.DeviceRole_DEVICE_ROLE_USER:
		role = core.DeviceRole("user")
	default:
		role = core.DeviceRoleClient
	}

	var approvalStatus core.DeviceApprovalStatus
	switch req.ApprovalStatus {
	case pb.ApprovalStatus_APPROVAL_STATUS_APPROVED:
		approvalStatus = core.DeviceApprovalApproved
	case pb.ApprovalStatus_APPROVAL_STATUS_REJECTED:
		approvalStatus = core.DeviceApprovalRejected
	default:
		approvalStatus = core.DeviceApprovalPending
	}

	now := time.Now().UTC()
	d := &core.Device{
		URN:            deviceURN,
		Name:           req.DeviceName,
		OwnerURN:       ownerURN,
		PublicKeyB64:   encodePublicKey(req.PublicKey),
		Role:           role,
		ApprovalStatus: approvalStatus,
		RegisteredAt:   now,
		LastSeenAt:     now,
	}

	if err := s.repo.RegisterDevice(ctx, d); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.AdminRegisterDeviceResponse{Device: coreDeviceToAdmin(d)}, nil
}

// ── AdminListDevices ──────────────────────────────────────────────────────────
// AdminListDevices is the HTTP-layer variant of ListDevices. It returns the full
// DeviceAdmin descriptor (with role, approval_status, owner_urn, revoked) and
// supports include_revoked so the admin HTTP API can surface revoked devices.

func (s *DeviceAdminServer) AdminListDevices(ctx context.Context, req *pb.AdminListDevicesRequest) (*pb.AdminListDevicesResponse, error) {
	result, err := s.repo.ListDevices(ctx, repo.DeviceListOptions{
		OwnerURN:       req.OwnerUrn,
		IncludeRevoked: req.IncludeRevoked,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list devices: %v", err)
	}

	infos := make([]*pb.DeviceAdmin, 0, len(result.Devices))
	for _, d := range result.Devices {
		infos = append(infos, coreDeviceToAdmin(d))
	}

	return &pb.AdminListDevicesResponse{Devices: infos}, nil
}

// ── GetDevice ────────────────────────────────────────────────────────────────

func (s *DeviceAdminServer) GetDevice(ctx context.Context, req *pb.GetDeviceRequest) (*pb.GetDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	d, err := s.repo.GetDevice(ctx, req.DeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.GetDeviceResponse{Device: coreDeviceToAdmin(d)}, nil
}

// ── UpdateDevice ─────────────────────────────────────────────────────────────

func (s *DeviceAdminServer) UpdateDevice(ctx context.Context, req *pb.UpdateDeviceRequest) (*pb.UpdateDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	d, err := s.repo.GetDevice(ctx, req.DeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	if req.Device != nil {
		if req.Device.DeviceName != "" {
			d.Name = req.Device.DeviceName
		}
		if req.Device.LastSeenAt != nil {
			d.LastSeenAt = req.Device.LastSeenAt.AsTime()
		}
	}

	if err := s.repo.UpdateDevice(ctx, d); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.UpdateDeviceResponse{Device: coreDeviceToAdmin(d)}, nil
}

// ── GetDeviceStatus ──────────────────────────────────────────────────────────

func (s *DeviceAdminServer) GetDeviceStatus(ctx context.Context, req *pb.GetDeviceStatusRequest) (*pb.GetDeviceStatusResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	d, err := s.repo.GetDevice(ctx, req.DeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	var approvalStatusPb pb.ApprovalStatus
	switch d.ApprovalStatus {
	case core.DeviceApprovalApproved:
		approvalStatusPb = pb.ApprovalStatus_APPROVAL_STATUS_APPROVED
	case core.DeviceApprovalRejected:
		approvalStatusPb = pb.ApprovalStatus_APPROVAL_STATUS_REJECTED
	case core.DeviceApprovalPending:
		approvalStatusPb = pb.ApprovalStatus_APPROVAL_STATUS_PENDING
	default:
		approvalStatusPb = pb.ApprovalStatus_APPROVAL_STATUS_UNSPECIFIED
	}

	return &pb.GetDeviceStatusResponse{
		DeviceUrn:      d.URN.String(),
		ApprovalStatus: approvalStatusPb,
		Revoked:        d.Revoked,
	}, nil
}

// ── ApproveDevice ────────────────────────────────────────────────────────────

func (s *DeviceAdminServer) ApproveDevice(ctx context.Context, req *pb.ApproveDeviceRequest) (*pb.ApproveDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	d, err := s.repo.GetDevice(ctx, req.DeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	if d.Revoked {
		return nil, status.Errorf(codes.FailedPrecondition, "device %q has been revoked", req.DeviceUrn)
	}
	if d.ApprovalStatus == core.DeviceApprovalRejected {
		return nil, status.Errorf(codes.FailedPrecondition, "device %q has been rejected and cannot be approved", req.DeviceUrn)
	}

	d.ApprovalStatus = core.DeviceApprovalApproved

	if err := s.repo.UpdateDevice(ctx, d); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.ApproveDeviceResponse{Device: coreDeviceToAdmin(d)}, nil
}

// ── RejectDevice ─────────────────────────────────────────────────────────────

func (s *DeviceAdminServer) RejectDevice(ctx context.Context, req *pb.RejectDeviceRequest) (*pb.RejectDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	d, err := s.repo.GetDevice(ctx, req.DeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	if d.Revoked {
		return nil, status.Errorf(codes.FailedPrecondition, "device %q has been revoked", req.DeviceUrn)
	}

	d.ApprovalStatus = core.DeviceApprovalRejected

	if err := s.repo.UpdateDevice(ctx, d); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.RejectDeviceResponse{Device: coreDeviceToAdmin(d)}, nil
}

// ── AdminRevokeDevice ────────────────────────────────────────────────────────

func (s *DeviceAdminServer) AdminRevokeDevice(ctx context.Context, req *pb.AdminRevokeDeviceRequest) (*pb.AdminRevokeDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	if err := s.repo.RevokeDevice(ctx, req.DeviceUrn); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.AdminRevokeDeviceResponse{DeviceUrn: req.DeviceUrn, Revoked: true}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Conversion helper
// ─────────────────────────────────────────────────────────────────────────────

// coreDeviceToAdmin converts a core.Device to the extended protobuf
// descriptor returned by the new device management RPCs.
func coreDeviceToAdmin(d *core.Device) *pb.DeviceAdmin {
	var role pb.DeviceRole
	switch d.Role {
	case core.DeviceRoleAdmin:
		role = pb.DeviceRole_DEVICE_ROLE_ADMIN
	case core.DeviceRole("relay"):
		role = pb.DeviceRole_DEVICE_ROLE_RELAY
	case core.DeviceRole("user"):
		role = pb.DeviceRole_DEVICE_ROLE_USER
	default:
		role = pb.DeviceRole_DEVICE_ROLE_UNSPECIFIED
	}

	var approvalStatus pb.ApprovalStatus
	switch d.ApprovalStatus {
	case core.DeviceApprovalApproved:
		approvalStatus = pb.ApprovalStatus_APPROVAL_STATUS_APPROVED
	case core.DeviceApprovalRejected:
		approvalStatus = pb.ApprovalStatus_APPROVAL_STATUS_REJECTED
	case core.DeviceApprovalPending:
		approvalStatus = pb.ApprovalStatus_APPROVAL_STATUS_PENDING
	default:
		approvalStatus = pb.ApprovalStatus_APPROVAL_STATUS_UNSPECIFIED
	}

	info := &pb.DeviceAdmin{
		DeviceUrn:      d.URN.String(),
		DeviceName:     d.Name,
		OwnerUrn:       d.OwnerURN.String(),
		Role:           role,
		ApprovalStatus: approvalStatus,
		Revoked:        d.Revoked,
		RegisteredAt:   timestamppb.New(d.RegisteredAt),
	}
	if !d.LastSeenAt.IsZero() {
		info.LastSeenAt = timestamppb.New(d.LastSeenAt)
	}
	if d.PublicKeyB64 != "" {
		if key, err := decodePublicKey(d.PublicKeyB64); err == nil {
			info.PublicKey = key
		}
	}
	return info
}
