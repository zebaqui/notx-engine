package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	b64 "encoding/base64"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Devices — route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

// routeDevicesOpen handles /v1/devices without requiring device auth.
// POST (registration) is always permitted; GET and other methods require
// a valid device and are dispatched through withDeviceAuth inline.
func (h *Handler) routeDevicesOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.handleRegisterDevice(w, r)
		return
	}
	// All other methods on /v1/devices require an authenticated device.
	h.withDeviceAuth(h.routeDevices)(w, r)
}

func (h *Handler) routeDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListDevices(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeDevice(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/devices/")
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "device URN is required")
		return
	}

	if idx := strings.Index(trimmed, "/"); idx != -1 {
		urn := trimmed[:idx]
		subPath := trimmed[idx+1:]
		if urn == "" {
			writeError(w, http.StatusBadRequest, "device URN is required")
			return
		}
		switch subPath {
		case "status":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleGetDeviceStatus(w, r, urn)
		case "status/stream":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleStreamDeviceStatus(w, r, urn)
		case "approve":
			if r.Method != http.MethodPatch {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleApproveDevice(w, r, urn)
		case "reject":
			if r.Method != http.MethodPatch {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleRejectDevice(w, r, urn)
		default:
			writeError(w, http.StatusNotFound, "unknown device action: "+subPath)
		}
		return
	}

	urn := trimmed
	switch r.Method {
	case http.MethodGet:
		h.handleGetDevice(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateDevice(w, r, urn)
	case http.MethodDelete:
		h.handleRevokeDevice(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — devices
// ─────────────────────────────────────────────────────────────────────────────

type deviceJSON struct {
	URN            string `json:"urn"`
	Name           string `json:"name"`
	OwnerURN       string `json:"owner_urn"`
	PublicKeyB64   string `json:"public_key_b64"`
	Role           string `json:"role"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked,omitempty"`
	RegisteredAt   string `json:"registered_at"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
}

type registerDeviceRequest struct {
	URN             string `json:"urn"`
	Name            string `json:"name"`
	OwnerURN        string `json:"owner_urn"`
	PublicKeyB64    string `json:"public_key_b64"`
	AdminPassphrase string `json:"admin_passphrase,omitempty"`
}

type updateDeviceRequest struct {
	Name       *string `json:"name,omitempty"`
	LastSeenAt *string `json:"last_seen_at,omitempty"`
}

type listDevicesResponse struct {
	Devices []*deviceJSON `json:"devices"`
}

// deviceRoleStr converts a pb.DeviceRole enum to the short lowercase string
// used by the HTTP API (e.g. "admin", "relay", "client").
func deviceRoleStr(r pb.DeviceRole) string {
	switch r {
	case pb.DeviceRole_DEVICE_ROLE_ADMIN:
		return "admin"
	case pb.DeviceRole_DEVICE_ROLE_RELAY:
		return "relay"
	case pb.DeviceRole_DEVICE_ROLE_USER:
		return "client"
	default:
		return "client"
	}
}

// approvalStatusStr converts a pb.ApprovalStatus enum to the short lowercase
// string used by the HTTP API (e.g. "approved", "pending", "rejected").
func approvalStatusStr(s pb.ApprovalStatus) string {
	switch s {
	case pb.ApprovalStatus_APPROVAL_STATUS_APPROVED:
		return "approved"
	case pb.ApprovalStatus_APPROVAL_STATUS_REJECTED:
		return "rejected"
	case pb.ApprovalStatus_APPROVAL_STATUS_PENDING:
		return "pending"
	default:
		return "pending"
	}
}

func deviceInfoExtToJSON(d *pb.DeviceAdmin) *deviceJSON {
	if d == nil {
		return nil
	}
	j := &deviceJSON{
		URN:            d.DeviceUrn,
		Name:           d.DeviceName,
		OwnerURN:       d.OwnerUrn,
		Role:           deviceRoleStr(d.Role),
		ApprovalStatus: approvalStatusStr(d.ApprovalStatus),
		Revoked:        d.Revoked,
	}
	// Encode raw public key bytes as base64 for the JSON response.
	if len(d.PublicKey) > 0 {
		j.PublicKeyB64 = b64.StdEncoding.EncodeToString(d.PublicKey)
	}
	if d.RegisteredAt != nil {
		j.RegisteredAt = d.RegisteredAt.AsTime().UTC().Format(time.RFC3339)
	}
	if d.LastSeenAt != nil {
		j.LastSeenAt = d.LastSeenAt.AsTime().UTC().Format(time.RFC3339)
	}
	return j
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/devices
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ownerURN := q.Get("owner_urn")
	includeRevoked := q.Get("include_revoked") == "true"

	_ = includeRevoked // honoured by underlying repo; passed through below

	resp, err := h.deviceAdminSvc.AdminListDevices(r.Context(), &pb.AdminListDevicesRequest{
		OwnerUrn:       ownerURN,
		IncludeRevoked: includeRevoked,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "list devices")
		return
	}

	out := make([]*deviceJSON, 0, len(resp.Devices))
	for _, d := range resp.Devices {
		out = append(out, deviceInfoExtToJSON(d))
	}
	writeJSON(w, http.StatusOK, &listDevicesResponse{Devices: out})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/devices
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.OwnerURN == "" {
		writeError(w, http.StatusBadRequest, "owner_urn is required")
		return
	}

	// Determine role and initial approval status.
	//
	// Admin path: a passphrase hash is configured AND the request supplies a
	// passphrase that matches it → role=admin, immediately approved.
	//
	// Client path (default): no passphrase, wrong passphrase, or no hash
	// configured → role=client, approval follows the onboarding config.
	role := "client"
	approvalStatus := "pending"
	if h.cfg.DeviceOnboarding.AutoApprove {
		approvalStatus = "approved"
	}

	if h.cfg.Admin.AdminPassphraseHash != "" && req.AdminPassphrase != "" {
		if err := bcrypt.CompareHashAndPassword(
			[]byte(h.cfg.Admin.AdminPassphraseHash),
			[]byte(req.AdminPassphrase),
		); err == nil {
			role = "admin"
			approvalStatus = "approved"
		}
		// Wrong passphrase: fall through silently as a regular client.
	}

	publicKeyBytes, err := b64.StdEncoding.DecodeString(req.PublicKeyB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid public_key_b64: "+err.Error())
		return
	}

	var roleEnum pb.DeviceRole
	switch role {
	case "admin":
		roleEnum = pb.DeviceRole_DEVICE_ROLE_ADMIN
	case "relay":
		roleEnum = pb.DeviceRole_DEVICE_ROLE_RELAY
	default:
		roleEnum = pb.DeviceRole_DEVICE_ROLE_USER
	}

	var approvalStatusEnum pb.ApprovalStatus
	switch approvalStatus {
	case "approved":
		approvalStatusEnum = pb.ApprovalStatus_APPROVAL_STATUS_APPROVED
	case "rejected":
		approvalStatusEnum = pb.ApprovalStatus_APPROVAL_STATUS_REJECTED
	default:
		approvalStatusEnum = pb.ApprovalStatus_APPROVAL_STATUS_PENDING
	}

	resp, err := h.deviceAdminSvc.AdminRegisterDevice(r.Context(), &pb.AdminRegisterDeviceRequest{
		DeviceUrn:      req.URN,
		DeviceName:     req.Name,
		OwnerUrn:       req.OwnerURN,
		PublicKey:      publicKeyBytes,
		Role:           roleEnum,
		ApprovalStatus: approvalStatusEnum,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "register device")
		return
	}

	log := loggerFromCtx(r.Context(), h.log)
	log.Info("device registered",
		"device_urn", req.URN,
		"device_name", req.Name,
		"owner_urn", req.OwnerURN,
		"role", role,
		"approval_status", approvalStatus,
		"auto_approve", h.cfg.DeviceOnboarding.AutoApprove,
	)

	writeJSON(w, http.StatusCreated, deviceInfoExtToJSON(resp.Device))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/devices/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetDevice(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.deviceAdminSvc.GetDevice(r.Context(), &pb.GetDeviceRequest{DeviceUrn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get device")
		return
	}
	writeJSON(w, http.StatusOK, deviceInfoExtToJSON(resp.Device))
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/devices/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleUpdateDevice(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateDeviceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	grpcReq := &pb.UpdateDeviceRequest{
		DeviceUrn: urn,
		Device:    &pb.DeviceAdmin{},
	}

	if req.Name != nil {
		grpcReq.Device.DeviceName = *req.Name
	}
	if req.LastSeenAt != nil {
		ts, err := time.Parse(time.RFC3339, *req.LastSeenAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid last_seen_at: "+err.Error())
			return
		}
		grpcReq.Device.LastSeenAt = timestamppb.New(ts.UTC())
	}

	resp, err := h.deviceAdminSvc.UpdateDevice(r.Context(), grpcReq)
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "update device")
		return
	}
	writeJSON(w, http.StatusOK, deviceInfoExtToJSON(resp.Device))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/devices/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRevokeDevice(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.deviceAdminSvc.AdminRevokeDevice(r.Context(), &pb.AdminRevokeDeviceRequest{DeviceUrn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "revoke device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": resp.Revoked})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/devices/:urn/status
// ─────────────────────────────────────────────────────────────────────────────

type deviceStatusResponse struct {
	URN            string `json:"urn"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked,omitempty"`
	Approved       bool   `json:"approved"`
}

func (h *Handler) handleGetDeviceStatus(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.deviceAdminSvc.GetDeviceStatus(r.Context(), &pb.GetDeviceStatusRequest{DeviceUrn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "get device status")
		return
	}
	approved := resp.ApprovalStatus == pb.ApprovalStatus_APPROVAL_STATUS_APPROVED
	writeJSON(w, http.StatusOK, &deviceStatusResponse{
		URN:            resp.DeviceUrn,
		ApprovalStatus: approvalStatusStr(resp.ApprovalStatus),
		Revoked:        resp.Revoked,
		Approved:       approved,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/devices/:urn/approve
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleApproveDevice(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.deviceAdminSvc.ApproveDevice(r.Context(), &pb.ApproveDeviceRequest{DeviceUrn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "approve device")
		return
	}
	log := loggerFromCtx(r.Context(), h.log)
	log.Info("device approved", "device_urn", urn)
	writeJSON(w, http.StatusOK, deviceInfoExtToJSON(resp.Device))
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/devices/:urn/reject
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRejectDevice(w http.ResponseWriter, r *http.Request, urn string) {
	resp, err := h.deviceAdminSvc.RejectDevice(r.Context(), &pb.RejectDeviceRequest{DeviceUrn: urn})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "reject device")
		return
	}
	log := loggerFromCtx(r.Context(), h.log)
	log.Info("device rejected", "device_urn", urn)
	writeJSON(w, http.StatusOK, deviceInfoExtToJSON(resp.Device))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/devices/:urn/status/stream  (SSE)
// ─────────────────────────────────────────────────────────────────────────────

// sseDeviceStatusInterval is how often the stream re-reads the device from the
// repo and emits an event if anything has changed.
const sseDeviceStatusInterval = 2 * time.Second

// sseDeviceStatusTimeout is the maximum wall-clock time a single SSE connection
// is kept open. After this the server sends a final "timeout" event and closes
// the stream. The client should reconnect if it still needs to wait.
const sseDeviceStatusTimeout = 5 * time.Minute

// deviceStatusEvent is the payload sent on the SSE stream.
type deviceStatusEvent struct {
	URN            string `json:"urn"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked,omitempty"`
	Approved       bool   `json:"approved"`
}

// isTerminalDeviceStatus reports whether the device has reached a state from
// which it will never transition back to pending.
func isTerminalDeviceStatus(d *deviceStatusEvent) bool {
	if d.Revoked {
		return true
	}
	switch d.ApprovalStatus {
	case "approved", "rejected":
		return true
	}
	return false
}

func (h *Handler) handleStreamDeviceStatus(w http.ResponseWriter, r *http.Request, urn string) {
	log := loggerFromCtx(r.Context(), h.log).With("device_urn", urn, "handler", "sse_device_status")

	// ── SSE prerequisites ────────────────────────────────────────────────────
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported by this server")
		return
	}

	// ── Initial device lookup ────────────────────────────────────────────────
	resp, err := h.deviceAdminSvc.GetDevice(r.Context(), &pb.GetDeviceRequest{DeviceUrn: urn})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "sse device status: initial lookup", err)
		return
	}
	d := protoDeviceToStatusEvent(resp.Device)

	// ── Switch to SSE mode ───────────────────────────────────────────────────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// ── Emit helpers ─────────────────────────────────────────────────────────
	emitStatus := func(ev *deviceStatusEvent) error {
		data, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("marshal status event: %w", err)
		}
		_, err = fmt.Fprintf(w, "event: status\ndata: %s\n\n", data)
		if err != nil {
			return fmt.Errorf("write status event: %w", err)
		}
		flusher.Flush()
		return nil
	}

	emitTimeout := func() {
		fmt.Fprintf(w, "event: timeout\ndata: {\"reason\":\"stream timeout, reconnect to continue waiting\"}\n\n")
		flusher.Flush()
	}

	emitKeepalive := func() {
		fmt.Fprintf(w, ": keepalive\n\n")
		flusher.Flush()
	}

	// Send the current state immediately.
	if err := emitStatus(d); err != nil {
		log.Warn("sse: failed to write initial event", "err", err)
		return
	}

	// If already terminal, we're done.
	if isTerminalDeviceStatus(d) {
		log.Debug("sse: device already terminal, closing stream")
		return
	}

	// ── Poll loop ────────────────────────────────────────────────────────────
	ticker := time.NewTicker(sseDeviceStatusInterval)
	defer ticker.Stop()

	timeout := time.NewTimer(sseDeviceStatusTimeout)
	defer timeout.Stop()

	lastStatus := d.ApprovalStatus
	lastRevoked := d.Revoked

	for {
		select {
		case <-r.Context().Done():
			log.Debug("sse: client disconnected")
			return

		case <-timeout.C:
			emitTimeout()
			log.Debug("sse: stream timeout reached", "urn", urn)
			return

		case <-ticker.C:
			emitKeepalive()

			pollResp, err := h.deviceAdminSvc.GetDevice(r.Context(), &pb.GetDeviceRequest{DeviceUrn: urn})
			if err != nil {
				if errors.Is(err, repo.ErrNotFound) {
					fmt.Fprintf(w, "event: error\ndata: {\"error\":\"device no longer exists\"}\n\n")
					flusher.Flush()
					return
				}
				log.Warn("sse: repo error during poll", "err", err)
				continue
			}
			current := protoDeviceToStatusEvent(pollResp.Device)

			if current.ApprovalStatus != lastStatus || current.Revoked != lastRevoked {
				lastStatus = current.ApprovalStatus
				lastRevoked = current.Revoked

				if err := emitStatus(current); err != nil {
					log.Warn("sse: failed to write status event", "err", err)
					return
				}

				log.Info("sse: device status changed",
					"approval_status", current.ApprovalStatus,
					"revoked", current.Revoked,
				)

				if isTerminalDeviceStatus(current) {
					log.Debug("sse: terminal state reached, closing stream")
					return
				}
			}
		}
	}
}

// protoDeviceToStatusEvent converts a pb.DeviceAdmin to the SSE event payload.
func protoDeviceToStatusEvent(d *pb.DeviceAdmin) *deviceStatusEvent {
	if d == nil {
		return &deviceStatusEvent{}
	}
	status := approvalStatusStr(d.ApprovalStatus)
	approved := d.ApprovalStatus == pb.ApprovalStatus_APPROVAL_STATUS_APPROVED && !d.Revoked
	return &deviceStatusEvent{
		URN:            d.DeviceUrn,
		ApprovalStatus: status,
		Revoked:        d.Revoked,
		Approved:       approved,
	}
}
