package core

import "time"

// Project is an index-only grouping entity. Unlike notes, projects have no
// .notx file; they exist solely in the Badger index so they are lightweight
// and fast to enumerate.
type Project struct {
	// URN is the globally unique identifier for this project.
	// Format: <namespace>:proj:<uuid>
	URN URN

	// Name is the human-readable display name (mutable).
	Name string

	// Description is an optional human-readable summary.
	Description string

	// Deleted marks the project as soft-deleted.
	Deleted bool

	// CreatedAt is the UTC timestamp of when the project was first created.
	CreatedAt time.Time

	// UpdatedAt is the UTC timestamp of the last metadata change.
	UpdatedAt time.Time
}

// Folder is an index-only sub-grouping entity that lives inside a Project.
// Notes reference a folder by storing its URN in their FolderURN field.
type Folder struct {
	// URN is the globally unique identifier for this folder.
	// Format: <namespace>:folder:<uuid>
	URN URN

	// ProjectURN is the URN of the owning project. Required; a folder
	// cannot exist without a parent project.
	ProjectURN URN

	// Name is the human-readable display name (mutable).
	Name string

	// Description is an optional human-readable summary.
	Description string

	// Deleted marks the folder as soft-deleted.
	Deleted bool

	// CreatedAt is the UTC timestamp of when the folder was first created.
	CreatedAt time.Time

	// UpdatedAt is the UTC timestamp of the last metadata change.
	UpdatedAt time.Time
}

// Device represents a registered client device in the notx security model.
// Device URNs are used for per-device CEK wrapping in secure notes.
type Device struct {
	// URN is the globally unique device identifier.
	// Format: <namespace>:device:<uuid>
	URN URN

	// Name is a human-readable label for the device (e.g. "MacBook Pro").
	Name string

	// OwnerURN is the URN of the user who owns this device.
	OwnerURN URN

	// PublicKeyB64 is the base64-encoded Ed25519 public key (32 bytes).
	PublicKeyB64 string

	// Revoked marks the device as permanently revoked.
	// A revoked device must be rejected by all layers.
	Revoked bool

	// RegisteredAt is the UTC timestamp of initial registration.
	RegisteredAt time.Time

	// LastSeenAt is the UTC timestamp of the last authenticated request.
	// May be zero if the device has never been used after registration.
	LastSeenAt time.Time
}
