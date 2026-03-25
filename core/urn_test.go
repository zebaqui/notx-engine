package core

import (
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// ParseURN
// ──────────────────────────────────────────────────────────────────────────────

func TestParseURN_ValidOfficialPlatform(t *testing.T) {
	raw := "notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
	u, err := ParseURN(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Namespace != "notx" {
		t.Errorf("Namespace: got %q, want %q", u.Namespace, "notx")
	}
	if u.ObjectType != ObjectTypeNote {
		t.Errorf("ObjectType: got %q, want %q", u.ObjectType, ObjectTypeNote)
	}
	if u.UUID != "018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a" {
		t.Errorf("UUID: got %q", u.UUID)
	}
}

func TestParseURN_ValidSelfHosted(t *testing.T) {
	raw := "acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
	u, err := ParseURN(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Namespace != "acme" {
		t.Errorf("Namespace: got %q, want %q", u.Namespace, "acme")
	}
	if u.ObjectType != ObjectTypeUser {
		t.Errorf("ObjectType: got %q, want %q", u.ObjectType, ObjectTypeUser)
	}
}

func TestParseURN_ValidAllObjectTypes(t *testing.T) {
	uuid := "3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
	cases := []struct {
		raw        string
		objectType ObjectType
	}{
		{"notx:note:" + uuid, ObjectTypeNote},
		{"notx:event:" + uuid, ObjectTypeEvent},
		{"notx:usr:" + uuid, ObjectTypeUser},
		{"notx:org:" + uuid, ObjectTypeOrg},
		{"notx:proj:" + uuid, ObjectTypeProject},
		{"notx:folder:" + uuid, ObjectTypeFolder},
	}

	for _, tc := range cases {
		t.Run(string(tc.objectType), func(t *testing.T) {
			u, err := ParseURN(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if u.ObjectType != tc.objectType {
				t.Errorf("ObjectType: got %q, want %q", u.ObjectType, tc.objectType)
			}
		})
	}
}

func TestParseURN_AnonSentinel(t *testing.T) {
	cases := []string{
		"notx:usr:anon",
		"acme:usr:anon",
		"mycompany:usr:anon",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			u, err := ParseURN(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !u.IsAnon() {
				t.Errorf("expected IsAnon() == true for %q", raw)
			}
		})
	}
}

func TestParseURN_NamespaceVariants(t *testing.T) {
	uuid := "3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
	valid := []string{
		"a:note:" + uuid,          // single character
		"acme:note:" + uuid,       // common short name
		"my-company:note:" + uuid, // hyphen in middle
		"abc123:note:" + uuid,     // alphanumeric mix
	}
	for _, raw := range valid {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseURN(raw); err != nil {
				t.Errorf("unexpected error for valid namespace: %v", err)
			}
		})
	}
}

func TestParseURN_UnknownObjectTypeAllowed(t *testing.T) {
	// Forward-compatibility: unknown object types must not cause parse errors.
	raw := "notx:widget:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
	u, err := ParseURN(raw)
	if err != nil {
		t.Fatalf("unexpected error for unknown object type: %v", err)
	}
	if u.IsKnownType() {
		t.Errorf("expected IsKnownType() == false for unknown type %q", u.ObjectType)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ParseURN — invalid inputs
// ──────────────────────────────────────────────────────────────────────────────

func TestParseURN_MissingSegments(t *testing.T) {
	invalid := []string{
		"",
		"notx",
		"notx:note",
		":",
		"::",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseURN(raw)
			if err == nil {
				t.Errorf("expected error for %q, got nil", raw)
			}
		})
	}
}

func TestParseURN_InvalidNamespace(t *testing.T) {
	uuid := "3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
	cases := []string{
		"-acme:note:" + uuid,   // leading hyphen
		"acme-:note:" + uuid,   // trailing hyphen
		"ACME:note:" + uuid,    // uppercase
		"acme_co:note:" + uuid, // underscore not allowed
		"acme.co:note:" + uuid, // dot not allowed
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseURN(raw)
			if err == nil {
				t.Errorf("expected error for invalid namespace in %q", raw)
			}
		})
	}
}

func TestParseURN_InvalidUUID(t *testing.T) {
	cases := []string{
		"notx:note:not-a-uuid",
		"notx:note:XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX",
		"notx:note:00000000000000000000000000000000", // missing hyphens
		"notx:note:", // empty uuid
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseURN(raw)
			if err == nil {
				t.Errorf("expected error for invalid UUID in %q", raw)
			}
		})
	}
}

func TestParseURN_TooLong(t *testing.T) {
	// Build a URN that exceeds 256 characters.
	raw := "notx:note:" + strings.Repeat("a", 250)
	_, err := ParseURN(raw)
	if err == nil {
		t.Errorf("expected error for URN exceeding 256 characters")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// MustParseURN
// ──────────────────────────────────────────────────────────────────────────────

func TestMustParseURN_Valid(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	u := MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	if u.Namespace != "notx" {
		t.Errorf("Namespace: got %q", u.Namespace)
	}
}

func TestMustParseURN_InvalidPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid URN, got nil")
		}
	}()
	MustParseURN("this-is-not-valid")
}

// ──────────────────────────────────────────────────────────────────────────────
// URN.String()
// ──────────────────────────────────────────────────────────────────────────────

func TestURN_String_RoundTrip(t *testing.T) {
	cases := []string{
		"notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a",
		"acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b",
		"notx:usr:anon",
		"mycompany:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			u, err := ParseURN(raw)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if got := u.String(); got != raw {
				t.Errorf("String(): got %q, want %q", got, raw)
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// URN.Equal()
// ──────────────────────────────────────────────────────────────────────────────

func TestURN_Equal(t *testing.T) {
	a := MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	b := MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	c := MustParseURN("acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")

	if !a.Equal(b) {
		t.Error("expected a == b")
	}
	if a.Equal(c) {
		t.Error("expected a != c (different namespace)")
	}
}

func TestURN_Equal_DifferentType(t *testing.T) {
	uuid := "018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
	note := MustParseURN("notx:note:" + uuid)
	event := MustParseURN("notx:event:" + uuid)

	if note.Equal(event) {
		t.Error("expected note != event (different object type)")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// URN.IsAnon()
// ──────────────────────────────────────────────────────────────────────────────

func TestURN_IsAnon(t *testing.T) {
	anon := MustParseURN("notx:usr:anon")
	if !anon.IsAnon() {
		t.Error("expected IsAnon() == true for notx:usr:anon")
	}

	real := MustParseURN("notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b")
	if real.IsAnon() {
		t.Error("expected IsAnon() == false for real user URN")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// URN.IsKnownType()
// ──────────────────────────────────────────────────────────────────────────────

func TestURN_IsKnownType(t *testing.T) {
	uuid := "3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
	knownRaws := []string{
		"notx:note:" + uuid,
		"notx:event:" + uuid,
		"notx:usr:" + uuid,
		"notx:org:" + uuid,
		"notx:proj:" + uuid,
		"notx:folder:" + uuid,
	}
	for _, raw := range knownRaws {
		t.Run(raw, func(t *testing.T) {
			u := MustParseURN(raw)
			if !u.IsKnownType() {
				t.Errorf("expected IsKnownType() == true for %q", raw)
			}
		})
	}

	unknown := MustParseURN("notx:widget:" + uuid)
	if unknown.IsKnownType() {
		t.Errorf("expected IsKnownType() == false for unknown type")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AnonURN()
// ──────────────────────────────────────────────────────────────────────────────

func TestAnonURN(t *testing.T) {
	cases := []struct {
		namespace string
		want      string
	}{
		{"notx", "notx:usr:anon"},
		{"acme", "acme:usr:anon"},
		{"mycompany", "mycompany:usr:anon"},
	}
	for _, tc := range cases {
		t.Run(tc.namespace, func(t *testing.T) {
			u := AnonURN(tc.namespace)
			if u.String() != tc.want {
				t.Errorf("AnonURN(%q).String() = %q, want %q", tc.namespace, u.String(), tc.want)
			}
			if u.ObjectType != ObjectTypeUser {
				t.Errorf("AnonURN ObjectType: got %q, want %q", u.ObjectType, ObjectTypeUser)
			}
			if !u.IsAnon() {
				t.Errorf("AnonURN(%q).IsAnon() should be true", tc.namespace)
			}
		})
	}
}
