package core

import (
	"fmt"
	"regexp"
	"strings"
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
)

// sentinelAnon is the special UUID value for anonymous/unknown authors.
const sentinelAnon = "anon"

var (
	// namespaceRe validates namespace segments: lowercase alphanumeric + hyphen,
	// no leading or trailing hyphens, 1–63 characters total.
	namespaceRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

	// uuidRe validates a standard UUID v4/v7 hex string.
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// knownTypes is the set of defined object types for strict validation.
	knownTypes = map[ObjectType]struct{}{
		ObjectTypeNote:    {},
		ObjectTypeEvent:   {},
		ObjectTypeUser:    {},
		ObjectTypeOrg:     {},
		ObjectTypeProject: {},
		ObjectTypeFolder:  {},
	}
)

// URN represents a notx Uniform Resource Name of the form:
//
//	<namespace>:<object-type>:<uuid>
//
// The uuid segment may also be the sentinel value "anon" for anonymous users.
type URN struct {
	Namespace  string
	ObjectType ObjectType
	UUID       string
}

// String returns the canonical string representation of the URN.
func (u URN) String() string {
	return u.Namespace + ":" + string(u.ObjectType) + ":" + u.UUID
}

// IsAnon reports whether this URN refers to the anonymous-author sentinel.
func (u URN) IsAnon() bool {
	return u.UUID == sentinelAnon
}

// IsKnownType reports whether the URN's ObjectType is one of the defined types.
func (u URN) IsKnownType() bool {
	_, ok := knownTypes[u.ObjectType]
	return ok
}

// Equal reports whether two URNs are identical (case-sensitive comparison).
func (u URN) Equal(other URN) bool {
	return u.Namespace == other.Namespace &&
		u.ObjectType == other.ObjectType &&
		u.UUID == other.UUID
}

// ParseURN parses a raw URN string into a URN value and validates it.
//
// The expected format is:
//
//	<namespace>:<object-type>:<uuid-or-anon>
//
// The uuid segment may contain hyphens; everything after the second colon is
// treated as the UUID (to accommodate the standard UUID format).
func ParseURN(raw string) (URN, error) {
	if len(raw) > 256 {
		return URN{}, fmt.Errorf("urn: too long (max 256 characters, got %d)", len(raw))
	}

	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 {
		return URN{}, fmt.Errorf("urn: invalid format %q: expected <namespace>:<type>:<uuid>", raw)
	}

	namespace := parts[0]
	objType := ObjectType(parts[1])
	uuid := parts[2]

	if !namespaceRe.MatchString(namespace) {
		return URN{}, fmt.Errorf("urn: invalid namespace %q: must match [a-z0-9][a-z0-9-]*[a-z0-9]", namespace)
	}

	if uuid != sentinelAnon && !uuidRe.MatchString(uuid) {
		return URN{}, fmt.Errorf("urn: invalid uuid %q: must be a standard UUID or %q", uuid, sentinelAnon)
	}

	return URN{
		Namespace:  namespace,
		ObjectType: objType,
		UUID:       uuid,
	}, nil
}

// MustParseURN parses a raw URN string and panics if it is invalid.
// Intended for use in tests and compile-time constants.
func MustParseURN(raw string) URN {
	u, err := ParseURN(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// AnonURN returns the instance-specific anonymous-author sentinel URN for the
// given namespace (e.g. "notx:usr:anon", "acme:usr:anon").
func AnonURN(namespace string) URN {
	return URN{
		Namespace:  namespace,
		ObjectType: ObjectTypeUser,
		UUID:       sentinelAnon,
	}
}
