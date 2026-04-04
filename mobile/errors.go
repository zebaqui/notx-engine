package mobile

import "errors"

// Error code constants bridged to Swift as integer constants.
// These are returned as the Code field of NSError on the Swift side.
const (
	// ErrCodeNotFound is returned when a requested key, cert, or config
	// value does not exist.
	ErrCodeNotFound = 1001

	// ErrCodeKeyExists is returned when GenerateKey is called for an alias
	// that already has a key and the platform requires explicit deletion first.
	ErrCodeKeyExists = 1002

	// ErrCodeKeychain is returned for any Keychain API failure other than
	// not-found.
	ErrCodeKeychain = 1003

	// ErrCodeCrypto is returned for any Secure Enclave / cryptographic
	// operation failure.
	ErrCodeCrypto = 1004

	// ErrCodeConfig is returned for any configuration persistence failure.
	ErrCodeConfig = 1005

	// ErrCodeStorage is returned for any file-system or SQLite storage failure.
	ErrCodeStorage = 1006

	// ErrCodePairing is returned when a pairing or renewal operation fails.
	ErrCodePairing = 1007

	// ErrCodeRenewal is returned specifically when cert renewal fails
	// (alias rotation, RPC error, or cert validation).
	ErrCodeRenewal = 1008
)

// Sentinel errors used internally by the Go engine and its SQLite provider.
var (
	// ErrNotFound is returned when a cert, key, or config value does not exist.
	ErrNotFound = errors.New("mobile: not found")

	// ErrPlatform is returned when the Platform implementation returns an
	// unexpected error that doesn't map to a more specific sentinel.
	ErrPlatform = errors.New("mobile: platform error")

	// ErrNotPaired is returned when an operation that requires a paired device
	// identity (cert + key) is attempted before pairing has completed.
	ErrNotPaired = errors.New("mobile: device not paired")

	// ErrRenewal is returned when cert renewal fails after all retry attempts.
	ErrRenewal = errors.New("mobile: cert renewal failed")
)
