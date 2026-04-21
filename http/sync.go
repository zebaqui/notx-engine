package http

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// SyncAdminRepo — storage interface used by sync admin handlers
// ─────────────────────────────────────────────────────────────────────────────

// SyncAdminRepo is a subset of repo.SyncRepository consumed by the HTTP layer.
// It is satisfied directly by *sqlite.Provider.
type SyncAdminRepo interface {
	ListSyncLog(pageSize int, pageToken string) ([]repo.SyncLogEntry, string, error)
	SyncLogCount() (int, error)
	ListSyncPending() ([]string, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON shapes
// ─────────────────────────────────────────────────────────────────────────────

type syncLogRow struct {
	ID         int64  `json:"id"`
	NoteURN    string `json:"note_urn"`
	Direction  string `json:"direction"`
	EventCount int    `json:"event_count"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	SyncedAt   string `json:"synced_at"`
}

type syncStatusResponse struct {
	Connected     bool    `json:"connected"`
	PeerAuthority string  `json:"peer_authority"`
	ConnectedAt   *string `json:"connected_at,omitempty"`
	LastPingAt    *string `json:"last_ping_at,omitempty"`
	PendingCount  int     `json:"pending_count"`
	CertExpiry    *string `json:"cert_expiry,omitempty"`
}

type syncLogResponse struct {
	Entries       []syncLogRow `json:"entries"`
	NextPageToken string       `json:"next_page_token,omitempty"`
	Total         int          `json:"total"`
}

type syncPendingResponse struct {
	NoteURNs []string `json:"note_urns"`
	Count    int      `json:"count"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Sync admin handlers
// ─────────────────────────────────────────────────────────────────────────────

// handleSyncStatus handles GET /v1/sync/status.
func (h *Handler) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.syncBus == nil {
		writeError(w, http.StatusServiceUnavailable, "sync bus not configured")
		return
	}

	snap := h.syncBus.Status()
	resp := syncStatusResponse{
		Connected:     snap.Connected,
		PeerAuthority: snap.PeerAuthority,
		PendingCount:  snap.PendingCount,
	}
	if snap.ConnectedAt != nil {
		s := snap.ConnectedAt.UTC().Format(time.RFC3339)
		resp.ConnectedAt = &s
	}
	if snap.LastPingAt != nil {
		s := snap.LastPingAt.UTC().Format(time.RFC3339)
		resp.LastPingAt = &s
	}
	if snap.CertExpiry != nil {
		s := snap.CertExpiry.UTC().Format(time.RFC3339)
		resp.CertExpiry = &s
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSyncLog handles GET /v1/sync/log.
// Query params: page_size (int), page_token (string).
func (h *Handler) handleSyncLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.syncRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "sync repo not configured")
		return
	}

	pageSize := 50
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 {
			pageSize = n
		}
	}
	pageToken := r.URL.Query().Get("page_token")

	entries, nextToken, err := h.syncRepo.ListSyncLog(pageSize, pageToken)
	if err != nil {
		h.internalError(w, r, "sync_log.list", err)
		return
	}
	total, err := h.syncRepo.SyncLogCount()
	if err != nil {
		h.internalError(w, r, "sync_log.count", err)
		return
	}

	rows := make([]syncLogRow, len(entries))
	for i, e := range entries {
		rows[i] = syncLogRow{
			ID:         e.ID,
			NoteURN:    e.NoteURN,
			Direction:  e.Direction,
			EventCount: e.EventCount,
			Status:     e.Status,
			Error:      e.Error,
			SyncedAt:   e.SyncedAt.UTC().Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, syncLogResponse{
		Entries:       rows,
		NextPageToken: nextToken,
		Total:         total,
	})
}

// handleSyncPending handles GET /v1/sync/pending.
func (h *Handler) handleSyncPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.syncRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "sync repo not configured")
		return
	}

	urns, err := h.syncRepo.ListSyncPending()
	if err != nil {
		h.internalError(w, r, "sync_pending.list", err)
		return
	}
	if urns == nil {
		urns = []string{}
	}
	writeJSON(w, http.StatusOK, syncPendingResponse{
		NoteURNs: urns,
		Count:    len(urns),
	})
}

// handleSyncTrigger handles POST /v1/sync/trigger.
// Enqueues all pending notes for an immediate sync attempt. Non-blocking; returns 202.
func (h *Handler) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.syncBus == nil || h.syncRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "sync bus not configured")
		return
	}

	urns, err := h.syncRepo.ListSyncPending()
	if err != nil {
		h.internalError(w, r, "sync_trigger.list_pending", err)
		return
	}
	for _, urn := range urns {
		h.syncBus.Notify(urn)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"triggered": len(urns),
	})
}

const (
	syncLogPollInterval = 500 * time.Millisecond
	syncStreamTimeout   = 30 * time.Minute
)

// syncProgressEventSSE is the shape of each line in ~/.notx/sync.log
// and the SSE data payload. Decoded only to check type for early close.
type syncProgressEventSSE struct {
	Type string `json:"type"`
}

func syncLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".notx", "sync.log")
}

func (h *Handler) handleSyncStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	logPath := syncLogPath()

	// emit sends one SSE event line and flushes.
	emit := func(data string) bool {
		_, err := fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	keepalive := func() bool {
		_, err := fmt.Fprintf(w, ": keepalive\n\n")
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Track how many bytes we've already sent so we only emit new lines.
	var offset int64
	done := false

	ticker := time.NewTicker(syncLogPollInterval)
	defer ticker.Stop()
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()
	timeout := time.NewTimer(syncStreamTimeout)
	defer timeout.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout.C:
			fmt.Fprintf(w, "event: timeout\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-keepaliveTicker.C:
			if !keepalive() {
				return
			}
		case <-ticker.C:
			if logPath == "" {
				continue
			}
			f, err := os.Open(logPath)
			if err != nil {
				continue
			}

			// If file was truncated (new sync started), reset offset.
			fi, err := f.Stat()
			if err != nil {
				f.Close()
				continue
			}
			if fi.Size() < offset {
				offset = 0
				done = false
			}

			if _, err := f.Seek(offset, 0); err != nil {
				f.Close()
				continue
			}

			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				if line == "" {
					continue
				}
				if !emit(line) {
					f.Close()
					return
				}
				offset += int64(len(line)) + 1 // +1 for newline

				// Check if sync completed — close stream after "done" or "error".
				var ev syncProgressEventSSE
				if json.Unmarshal([]byte(line), &ev) == nil {
					if ev.Type == "done" || ev.Type == "error" {
						done = true
					}
				}
			}
			f.Close()

			if done {
				// Give the client a moment to receive the last event, then close.
				time.Sleep(200 * time.Millisecond)
				return
			}
		}
	}
}
