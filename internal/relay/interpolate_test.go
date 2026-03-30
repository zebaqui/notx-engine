package relay

import (
	"testing"
)

func TestInterpolate_NoPlaceholders(t *testing.T) {
	got := Interpolate("hello world", nil)
	if got != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", got)
	}
}

func TestInterpolate_SingleVar(t *testing.T) {
	vars := map[string]string{"name": "Alice"}
	got := Interpolate("Hello, {{name}}!", vars)
	want := "Hello, Alice!"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestInterpolate_MultipleVars(t *testing.T) {
	vars := map[string]string{
		"host":  "api.example.com",
		"token": "secret-abc",
	}
	got := Interpolate("https://{{host}}/auth?token={{token}}", vars)
	want := "https://api.example.com/auth?token=secret-abc"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestInterpolate_UnknownVarPreserved(t *testing.T) {
	vars := map[string]string{"known": "yes"}
	got := Interpolate("{{known}}-{{unknown}}", vars)
	want := "yes-{{unknown}}"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestInterpolate_WhitespaceAroundVar(t *testing.T) {
	vars := map[string]string{"x": "42"}
	got := Interpolate("value={{ x }}", vars)
	want := "value=42"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestInterpolate_UnmatchedBrace(t *testing.T) {
	got := Interpolate("{{open", nil)
	// Should contain the literal "{{" and the rest of the string
	if got != "{{open" {
		t.Errorf("expected %q, got %q", "{{open", got)
	}
}

func TestInterpolateBytes_EmptyBody(t *testing.T) {
	got := InterpolateBytes(nil, nil)
	if got != nil {
		t.Errorf("expected nil for empty body")
	}
}

func TestInterpolateBytes_JSONBody(t *testing.T) {
	vars := map[string]string{"user": "bob", "pass": "hunter2"}
	body := []byte(`{"username":"{{user}}","password":"{{pass}}"}`)
	got := InterpolateBytes(body, vars)
	want := `{"username":"bob","password":"hunter2"}`
	if string(got) != want {
		t.Errorf("expected %q, got %q", want, string(got))
	}
}
