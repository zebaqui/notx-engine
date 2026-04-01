package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// sseDeviceStatusInterval is how often the stream re-reads the device from the
// repo and emits an event if anything has changed.
const sseDeviceStatusInterval = 2 * time.Second

// sseDeviceStatusTimeout is the maximum wall-clock time a single SSE connection
// is kept open. After this the server sends a final "timeout" event and closes
// the stream. The client should reconnect if it still needs to wait.
const sseDeviceStatusTimeout = 5 * time.Minute

// deviceStatusEvent is the payload sent on the SSE stream.
// It mirrors deviceStatusResponse but is self-contained so the two types can
// evolve independently.
type deviceStatusEvent struct {
	URN            string `json:"urn"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked,omitempty"`
	Approved       bool   `json:"approved"`
}

// isTerminalDeviceStatus reports whether the device has reached a state from
// which it will never transition back to pending. Once we emit one of these we
// can close the stream — there is nothing more to wait for.
func isTerminalDeviceStatus(d *core.Device) bool {
	if d.Revoked {
		return true
	}
	switch d.ApprovalStatus {
	case core.DeviceApprovalApproved, core.DeviceApprovalRejected:
		return true
	}
	return false
}

// handleStreamDeviceStatus is the SSE handler for:
//
//	GET /v1/devices/:urn/status/stream
//
// It streams approval-status change events to the caller using the
// text/event-stream protocol. The connection is:
//
//   - Open: while the device is in "pending" state.
//   - Closed (by server): as soon as the status transitions to a terminal
//     value ("approved", "rejected", or "revoked"), or after
//     sseDeviceStatusTimeout to avoid holding connections indefinitely.
//   - Closed (by client): the request context is cancelled, the handler
//     exits cleanly.
//
// The endpoint is intentionally open — it does NOT require an X-Device-ID
// header — mirroring the GET /v1/devices/:urn/status behaviour.
//
// SSE wire format
//
//	event: status
//	data: {"urn":"...","approval_status":"pending","approved":false}
//
//	event: status
//	data: {"urn":"...","approval_status":"approved","approved":true}
//
// A keepalive comment line (": keepalive") is emitted every tick so that
// proxies and load-balancers do not close idle connections.
//
// On timeout the server emits:
//
//	event: timeout
//	data: {"reason":"stream timeout, reconnect to continue waiting"}
func (h *Handler) handleStreamDeviceStatus(w http.ResponseWriter, r *http.Request, urn string) {
	log := loggerFromCtx(r.Context(), h.log).With("device_urn", urn, "handler", "sse_device_status")

	// ── SSE prerequisites ────────────────────────────────────────────────────

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Should never happen with Go's standard HTTP/1.1 server, but guard
		// defensively so we never silently serve a broken stream.
		writeError(w, http.StatusInternalServerError, "streaming not supported by this server")
		return
	}

	// ── Initial device lookup ────────────────────────────────────────────────
	// We do this before setting SSE headers so we can still return a normal
	// JSON 404 if the device doesn't exist.

	d, err := h.dev.GetDevice(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "sse device status: initial lookup", err)
		return
	}

	// ── Switch to SSE mode ───────────────────────────────────────────────────

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable any response buffering that a reverse proxy might apply.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// ── Emit helpers ─────────────────────────────────────────────────────────

	emitStatus := func(dev *core.Device) error {
		approved := dev.ApprovalStatus == core.DeviceApprovalApproved && !dev.Revoked
		payload := deviceStatusEvent{
			URN:            dev.URN.String(),
			ApprovalStatus: string(dev.ApprovalStatus),
			Revoked:        dev.Revoked,
			Approved:       approved,
		}
		data, err := json.Marshal(payload)
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

	// ── Send the current state immediately so the client doesn't have to wait
	// for the first tick.

	if err := emitStatus(d); err != nil {
		log.Warn("sse: failed to write initial event", "err", err)
		return
	}

	// If the device is already in a terminal state, we're done.
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
			// Client disconnected or request was cancelled.
			log.Debug("sse: client disconnected")
			return

		case <-timeout.C:
			emitTimeout()
			log.Debug("sse: stream timeout reached", "urn", urn)
			return

		case <-ticker.C:
			emitKeepalive()

			current, err := h.dev.GetDevice(r.Context(), urn)
			if err != nil {
				if errors.Is(err, repo.ErrNotFound) {
					// Device was hard-deleted — unlikely but handle gracefully.
					fmt.Fprintf(w, "event: error\ndata: {\"error\":\"device no longer exists\"}\n\n")
					flusher.Flush()
					return
				}
				// Transient repo error — log and keep trying.
				log.Warn("sse: repo error during poll", "err", err)
				continue
			}

			// Only emit an event when something actually changed.
			if current.ApprovalStatus != lastStatus || current.Revoked != lastRevoked {
				lastStatus = current.ApprovalStatus
				lastRevoked = current.Revoked

				if err := emitStatus(current); err != nil {
					log.Warn("sse: failed to write status event", "err", err)
					return
				}

				log.Info("sse: device status changed",
					"approval_status", string(current.ApprovalStatus),
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
