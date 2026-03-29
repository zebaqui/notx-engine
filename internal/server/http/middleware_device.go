package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/repo"
)

// deviceIDHeader is the HTTP request header that clients must set to identify
// themselves. Its value must be the fully-qualified device URN.
const deviceIDHeader = "X-Device-ID"

// ctxKeyDevice is the context key under which the authenticated *core.Device
// is stored after the middleware succeeds.
type ctxKeyDevice struct{}

// DeviceFromCtx retrieves the authenticated device stored in the context by
// withDeviceAuth. It returns nil if no device is present (e.g. on routes that
// skip device auth).
func DeviceFromCtx(ctx context.Context) *core.Device {
	d, _ := ctx.Value(ctxKeyDevice{}).(*core.Device)
	return d
}

// withDeviceAuth returns a middleware that enforces device-level access
// control using the X-Device-ID request header.
//
// Enforcement rules (in order):
//  1. The header must be present and non-empty — 401 otherwise.
//  2. The device URN must exist in the repository — 401 if not found.
//  3. If the device has role=admin, steps 4 and 5 are skipped — admin
//     devices are always permitted regardless of approval status or
//     revocation flag.
//  4. The device must not be revoked — 403 if revoked.
//  5. The device approval status must be "approved" —
//     • "pending"  → 403 with a clear "awaiting approval" message.
//     • "rejected" → 403 with a clear "device rejected" message.
//
// On success the resolved *core.Device is stored in the request context under
// ctxKeyDevice{} and the next handler is called.
func (h *Handler) withDeviceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log := loggerFromCtx(r.Context(), slog.Default())

		// ── 1. Header presence ───────────────────────────────────────────────
		deviceID := r.Header.Get(deviceIDHeader)
		if deviceID == "" {
			log.Warn("device auth: missing header", "header", deviceIDHeader)
			writeError(w, http.StatusUnauthorized,
				"missing "+deviceIDHeader+" header: all data requests must identify the calling device")
			return
		}

		// ── 2. Device lookup ─────────────────────────────────────────────────
		d, err := h.dev.GetDevice(r.Context(), deviceID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				log.Warn("device auth: unknown device", "device_id", deviceID)
				writeError(w, http.StatusUnauthorized,
					"device not registered: register this device before making data requests")
				return
			}
			h.log.Error("device auth: repo error", "device_id", deviceID, "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		// ── 3. Admin role fast-path ──────────────────────────────────────────
		// Any device with role=admin is always permitted regardless of its
		// stored approval status or revocation flag — it bypasses steps 4 & 5.
		// This covers both the local-mode sentinel device and remote admin
		// devices registered via the passphrase flow.
		if d.Role == core.DeviceRoleAdmin {
			log.Debug("device auth: admin role, skipping approval check",
				"device_id", deviceID,
				"device_name", d.Name,
			)
			ctx := context.WithValue(r.Context(), ctxKeyDevice{}, d)
			next(w, r.WithContext(ctx))
			return
		}

		// ── 4. Revocation check ──────────────────────────────────────────────
		if d.Revoked {
			log.Warn("device auth: revoked device", "device_id", deviceID)
			writeError(w, http.StatusForbidden, "device has been revoked")
			return
		}

		// ── 5. Approval status check ─────────────────────────────────────────
		switch d.ApprovalStatus {
		case core.DeviceApprovalApproved:
			// All good — fall through.

		case core.DeviceApprovalPending:
			log.Warn("device auth: pending approval", "device_id", deviceID)
			writeError(w, http.StatusForbidden,
				"device is awaiting administrator approval before it can access data")
			return

		case core.DeviceApprovalRejected:
			log.Warn("device auth: rejected device", "device_id", deviceID)
			writeError(w, http.StatusForbidden,
				"device registration has been rejected")
			return

		default:
			// Treat any unknown status conservatively as denied.
			log.Warn("device auth: unknown approval status",
				"device_id", deviceID,
				"status", string(d.ApprovalStatus),
			)
			writeError(w, http.StatusForbidden, "device access not permitted")
			return
		}

		// ── Store device in context and continue ─────────────────────────────
		log.Debug("device auth: ok", "device_id", deviceID)
		ctx := context.WithValue(r.Context(), ctxKeyDevice{}, d)
		next(w, r.WithContext(ctx))
	}
}

// withDeviceAuthMiddleware composes withMiddleware (logging + panic recovery)
// with withDeviceAuth so that callers only need one wrapper call for data
// routes that require an identified device.
func (h *Handler) withDeviceAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return h.withMiddleware(h.withDeviceAuth(next))
}

// logDeviceAuth emits a structured log line at the Info level after a
// successful device authentication. Intended for audit-trail purposes.
func logDeviceAuth(log *slog.Logger, d *core.Device, method, path string) {
	log.Info("device authenticated",
		"device_urn", d.URN.String(),
		"device_name", d.Name,
		"owner_urn", d.OwnerURN.String(),
		"method", method,
		"path", path,
	)
}
