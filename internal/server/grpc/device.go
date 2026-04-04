package grpc

import (
	"context"
	b64 "encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// DeviceServer implements pb.DeviceServiceServer backed by the shared
// repo.DeviceRepository so the gRPC and HTTP layers read from and write to the
// same device store.
//
// Pairing sessions are the only state kept in-memory: they are short-lived
// (5-minute TTL), never need to survive a restart, and have no repo analogue.
type DeviceServer struct {
	pb.UnimplementedDeviceServiceServer

	repo repo.DeviceRepository

	// pairMu guards the pairs map only.
	pairMu sync.Mutex
	pairs  map[string]*pairSession // session_token → session
}

type pairSession struct {
	InitiatorURN string
	ExpiresAt    time.Time
}

// NewDeviceServer returns a ready-to-register DeviceServer backed by the
// supplied DeviceRepository.
func NewDeviceServer(r repo.DeviceRepository) *DeviceServer {
	return &DeviceServer{
		repo:  r,
		pairs: make(map[string]*pairSession),
	}
}

// ── RegisterDevice ───────────────────────────────────────────────────────────

func (s *DeviceServer) RegisterDevice(ctx context.Context, req *pb.RegisterDeviceRequest) (*pb.RegisterDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}
	if len(req.PublicKey) == 0 {
		return nil, status.Error(codes.InvalidArgument, "public_key is required")
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

	// Security invariant: Ed25519 public keys are exactly 32 bytes.
	// The private key must never appear in this request.
	if len(req.PublicKey) != 32 {
		return nil, status.Errorf(codes.InvalidArgument,
			"public_key must be 32 bytes (Ed25519), got %d", len(req.PublicKey))
	}

	now := time.Now().UTC()
	d := &core.Device{
		URN:            deviceURN,
		Name:           req.DeviceName,
		OwnerURN:       ownerURN,
		PublicKeyB64:   encodePublicKey(req.PublicKey),
		ApprovalStatus: core.DeviceApprovalApproved, // gRPC registration is always trusted
		RegisteredAt:   now,
		LastSeenAt:     now,
	}

	if err := s.repo.RegisterDevice(ctx, d); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.RegisterDeviceResponse{
		DeviceUrn:    req.DeviceUrn,
		RegisteredAt: timestamppb.New(now),
	}, nil
}

// ── GetDevicePublicKey ───────────────────────────────────────────────────────

func (s *DeviceServer) GetDevicePublicKey(ctx context.Context, req *pb.GetDevicePublicKeyRequest) (*pb.GetDevicePublicKeyResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	d, err := s.repo.GetDevice(ctx, req.DeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	if d.Revoked {
		return nil, status.Errorf(codes.PermissionDenied, "device %q has been revoked", req.DeviceUrn)
	}

	key, err := decodePublicKey(d.PublicKeyB64)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stored public key for device %q is invalid: %v", req.DeviceUrn, err)
	}

	return &pb.GetDevicePublicKeyResponse{
		DeviceUrn: d.URN.String(),
		PublicKey: key,
	}, nil
}

// ── ListDevices ──────────────────────────────────────────────────────────────

func (s *DeviceServer) ListDevices(ctx context.Context, req *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	if req.OwnerUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_urn is required")
	}

	result, err := s.repo.ListDevices(ctx, repo.DeviceListOptions{
		OwnerURN:       req.OwnerUrn,
		IncludeRevoked: false, // never surface revoked devices for key-exchange use
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list devices: %v", err)
	}

	infos := make([]*pb.Device, 0, len(result.Devices))
	for _, d := range result.Devices {
		key, err := decodePublicKey(d.PublicKeyB64)
		if err != nil {
			// Skip devices whose key is undecodable rather than hard-failing the
			// whole list — the device may have been registered without a key.
			continue
		}
		info := &pb.Device{
			DeviceUrn:    d.URN.String(),
			DeviceName:   d.Name,
			PublicKey:    key,
			RegisteredAt: timestamppb.New(d.RegisteredAt),
		}
		if !d.LastSeenAt.IsZero() {
			info.LastSeenAt = timestamppb.New(d.LastSeenAt)
		}
		infos = append(infos, info)
	}

	return &pb.ListDevicesResponse{Devices: infos}, nil
}

// ── RevokeDevice ─────────────────────────────────────────────────────────────

func (s *DeviceServer) RevokeDevice(ctx context.Context, req *pb.RevokeDeviceRequest) (*pb.RevokeDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	if err := s.repo.RevokeDevice(ctx, req.DeviceUrn); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.RevokeDeviceResponse{DeviceUrn: req.DeviceUrn, Revoked: true}, nil
}

// ── InitiatePairing ──────────────────────────────────────────────────────────

func (s *DeviceServer) InitiatePairing(ctx context.Context, req *pb.InitiatePairingRequest) (*pb.InitiatePairingResponse, error) {
	if req.InitiatorDeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "initiator_device_urn is required")
	}

	// The initiator must be a registered, non-revoked device.
	initiator, err := s.repo.GetDevice(ctx, req.InitiatorDeviceUrn)
	if err != nil {
		return nil, deviceRepoErrToStatus(err, req.InitiatorDeviceUrn)
	}
	if initiator.Revoked {
		return nil, status.Errorf(codes.PermissionDenied,
			"initiator device %q has been revoked", req.InitiatorDeviceUrn)
	}

	// Generate a short-lived session token.
	// TODO(phase7): replace with a crypto-random token.
	token := fmt.Sprintf("pair-%s-%d", sanitiseURNForToken(req.InitiatorDeviceUrn), time.Now().UnixNano())
	expiresAt := time.Now().UTC().Add(5 * time.Minute)

	s.pairMu.Lock()
	s.pairs[token] = &pairSession{
		InitiatorURN: req.InitiatorDeviceUrn,
		ExpiresAt:    expiresAt,
	}
	s.pairMu.Unlock()

	return &pb.InitiatePairingResponse{
		SessionToken: token,
		ExpiresAt:    timestamppb.New(expiresAt),
	}, nil
}

// ── CompletePairing ──────────────────────────────────────────────────────────

func (s *DeviceServer) CompletePairing(ctx context.Context, req *pb.CompletePairingRequest) (*pb.CompletePairingResponse, error) {
	if req.SessionToken == "" {
		return nil, status.Error(codes.InvalidArgument, "session_token is required")
	}
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}
	if len(req.PublicKey) == 0 {
		return nil, status.Error(codes.InvalidArgument, "public_key is required")
	}
	if len(req.PublicKey) != 32 {
		return nil, status.Errorf(codes.InvalidArgument,
			"public_key must be 32 bytes (Ed25519), got %d", len(req.PublicKey))
	}

	s.pairMu.Lock()
	session, ok := s.pairs[req.SessionToken]
	if ok {
		delete(s.pairs, req.SessionToken) // consume immediately; single-use
	}
	s.pairMu.Unlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "pairing session not found or already used")
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		return nil, status.Error(codes.DeadlineExceeded, "pairing session has expired")
	}

	// Validate new device URN.
	deviceURN, err := core.ParseURN(req.DeviceUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid device_urn: %v", err)
	}
	if deviceURN.ObjectType != core.ObjectTypeDevice {
		return nil, status.Errorf(codes.InvalidArgument,
			"device_urn must be of type %q, got %q", core.ObjectTypeDevice, deviceURN.ObjectType)
	}

	// Look up the initiator to inherit the owner URN.
	initiator, err := s.repo.GetDevice(ctx, session.InitiatorURN)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"initiator device %q is no longer registered", session.InitiatorURN)
		}
		return nil, status.Errorf(codes.Internal, "look up initiator: %v", err)
	}

	now := time.Now().UTC()
	d := &core.Device{
		URN:            deviceURN,
		Name:           req.DeviceName,
		OwnerURN:       initiator.OwnerURN,
		PublicKeyB64:   encodePublicKey(req.PublicKey),
		ApprovalStatus: core.DeviceApprovalApproved, // pairing implies trust from an existing device
		RegisteredAt:   now,
		LastSeenAt:     now,
	}

	if err := s.repo.RegisterDevice(ctx, d); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.CompletePairingResponse{
		DeviceUrn:    req.DeviceUrn,
		RegisteredAt: timestamppb.New(now),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared device helpers (used by device.go and device_admin.go)
// ─────────────────────────────────────────────────────────────────────────────

func sanitiseURNForToken(urn string) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(urn)
}

// deviceRepoErrToStatus maps DeviceRepository errors to gRPC status codes.
func deviceRepoErrToStatus(err error, urn string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "device %q not found", urn)
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "device %q already exists", urn)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// encodePublicKey base64-encodes a raw Ed25519 public key byte slice for
// storage in core.Device.PublicKeyB64.
func encodePublicKey(key []byte) string {
	return b64.StdEncoding.EncodeToString(key)
}

// decodePublicKey decodes a base64-encoded Ed25519 public key back to raw
// bytes. Returns an error if the string is empty or not valid base64.
func decodePublicKey(b64key string) ([]byte, error) {
	if b64key == "" {
		return nil, fmt.Errorf("public key is empty")
	}
	return b64.StdEncoding.DecodeString(b64key)
}
