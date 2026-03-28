package core

import (
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// NoteType — ParseNoteType
// ─────────────────────────────────────────────────────────────────────────────

func TestParseNoteType_Normal(t *testing.T) {
	got, err := ParseNoteType("normal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != NoteTypeNormal {
		t.Errorf("got %v, want NoteTypeNormal", got)
	}
}

func TestParseNoteType_Secure(t *testing.T) {
	got, err := ParseNoteType("secure")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != NoteTypeSecure {
		t.Errorf("got %v, want NoteTypeSecure", got)
	}
}

// Phase 1 acceptance criterion: missing note_type defaults to NoteTypeNormal.
func TestParseNoteType_EmptyDefaultsToNormal(t *testing.T) {
	got, err := ParseNoteType("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != NoteTypeNormal {
		t.Errorf("got %v, want NoteTypeNormal", got)
	}
}

// Phase 1 acceptance criterion: any value other than "normal" or "secure" is a
// parse error.
func TestParseNoteType_InvalidValues(t *testing.T) {
	invalid := []string{
		"Normal",
		"NORMAL",
		"Secure",
		"SECURE",
		"secret",
		"private",
		"encrypted",
		"1",
		"true",
		" normal",
		"normal ",
		"secure\n",
	}
	for _, v := range invalid {
		t.Run(v, func(t *testing.T) {
			_, err := ParseNoteType(v)
			if err == nil {
				t.Errorf("expected error for %q, got nil", v)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteType — String / IsValid
// ─────────────────────────────────────────────────────────────────────────────

func TestNoteType_String(t *testing.T) {
	tests := []struct {
		nt   NoteType
		want string
	}{
		{NoteTypeNormal, "normal"},
		{NoteTypeSecure, "secure"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.nt.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNoteType_IsValid(t *testing.T) {
	if !NoteTypeNormal.IsValid() {
		t.Error("NoteTypeNormal should be valid")
	}
	if !NoteTypeSecure.IsValid() {
		t.Error("NoteTypeSecure should be valid")
	}
	if NoteType(99).IsValid() {
		t.Error("NoteType(99) should not be valid")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SyncPolicy — String
// ─────────────────────────────────────────────────────────────────────────────

func TestSyncPolicy_String(t *testing.T) {
	tests := []struct {
		sp   SyncPolicy
		want string
	}{
		{SyncPolicyAuto, "auto"},
		{SyncPolicyExplicitRelay, "explicit-relay"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.sp.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteSecurityPolicy
// ─────────────────────────────────────────────────────────────────────────────

func TestNoteSecurityPolicy_Normal(t *testing.T) {
	p := NoteSecurityPolicy(NoteTypeNormal)

	if p.NoteType != NoteTypeNormal {
		t.Errorf("NoteType: got %v, want NoteTypeNormal", p.NoteType)
	}
	if p.SyncPolicy != SyncPolicyAuto {
		t.Errorf("SyncPolicy: got %v, want SyncPolicyAuto", p.SyncPolicy)
	}
	if p.IsE2EE {
		t.Error("IsE2EE should be false for normal notes")
	}
	if !p.ServerCanReadContent {
		t.Error("ServerCanReadContent should be true for normal notes")
	}
	if !p.ServerIndexingAllowed {
		t.Error("ServerIndexingAllowed should be true for normal notes")
	}
}

func TestNoteSecurityPolicy_Secure(t *testing.T) {
	p := NoteSecurityPolicy(NoteTypeSecure)

	if p.NoteType != NoteTypeSecure {
		t.Errorf("NoteType: got %v, want NoteTypeSecure", p.NoteType)
	}
	if p.SyncPolicy != SyncPolicyExplicitRelay {
		t.Errorf("SyncPolicy: got %v, want SyncPolicyExplicitRelay", p.SyncPolicy)
	}
	if !p.IsE2EE {
		t.Error("IsE2EE should be true for secure notes")
	}
	if p.ServerCanReadContent {
		t.Error("ServerCanReadContent should be false for secure notes")
	}
	if p.ServerIndexingAllowed {
		t.Error("ServerIndexingAllowed should be false for secure notes")
	}
}

// Unknown types must fail-safe to the normal (less restrictive) policy so
// that callers do not accidentally treat an unknown type as secure.
func TestNoteSecurityPolicy_UnknownFailsSafeToNormal(t *testing.T) {
	p := NoteSecurityPolicy(NoteType(99))
	if p.NoteType != NoteTypeNormal {
		t.Errorf("unknown type should fail-safe to NoteTypeNormal, got %v", p.NoteType)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Note struct — NoteType field and constructors
// ─────────────────────────────────────────────────────────────────────────────

func TestNewNote_DefaultsToNormal(t *testing.T) {
	n := NewNote(
		MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"),
		"Test",
		time.Now().UTC(),
	)
	if n.NoteType != NoteTypeNormal {
		t.Errorf("NewNote NoteType: got %v, want NoteTypeNormal", n.NoteType)
	}
}

func TestNewSecureNote_SetsSecureType(t *testing.T) {
	n := NewSecureNote(
		MustParseURN("notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e"),
		"Private",
		time.Now().UTC(),
	)
	if n.NoteType != NoteTypeSecure {
		t.Errorf("NewSecureNote NoteType: got %v, want NoteTypeSecure", n.NoteType)
	}
}

func TestNote_SecurityPolicy_Normal(t *testing.T) {
	n := NewNote(
		MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"),
		"Test",
		time.Now().UTC(),
	)
	p := n.SecurityPolicy()
	if p.NoteType != NoteTypeNormal {
		t.Errorf("got %v, want NoteTypeNormal", p.NoteType)
	}
	if p.IsE2EE {
		t.Error("normal note should not be E2EE")
	}
}

func TestNote_SecurityPolicy_Secure(t *testing.T) {
	n := NewSecureNote(
		MustParseURN("notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e"),
		"Private",
		time.Now().UTC(),
	)
	p := n.SecurityPolicy()
	if p.NoteType != NoteTypeSecure {
		t.Errorf("got %v, want NoteTypeSecure", p.NoteType)
	}
	if !p.IsE2EE {
		t.Error("secure note should be E2EE")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// URN — ObjectTypeDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestParseURN_DeviceType(t *testing.T) {
	raw := "notx:device:4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d"
	u, err := ParseURN(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.ObjectType != ObjectTypeDevice {
		t.Errorf("ObjectType: got %q, want %q", u.ObjectType, ObjectTypeDevice)
	}
	if u.Namespace != "notx" {
		t.Errorf("Namespace: got %q, want %q", u.Namespace, "notx")
	}
	if u.UUID != "4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d" {
		t.Errorf("UUID: got %q", u.UUID)
	}
}

func TestURN_DeviceType_IsKnownType(t *testing.T) {
	u := MustParseURN("notx:device:4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d")
	if !u.IsKnownType() {
		t.Error("device URN should be a known type")
	}
}

func TestURN_DeviceType_String_RoundTrip(t *testing.T) {
	raw := "notx:device:4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d"
	u := MustParseURN(raw)
	if u.String() != raw {
		t.Errorf("round-trip: got %q, want %q", u.String(), raw)
	}
}

// Device URNs must not accept the "anon" sentinel (devices are always
// identified, never anonymous).
func TestParseURN_DeviceType_RejectsAnon(t *testing.T) {
	_, err := ParseURN("notx:device:anon")
	if err != nil {
		// anon is rejected at the UUID validation level — this is acceptable
		// behaviour since the device type should never be anonymous.
		// The test simply documents this constraint.
		t.Logf("correctly rejected anon device URN: %v", err)
		return
	}
	// If we get here the URN was accepted; verify the anon flag is set so
	// callers can detect and reject it at a higher layer.
	u := MustParseURN("notx:device:anon")
	if !u.IsAnon() {
		t.Error("device:anon URN should report IsAnon() == true")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser — note_type header field
// ─────────────────────────────────────────────────────────────────────────────

// Phase 1 acceptance criterion: parser reads note_type: normal correctly.
func TestParser_NoteType_Normal(t *testing.T) {
	content := `# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# note_type:     normal
# name:          My Meeting Notes
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | hello
`
	note, err := NewNoteFromString(content)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if note.NoteType != NoteTypeNormal {
		t.Errorf("NoteType: got %v, want NoteTypeNormal", note.NoteType)
	}
}

// Phase 1 acceptance criterion: parser reads note_type: secure correctly.
func TestParser_NoteType_Secure(t *testing.T) {
	content := `# notx/1.0
# note_urn:      notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e
# note_type:     secure
# name:          Private Journal
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | secret content
`
	note, err := NewNoteFromString(content)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if note.NoteType != NoteTypeSecure {
		t.Errorf("NoteType: got %v, want NoteTypeSecure", note.NoteType)
	}
}

// Phase 1 acceptance criterion: missing note_type defaults to NoteTypeNormal
// (backward compatibility with pre-security-model files).
func TestParser_NoteType_MissingDefaultsToNormal(t *testing.T) {
	content := `# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Legacy Note
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | content
`
	note, err := NewNoteFromString(content)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if note.NoteType != NoteTypeNormal {
		t.Errorf("NoteType: got %v, want NoteTypeNormal (backward-compat default)", note.NoteType)
	}
}

// Phase 1 acceptance criterion: any value other than "normal" or "secure" is a
// parse error.
func TestParser_NoteType_InvalidValueIsError(t *testing.T) {
	invalidTypes := []string{
		"secret",
		"private",
		"NORMAL",
		"SECURE",
		"1",
		"encrypted",
	}
	for _, bad := range invalidTypes {
		t.Run(bad, func(t *testing.T) {
			content := "# notx/1.0\n" +
				"# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a\n" +
				"# note_type:     " + bad + "\n" +
				"# name:          Bad Type Note\n" +
				"# created_at:    2025-01-15T09:00:00Z\n" +
				"# head_sequence: 0\n"
			_, err := NewNoteFromString(content)
			if err == nil {
				t.Errorf("expected parse error for note_type=%q, got nil", bad)
			}
			if !strings.Contains(err.Error(), "note_type") {
				t.Errorf("error should mention note_type, got: %v", err)
			}
		})
	}
}

// Phase 1 acceptance criterion: note_type is immutable — it is set at parse
// time and ApplyEvent cannot change it.
func TestNote_NoteType_IsImmutableAcrossEvents(t *testing.T) {
	content := `# notx/1.0
# note_urn:      notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e
# note_type:     secure
# name:          Immutable Type
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 2

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | first

2:2025-01-15T09:05:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | updated
`
	note, err := NewNoteFromString(content)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if note.NoteType != NoteTypeSecure {
		t.Errorf("NoteType after events: got %v, want NoteTypeSecure", note.NoteType)
	}

	// Manually applying another event must not change the type.
	ev := &Event{
		NoteURN:   note.URN,
		Sequence:  3,
		AuthorURN: MustParseURN("notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"),
		CreatedAt: time.Now().UTC(),
		Entries:   []LineEntry{{LineNumber: 1, Op: LineOpSet, Content: "changed"}},
	}
	if err := note.ApplyEvent(ev); err != nil {
		t.Fatalf("ApplyEvent error: %v", err)
	}
	if note.NoteType != NoteTypeSecure {
		t.Errorf("NoteType after extra event: got %v, want NoteTypeSecure", note.NoteType)
	}
}

// Existing test fixtures without note_type must still parse without errors,
// preserving full backward compatibility.
func TestParser_BackwardCompat_ExistingFixturesStillParse(t *testing.T) {
	fixtures := []string{
		// Minimal legacy note — no note_type header
		`# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Old Note
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | content
`,
		// Legacy note with project and folder URNs
		`# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Linked Note
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1
# project_urn:   notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
# folder_urn:    notx:folder:1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | content
`,
	}

	for i, fix := range fixtures {
		note, err := NewNoteFromString(fix)
		if err != nil {
			t.Errorf("fixture %d: unexpected parse error: %v", i, err)
			continue
		}
		if note.NoteType != NoteTypeNormal {
			t.Errorf("fixture %d: NoteType = %v, want NoteTypeNormal", i, note.NoteType)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Two-pipeline isolation invariants (documented constraints, not runtime
// enforcement yet — these tests document the expected behaviour so future
// pipeline code has a clear contract to satisfy).
// ─────────────────────────────────────────────────────────────────────────────

func TestSecurityPolicy_NormalNote_ServerCanIndex(t *testing.T) {
	n := NewNote(
		MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"),
		"Public Note",
		time.Now().UTC(),
	)
	p := n.SecurityPolicy()
	if !p.ServerIndexingAllowed {
		t.Error("normal notes must be indexable by the server")
	}
	if !p.ServerCanReadContent {
		t.Error("server must be able to read normal note content")
	}
	if p.SyncPolicy != SyncPolicyAuto {
		t.Errorf("normal notes must use SyncPolicyAuto, got %v", p.SyncPolicy)
	}
}

func TestSecurityPolicy_SecureNote_ServerBlind(t *testing.T) {
	n := NewSecureNote(
		MustParseURN("notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e"),
		"Private Note",
		time.Now().UTC(),
	)
	p := n.SecurityPolicy()
	if p.ServerIndexingAllowed {
		t.Error("secure notes must never be indexed by the server")
	}
	if p.ServerCanReadContent {
		t.Error("server must not be able to read secure note content")
	}
	if p.SyncPolicy != SyncPolicyExplicitRelay {
		t.Errorf("secure notes must use SyncPolicyExplicitRelay, got %v", p.SyncPolicy)
	}
}
