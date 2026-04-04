package mobile

const (
	// AliasDeviceKeyV1 is the Keychain alias for the first-generation device
	// mTLS private key. Set ConfigKeyActiveKeyAlias to this value on initial pairing.
	AliasDeviceKeyV1 = "notx.device.key.v1"

	// AliasDeviceKeyV2 is the Keychain alias for the second-generation device
	// mTLS private key. Set ConfigKeyActiveKeyAlias to this value after the
	// first cert renewal cycle.
	AliasDeviceKeyV2 = "notx.device.key.v2"

	// AliasDeviceCert is the alias for the device's mTLS client certificate
	// (PEM), issued by the authority CA.
	AliasDeviceCert = "notx.device.cert"

	// AliasAuthorityCACert is the alias for the authority's CA certificate
	// (PEM), used to verify the server's TLS certificate.
	AliasAuthorityCACert = "notx.authority.ca.cert"

	// ConfigKeyDeviceURN holds the device's own notx:device:<uuid> URN
	// string in the config store (NSUserDefaults on iOS).
	ConfigKeyDeviceURN = "notx.device.urn"

	// ConfigKeyAuthorityAddr holds the host:port of the authority gRPC server.
	ConfigKeyAuthorityAddr = "notx.authority.addr"

	// ConfigKeyActiveKeyAlias holds the alias string of the currently active
	// device key (e.g. "notx.device.key.v1"). This is the single source of
	// truth for which versioned key is in use. Updated atomically by the
	// renewal flow after the new cert is fully validated and stored.
	ConfigKeyActiveKeyAlias = "notx.device.key.active"
)

// NextVersionAlias returns the next versioned key alias after current.
// It handles the v1→v2 and any subsequent versioned alias transitions by
// appending an incremented integer suffix.
//
// Examples:
//
//	NextVersionAlias("notx.device.key.v1") → "notx.device.key.v2"
//	NextVersionAlias("notx.device.key.v2") → "notx.device.key.v3"
func NextVersionAlias(current string) string {
	const prefix = "notx.device.key.v"
	if !hasPrefix(current, prefix) {
		return prefix + "2"
	}
	numStr := current[len(prefix):]
	n := 0
	for _, c := range numStr {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	if n == 0 {
		n = 1
	}
	return prefix + itoa(n+1)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
