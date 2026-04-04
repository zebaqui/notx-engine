package core

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ObjectType represents the type of entity a URN identifies.
type ObjectType string

const (
	ObjectTypeNote    ObjectType = "note"
	ObjectTypeEvent   ObjectType = "event"
	ObjectTypeUser    ObjectType = "usr"
	ObjectTypeOrg     ObjectType = "org"
	ObjectTypeProject ObjectType = "proj"
	ObjectTypeFolder  ObjectType = "folder"
	// ObjectTypeDevice identifies a registered user device in the notx
	// security model. Device URNs are used exclusively for cryptographic
	// identity — specifically for per-device CEK wrapping in secure notes.
	//
	// Format: urn:notx:device:<uuidv7>
	// Example: urn:notx:device:019063a5-1f67-7a42-afd3-5543f01e93c3
	ObjectTypeDevice ObjectType = "device"
	// ObjectTypeServer identifies a trusted peer notx server instance that
	// has been paired with this authority via the ServerPairingService.
	//
	// Format: urn:notx:srv:<uuidv7>
	// Example: urn:notx:srv:019063a5-2b34-7c81-bfe2-1a2b3c4d5e6f
	ObjectTypeServer ObjectType = "srv"
)

// sentinelAnon is the special ID value for the anonymous/unknown author sentinel.
const sentinelAnon = "anon"

// urnPrefix is the fixed scheme prefix for all notx URNs.
const urnPrefix = "urn:notx:"

var (
	// uuidv7Re validates a UUIDv7 string (standard hyphenated UUID format).
	// UUIDv7 uses the same wire format as other UUID versions:
	//   xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx
	// where the version nibble is 7 and the variant bits are set in y.
	// We accept any standard UUID format here and validate the version byte
	// separately via TimeFromV7 when timestamp extraction is needed.
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// knownTypes is the set of defined object types for strict validation.
	knownTypes = map[ObjectType]struct{}{
		ObjectTypeNote:    {},
		ObjectTypeEvent:   {},
		ObjectTypeUser:    {},
		ObjectTypeOrg:     {},
		ObjectTypeProject: {},
		ObjectTypeFolder:  {},
		ObjectTypeDevice:  {},
		ObjectTypeServer:  {},
	}
)

// URN represents a notx Uniform Resource Name of the form:
//
//	urn:notx:<object-type>:<uuidv7>
//
// Identity is globally unique and immutable. The ID segment is a UUIDv7,
// whose embedded millisecond timestamp makes it time-ordered and
// self-describing. The anonymous-author sentinel uses the special ID "anon"
// instead of a UUID.
//
// Namespace is NOT part of the URN — it is separate metadata stored on the
// object (e.g. "namespace": "acme"). Authority is encoded as a server URN
// (urn:notx:srv:<uuidv7>) stored as a field on each object.
type URN struct {
	ObjectType ObjectType
	ID         string // UUIDv7 (hyphenated lowercase) or "anon" sentinel
}

// String returns the canonical string representation of the URN.
//
//	urn:notx:<object-type>:<id>
func (u URN) String() string {
	return urnPrefix + string(u.ObjectType) + ":" + u.ID
}

// IsAnon reports whether this URN refers to the anonymous-author sentinel.
func (u URN) IsAnon() bool {
	return u.ID == sentinelAnon
}

// IsKnownType reports whether the URN's ObjectType is one of the defined types.
func (u URN) IsKnownType() bool {
	_, ok := knownTypes[u.ObjectType]
	return ok
}

// Equal reports whether two URNs are identical.
func (u URN) Equal(other URN) bool {
	return u.ObjectType == other.ObjectType && u.ID == other.ID
}

// Time returns the timestamp embedded in the URN's UUIDv7 ID.
// Returns the zero time and false for the anon sentinel or any non-v7 UUID.
func (u URN) Time() (time.Time, bool) {
	if u.IsAnon() {
		return time.Time{}, false
	}
	parsed, err := uuid.Parse(u.ID)
	if err != nil || parsed.Version() != 7 {
		return time.Time{}, false
	}
	// UUIDv7 stores Unix epoch milliseconds in the top 48 bits.
	// Extract: bytes 0-5 are the 48-bit big-endian millisecond timestamp.
	b := parsed[:]
	ms := int64(b[0])<<40 | int64(b[1])<<32 | int64(b[2])<<24 |
		int64(b[3])<<16 | int64(b[4])<<8 | int64(b[5])
	return time.UnixMilli(ms).UTC(), true
}

// NewURN generates a new URN with a freshly minted UUIDv7 identifier.
// UUIDv7 embeds the current Unix millisecond timestamp, making the resulting
// URN time-ordered and allowing timestamp inference without a separate field.
//
// Panics if the system random source is unavailable (extremely rare).
func NewURN(objType ObjectType) URN {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("urn: failed to generate UUIDv7: %v", err))
	}
	return URN{ObjectType: objType, ID: id.String()}
}

// ParseURN parses a raw URN string into a URN value and validates it.
//
// The expected format is:
//
//	urn:notx:<object-type>:<uuidv7-or-anon>
//
// The ID segment must be a valid lowercase hyphenated UUID (any version is
// accepted syntactically; callers that require v7 should check URN.Time())
// or the sentinel value "anon".
//
// Unknown object types are accepted for forward compatibility — only the
// format of the ID segment is validated, not whether the type is in the
// current knownTypes set.
func ParseURN(raw string) (URN, error) {
	if len(raw) > 256 {
		return URN{}, fmt.Errorf("urn: too long (max 256 characters, got %d)", len(raw))
	}

	if !strings.HasPrefix(raw, urnPrefix) {
		return URN{}, fmt.Errorf("urn: invalid format %q: must start with %q", raw, urnPrefix)
	}

	rest := raw[len(urnPrefix):]

	// rest is now "<object-type>:<id>"
	colonIdx := strings.IndexByte(rest, ':')
	if colonIdx < 0 {
		return URN{}, fmt.Errorf("urn: invalid format %q: missing <type>:<id> separator", raw)
	}

	objType := ObjectType(rest[:colonIdx])
	id := rest[colonIdx+1:]

	if len(objType) == 0 {
		return URN{}, fmt.Errorf("urn: empty object type in %q", raw)
	}

	if id != sentinelAnon && !uuidRe.MatchString(id) {
		return URN{}, fmt.Errorf(
			"urn: invalid id %q in %q: must be a lowercase hyphenated UUID or %q",
			id, raw, sentinelAnon,
		)
	}

	return URN{ObjectType: objType, ID: id}, nil
}

// MustParseURN parses a raw URN string and panics if it is invalid.
// Intended for use in tests and compile-time constants only.
func MustParseURN(raw string) URN {
	u, err := ParseURN(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// AnonURN returns the global anonymous-author sentinel URN.
//
//	urn:notx:usr:anon
//
// This is a single, namespace-independent sentinel. Any unauthenticated edit
// or edit with an unknown author is attributed to this URN regardless of which
// server or namespace recorded it.
func AnonURN() URN {
	return URN{ObjectType: ObjectTypeUser, ID: sentinelAnon}
}
