package repo

import "time"

// SyncLogEntry is a completed sync operation recorded in the sync_log table.
type SyncLogEntry struct {
	ID         int64
	NoteURN    string
	Direction  string // "push" | "pull"
	EventCount int
	Status     string // "ok" | "error"
	Error      string
	SyncedAt   time.Time
}

// SyncRepository is the storage interface for sync history and pending state.
type SyncRepository interface {
	// AppendSyncLogEntry writes a completed sync operation to persistent storage.
	AppendSyncLogEntry(e SyncLogEntry) error
	// ListSyncLog returns paginated sync_log rows ordered by synced_at DESC.
	// pageSize 0 defaults to 50, max 200.
	// pageToken is an opaque cursor returned from a previous call; pass "" for the first page.
	// Returns the entries, the next page token (empty string if no more pages), and any error.
	ListSyncLog(pageSize int, pageToken string) ([]SyncLogEntry, string, error)
	// SyncLogCount returns the total number of sync_log rows.
	SyncLogCount() (int, error)
	// ListSyncPending returns all note URNs with pending sync rows.
	ListSyncPending() ([]string, error)
}
