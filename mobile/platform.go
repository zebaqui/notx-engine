// Package mobile is the gomobile-exported package for the notx engine.
// It is the ONLY package exposed to Swift/Objective-C via gomobile bind.
//
// gomobile bind restrictions that apply to this package:
//   - Exported method parameters must be Go primitive types, []byte, or types
//     declared in this package.
//   - Interfaces implemented in Swift can be passed into Go only if declared here.
//   - No map types in exported signatures.
//   - No multiple return values beyond (T, error) or bare error.
//   - The (T, error) pattern is automatically bridged to Swift throws.
package mobile

// Platform abstracts every platform-specific operation the engine needs.
// Implement this in Swift and pass it to Engine.New() at startup.
//
// All methods must be safe to call from multiple goroutines.
type Platform interface {

	// ── Key operations (Secure Enclave on iOS) ───────────────────────────

	// GenerateKey creates a new EC P-256 key pair identified by alias and
	// returns the DER-encoded public key. On iOS the private key is generated
	// inside the Secure Enclave and never leaves it. Pass alias =
	// AliasDeviceKeyV1 for the device mTLS identity key.
	GenerateKey(alias string) ([]byte, error)

	// Sign produces a raw ECDSA signature over digest using the private key
	// for alias. On iOS this executes entirely inside the Secure Enclave.
	// Used during cert renewal only — Go passes the TBS digest and receives
	// the signature bytes. Must NOT be used to sign a CSR directly; use
	// BuildCSR for that.
	Sign(alias string, digest []byte) ([]byte, error)

	// BuildCSR constructs and signs a PKCS#10 Certificate Signing Request
	// for the key identified by alias using the Secure Enclave key reference.
	// Returns DER-encoded CSR bytes. Go must treat the returned bytes as
	// opaque — it must not parse, modify, or re-sign them.
	// This is the ONLY method through which a CSR may be produced; Go must
	// never assemble a CSR structure itself.
	BuildCSR(alias string, commonName string) ([]byte, error)

	// PublicKeyDER returns the DER-encoded public key for alias without
	// exposing the private key.
	PublicKeyDER(alias string) ([]byte, error)

	// DeleteKey removes all key material for alias.
	DeleteKey(alias string) error

	// HasKey reports whether a key for alias exists.
	HasKey(alias string) (bool, error)

	// ── Certificate operations (Keychain on iOS) ─────────────────────────

	// StoreCert saves a PEM-encoded certificate under alias.
	// On iOS: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
	StoreCert(alias string, certPEM []byte) error

	// LoadCert returns the PEM-encoded certificate for alias.
	// Returns ErrNotFound if the alias does not exist.
	LoadCert(alias string) ([]byte, error)

	// DeleteCert removes the certificate for alias.
	DeleteCert(alias string) error

	// HasCert reports whether a cert for alias exists.
	HasCert(alias string) (bool, error)

	// ── Configuration (NSUserDefaults on iOS) ────────────────────────────

	// GetConfig returns the string value for the given configuration key.
	// Returns ("", nil) if the key does not exist.
	GetConfig(key string) (string, error)

	// SetConfig persists a configuration key-value pair.
	SetConfig(key, value string) error

	// ── Storage location ─────────────────────────────────────────────────

	// DataDir returns the root directory for note files and index.db.
	// The engine appends subdirectory names itself; DataDir must return
	// a path that exists and is writable.
	// On iOS: <Application Support>/notx/
	DataDir() (string, error)
}
