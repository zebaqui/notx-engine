package http

import (
	"net/http"
	"strings"
)

// AICredentialStore is the interface the HTTP handler uses to access AI credentials.
// It is satisfied by *credentials.Store once that package is implemented.
type AICredentialStore interface {
	List() ([]ProviderEntry, error)
	Set(provider string, apiKey string) error
	Delete(provider string) error
}

// ProviderEntry is a single masked credential entry returned by the API.
type ProviderEntry struct {
	Provider string `json:"provider"`
	Masked   string `json:"masked"` // e.g. "sk-...a1b2" — never the real key
}

// routeAICredentials handles GET /v1/ai/credentials and POST /v1/ai/credentials.
func (h *Handler) routeAICredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListAICredentials(w, r)
	case http.MethodPost:
		h.handleSetAICredential(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeAICredential handles DELETE /v1/ai/credentials/{provider}.
func (h *Handler) routeAICredential(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimPrefix(r.URL.Path, "/v1/ai/credentials/")
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.handleDeleteAICredential(w, r, provider)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type listAICredentialsResponse struct {
	Providers []ProviderEntry `json:"providers"`
}

func (h *Handler) handleListAICredentials(w http.ResponseWriter, r *http.Request) {
	// TODO: wire to credentials.Store once the package exists.
	writeJSON(w, http.StatusOK, listAICredentialsResponse{Providers: []ProviderEntry{}})
}

type setAICredentialRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

type setAICredentialResponse struct {
	Provider string `json:"provider"`
	Masked   string `json:"masked"`
}

func (h *Handler) handleSetAICredential(w http.ResponseWriter, r *http.Request) {
	var req setAICredentialRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	if req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "api_key is required")
		return
	}
	// TODO: wire to credentials.Store once the package exists.
	masked := maskKey(req.APIKey)
	writeJSON(w, http.StatusCreated, setAICredentialResponse{
		Provider: req.Provider,
		Masked:   masked,
	})
}

func (h *Handler) handleDeleteAICredential(w http.ResponseWriter, r *http.Request, provider string) {
	// TODO: wire to credentials.Store once the package exists.
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// maskKey returns a masked preview of the key, e.g. "sk-...a1b2".
// Never stores or logs the full key.
func maskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:3] + "..." + key[len(key)-4:]
}
