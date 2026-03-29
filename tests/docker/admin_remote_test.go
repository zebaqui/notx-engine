//go:build integration

// Package docker contains integration tests that spin up a real notx container
// and exercise the full remote-admin passphrase flow from the local machine.
//
// These tests require Docker to be present on the host and are intentionally
// gated behind the "integration" build tag so they never run as part of the
// normal `go test ./...` sweep.
//
// Run them with:
//
//	go test -v -tags integration -timeout 120s ./tests/docker/
//
// The test binary builds the notx Docker image from the project Dockerfile,
// starts a container with --admin-passphrase set, registers a remote admin
// device from the local machine by presenting the passphrase, and then drives
// several authenticated API calls through the admin device URN in the
// X-Device-ID header to confirm end-to-end access.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	// adminPassphrase is the shared secret we hand to the container via
	// --admin-passphrase and use when registering the remote admin device.
	adminPassphrase = "supersecret-test-passphrase"

	// containerReadyTimeout is how long we wait for the HTTP server inside the
	// container to start accepting connections.
	containerReadyTimeout = 30 * time.Second

	// imageName is the local Docker image tag built for these tests.
	imageName = "notx:integration-test"
)

// ─────────────────────────────────────────────────────────────────────────────
// Wire types (mirrors internal/server/http/handler.go)
// ─────────────────────────────────────────────────────────────────────────────

type registerDeviceRequest struct {
	URN             string `json:"urn"`
	Name            string `json:"name"`
	OwnerURN        string `json:"owner_urn"`
	AdminPassphrase string `json:"admin_passphrase,omitempty"`
}

type deviceJSON struct {
	URN            string `json:"urn"`
	Name           string `json:"name"`
	OwnerURN       string `json:"owner_urn"`
	Role           string `json:"role"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked"`
}

type createNoteRequest struct {
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NoteType string `json:"note_type"`
}

type noteHeaderJSON struct {
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NoteType string `json:"note_type"`
	Deleted  bool   `json:"deleted"`
}

type createNoteResponse struct {
	Note *noteHeaderJSON `json:"note"`
}

type listNotesResponse struct {
	Notes         []*noteHeaderJSON `json:"notes"`
	NextPageToken string            `json:"next_page_token"`
}

type createProjectRequest struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type projectJSON struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Deleted     bool   `json:"deleted"`
}

// createProjectResponse: the server returns the projectJSON object directly
// (not wrapped in an envelope), so we decode into projectJSON directly.
// This type alias exists only for naming clarity in test assertions.
type createProjectResponse = projectJSON

type listProjectsResponse struct {
	Projects      []*projectJSON `json:"projects"`
	NextPageToken string         `json:"next_page_token"`
}

type createUserRequest struct {
	URN         string `json:"urn"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type userJSON struct {
	URN         string `json:"urn"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Deleted     bool   `json:"deleted"`
}

// createUserResponse: the server returns the userJSON object directly
// (not wrapped in an envelope).
type createUserResponse = userJSON

type listUsersResponse struct {
	Users         []*userJSON `json:"users"`
	NextPageToken string      `json:"next_page_token"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Docker helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildImage builds the notx Docker image from the project root Dockerfile.
// It tags the resulting image as imageName. The project root is inferred from
// the location of this test file (two levels up from tests/docker/).
func buildImage(t *testing.T) {
	t.Helper()

	// Resolve the project root relative to this file's package directory.
	// Since tests run with the working directory set to the package, we walk up.
	projectRoot := "../.."

	t.Log("Building Docker image — this may take a minute on first run …")

	cmd := exec.Command("docker", "build",
		"--tag", imageName,
		"--file", projectRoot+"/Dockerfile",
		projectRoot,
	)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		t.Logf("docker build output:\n%s", out.String())
		t.Fatalf("docker build failed: %v", err)
	}

	t.Logf("Docker image %s built successfully", imageName)
}

// startContainer starts an ephemeral notx container on a random free host port
// with the admin passphrase configured. It returns the container ID and the
// base URL of the HTTP API (e.g. "http://127.0.0.1:52345").
//
// The container is automatically stopped and removed when the test ends via
// t.Cleanup.
func startContainer(t *testing.T) (containerID, baseURL string) {
	t.Helper()

	hostPort := freePort(t)

	args := []string{
		"run",
		"--detach",
		"--rm",                                        // auto-remove on stop
		"--publish", fmt.Sprintf("%d:4060", hostPort), // map random host port → container 4060
		imageName,
		"server",
		"--data-dir", "/data",
		"--grpc=false",
		"--admin-passphrase", adminPassphrase,
	}

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	containerID = strings.TrimSpace(string(out))
	if containerID == "" {
		t.Fatal("docker run returned an empty container ID")
	}

	t.Logf("Container started: %s (host port %d)", containerID[:12], hostPort)

	// Register a cleanup that stops the container when the test finishes.
	t.Cleanup(func() {
		stopCmd := exec.Command("docker", "stop", containerID)
		if err := stopCmd.Run(); err != nil {
			t.Logf("warning: docker stop %s: %v", containerID[:12], err)
		} else {
			t.Logf("Container stopped: %s", containerID[:12])
		}
	})

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	return containerID, baseURL
}

// waitForServer polls the /healthz endpoint until it responds with HTTP 200 or
// the timeout elapses. It calls t.Fatal if the server never becomes ready.
func waitForServer(t *testing.T, baseURL string) {
	t.Helper()

	deadline := time.Now().Add(containerReadyTimeout)
	addr := strings.TrimPrefix(baseURL, "http://")

	for time.Now().Before(deadline) {
		// First check TCP connectivity.
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		conn.Close()

		// Then probe the health endpoint.
		resp, err := http.Get(baseURL + "/healthz") //nolint:noctx
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Logf("Server ready at %s", baseURL)
			return
		}

		time.Sleep(200 * time.Millisecond)
	}

	// Dump container logs before failing so CI has context.
	t.Log("Server did not become ready in time. Dumping container logs …")
	t.Fatalf("server at %s never became ready within %s", baseURL, containerReadyTimeout)
}

// freePort asks the OS for an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// containerLogs returns the stdout+stderr of a running container.
func containerLogs(containerID string) string {
	out, _ := exec.Command("docker", "logs", containerID).CombinedOutput()
	return string(out)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

// doJSON performs a JSON request and returns the raw body bytes and status code.
// If deviceID is non-empty it is set as X-Device-ID.
func doJSON(t *testing.T, method, url, deviceID string, body any) (int, []byte) {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("doJSON marshal: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, bodyReader)
	if err != nil {
		t.Fatalf("doJSON new request %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("doJSON %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("doJSON read body: %v", err)
	}

	return resp.StatusCode, raw
}

// mustDecode unmarshals raw into dst and calls t.Fatal on error.
func mustDecode(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("mustDecode: %v\nraw: %s", err, raw)
	}
}

// assertStatus calls t.Errorf if the actual status code does not match expected.
func assertStatus(t *testing.T, label string, expected, actual int, body []byte) {
	t.Helper()
	if actual != expected {
		t.Errorf("%s: expected HTTP %d, got %d\nbody: %s", label, expected, actual, body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Domain helpers
// ─────────────────────────────────────────────────────────────────────────────

// newDeviceURN returns a fresh notx:device:<uuidv4> URN.
func newDeviceURN() string {
	return "notx:device:" + uuid.New().String()
}

// newOwnerURN returns a fresh notx:usr:<uuidv4> URN.
func newOwnerURN() string {
	return "notx:usr:" + uuid.New().String()
}

// newNoteURN returns a fresh notx:note:<uuidv7-ish> URN.
// We use a plain random UUID here to keep the test self-contained.
func newNoteURN() string {
	return "notx:note:" + uuid.New().String()
}

// newProjectURN returns a fresh notx:proj:<uuidv4> URN.
func newProjectURN() string {
	return "notx:proj:" + uuid.New().String()
}

// newUserURN returns a fresh notx:usr:<uuidv4> URN.
func newUserURN() string {
	return "notx:usr:" + uuid.New().String()
}

// randomName returns a short random name string suitable for display names /
// project names in test data.
func randomName(prefix string) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 6)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return fmt.Sprintf("%s-%s", prefix, string(b))
}

// ─────────────────────────────────────────────────────────────────────────────
// registerAdminDevice registers a new device with the admin passphrase and
// asserts the server grants it role=admin + approval_status=approved.
// Returns the device URN to use as X-Device-ID on subsequent requests.
// ─────────────────────────────────────────────────────────────────────────────
func registerAdminDevice(t *testing.T, baseURL string) string {
	t.Helper()

	deviceURN := newDeviceURN()
	ownerURN := newOwnerURN()

	status, raw := doJSON(t, http.MethodPost, baseURL+"/v1/devices", "", registerDeviceRequest{
		URN:             deviceURN,
		Name:            "integration-test-admin",
		OwnerURN:        ownerURN,
		AdminPassphrase: adminPassphrase,
	})

	assertStatus(t, "register admin device", http.StatusCreated, status, raw)

	var got deviceJSON
	mustDecode(t, raw, &got)

	if got.URN != deviceURN {
		t.Errorf("register admin device: URN = %q, want %q", got.URN, deviceURN)
	}
	if got.Role != "admin" {
		t.Errorf("register admin device: role = %q, want \"admin\"", got.Role)
	}
	if got.ApprovalStatus != "approved" {
		t.Errorf("register admin device: approval_status = %q, want \"approved\"", got.ApprovalStatus)
	}
	if got.Revoked {
		t.Errorf("register admin device: revoked = true, want false")
	}

	t.Logf("Admin device registered: %s (role=%s, status=%s)", deviceURN, got.Role, got.ApprovalStatus)
	return deviceURN
}

// ─────────────────────────────────────────────────────────────────────────────
// TestAdminRemote_PassphraseFlow is the primary integration test.
//
// It exercises the complete remote admin lifecycle:
//
//  1. Build the notx Docker image.
//  2. Start an ephemeral container with --admin-passphrase set.
//  3. Register a remote admin device from the local machine using the
//     passphrase; assert role=admin + approval_status=approved.
//  4. Use the admin device URN as X-Device-ID to perform a series of
//     authenticated API requests and confirm each succeeds.
//  5. Confirm that a device with a wrong passphrase is NOT granted admin role.
//  6. Confirm that a request without any X-Device-ID header is rejected (401).
//
// ─────────────────────────────────────────────────────────────────────────────
func TestAdminRemote_PassphraseFlow(t *testing.T) {
	// ── Pre-flight: make sure Docker is available ────────────────────────────
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker is not available on this machine — skipping integration test")
	}

	// ── Step 1: build image ───────────────────────────────────────────────────
	buildImage(t)

	// ── Step 2: start ephemeral container ────────────────────────────────────
	containerID, baseURL := startContainer(t)
	waitForServer(t, baseURL)

	t.Logf("notx server available at %s", baseURL)

	// ── Step 3: register remote admin device using passphrase ─────────────────
	t.Run("register_admin_device_with_passphrase", func(t *testing.T) {
		adminDeviceURN := registerAdminDevice(t, baseURL)

		// Quick sanity: the server should accept the URN as X-Device-ID on a
		// simple GET immediately after registration.
		status, raw := doJSON(t, http.MethodGet, baseURL+"/v1/notes", adminDeviceURN, nil)
		assertStatus(t, "initial GET /v1/notes as admin", http.StatusOK, status, raw)
	})

	// Obtain a fresh admin device URN for the remaining sub-tests.
	adminDeviceURN := registerAdminDevice(t, baseURL)

	// ── Step 4a: admin can create and list notes ──────────────────────────────
	t.Run("admin_can_create_and_list_notes", func(t *testing.T) {
		noteURN := newNoteURN()
		noteName := randomName("note")

		// Create
		status, raw := doJSON(t, http.MethodPost, baseURL+"/v1/notes", adminDeviceURN, createNoteRequest{
			URN:      noteURN,
			Name:     noteName,
			NoteType: "normal",
		})
		assertStatus(t, "create note", http.StatusCreated, status, raw)

		var created createNoteResponse
		mustDecode(t, raw, &created)

		if created.Note == nil {
			t.Fatal("create note: response.note is nil")
		}
		if created.Note.URN != noteURN {
			t.Errorf("create note: URN = %q, want %q", created.Note.URN, noteURN)
		}
		if created.Note.Name != noteName {
			t.Errorf("create note: Name = %q, want %q", created.Note.Name, noteName)
		}
		if created.Note.NoteType != "normal" {
			t.Errorf("create note: NoteType = %q, want \"normal\"", created.Note.NoteType)
		}

		// List
		status, raw = doJSON(t, http.MethodGet, baseURL+"/v1/notes", adminDeviceURN, nil)
		assertStatus(t, "list notes", http.StatusOK, status, raw)

		var listed listNotesResponse
		mustDecode(t, raw, &listed)

		found := false
		for _, n := range listed.Notes {
			if n.URN == noteURN {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("list notes: created note %s not found in response", noteURN)
		}

		t.Logf("Note created and listed: %s (%s)", noteName, noteURN)
	})

	// ── Step 4b: admin can create and list projects ───────────────────────────
	t.Run("admin_can_create_and_list_projects", func(t *testing.T) {
		projURN := newProjectURN()
		projName := randomName("project")

		// Create
		status, raw := doJSON(t, http.MethodPost, baseURL+"/v1/projects", adminDeviceURN, createProjectRequest{
			URN:         projURN,
			Name:        projName,
			Description: "created by integration test",
		})
		assertStatus(t, "create project", http.StatusCreated, status, raw)

		var created createProjectResponse
		mustDecode(t, raw, &created)

		if created.URN != projURN {
			t.Errorf("create project: URN = %q, want %q", created.URN, projURN)
		}
		if created.Name != projName {
			t.Errorf("create project: Name = %q, want %q", created.Name, projName)
		}

		// List
		status, raw = doJSON(t, http.MethodGet, baseURL+"/v1/projects", adminDeviceURN, nil)
		assertStatus(t, "list projects", http.StatusOK, status, raw)

		var listed listProjectsResponse
		mustDecode(t, raw, &listed)

		found := false
		for _, p := range listed.Projects {
			if p.URN == projURN {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("list projects: created project %s not found in response", projURN)
		}

		t.Logf("Project created and listed: %s (%s)", created.Name, projURN)
	})

	// ── Step 4c: admin can create and list users ──────────────────────────────
	t.Run("admin_can_create_and_list_users", func(t *testing.T) {
		userURN := newUserURN()
		displayName := randomName("user")
		email := displayName + "@test.example"

		// Create
		status, raw := doJSON(t, http.MethodPost, baseURL+"/v1/users", adminDeviceURN, createUserRequest{
			URN:         userURN,
			DisplayName: displayName,
			Email:       email,
		})
		assertStatus(t, "create user", http.StatusCreated, status, raw)

		var created createUserResponse
		mustDecode(t, raw, &created)

		if created.URN != userURN {
			t.Errorf("create user: URN = %q, want %q", created.URN, userURN)
		}

		// List
		status, raw = doJSON(t, http.MethodGet, baseURL+"/v1/users", adminDeviceURN, nil)
		assertStatus(t, "list users", http.StatusOK, status, raw)

		var listed listUsersResponse
		mustDecode(t, raw, &listed)

		found := false
		for _, u := range listed.Users {
			if u.URN == userURN {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("list users: created user %s not found in response", userURN)
		}

		t.Logf("User created and listed: %s (%s)", created.DisplayName, userURN)
	})

	// ── Step 4d: admin can GET device status for itself ───────────────────────
	t.Run("admin_can_get_own_device_status", func(t *testing.T) {
		url := baseURL + "/v1/devices/" + adminDeviceURN + "/status"
		status, raw := doJSON(t, http.MethodGet, url, adminDeviceURN, nil)
		assertStatus(t, "GET device status", http.StatusOK, status, raw)

		// The response is a deviceStatusResponse; we only care that it parses
		// and the approved field is true.
		var resp map[string]any
		mustDecode(t, raw, &resp)

		approved, _ := resp["approved"].(bool)
		if !approved {
			t.Errorf("GET device status: approved = false, want true; body: %s", raw)
		}

		t.Logf("Device status confirmed: approved=true for %s", adminDeviceURN)
	})

	// ── Step 5: wrong passphrase yields role=client, NOT admin ────────────────
	t.Run("wrong_passphrase_does_not_grant_admin", func(t *testing.T) {
		wrongDeviceURN := newDeviceURN()
		ownerURN := newOwnerURN()

		status, raw := doJSON(t, http.MethodPost, baseURL+"/v1/devices", "", registerDeviceRequest{
			URN:             wrongDeviceURN,
			Name:            "wrong-passphrase-device",
			OwnerURN:        ownerURN,
			AdminPassphrase: "this-is-not-the-right-passphrase",
		})
		// Registration itself should still succeed (falls through as client).
		assertStatus(t, "register with wrong passphrase", http.StatusCreated, status, raw)

		var got deviceJSON
		mustDecode(t, raw, &got)

		if got.Role == "admin" {
			t.Errorf("wrong passphrase: device was granted admin role — this should NOT happen")
		}
		// Without auto-approve the device lands in pending; role must be client.
		if got.Role != "client" {
			t.Errorf("wrong passphrase: role = %q, want \"client\"", got.Role)
		}

		// Any data request with a pending/non-admin device should be rejected.
		status, raw = doJSON(t, http.MethodGet, baseURL+"/v1/notes", wrongDeviceURN, nil)
		if status == http.StatusOK {
			t.Errorf("wrong-passphrase device got HTTP 200 on GET /v1/notes — should have been blocked")
		}

		t.Logf("Wrong passphrase correctly yields role=%s, data access blocked (HTTP %d)", got.Role, status)
	})

	// ── Step 6: missing X-Device-ID header is rejected with 401 ──────────────
	t.Run("missing_device_id_header_rejected", func(t *testing.T) {
		status, raw := doJSON(t, http.MethodGet, baseURL+"/v1/notes", "", nil)
		assertStatus(t, "missing X-Device-ID", http.StatusUnauthorized, status, raw)

		var errResp errorResponse
		mustDecode(t, raw, &errResp)

		if errResp.Error == "" {
			t.Errorf("missing X-Device-ID: expected a non-empty error message in body")
		}

		t.Logf("Missing X-Device-ID correctly returns 401: %s", errResp.Error)
	})

	// ── Step 7: unknown device URN in X-Device-ID is rejected with 401 ───────
	t.Run("unknown_device_id_rejected", func(t *testing.T) {
		phantom := newDeviceURN() // never registered
		status, raw := doJSON(t, http.MethodGet, baseURL+"/v1/notes", phantom, nil)
		assertStatus(t, "unknown X-Device-ID", http.StatusUnauthorized, status, raw)

		t.Logf("Unknown X-Device-ID correctly returns 401")
	})

	// ── Bonus: dump container logs at the end (visible with -v) ──────────────
	t.Logf("--- container logs for %s ---\n%s", containerID[:12], containerLogs(containerID))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestAdminRemote_NoPassphraseOnServer verifies that when the server is started
// WITHOUT --admin-passphrase, sending a passphrase in the registration request
// is harmless: the device is still registered as role=client (not admin).
// ─────────────────────────────────────────────────────────────────────────────
func TestAdminRemote_NoPassphraseOnServer(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker is not available on this machine — skipping integration test")
	}

	buildImage(t)

	// Start a container WITHOUT --admin-passphrase (local-mode bootstrap only).
	hostPort := freePort(t)
	args := []string{
		"run", "--detach", "--rm",
		"--publish", fmt.Sprintf("%d:4060", hostPort),
		imageName,
		"server", "--data-dir", "/data", "--grpc=false",
	}

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker run (no-passphrase server): %v", err)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		exec.Command("docker", "stop", containerID).Run() //nolint:errcheck
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	waitForServer(t, baseURL)

	t.Run("passphrase_field_ignored_when_no_hash_configured", func(t *testing.T) {
		deviceURN := newDeviceURN()
		ownerURN := newOwnerURN()

		status, raw := doJSON(t, http.MethodPost, baseURL+"/v1/devices", "", registerDeviceRequest{
			URN:             deviceURN,
			Name:            "hopeful-admin",
			OwnerURN:        ownerURN,
			AdminPassphrase: "whatever-passphrase",
		})
		assertStatus(t, "register with passphrase on no-passphrase server", http.StatusCreated, status, raw)

		var got deviceJSON
		mustDecode(t, raw, &got)

		if got.Role == "admin" {
			t.Errorf("device was granted admin role on a server with no passphrase configured — should be client")
		}

		t.Logf("Passphrase field correctly ignored — device role: %s", got.Role)
	})

	t.Logf("--- container logs for %s ---\n%s", containerID[:12], containerLogs(containerID))
}
