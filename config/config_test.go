package config

import (
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Default() field tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDefault_HTTPPort(t *testing.T) {
	cfg := Default()
	if cfg.HTTPPort != 7430 {
		t.Errorf("HTTPPort: got %d, want 7430", cfg.HTTPPort)
	}
}

func TestDefault_Host(t *testing.T) {
	cfg := Default()
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host: got %q, want %q", cfg.Host, "127.0.0.1")
	}
}

func TestDefault_AICredentials(t *testing.T) {
	cfg := Default()
	if cfg.AICredentials.KeySource != "passphrase" {
		t.Errorf("AICredentials.KeySource: got %q, want %q", cfg.AICredentials.KeySource, "passphrase")
	}
}

func TestDefault_HTTPEnabled(t *testing.T) {
	cfg := Default()
	if !cfg.EnableHTTP {
		t.Error("EnableHTTP: got false, want true")
	}
}

func TestDefault_GRPCEnabled(t *testing.T) {
	cfg := Default()
	if !cfg.EnableGRPC {
		t.Error("EnableGRPC: got false, want true")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Validate() tests
// ─────────────────────────────────────────────────────────────────────────────

func TestValidate_OK(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() on Default() returned unexpected error: %v", err)
	}
}

func TestValidate_NoServers(t *testing.T) {
	cfg := Default()
	cfg.EnableHTTP = false
	cfg.EnableGRPC = false
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error when both EnableHTTP and EnableGRPC are false, got nil")
	}
}

func TestValidate_SamePorts(t *testing.T) {
	cfg := Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = true
	cfg.GRPCPort = cfg.HTTPPort // collide
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error when HTTPPort == GRPCPort with both servers enabled, got nil")
	}
}

func TestValidate_EmptyDataDir(t *testing.T) {
	cfg := Default()
	cfg.DataDir = ""
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for empty DataDir, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Address helpers
// ─────────────────────────────────────────────────────────────────────────────

func TestHTTPAddr(t *testing.T) {
	cfg := Default()
	want := "127.0.0.1:7430"
	if got := cfg.HTTPAddr(); got != want {
		t.Errorf("HTTPAddr(): got %q, want %q", got, want)
	}
}

func TestGRPCAddr(t *testing.T) {
	cfg := Default()
	want := "127.0.0.1:50051"
	if got := cfg.GRPCAddr(); got != want {
		t.Errorf("GRPCAddr(): got %q, want %q", got, want)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Mode() tests
// ─────────────────────────────────────────────────────────────────────────────

func TestMode_BothEnabled(t *testing.T) {
	cfg := Default() // EnableHTTP=true, EnableGRPC=true
	if got := cfg.Mode(); got != ModeBoth {
		t.Errorf("Mode(): got %v, want ModeBoth (%v)", got, ModeBoth)
	}
}

func TestMode_HTTPOnly(t *testing.T) {
	cfg := Default()
	cfg.EnableGRPC = false
	if got := cfg.Mode(); got != ModeHTTP {
		t.Errorf("Mode(): got %v, want ModeHTTP (%v)", got, ModeHTTP)
	}
}
