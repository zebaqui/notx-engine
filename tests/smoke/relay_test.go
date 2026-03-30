package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/internal/repo/memory"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/internal/server/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// Relay-specific server bootstrap
// ─────────────────────────────────────────────────────────────────────────────

// startRelayServer spins up a notx server with AllowLocalhost=true so that the
// relay engine is permitted to reach the upstream stub server that also runs on
// 127.0.0.1 during the test.  All other defaults match startServer.
func startRelayServer(t *testing.T) (baseURL string, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	cfg := config.Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = true
	cfg.HTTPPort = httpPort
	cfg.GRPCPort = grpcPort
	cfg.DeviceOnboarding.AutoApprove = true

	// Allow the relay engine to reach localhost — needed for the in-process
	// upstream stub that listens on 127.0.0.1.
	cfg.Relay.AllowLocalhost = true

	provider := memory.New()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	srv, err := server.New(cfg, provider, provider, provider, provider, provider, provider, log)
	if err != nil {
		t.Fatalf("startRelayServer: server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithContext(ctx) }()

	// Wait until the HTTP port is accepting connections (up to 3 s).
	addr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop = func() {
		cancel()
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
			t.Log("warning: relay server did not stop within 5 s")
		}
	}

	return fmt.Sprintf("http://127.0.0.1:%d", httpPort), stop
}

// ─────────────────────────────────────────────────────────────────────────────
// Upstream stub helpers
// ─────────────────────────────────────────────────────────────────────────────

// stubResponse is the shape written by every upstream stub handler.
type stubResponse struct {
	Message string            `json:"message"`
	Token   string            `json:"token,omitempty"`
	Echo    map[string]string `json:"echo,omitempty"`
}

// newStubServer starts an httptest.Server whose single handler dispatches on
// path.  handlers maps URL path → http.HandlerFunc.  Any unregistered path
// returns 404.
func newStubServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	stub := httptest.NewServer(mux)
	t.Cleanup(stub.Close)
	return stub
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types — mirror relay_handler.go without importing the internal pkg
// ─────────────────────────────────────────────────────────────────────────────

type relayHTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type relayExecuteReq struct {
	Request   relayHTTPRequest  `json:"request"`
	Variables map[string]string `json:"variables,omitempty"`
}

type relayHTTPResp struct {
	Status     int32             `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	DurationMS int64             `json:"duration_ms"`
}

type relayExecuteResp struct {
	Response relayHTTPResp `json:"response"`
}

type relayStep struct {
	ID      string            `json:"id"`
	Request relayHTTPRequest  `json:"request"`
	Extract map[string]string `json:"extract,omitempty"`
}

type relayFlowReq struct {
	Steps     []relayStep       `json:"steps"`
	Variables map[string]string `json:"variables,omitempty"`
}

type relayStepResult struct {
	ID       string        `json:"id"`
	Response relayHTTPResp `json:"response"`
}

type relayFlowResp struct {
	Results   []relayStepResult `json:"results"`
	Variables map[string]string `json:"variables,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Smoke test 1 — Execute: single outbound HTTP request
// ─────────────────────────────────────────────────────────────────────────────

// TestRelayExecute exercises the full end-to-end path for a single relay
// execution:
//
//  1. Spin up a notx server with AllowLocalhost=true.
//  2. Spin up a lightweight upstream stub server.
//  3. Register and auto-approve a test device.
//  4. POST /v1/relay/execute with a variable-interpolated URL.
//  5. Assert the relay returned the upstream's JSON body and the correct status.
func TestRelayExecute(t *testing.T) {
	// ── Start notx server ────────────────────────────────────────────────────
	baseURL, stop := startRelayServer(t)
	defer stop()

	// ── Start upstream stub ──────────────────────────────────────────────────
	stub := newStubServer(t, map[string]http.HandlerFunc{
		"/ping": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(stubResponse{
				Message: "pong",
			})
		},
	})

	// ── Register device ──────────────────────────────────────────────────────
	deviceID := registerTestDevice(t, http.DefaultClient, baseURL)

	// ── Extract stub host for use in variable interpolation ──────────────────
	// stub.URL is "http://127.0.0.1:<port>" — we pass it as a variable so the
	// interpolation path is exercised end-to-end.
	stubHost := stub.URL // e.g. "http://127.0.0.1:54321"

	// ── POST /v1/relay/execute ───────────────────────────────────────────────
	reqBody := relayExecuteReq{
		Variables: map[string]string{
			"upstream": stubHost,
			"path":     "ping",
		},
		Request: relayHTTPRequest{
			Method: "GET",
			URL:    "{{upstream}}/{{path}}",
			Headers: map[string]string{
				"Accept": "application/json",
			},
		},
	}

	resp := postJSONWithDeviceID(t, http.DefaultClient, baseURL+"/v1/relay/execute", deviceID, reqBody)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/relay/execute: expected 200, got %d — %s", resp.StatusCode, raw)
	}

	// ── Decode and assert ────────────────────────────────────────────────────
	var got relayExecuteResp
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode relay execute response: %v\nraw: %s", err, raw)
	}

	if got.Response.Status != http.StatusOK {
		t.Errorf("relay response.status = %d, want 200", got.Response.Status)
	}

	if got.Response.DurationMS < 0 {
		t.Errorf("relay response.duration_ms should be >= 0, got %d", got.Response.DurationMS)
	}

	// The body must unmarshal to the stub's response shape.
	var body stubResponse
	if err := json.Unmarshal([]byte(got.Response.Body), &body); err != nil {
		t.Fatalf("relay response body is not valid JSON: %v\nbody: %s", err, got.Response.Body)
	}
	if body.Message != "pong" {
		t.Errorf("relay response body.message = %q, want %q", body.Message, "pong")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Smoke test 2 — ExecuteFlow: two-step pipeline with extraction
// ─────────────────────────────────────────────────────────────────────────────

// TestRelayExecuteFlow exercises the full end-to-end path for a two-step flow:
//
//  1. Spin up a notx server with AllowLocalhost=true.
//  2. Spin up an upstream stub with two endpoints:
//     • POST /auth  → returns {"token": "test-token-xyz"}
//     • GET  /data  → echoes the Authorization header value in the body
//  3. Register and auto-approve a test device.
//  4. POST /v1/relay/execute-flow with two steps:
//     Step "login"  — POST /auth, extract token from response.body.token
//     Step "fetch"  — GET  /data with Authorization: Bearer {{token}}
//  5. Assert both steps completed, the extracted token is in the final
//     variables, and the second step received the token from step one.
func TestRelayExecuteFlow(t *testing.T) {
	// ── Start notx server ────────────────────────────────────────────────────
	baseURL, stop := startRelayServer(t)
	defer stop()

	// ── Start upstream stub ──────────────────────────────────────────────────
	const wantToken = "test-token-xyz"

	stub := newStubServer(t, map[string]http.HandlerFunc{
		// Step 1 target: returns a JSON token.
		"/auth": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(stubResponse{
				Token: wantToken,
			})
		},
		// Step 2 target: echoes the Authorization header back in the body.
		"/data": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(stubResponse{
				Message: "ok",
				Echo: map[string]string{
					"authorization": r.Header.Get("Authorization"),
				},
			})
		},
	})

	// ── Register device ──────────────────────────────────────────────────────
	deviceID := registerTestDevice(t, http.DefaultClient, baseURL)

	// ── Build the two-step flow ──────────────────────────────────────────────
	stubBase := stub.URL // "http://127.0.0.1:<port>"

	flowReq := relayFlowReq{
		Variables: map[string]string{
			"base": stubBase,
		},
		Steps: []relayStep{
			{
				ID: "login",
				Request: relayHTTPRequest{
					Method: "POST",
					URL:    "{{base}}/auth",
					Headers: map[string]string{
						"Content-Type": "application/json",
					},
					Body: `{"client":"smoke-test"}`,
				},
				// Extract the token from the JSON body into the "token" variable.
				Extract: map[string]string{
					"token": "response.body.token",
				},
			},
			{
				ID: "fetch",
				Request: relayHTTPRequest{
					Method: "GET",
					URL:    "{{base}}/data",
					Headers: map[string]string{
						// {{token}} is resolved from the extraction of step "login".
						"Authorization": "Bearer {{token}}",
					},
				},
			},
		},
	}

	// ── POST /v1/relay/execute-flow ──────────────────────────────────────────
	resp := postJSONWithDeviceID(t, http.DefaultClient, baseURL+"/v1/relay/execute-flow", deviceID, flowReq)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/relay/execute-flow: expected 200, got %d — %s", resp.StatusCode, raw)
	}

	// ── Decode response ──────────────────────────────────────────────────────
	var got relayFlowResp
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode relay flow response: %v\nraw: %s", err, raw)
	}

	// ── Assert step count ────────────────────────────────────────────────────
	if len(got.Results) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(got.Results))
	}

	// ── Assert step "login" ──────────────────────────────────────────────────
	loginResult := got.Results[0]
	if loginResult.ID != "login" {
		t.Errorf("results[0].id = %q, want %q", loginResult.ID, "login")
	}
	if loginResult.Response.Status != http.StatusOK {
		t.Errorf("step login: response.status = %d, want 200", loginResult.Response.Status)
	}

	var loginBody stubResponse
	if err := json.Unmarshal([]byte(loginResult.Response.Body), &loginBody); err != nil {
		t.Fatalf("step login: body is not valid JSON: %v\nbody: %s", err, loginResult.Response.Body)
	}
	if loginBody.Token != wantToken {
		t.Errorf("step login: body.token = %q, want %q", loginBody.Token, wantToken)
	}

	// ── Assert extraction propagated into final variables ────────────────────
	if got.Variables["token"] != wantToken {
		t.Errorf("final variables[token] = %q, want %q", got.Variables["token"], wantToken)
	}

	// ── Assert step "fetch" ──────────────────────────────────────────────────
	fetchResult := got.Results[1]
	if fetchResult.ID != "fetch" {
		t.Errorf("results[1].id = %q, want %q", fetchResult.ID, "fetch")
	}
	if fetchResult.Response.Status != http.StatusOK {
		t.Errorf("step fetch: response.status = %d, want 200", fetchResult.Response.Status)
	}

	var fetchBody stubResponse
	if err := json.Unmarshal([]byte(fetchResult.Response.Body), &fetchBody); err != nil {
		t.Fatalf("step fetch: body is not valid JSON: %v\nbody: %s", err, fetchResult.Response.Body)
	}

	// The stub echoes the Authorization header — it must carry the token
	// that was extracted from the previous step.
	wantAuth := "Bearer " + wantToken
	if fetchBody.Echo["authorization"] != wantAuth {
		t.Errorf("step fetch: echo[authorization] = %q, want %q",
			fetchBody.Echo["authorization"], wantAuth)
	}
}
