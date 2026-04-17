package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PairingInfo is returned by GET /api/engine/pairing-info on the cloud.
type PairingInfo struct {
	BootstrapAddr string `json:"bootstrap_addr"`
	PrimaryAddr   string `json:"primary_addr"`
	CAFingerprint string `json:"ca_fingerprint"`
}

// CloudPairingSecretResponse is returned by POST /api/engine/pairing-secrets.
type CloudPairingSecretResponse struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Plaintext string    `json:"plaintext"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetPairingInfo fetches GET /api/engine/pairing-info (no auth needed).
func GetPairingInfo() (*PairingInfo, error) {
	url := CloudBaseURL() + "/api/engine/pairing-info"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cloud: build pairing-info request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloud: pairing-info request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cloud: read pairing-info response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("cloud: pairing-info returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var info PairingInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("cloud: parse pairing-info response: %w", err)
	}

	return &info, nil
}

// CreatePairingSecret calls POST /api/engine/pairing-secrets with the JWT token.
// label is a human-readable hint (e.g. "auto-pair").
func CreatePairingSecret(token, label string) (*CloudPairingSecretResponse, error) {
	url := CloudBaseURL() + "/api/engine/pairing-secrets"

	payload, err := json.Marshal(struct {
		Label string `json:"label"`
	}{Label: label})
	if err != nil {
		return nil, fmt.Errorf("cloud: marshal pairing-secret request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("cloud: build pairing-secret request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloud: pairing-secret request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cloud: read pairing-secret response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("cloud: pairing-secrets returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result CloudPairingSecretResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("cloud: parse pairing-secret response: %w", err)
	}

	return &result, nil
}
