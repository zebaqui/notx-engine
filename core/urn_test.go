package core

import (
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// ParseURN
// ──────────────────────────────────────────────────────────────────────────────

func TestParseURN_ValidOfficialPlatform(t *testing.T) {
	raw := "urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
	u, err := ParseURN(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.ObjectType != ObjectTypeNote {
		t.Errorf("ObjectType: got %q, want %q", u.ObjectType, ObjectTypeNote)
	}
	if u.ID != "018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a" {
		t.Errorf("ID: got %q", u.ID)
	}
}

func TestParseURN_ValidAllObjectTypes(t *testing.T) {
	uuid := "3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
	cases := []struct {
		raw        string
		objectType ObjectType
	}{
		{"urn:notx:note:" + uuid, ObjectTypeNote},
		{"urn:notx:event:" + uuid, ObjectTypeEvent},
		{"urn:notx:usr:" + uuid, ObjectTypeUser},
		{"urn:notx:org:" + uuid, ObjectTypeOrg},
		{"urn:notx:proj:" + uuid, ObjectTypeProject},
		{"urn:notx:folder:" + uuid, ObjectTypeFolder},
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
	raw := "urn:notx:usr:anon"
	u, err := ParseURN(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !u.IsAnon() {
		t.Errorf("expected IsAnon() == true for %q", raw)
	}
}

func TestParseURN_UnknownObjectTypeAllowed(t *testing.T) {
	// Forward-compatibility: unknown object types must not cause parse errors.
	raw := "urn:notx:widget:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"
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
		"urn:notx:note",
		"urn:notx:",
		"notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a", // missing urn: prefix
		"acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b",  // old namespace-based format
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

func TestParseURN_InvalidID(t *testing.T) {
	cases := []string{
		"urn:notx:note:not-a-uuid",
		"urn:notx:note:XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX",
		"urn:notx:note:00000000000000000000000000000000", // missing hyphens
		"urn:notx:note:", // empty id
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseURN(raw)
			if err == nil {
				t.Errorf("expected error for invalid ID in %q", raw)
			}
		})
	}
}

func TestParseURN_TooLong(t *testing.T) {
	// Build a URN that exceeds 256 characters.
	raw := "urn:notx:note:" + strings.Repeat("a", 250)
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
	u := MustParseURN("urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	if u.ObjectType != ObjectTypeNote {
		t.Errorf("ObjectType: got %q, want %q", u.ObjectType, ObjectTypeNote)
	}
	if u.ID != "018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a" {
		t.Errorf("ID: got %q", u.ID)
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
		"urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a",
		"urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b",
		"urn:notx:usr:anon",
		"urn:notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d",
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
	a := MustParseURN("urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	b := MustParseURN("urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")

	if !a.Equal(b) {
		t.Error("expected a == b")
	}
}

func TestURN_Equal_DifferentType(t *testing.T) {
	uuid := "018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
	note := MustParseURN("urn:notx:note:" + uuid)
	event := MustParseURN("urn:notx:event:" + uuid)

	if note.Equal(event) {
		t.Error("expected note != event (different object type)")
	}
}

func TestURN_Equal_DifferentID(t *testing.T) {
	a := MustParseURN("urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	b := MustParseURN("urn:notx:note:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b")

	if a.Equal(b) {
		t.Error("expected a != b (different ID)")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// URN.IsAnon()
// ──────────────────────────────────────────────────────────────────────────────

func TestURN_IsAnon(t *testing.T) {
	anon := MustParseURN("urn:notx:usr:anon")
	if !anon.IsAnon() {
		t.Error("expected IsAnon() == true for urn:notx:usr:anon")
	}

	real := MustParseURN("urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b")
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
		"urn:notx:note:" + uuid,
		"urn:notx:event:" + uuid,
		"urn:notx:usr:" + uuid,
		"urn:notx:org:" + uuid,
		"urn:notx:proj:" + uuid,
		"urn:notx:folder:" + uuid,
	}
	for _, raw := range knownRaws {
		t.Run(raw, func(t *testing.T) {
			u := MustParseURN(raw)
			if !u.IsKnownType() {
				t.Errorf("expected IsKnownType() == true for %q", raw)
			}
		})
	}

	unknown := MustParseURN("urn:notx:widget:" + uuid)
	if unknown.IsKnownType() {
		t.Errorf("expected IsKnownType() == false for unknown type")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AnonURN()
// ──────────────────────────────────────────────────────────────────────────────

func TestAnonURN(t *testing.T) {
	u := AnonURN()
	want := "urn:notx:usr:anon"
	if u.String() != want {
		t.Errorf("AnonURN().String() = %q, want %q", u.String(), want)
	}
	if u.ObjectType != ObjectTypeUser {
		t.Errorf("AnonURN ObjectType: got %q, want %q", u.ObjectType, ObjectTypeUser)
	}
	if !u.IsAnon() {
		t.Errorf("AnonURN().IsAnon() should be true")
	}
}
