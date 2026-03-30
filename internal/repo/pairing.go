package repo

import (
	"context"
	"time"

	"github.com/zebaqui/notx-engine/core"
)

// ServerListOptions controls which servers are returned by ListServers.
type ServerListOptions struct {
	IncludeRevoked bool
}

// ServerListResult is the result of ListServers.
type ServerListResult struct {
	Servers []*core.Server
}

// ServerRepository persists paired server records and their certificates.
type ServerRepository interface {
	RegisterServer(ctx context.Context, s *core.Server) error
	GetServer(ctx context.Context, urn string) (*core.Server, error)
	ListServers(ctx context.Context, opts ServerListOptions) (*ServerListResult, error)
	UpdateServer(ctx context.Context, s *core.Server) error
	RevokeServer(ctx context.Context, urn string) error
}

// PairingSecret is an authority-side record for a single-use registration token.
// Only the bcrypt hash is ever persisted; the plaintext is returned once to the
// admin and then discarded.
type PairingSecret struct {
	ID         string
	LabelHint  string
	HashBcrypt string
	ExpiresAt  time.Time
	UsedAt     *time.Time
}

// PairingSecretStore manages single-use registration secrets on the authority.
type PairingSecretStore interface {
	AddSecret(ctx context.Context, s *PairingSecret) error

	// ConsumeSecret validates the plaintext against all unexpired, unused secrets.
	// On a match it atomically marks the secret used and returns the record.
	// Returns ErrNotFound if no matching unexpired unused secret exists.
	ConsumeSecret(ctx context.Context, plaintext string) (*PairingSecret, error)

	// PruneExpired removes all expired secret records from storage.
	PruneExpired(ctx context.Context) error
}
