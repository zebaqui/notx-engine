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
	"github.com/zebaqui/notx-engine/repo"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// NoteServiceServer
// ─────────────────────────────────────────────────────────────────────────────

// NoteServiceServer implements pb.NoteServiceServer backed by a NoteRepository.
type NoteServiceServer struct {
	pb.UnimplementedNoteServiceServer
	repo        repo.NoteRepository
	defaultPage int
	maxPage     int
}

// NewNoteServiceServer returns a ready-to-register NoteServiceServer.
func NewNoteServiceServer(r repo.NoteRepository, defaultPage, maxPage int) *NoteServiceServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &NoteServiceServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// ── GetNote ──────────────────────────────────────────────────────────────────

func (s *NoteServiceServer) GetNote(ctx context.Context, req *pb.GetNoteRequest) (*pb.GetNoteResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	note, err := s.repo.Get(ctx, req.Urn)
	if err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	events, err := s.repo.Events(ctx, req.Urn, 1)
	if err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	pbEvents := make([]*pb.EventProto, 0, len(events))
	for _, ev := range events {
		pbEvents = append(pbEvents, coreEventToProto(ev))
	}

	return &pb.GetNoteResponse{
		Header: coreNoteToHeader(note),
		Events: pbEvents,
	}, nil
}

// ── ListNotes ────────────────────────────────────────────────────────────────

func (s *NoteServiceServer) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	opts := repo.ListOptions{
		ProjectURN:     req.ProjectUrn,
		FolderURN:      req.FolderUrn,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       pageSize,
		PageToken:      req.PageToken,
	}

	if req.NoteType != pb.NoteType_NOTE_TYPE_UNSPECIFIED {
		opts.FilterByType = true
		opts.NoteTypeFilter = protoNoteTypeToCore(req.NoteType)
	}

	result, err := s.repo.List(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list notes: %v", err)
	}

	headers := make([]*pb.NoteHeader, 0, len(result.Notes))
	for _, n := range result.Notes {
		headers = append(headers, coreNoteToHeader(n))
	}

	return &pb.ListNotesResponse{
		Notes:         headers,
		NextPageToken: result.NextPageToken,
	}, nil
}

// ── CreateNote ───────────────────────────────────────────────────────────────

func (s *NoteServiceServer) CreateNote(ctx context.Context, req *pb.CreateNoteRequest) (*pb.CreateNoteResponse, error) {
	if req.Header == nil {
		return nil, status.Error(codes.InvalidArgument, "header is required")
	}
	if req.Header.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "header.urn is required")
	}
	if req.Header.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "header.name is required")
	}

	note, err := protoHeaderToCoreNote(req.Header)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid header: %v", err)
	}

	if err := s.repo.Create(ctx, note); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			return nil, status.Errorf(codes.AlreadyExists, "note %q already exists", req.Header.Urn)
		}
		return nil, status.Errorf(codes.Internal, "create note: %v", err)
	}

	return &pb.CreateNoteResponse{
		Header: coreNoteToHeader(note),
	}, nil
}

// ── DeleteNote ───────────────────────────────────────────────────────────────

func (s *NoteServiceServer) DeleteNote(ctx context.Context, req *pb.DeleteNoteRequest) (*pb.DeleteNoteResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.Delete(ctx, req.Urn); err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteNoteResponse{Deleted: true}, nil
}

// ── AppendEvent ──────────────────────────────────────────────────────────────

func (s *NoteServiceServer) AppendEvent(ctx context.Context, req *pb.AppendEventRequest) (*pb.AppendEventResponse, error) {
	if req.Event == nil {
		return nil, status.Error(codes.InvalidArgument, "event is required")
	}
	if req.Event.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "event.note_urn is required")
	}
	if req.Event.Sequence <= 0 {
		return nil, status.Error(codes.InvalidArgument, "event.sequence must be >= 1")
	}
	if req.Event.AuthorUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "event.author_urn is required")
	}

	ev, err := protoEventToCore(req.Event)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid event: %v", err)
	}

	opts := repo.AppendEventOptions{
		ExpectSequence: int(req.Event.Sequence),
	}

	if err := s.repo.AppendEvent(ctx, ev, opts); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "note %q not found", req.Event.NoteUrn)
		}
		if errors.Is(err, repo.ErrSequenceConflict) {
			return nil, status.Errorf(codes.Aborted, "sequence conflict: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "append event: %v", err)
	}

	return &pb.AppendEventResponse{Sequence: req.Event.Sequence}, nil
}

// ── StreamEvents ─────────────────────────────────────────────────────────────

func (s *NoteServiceServer) StreamEvents(req *pb.StreamEventsRequest, stream pb.NoteService_StreamEventsServer) error {
	if req.NoteUrn == "" {
		return status.Error(codes.InvalidArgument, "note_urn is required")
	}

	fromSeq := int(req.FromSequence)
	if fromSeq < 1 {
		fromSeq = 1
	}

	events, err := s.repo.Events(stream.Context(), req.NoteUrn, fromSeq)
	if err != nil {
		return repoErrToStatus(err, req.NoteUrn)
	}

	for _, ev := range events {
		if err := stream.Send(coreEventToProto(ev)); err != nil {
			return status.Errorf(codes.Unavailable, "stream send: %v", err)
		}
	}

	return nil
}

// ── SearchNotes ──────────────────────────────────────────────────────────────

func (s *NoteServiceServer) SearchNotes(ctx context.Context, req *pb.SearchNotesRequest) (*pb.SearchNotesResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	opts := repo.SearchOptions{
		Query:     req.Query,
		PageSize:  pageSize,
		PageToken: req.PageToken,
	}

	results, err := s.repo.Search(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}

	pbResults := make([]*pb.SearchResult, 0, len(results.Results))
	for _, r := range results.Results {
		pbResults = append(pbResults, &pb.SearchResult{
			Header:  coreNoteToHeader(r.Note),
			Excerpt: r.Excerpt,
		})
	}

	return &pb.SearchNotesResponse{
		Results:       pbResults,
		NextPageToken: results.NextPageToken,
	}, nil
}

// ── ShareSecureNote ──────────────────────────────────────────────────────────

// ShareSecureNote is a relay-only operation: the server updates the
// per_device_keys map in stored encrypted events without ever decrypting them.
// The actual re-wrapping of CEKs is performed client-side before this call.
func (s *NoteServiceServer) ShareSecureNote(ctx context.Context, req *pb.ShareSecureNoteRequest) (*pb.ShareSecureNoteResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}
	if len(req.WrappedKeys) == 0 {
		return nil, status.Error(codes.InvalidArgument, "wrapped_keys must not be empty")
	}

	// Validate that the note exists and is of type secure.
	note, err := s.repo.Get(ctx, req.NoteUrn)
	if err != nil {
		return nil, repoErrToStatus(err, req.NoteUrn)
	}
	if note.NoteType != core.NoteTypeSecure {
		return nil, status.Errorf(codes.InvalidArgument,
			"note %q is not a secure note; ShareSecureNote only applies to secure notes", req.NoteUrn)
	}

	// The actual per_device_keys update is a Phase 3 operation (requires the
	// encrypted event store to be extended). For Phase 1 we validate inputs and
	// return a stub response so the RPC contract is established.
	//
	// TODO(phase3): iterate over all events for the note, locate each
	// EncryptedEventProto, and merge req.WrappedKeys into its per_device_keys map.

	return &pb.ShareSecureNoteResponse{
		EventsUpdated: 0, // will be non-zero once Phase 3 is complete
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceServiceServer
// ─────────────────────────────────────────────────────────────────────────────

// DeviceServiceServer implements pb.DeviceServiceServer backed by the shared
// repo.DeviceRepository so the gRPC and HTTP layers read from and write to the
// same device store.
//
// Pairing sessions are the only state kept in-memory: they are short-lived
// (5-minute TTL), never need to survive a restart, and have no repo analogue.
type DeviceServiceServer struct {
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

// NewDeviceServiceServer returns a ready-to-register DeviceServiceServer
// backed by the supplied DeviceRepository.
func NewDeviceServiceServer(r repo.DeviceRepository) *DeviceServiceServer {
	return &DeviceServiceServer{
		repo:  r,
		pairs: make(map[string]*pairSession),
	}
}

// ── RegisterDevice ───────────────────────────────────────────────────────────

func (s *DeviceServiceServer) RegisterDevice(ctx context.Context, req *pb.RegisterDeviceRequest) (*pb.RegisterDeviceResponse, error) {
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

func (s *DeviceServiceServer) GetDevicePublicKey(ctx context.Context, req *pb.GetDevicePublicKeyRequest) (*pb.GetDevicePublicKeyResponse, error) {
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

func (s *DeviceServiceServer) ListDevices(ctx context.Context, req *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
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

	infos := make([]*pb.DeviceInfo, 0, len(result.Devices))
	for _, d := range result.Devices {
		key, err := decodePublicKey(d.PublicKeyB64)
		if err != nil {
			// Skip devices whose key is undecodable rather than hard-failing the
			// whole list — the device may have been registered without a key.
			continue
		}
		info := &pb.DeviceInfo{
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

func (s *DeviceServiceServer) RevokeDevice(ctx context.Context, req *pb.RevokeDeviceRequest) (*pb.RevokeDeviceResponse, error) {
	if req.DeviceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "device_urn is required")
	}

	if err := s.repo.RevokeDevice(ctx, req.DeviceUrn); err != nil {
		return nil, deviceRepoErrToStatus(err, req.DeviceUrn)
	}

	return &pb.RevokeDeviceResponse{Revoked: true}, nil
}

// ── InitiatePairing ──────────────────────────────────────────────────────────

func (s *DeviceServiceServer) InitiatePairing(ctx context.Context, req *pb.InitiatePairingRequest) (*pb.InitiatePairingResponse, error) {
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

func (s *DeviceServiceServer) CompletePairing(ctx context.Context, req *pb.CompletePairingRequest) (*pb.CompletePairingResponse, error) {
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
// Proto ↔ core conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func coreNoteToHeader(n *core.Note) *pb.NoteHeader {
	h := &pb.NoteHeader{
		Urn:       n.URN.String(),
		Name:      n.Name,
		NoteType:  coreNoteTypeToProto(n.NoteType),
		Deleted:   n.Deleted,
		CreatedAt: timestamppb.New(n.CreatedAt),
		UpdatedAt: timestamppb.New(n.UpdatedAt),
	}
	if n.ProjectURN != nil {
		h.ProjectUrn = n.ProjectURN.String()
	}
	if n.FolderURN != nil {
		h.FolderUrn = n.FolderURN.String()
	}
	return h
}

func protoHeaderToCoreNote(h *pb.NoteHeader) (*core.Note, error) {
	urn, err := core.ParseURN(h.Urn)
	if err != nil {
		return nil, fmt.Errorf("invalid urn: %w", err)
	}

	createdAt := time.Now().UTC()
	if h.CreatedAt != nil {
		createdAt = h.CreatedAt.AsTime()
	}

	noteType := protoNoteTypeToCore(h.NoteType)
	var note *core.Note
	if noteType == core.NoteTypeSecure {
		note = core.NewSecureNote(urn, h.Name, createdAt)
	} else {
		note = core.NewNote(urn, h.Name, createdAt)
	}

	note.Deleted = h.Deleted

	if h.ProjectUrn != "" {
		projURN, err := core.ParseURN(h.ProjectUrn)
		if err != nil {
			return nil, fmt.Errorf("invalid project_urn: %w", err)
		}
		note.ProjectURN = &projURN
	}
	if h.FolderUrn != "" {
		folderURN, err := core.ParseURN(h.FolderUrn)
		if err != nil {
			return nil, fmt.Errorf("invalid folder_urn: %w", err)
		}
		note.FolderURN = &folderURN
	}

	return note, nil
}

func coreEventToProto(ev *core.Event) *pb.EventProto {
	p := &pb.EventProto{
		Urn:       ev.URN.String(),
		NoteUrn:   ev.NoteURN.String(),
		Sequence:  int32(ev.Sequence),
		AuthorUrn: ev.AuthorURN.String(),
		CreatedAt: timestamppb.New(ev.CreatedAt),
	}
	for _, e := range ev.Entries {
		p.Entries = append(p.Entries, &pb.LineEntryProto{
			Op:         int32(e.Op),
			LineNumber: int32(e.LineNumber),
			Content:    e.Content,
		})
	}
	return p
}

func protoEventToCore(p *pb.EventProto) (*core.Event, error) {
	noteURN, err := core.ParseURN(p.NoteUrn)
	if err != nil {
		return nil, fmt.Errorf("invalid note_urn: %w", err)
	}
	authorURN, err := core.ParseURN(p.AuthorUrn)
	if err != nil {
		return nil, fmt.Errorf("invalid author_urn: %w", err)
	}

	createdAt := time.Now().UTC()
	if p.CreatedAt != nil {
		createdAt = p.CreatedAt.AsTime()
	}

	var urn core.URN
	if p.Urn != "" {
		if parsed, err := core.ParseURN(p.Urn); err == nil {
			urn = parsed
		}
	}

	entries := make([]core.LineEntry, 0, len(p.Entries))
	for _, e := range p.Entries {
		entries = append(entries, core.LineEntry{
			LineNumber: int(e.LineNumber),
			Op:         core.LineOp(e.Op),
			Content:    e.Content,
		})
	}

	return &core.Event{
		URN:       urn,
		NoteURN:   noteURN,
		Sequence:  int(p.Sequence),
		AuthorURN: authorURN,
		CreatedAt: createdAt,
		Entries:   entries,
	}, nil
}

func coreNoteTypeToProto(t core.NoteType) pb.NoteType {
	switch t {
	case core.NoteTypeSecure:
		return pb.NoteType_NOTE_TYPE_SECURE
	default:
		return pb.NoteType_NOTE_TYPE_NORMAL
	}
}

func protoNoteTypeToCore(t pb.NoteType) core.NoteType {
	switch t {
	case pb.NoteType_NOTE_TYPE_SECURE:
		return core.NoteTypeSecure
	default:
		return core.NoteTypeNormal
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Error mapping
// ─────────────────────────────────────────────────────────────────────────────

func repoErrToStatus(err error, urn string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "%q not found", urn)
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%q already exists", urn)
	case errors.Is(err, repo.ErrSequenceConflict):
		return status.Errorf(codes.Aborted, "sequence conflict: %v", err)
	case errors.Is(err, repo.ErrNoteTypeImmutable):
		return status.Errorf(codes.InvalidArgument, "note_type is immutable: %v", err)
	case errors.Is(err, repo.ErrInvalidURN):
		return status.Errorf(codes.InvalidArgument, "invalid URN: %v", err)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
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
