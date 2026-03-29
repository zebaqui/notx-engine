package core

import "time"

// User represents a human actor in the notx system. Every note author,
// device owner, and share recipient is identified by a User URN.
type User struct {
	// URN is the globally unique user identifier.
	// Format: <namespace>:usr:<uuid>
	URN URN

	// DisplayName is the human-readable name shown in the UI.
	DisplayName string

	// Email is the user's contact address. Optional but must be unique
	// when non-empty.
	Email string

	// Deleted marks the user as soft-deleted.
	Deleted bool

	// CreatedAt is the UTC timestamp of initial registration.
	CreatedAt time.Time

	// UpdatedAt is the UTC timestamp of the last metadata change.
	UpdatedAt time.Time
}
