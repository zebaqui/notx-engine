package http

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// Servers (pairing management) — route dispatchers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routePairingSecrets(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil || h.secretStore == nil {
		writeError(w, http.StatusServiceUnavailable, "server pairing is not enabled")
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.handleCreatePairingSecret(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeServersCA(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		writeError(w, http.StatusServiceUnavailable, "server pairing is not enabled")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleGetCACertificate(w, r)
}

func (h *Handler) routeServers(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		writeError(w, http.StatusServiceUnavailable, "server pairing is not enabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleListServers(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeServer(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		writeError(w, http.StatusServiceUnavailable, "server pairing is not enabled")
		return
	}
	urn := strings.TrimPrefix(r.URL.Path, "/v1/servers/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "missing server URN")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.handleRevokeServer(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeServerInfo(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		writeJSON(w, http.StatusOK, serverInfoResponse{})
		return
	}
	writeJSON(w, http.StatusOK, serverInfoResponse{ServerURN: h.pairing.URN()})
}

func (h *Handler) routeOutboundPair(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		writeError(w, http.StatusServiceUnavailable, "server pairing is not enabled")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleOutboundPair(w, r)
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — servers/pairing
// ─────────────────────────────────────────────────────────────────────────────

type createPairingSecretRequest struct {
	Label string `json:"label"`
}

type createPairingSecretResponse struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Plaintext string    `json:"plaintext"`
	ExpiresAt time.Time `json:"expires_at"`
}

type serverInfoJSON struct {
	URN          string     `json:"urn"`
	Name         string     `json:"name"`
	Endpoint     string     `json:"endpoint"`
	Revoked      bool       `json:"revoked"`
	RegisteredAt time.Time  `json:"registered_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
}

type listServersResponse struct {
	Servers []serverInfoJSON `json:"servers"`
}

type caCertificateResponse struct {
	CACertificate string `json:"ca_certificate"`
	Fingerprint   string `json:"fingerprint,omitempty"`
}

type serverInfoResponse struct {
	ServerURN string `json:"server_urn,omitempty"`
}

type outboundPairRequest struct {
	URL    string `json:"url"`    // e.g. "remote-host:50052"
	Secret string `json:"secret"` // NTXP-... pairing secret
}

type outboundPairResponse struct {
	ServerURN string    `json:"server_urn"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/pairing-secrets
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleCreatePairingSecret(w http.ResponseWriter, r *http.Request) {
	var req createPairingSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Label == "" {
		req.Label = "admin-generated"
	}

	resp, err := h.pairing.CreatePairingSecret(r.Context(), &pb.CreatePairingSecretRequest{
		Label: req.Label,
	})
	if err != nil {
		grpcErrToHTTP(w, r, h, err, "create pairing secret")
		return
	}

	writeJSON(w, http.StatusCreated, createPairingSecretResponse{
		ID:        resp.Id,
		Label:     resp.Label,
		Plaintext: resp.Plaintext,
		ExpiresAt: resp.ExpiresAt.AsTime(),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/servers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	includeRevoked := r.URL.Query().Get("include_revoked") == "true"
	resp, err := h.pairing.ListServers(r.Context(), &pb.ListServersRequest{
		IncludeRevoked: includeRevoked,
	})
	if err != nil {
		h.internalError(w, r, "list servers", err)
		return
	}
	servers := make([]serverInfoJSON, 0, len(resp.Servers))
	for _, sv := range resp.Servers {
		info := serverInfoJSON{
			URN:          sv.ServerUrn,
			Name:         sv.ServerName,
			Endpoint:     sv.Endpoint,
			Revoked:      sv.Revoked,
			RegisteredAt: sv.RegisteredAt.AsTime(),
			ExpiresAt:    sv.ExpiresAt.AsTime(),
		}
		if sv.LastSeenAt != nil {
			t := sv.LastSeenAt.AsTime()
			info.LastSeenAt = &t
		}
		servers = append(servers, info)
	}
	writeJSON(w, http.StatusOK, listServersResponse{Servers: servers})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/servers/:urn
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRevokeServer(w http.ResponseWriter, r *http.Request, urn string) {
	_, err := h.pairing.RevokeServer(r.Context(), &pb.RevokeServerRequest{ServerUrn: urn})
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		h.internalError(w, r, "revoke server", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/servers/ca
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleGetCACertificate(w http.ResponseWriter, r *http.Request) {
	resp, err := h.pairing.GetCACertificate(r.Context(), &pb.GetCACertificateRequest{})
	if err != nil {
		h.internalError(w, r, "get CA certificate", err)
		return
	}

	response := caCertificateResponse{
		CACertificate: string(resp.CaCertificate),
	}

	if fp, err := caPEMFingerprint(resp.CaCertificate); err == nil {
		response.Fingerprint = fp
	}

	writeJSON(w, http.StatusOK, response)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/servers/outbound-pair
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleOutboundPair(w http.ResponseWriter, r *http.Request) {
	var req outboundPairRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}

	resp, err := h.pairing.RegisterWithPeer(r.Context(), req.URL, req.Secret)
	if err != nil {
		h.log.Error("outbound_pair_failed", "error", err, "url", req.URL)
		writeError(w, http.StatusBadGateway, "pairing failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, outboundPairResponse{
		ServerURN: resp.ServerUrn,
		ExpiresAt: resp.ExpiresAt.AsTime(),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// caPEMFingerprint parses a PEM-encoded certificate and returns its SHA-256
// fingerprint as uppercase colon-separated hex (e.g. "AA:BB:CC:...").
func caPEMFingerprint(pemBytes []byte) (string, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return "", fmt.Errorf("no PEM block found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	sum := sha256.Sum256(cert.Raw)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(pairs, ":"), nil
}
