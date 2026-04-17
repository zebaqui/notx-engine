package http

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

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
