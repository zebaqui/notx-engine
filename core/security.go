package core

import "fmt"

// NoteType classifies a note into one of two distinct data pipelines.
//
// Security in notx is explicit and opt-in at the data level. A note's type is
// set at creation time and is immutable for the lifetime of the note.
//
// The two types are NOT two security levels — they are two entirely different
// pipelines with different storage, transport, sync, and search behaviours.
type NoteType int

const (
	// NoteTypeNormal is the default note type.
	//
	// Normal notes are:
	//   - Synced automatically by the server
	//   - Readable by the server (plaintext stored and indexed server-side)
	//   - Protected in transit by TLS 1.3
	//   - NOT end-to-end encrypted
	//
	// Corresponds to the "normal" value in the .notx file header.
	NoteTypeNormal NoteType = iota

	// NoteTypeSecure is the end-to-end encrypted note type.
	//
	// Secure notes are:
	//   - End-to-end encrypted (AES-256-GCM with per-device key wrapping)
	//   - Never readable by the server — only ciphertext is stored/relayed
	//   - Never automatically synced — sharing is explicit and device-to-device
	//   - Never indexed or searchable server-side
	//   - Decryptable only on devices that hold the corresponding private key
	//
	// Corresponds to the "secure" value in the .notx file header.
	NoteTypeSecure NoteType = iota
)

// String returns the canonical lowercase string representation of the
// NoteType as it appears in a .notx file header.
func (t NoteType) String() string {
	switch t {
	case NoteTypeNormal:
		return "normal"
	case NoteTypeSecure:
		return "secure"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// IsValid reports whether t is one of the defined NoteType constants.
func (t NoteType) IsValid() bool {
	return t == NoteTypeNormal || t == NoteTypeSecure
}

// ParseNoteType parses the string value of the note_type header field.
//
// Accepted values (case-sensitive):
//
//	"normal" → NoteTypeNormal
//	"secure" → NoteTypeSecure
//
// An empty string defaults to NoteTypeNormal for backward compatibility with
// files that pre-date the note_type field.
// Any other value is a parse error.
func ParseNoteType(s string) (NoteType, error) {
	switch s {
	case "", "normal":
		return NoteTypeNormal, nil
	case "secure":
		return NoteTypeSecure, nil
	default:
		return NoteTypeNormal, fmt.Errorf("security: invalid note_type %q: must be \"normal\" or \"secure\"", s)
	}
}

// SyncPolicy describes how a note's events are distributed across devices.
//
// The sync policy is determined solely by the note's NoteType at creation time
// and cannot be changed afterwards.
type SyncPolicy int

const (
	// SyncPolicyAuto is the sync policy for normal notes.
	//
	// The server replicates events to all authorised devices automatically and
	// in near-real-time. The server may read, index, and search the content.
	SyncPolicyAuto SyncPolicy = iota

	// SyncPolicyExplicitRelay is the sync policy for secure notes.
	//
	// The server acts as a relay only: it stores and forwards the encrypted
	// blob without ever decrypting it. Distribution to other devices requires
	// an explicit share action initiated by a device that already holds the
	// Content Encryption Key (CEK).
	SyncPolicyExplicitRelay SyncPolicy = iota
)

// String returns a human-readable label for the SyncPolicy.
func (p SyncPolicy) String() string {
	switch p {
	case SyncPolicyAuto:
		return "auto"
	case SyncPolicyExplicitRelay:
		return "explicit-relay"
	default:
		return fmt.Sprintf("unknown(%d)", int(p))
	}
}

// SecurityPolicy bundles all security-related attributes derived from a note's
// NoteType. It is a read-only value computed once at parse/creation time.
//
// Callers should use NoteSecurityPolicy to obtain the policy for a given type
// rather than constructing SecurityPolicy manually.
type SecurityPolicy struct {
	// NoteType is the immutable classification of the note.
	NoteType NoteType

	// SyncPolicy governs how events are distributed to devices.
	SyncPolicy SyncPolicy

	// IsE2EE reports whether the note's content is end-to-end encrypted.
	// True only for NoteTypeSecure.
	IsE2EE bool

	// ServerCanReadContent reports whether the server has access to the
	// plaintext content of this note's events.
	// True for NoteTypeNormal, false for NoteTypeSecure.
	ServerCanReadContent bool

	// ServerIndexingAllowed reports whether the server may index this note's
	// content for search.
	// True for NoteTypeNormal, false for NoteTypeSecure.
	ServerIndexingAllowed bool
}

// NoteSecurityPolicy returns the immutable SecurityPolicy for the given NoteType.
//
// This is the canonical way to obtain security attributes for a note type.
// The returned value reflects the non-negotiable rules defined in the security
// model: it cannot be overridden per-note or per-request.
func NoteSecurityPolicy(t NoteType) SecurityPolicy {
	switch t {
	case NoteTypeSecure:
		return SecurityPolicy{
			NoteType:              NoteTypeSecure,
			SyncPolicy:            SyncPolicyExplicitRelay,
			IsE2EE:                true,
			ServerCanReadContent:  false,
			ServerIndexingAllowed: false,
		}
	default: // NoteTypeNormal (and any unknown type — fail safe to normal)
		return SecurityPolicy{
			NoteType:              NoteTypeNormal,
			SyncPolicy:            SyncPolicyAuto,
			IsE2EE:                false,
			ServerCanReadContent:  true,
			ServerIndexingAllowed: true,
		}
	}
}
