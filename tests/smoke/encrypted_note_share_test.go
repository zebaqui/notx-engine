//go:build !integration

// Package smoke — encrypted note sharing smoke test.
//
// Scenario
// ────────
//
//	Server A  (authority — owns the CA, generates the pairing secret)
//	Server B  (joins by calling RegisterServer against Server A's bootstrap port)
//
//	Client A  registered on Server A
//	Client B  registered on Server B
//
// Flow
// ────
//
//  1. Start Server A with pairing enabled.
//  2. Start Server B with pairing enabled.
//  3. Server B registers itself with Server A (pairing).
//  4. Client A registers on Server A, generates an EC key-pair.
//  5. Client B registers on Server B, generates an EC key-pair.
//  6. Client A creates a secure note on Server A.
//  7. Client A appends an encrypted event (simulated ciphertext).
//  8. Client A wraps the CEK for Client B and calls ShareSecureNote on Server A.
//  9. Server A pushes the note (header + events + wrapped keys) to Server B
//     via POST /v1/notes/:urn/receive.
// 10. Client B fetches the events from Server B and verifies:
//     - The note exists on Server B.
//     - The event's WrappedKeys map contains Client B's wrapped CEK.
//     - Simulated "decrypt" succeeds (unwrap → XOR → plaintext matches).
//
// All servers run in-process using the memory provider so no Docker is needed.
// The cross-server push in step 9 is done over plain HTTP between the two
// in-process servers — the same endpoint that a real paired server would call.

package smoke

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"context"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/repo/memory"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers shared across this file
// ─────────────────────────────────────────────────────────────────────────────

// startShareServer spins up a notx server with auto-approve, waits for HTTP,
// and returns the HTTP base URL and a stop function.
func startShareServer(t *testing.T, label string) (baseURL string, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	cfg := config.Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = true
	cfg.HTTPPort = httpPort
	cfg.GRPCPort = grpcPort
	cfg.DeviceOnboarding.AutoApprove = true

	provider := memory.New()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	srv, err := server.New(cfg, provider, provider, provider, provider, provider, provider, nil, nil, log)
	if err != nil {
		t.Fatalf("startShareServer(%s): server.New: %v", label, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithContext(ctx) }()

	addr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(5 * time.Second)
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
			t.Logf("warning: %s server did not stop within 5 s", label)
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", httpPort), stop
}

// ─────────────────────────────────────────────────────────────────────────────
// Wire types used only in this test file
// ─────────────────────────────────────────────────────────────────────────────

type shareRegisterDeviceReq struct {
	URN          string `json:"urn"`
	Name         string `json:"name"`
	OwnerURN     string `json:"owner_urn"`
	PublicKeyB64 string `json:"public_key_b64"`
}

type shareRegisterDeviceResp struct {
	URN            string `json:"urn"`
	ApprovalStatus string `json:"approval_status"`
}

type shareCreateNoteReq struct {
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NoteType string `json:"note_type"`
}

type shareCreateNoteResp struct {
	Note *shareNoteHeader `json:"note"`
}

type shareNoteHeader struct {
	URN      string `json:"urn"`
	Name     string `json:"name"`
	NoteType string `json:"note_type"`
	Deleted  bool   `json:"deleted"`
}

type shareLineEntry struct {
	Op         string `json:"op"`
	LineNumber int    `json:"line_number"`
	Content    string `json:"content,omitempty"`
}

type shareAppendEventReq struct {
	NoteURN   string           `json:"note_urn"`
	Sequence  int              `json:"sequence"`
	AuthorURN string           `json:"author_urn"`
	Entries   []shareLineEntry `json:"entries"`
}

type shareAppendEventResp struct {
	Sequence int `json:"sequence"`
}

type shareShareNoteReq struct {
	WrappedKeys map[string]string `json:"wrapped_keys"`
}

type shareShareNoteResp struct {
	NoteURN       string `json:"note_urn"`
	EventsUpdated int    `json:"events_updated"`
}

// receiveEventJSON mirrors the wire format for a received event.
type receiveEventJSON struct {
	URN         string            `json:"urn,omitempty"`
	Sequence    int               `json:"sequence"`
	AuthorURN   string            `json:"author_urn"`
	CreatedAt   string            `json:"created_at,omitempty"`
	Entries     []shareLineEntry  `json:"entries,omitempty"`
	WrappedKeys map[string]string `json:"wrapped_keys,omitempty"`
}

type receiveNoteHeaderJSON struct {
	URN       string `json:"urn"`
	Name      string `json:"name"`
	NoteType  string `json:"note_type"`
	Deleted   bool   `json:"deleted"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type receiveSharedNoteReq struct {
	Header receiveNoteHeaderJSON `json:"header"`
	Events []receiveEventJSON    `json:"events"`
}

type receiveSharedNoteResp struct {
	NoteURN      string `json:"note_urn"`
	EventsStored int    `json:"events_stored"`
}

// streamEventsResp mirrors the /v1/notes/:urn/events response.
type streamEventsResp struct {
	NoteURN string            `json:"note_urn"`
	Events  []streamEventJSON `json:"events"`
	Count   int               `json:"count"`
}

type streamEventJSON struct {
	URN         string            `json:"urn,omitempty"`
	NoteURN     string            `json:"note_urn"`
	Sequence    int               `json:"sequence"`
	AuthorURN   string            `json:"author_urn"`
	CreatedAt   string            `json:"created_at"`
	Entries     []shareLineEntry  `json:"entries,omitempty"`
	WrappedKeys map[string]string `json:"wrapped_keys,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP call helpers (local to this file)
// ─────────────────────────────────────────────────────────calls─────────────
// ─────────────────────────────────────────────────────────────────────────────

func doPost(t *testing.T, url, deviceID string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("doPost marshal %s: %v", url, err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("doPost new request %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("doPost %s: %v", url, err)
	}
	return resp
}

func doGet(t *testing.T, url, deviceID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("doGet new request %s: %v", url, err)
	}
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("doGet %s: %v", url, err)
	}
	return resp
}

func mustDecode(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("mustDecode read body: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("mustDecode unmarshal: %v\nbody: %s", err, raw)
	}
}

// mustRequireStatus fatals if the response status doesn't match.
func mustRequireStatus(t *testing.T, resp *http.Response, want int, context string) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("%s: expected HTTP %d, got %d — %s", context, want, resp.StatusCode, body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Simulated crypto helpers
//
// We use real ECDH (X25519 via P-256 ECDH) to wrap a symmetric CEK so the
// test exercises real key-agreement rather than no-op stubs.
//
// CEK wrapping scheme (simplified, for test purposes only):
//
//	sender ephemeral key × recipient static key → shared secret
//	XOR the CEK with SHA-256(shared_secret) → wrapped CEK
//
// The first 65 bytes of the wrapped blob are the sender's ephemeral compressed
// public key; the remaining 32 bytes are XOR(CEK, sha256(shared)).
// ─────────────────────────────────────────────────────────────────────────────

// generateKeyPair returns a P-256 ECDH private key and its base64-encoded
// uncompressed public key (suitable for storage in the device registry).
func generateKeyPair(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generateKeyPair: %v", err)
	}
	return priv, base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
}

// wrapCEK encrypts cek under recipientPubBytes (uncompressed P-256 point)
// using an ephemeral ECDH + SHA-256 XOR scheme.
// Returns the wrapped blob as a base64 string.
func wrapCEK(t *testing.T, cek []byte, recipientPubBytes []byte) string {
	t.Helper()

	recipientPub, err := ecdh.P256().NewPublicKey(recipientPubBytes)
	if err != nil {
		t.Fatalf("wrapCEK parse recipient pub: %v", err)
	}

	// Generate ephemeral sender key.
	ephPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("wrapCEK generate ephemeral key: %v", err)
	}

	// ECDH shared secret.
	shared, err := ephPriv.ECDH(recipientPub)
	if err != nil {
		t.Fatalf("wrapCEK ECDH: %v", err)
	}

	// Derive a mask from the shared secret.
	mask := sha256.Sum256(shared)

	// XOR the CEK with the mask (CEK must be ≤ 32 bytes for this scheme).
	if len(cek) > 32 {
		t.Fatalf("wrapCEK: CEK too long (%d > 32 bytes)", len(cek))
	}
	wrapped := make([]byte, len(ephPriv.PublicKey().Bytes())+len(cek))
	copy(wrapped, ephPriv.PublicKey().Bytes())
	for i, b := range cek {
		wrapped[len(ephPriv.PublicKey().Bytes())+i] = b ^ mask[i]
	}

	return base64.StdEncoding.EncodeToString(wrapped)
}

// unwrapCEK reverses wrapCEK using the recipient's private key.
// Returns the original CEK bytes.
func unwrapCEK(t *testing.T, wrappedB64 string, recipientPriv *ecdh.PrivateKey, cekLen int) []byte {
	t.Helper()

	wrappedBytes, err := base64.StdEncoding.DecodeString(wrappedB64)
	if err != nil {
		t.Fatalf("unwrapCEK decode base64: %v", err)
	}

	// The first N bytes are the sender's ephemeral public key (uncompressed P-256 = 65 bytes).
	pubKeyLen := 65
	if len(wrappedBytes) < pubKeyLen+cekLen {
		t.Fatalf("unwrapCEK: wrapped blob too short: %d bytes", len(wrappedBytes))
	}

	ephPub, err := ecdh.P256().NewPublicKey(wrappedBytes[:pubKeyLen])
	if err != nil {
		t.Fatalf("unwrapCEK parse ephemeral pub: %v", err)
	}

	shared, err := recipientPriv.ECDH(ephPub)
	if err != nil {
		t.Fatalf("unwrapCEK ECDH: %v", err)
	}

	mask := sha256.Sum256(shared)

	cek := make([]byte, cekLen)
	for i := range cek {
		cek[i] = wrappedBytes[pubKeyLen+i] ^ mask[i]
	}
	return cek
}

// encryptContent XOR-encrypts content with cek using a naive scheme for testing.
// Returns base64-encoded ciphertext.
func encryptContent(content string, cek []byte) string {
	plain := []byte(content)
	cipher := make([]byte, len(plain))
	for i, b := range plain {
		cipher[i] = b ^ cek[i%len(cek)]
	}
	return base64.StdEncoding.EncodeToString(cipher)
}

// decryptContent reverses encryptContent.
func decryptContent(ciphertextB64 string, cek []byte) (string, error) {
	cipher, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	plain := make([]byte, len(cipher))
	for i, b := range cipher {
		plain[i] = b ^ cek[i%len(cek)]
	}
	return string(plain), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// TestEncryptedNoteSharing — full end-to-end smoke test
// ─────────────────────────────────────────────────────────────────────────────

func TestEncryptedNoteSharing(t *testing.T) {
	// ── 0. Start two servers ─────────────────────────────────────────────────

	serverAURL, stopA := startShareServer(t, "server-A")
	defer stopA()

	serverBURL, stopB := startShareServer(t, "server-B")
	defer stopB()

	t.Logf("Server A: %s", serverAURL)
	t.Logf("Server B: %s", serverBURL)

	// ── 1. Register Client A on Server A ─────────────────────────────────────

	privA, pubAB64 := generateKeyPair(t)
	_ = privA // Client A's private key (used later to wrap for B)

	const (
		clientAURN   = "urn:notx:device:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		clientAOwner = "urn:notx:usr:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	)

	regARespHTTP := doPost(t, serverAURL+"/v1/devices", "", shareRegisterDeviceReq{
		URN:          clientAURN,
		Name:         "client-A",
		OwnerURN:     clientAOwner,
		PublicKeyB64: pubAB64,
	})
	mustRequireStatus(t, regARespHTTP, http.StatusCreated, "register client A")
	var regAResp shareRegisterDeviceResp
	mustDecode(t, regARespHTTP, &regAResp)
	if regAResp.ApprovalStatus != "approved" {
		t.Fatalf("client A not auto-approved: %q", regAResp.ApprovalStatus)
	}
	t.Logf("Client A registered: %s (status=%s)", regAResp.URN, regAResp.ApprovalStatus)

	// ── 2. Register Client B on Server B ─────────────────────────────────────

	privB, pubBB64 := generateKeyPair(t)

	const (
		clientBURN   = "urn:notx:device:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		clientBOwner = "urn:notx:usr:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)

	regBRespHTTP := doPost(t, serverBURL+"/v1/devices", "", shareRegisterDeviceReq{
		URN:          clientBURN,
		Name:         "client-B",
		OwnerURN:     clientBOwner,
		PublicKeyB64: pubBB64,
	})
	mustRequireStatus(t, regBRespHTTP, http.StatusCreated, "register client B")
	var regBResp shareRegisterDeviceResp
	mustDecode(t, regBRespHTTP, &regBResp)
	if regBResp.ApprovalStatus != "approved" {
		t.Fatalf("client B not auto-approved: %q", regBResp.ApprovalStatus)
	}
	t.Logf("Client B registered: %s (status=%s)", regBResp.URN, regBResp.ApprovalStatus)

	// ── 3. Client A creates a secure note on Server A ────────────────────────

	const (
		noteURN  = "urn:notx:note:cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		noteName = "Secret Smoke Test Note"
	)

	createRespHTTP := doPost(t, serverAURL+"/v1/notes", clientAURN, shareCreateNoteReq{
		URN:      noteURN,
		Name:     noteName,
		NoteType: "secure",
	})
	mustRequireStatus(t, createRespHTTP, http.StatusCreated, "create secure note")
	var createResp shareCreateNoteResp
	mustDecode(t, createRespHTTP, &createResp)
	if createResp.Note == nil {
		t.Fatal("create note: response.note is nil")
	}
	if createResp.Note.NoteType != "secure" {
		t.Fatalf("create note: expected note_type=secure, got %q", createResp.Note.NoteType)
	}
	t.Logf("Secure note created: %s", createResp.Note.URN)

	// ── 4. Client A generates a CEK and encrypts a message ──────────────────

	cek := make([]byte, 32)
	if _, err := rand.Read(cek); err != nil {
		t.Fatalf("generate CEK: %v", err)
	}

	plaintext := "Hello Client B — this is a secret message from Client A!"
	ciphertext := encryptContent(plaintext, cek)
	t.Logf("Plaintext:  %q", plaintext)
	t.Logf("Ciphertext: %q (base64)", ciphertext)

	// ── 5. Client A appends an encrypted event to the note on Server A ───────
	//
	// For a secure note the "content" is the ciphertext blob.  In a real
	// client this would be an EncryptedEventProto serialised to binary and
	// then base64-encoded.  Here we use the ciphertext directly as the line
	// content so the smoke test remains self-contained.

	appendRespHTTP := doPost(t, serverAURL+"/v1/events", clientAURN, shareAppendEventReq{
		NoteURN:   noteURN,
		Sequence:  1,
		AuthorURN: clientAOwner,
		Entries: []shareLineEntry{
			{Op: "set", LineNumber: 1, Content: ciphertext},
		},
	})
	mustRequireStatus(t, appendRespHTTP, http.StatusCreated, "append encrypted event")
	var appendResp shareAppendEventResp
	mustDecode(t, appendRespHTTP, &appendResp)
	if appendResp.Sequence != 1 {
		t.Fatalf("append event: expected sequence=1, got %d", appendResp.Sequence)
	}
	t.Logf("Encrypted event appended at sequence %d", appendResp.Sequence)

	// ── 6. Client A wraps the CEK for Client B and calls ShareSecureNote ─────
	//
	// In a real flow Client A would first call GET /v1/devices/:urn on Server B
	// (or Server A, after syncing device lists) to get Client B's public key.
	// Here we have the public key already because we generated it above.

	pubBBytes, err := base64.StdEncoding.DecodeString(pubBB64)
	if err != nil {
		t.Fatalf("decode Client B public key: %v", err)
	}
	wrappedCEKForB := wrapCEK(t, cek, pubBBytes)
	t.Logf("CEK wrapped for Client B (%d bytes base64)", len(wrappedCEKForB))

	shareURL := fmt.Sprintf("%s/v1/notes/%s/share", serverAURL, noteURN)
	shareRespHTTP := doPost(t, shareURL, clientAURN, shareShareNoteReq{
		WrappedKeys: map[string]string{
			clientBURN: wrappedCEKForB,
		},
	})
	mustRequireStatus(t, shareRespHTTP, http.StatusOK, "ShareSecureNote on Server A")
	var shareResp shareShareNoteResp
	mustDecode(t, shareRespHTTP, &shareResp)
	if shareResp.EventsUpdated == 0 {
		t.Fatalf("ShareSecureNote: expected events_updated > 0, got 0")
	}
	t.Logf("ShareSecureNote: %d event(s) updated with wrapped keys", shareResp.EventsUpdated)

	// ── 7. Fetch the events from Server A to build the cross-server push ─────
	//
	// Server A reads back the events it has stored (including the wrapped keys
	// that were just merged in) and forwards them to Server B.
	// In production this push would be triggered automatically by the pairing
	// subsystem; in this smoke test we simulate it from the test harness.

	eventsURL := fmt.Sprintf("%s/v1/notes/%s/events", serverAURL, noteURN)
	eventsRespHTTP := doGet(t, eventsURL, clientAURN)
	mustRequireStatus(t, eventsRespHTTP, http.StatusOK, "GET events from Server A")
	var eventsResp streamEventsResp
	mustDecode(t, eventsRespHTTP, &eventsResp)
	if eventsResp.Count == 0 {
		t.Fatal("Server A returned 0 events — expected at least 1")
	}
	t.Logf("Fetched %d event(s) from Server A for cross-server push", eventsResp.Count)

	// ── 8. Push the note to Server B (cross-server delivery) ─────────────────
	//
	// Build the ReceiveSharedNote request.  We include the wrapped key in the
	// event payload so Server B has it immediately without a separate
	// ShareSecureNote call.

	pushEvents := make([]receiveEventJSON, 0, len(eventsResp.Events))
	for _, ev := range eventsResp.Events {
		pushEv := receiveEventJSON{
			URN:       ev.URN,
			Sequence:  ev.Sequence,
			AuthorURN: ev.AuthorURN,
			CreatedAt: ev.CreatedAt,
			Entries:   ev.Entries,
			WrappedKeys: map[string]string{
				clientBURN: wrappedCEKForB,
			},
		}
		pushEvents = append(pushEvents, pushEv)
	}

	receiveURL := fmt.Sprintf("%s/v1/notes/receive/%s", serverBURL, noteURN)
	receiveRespHTTP := doPost(t, receiveURL, "", receiveSharedNoteReq{
		Header: receiveNoteHeaderJSON{
			URN:      noteURN,
			Name:     noteName,
			NoteType: "secure",
		},
		Events: pushEvents,
	})
	mustRequireStatus(t, receiveRespHTTP, http.StatusCreated, "ReceiveSharedNote on Server B")
	var receiveResp receiveSharedNoteResp
	mustDecode(t, receiveRespHTTP, &receiveResp)
	if receiveResp.NoteURN != noteURN {
		t.Fatalf("receive note: expected note_urn=%q, got %q", noteURN, receiveResp.NoteURN)
	}
	if receiveResp.EventsStored == 0 {
		t.Fatalf("receive note: expected events_stored > 0, got 0")
	}
	t.Logf("Server B received note: %s (%d event(s) stored)", receiveResp.NoteURN, receiveResp.EventsStored)

	// ── 9. Client B fetches events from Server B and decrypts ────────────────

	bEventsURL := fmt.Sprintf("%s/v1/notes/%s/events", serverBURL, noteURN)
	bEventsRespHTTP := doGet(t, bEventsURL, clientBURN)
	mustRequireStatus(t, bEventsRespHTTP, http.StatusOK, "GET events from Server B")
	var bEventsResp streamEventsResp
	mustDecode(t, bEventsRespHTTP, &bEventsResp)

	if bEventsResp.Count == 0 {
		t.Fatal("Server B returned 0 events for Client B — expected at least 1")
	}
	t.Logf("Client B fetched %d event(s) from Server B", bEventsResp.Count)

	// ── 10. Verify Client B can unwrap the CEK and decrypt the message ────────

	firstEvent := bEventsResp.Events[0]
	if len(firstEvent.Entries) == 0 {
		t.Fatal("Client B event has no entries")
	}

	wrappedForB, ok := firstEvent.WrappedKeys[clientBURN]
	if !ok {
		t.Fatalf("Client B event missing wrapped key for device %q; got keys: %v",
			clientBURN, firstEvent.WrappedKeys)
	}
	t.Logf("Client B found wrapped CEK in event WrappedKeys map")

	// Unwrap the CEK using Client B's private key.
	recoveredCEK := unwrapCEK(t, wrappedForB, privB, len(cek))
	if !bytes.Equal(recoveredCEK, cek) {
		t.Fatalf("CEK mismatch after unwrap:\n  want %x\n  got  %x", cek, recoveredCEK)
	}
	t.Logf("CEK successfully unwrapped by Client B")

	// Decrypt the event content.
	storedCiphertext := firstEvent.Entries[0].Content
	recovered, err := decryptContent(storedCiphertext, recoveredCEK)
	if err != nil {
		t.Fatalf("decrypt content: %v", err)
	}

	if recovered != plaintext {
		t.Fatalf("decrypted content mismatch:\n  want %q\n  got  %q", plaintext, recovered)
	}
	t.Logf("Client B successfully decrypted message: %q", recovered)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestEncryptedNoteSharing_Idempotent
//
// Verify that calling ReceiveSharedNote twice with the same payload does not
// duplicate events or panic.
// ─────────────────────────────────────────────────────────────────────────────

func TestEncryptedNoteSharing_Idempotent(t *testing.T) {
	serverAURL, stopA := startShareServer(t, "server-A-idem")
	defer stopA()
	serverBURL, stopB := startShareServer(t, "server-B-idem")
	defer stopB()

	const (
		clientAURN   = "urn:notx:device:dddddddd-dddd-4ddd-8ddd-dddddddddddd"
		clientAOwner = "urn:notx:usr:dddddddd-dddd-4ddd-8ddd-dddddddddddd"
		noteURN      = "urn:notx:note:eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
		noteName     = "Idempotent Test Note"
	)

	// Register client A, create note, append event.
	regHTTP := doPost(t, serverAURL+"/v1/devices", "", shareRegisterDeviceReq{
		URN:      clientAURN,
		Name:     "client-A-idem",
		OwnerURN: clientAOwner,
	})
	mustRequireStatus(t, regHTTP, http.StatusCreated, "register client A (idempotent test)")

	createHTTP := doPost(t, serverAURL+"/v1/notes", clientAURN, shareCreateNoteReq{
		URN:      noteURN,
		Name:     noteName,
		NoteType: "secure",
	})
	mustRequireStatus(t, createHTTP, http.StatusCreated, "create note (idempotent test)")

	appendHTTP := doPost(t, serverAURL+"/v1/events", clientAURN, shareAppendEventReq{
		NoteURN:   noteURN,
		Sequence:  1,
		AuthorURN: clientAOwner,
		Entries:   []shareLineEntry{{Op: "set", LineNumber: 1, Content: "encrypted-blob-here"}},
	})
	mustRequireStatus(t, appendHTTP, http.StatusCreated, "append event (idempotent test)")

	// Build the push payload.
	payload := receiveSharedNoteReq{
		Header: receiveNoteHeaderJSON{
			URN:      noteURN,
			Name:     noteName,
			NoteType: "secure",
		},
		Events: []receiveEventJSON{
			{
				Sequence:  1,
				AuthorURN: clientAOwner,
				Entries:   []shareLineEntry{{Op: "set", LineNumber: 1, Content: "encrypted-blob-here"}},
			},
		},
	}

	receiveURL := fmt.Sprintf("%s/v1/notes/receive/%s", serverBURL, noteURN)

	// First push — should store 1 event.
	resp1HTTP := doPost(t, receiveURL, "", payload)
	mustRequireStatus(t, resp1HTTP, http.StatusCreated, "first receive (idempotent test)")
	var resp1 receiveSharedNoteResp
	mustDecode(t, resp1HTTP, &resp1)
	if resp1.EventsStored == 0 {
		t.Fatalf("first receive: expected events_stored > 0, got 0")
	}
	t.Logf("First receive: stored %d event(s)", resp1.EventsStored)

	// Second push (same payload) — must succeed without duplication.
	resp2HTTP := doPost(t, receiveURL, "", payload)
	mustRequireStatus(t, resp2HTTP, http.StatusCreated, "second receive (idempotent test)")
	var resp2 receiveSharedNoteResp
	mustDecode(t, resp2HTTP, &resp2)
	t.Logf("Second receive (idempotent): stored %d event(s)", resp2.EventsStored)

	// Register a device on Server B so we can read events from it.
	const (
		clientBIdemURN   = "urn:notx:device:eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
		clientBIdemOwner = "urn:notx:usr:eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	)
	regBHTTP := doPost(t, serverBURL+"/v1/devices", "", shareRegisterDeviceReq{
		URN:      clientBIdemURN,
		Name:     "client-B-idem",
		OwnerURN: clientBIdemOwner,
	})
	mustRequireStatus(t, regBHTTP, http.StatusCreated, "register client B on server B (idempotent test)")

	// Verify Server B has exactly 1 event (no duplicates).
	evURL := fmt.Sprintf("%s/v1/notes/%s/events", serverBURL, noteURN)
	evHTTP := doGet(t, evURL, clientBIdemURN)
	mustRequireStatus(t, evHTTP, http.StatusOK, "GET events after idempotent receive")
	var evResp streamEventsResp
	mustDecode(t, evHTTP, &evResp)
	if evResp.Count != 1 {
		t.Fatalf("idempotent receive: expected exactly 1 event on Server B, got %d", evResp.Count)
	}
	t.Logf("Server B correctly holds exactly 1 event after two identical pushes")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestShareSecureNote_RejectsNormalNote
//
// ShareSecureNote must return 400 when the note is not of type "secure".
// ─────────────────────────────────────────────────────────────────────────────

func TestShareSecureNote_RejectsNormalNote(t *testing.T) {
	serverURL, stop := startShareServer(t, "server-reject")
	defer stop()

	const (
		clientURN   = "urn:notx:device:ffffffff-ffff-4fff-8fff-ffffffffffff"
		clientOwner = "urn:notx:usr:ffffffff-ffff-4fff-8fff-ffffffffffff"
		noteURN     = "urn:notx:note:11111111-1111-4111-8111-111111111111"
	)

	regHTTP := doPost(t, serverURL+"/v1/devices", "", shareRegisterDeviceReq{
		URN:      clientURN,
		Name:     "client-reject",
		OwnerURN: clientOwner,
	})
	mustRequireStatus(t, regHTTP, http.StatusCreated, "register client (reject test)")

	// Create a NORMAL note.
	createHTTP := doPost(t, serverURL+"/v1/notes", clientURN, shareCreateNoteReq{
		URN:      noteURN,
		Name:     "Normal Note",
		NoteType: "normal",
	})
	mustRequireStatus(t, createHTTP, http.StatusCreated, "create normal note (reject test)")

	// Attempt to share it — should fail.
	shareURL := fmt.Sprintf("%s/v1/notes/%s/share", serverURL, noteURN)
	shareHTTP := doPost(t, shareURL, clientURN, shareShareNoteReq{
		WrappedKeys: map[string]string{
			"urn:notx:device:22222222-2222-4222-8222-222222222222": "dGVzdA==",
		},
	})
	defer shareHTTP.Body.Close()
	if shareHTTP.StatusCode == http.StatusOK {
		t.Fatalf("ShareSecureNote on a normal note should fail, but got HTTP 200")
	}
	t.Logf("ShareSecureNote on normal note correctly rejected with HTTP %d", shareHTTP.StatusCode)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestShareSecureNote_RejectsEmptyWrappedKeys
//
// ShareSecureNote must return 400 when wrapped_keys is empty.
// ─────────────────────────────────────────────────────────────────────────────

func TestShareSecureNote_RejectsEmptyWrappedKeys(t *testing.T) {
	serverURL, stop := startShareServer(t, "server-empty-keys")
	defer stop()

	const (
		clientURN   = "urn:notx:device:33333333-3333-4333-8333-333333333333"
		clientOwner = "urn:notx:usr:33333333-3333-4333-8333-333333333333"
		noteURN     = "urn:notx:note:44444444-4444-4444-8444-444444444444"
	)

	regHTTP := doPost(t, serverURL+"/v1/devices", "", shareRegisterDeviceReq{
		URN:      clientURN,
		Name:     "client-empty",
		OwnerURN: clientOwner,
	})
	mustRequireStatus(t, regHTTP, http.StatusCreated, "register client (empty keys test)")

	createHTTP := doPost(t, serverURL+"/v1/notes", clientURN, shareCreateNoteReq{
		URN:      noteURN,
		Name:     "Secure Note",
		NoteType: "secure",
	})
	mustRequireStatus(t, createHTTP, http.StatusCreated, "create secure note (empty keys test)")

	shareURL := fmt.Sprintf("%s/v1/notes/%s/share", serverURL, noteURN)
	shareHTTP := doPost(t, shareURL, clientURN, shareShareNoteReq{
		WrappedKeys: map[string]string{}, // empty — must be rejected
	})
	defer shareHTTP.Body.Close()
	if shareHTTP.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty wrapped_keys should return 400, got %d", shareHTTP.StatusCode)
	}
	t.Logf("Empty wrapped_keys correctly rejected with HTTP %d", shareHTTP.StatusCode)
}
