package relay

import (
	"testing"
)

func TestPolicy_ValidateURL_AllowsHTTPS(t *testing.T) {
	p := DefaultPolicy()
	// With an empty AllowedHosts list and AllowLocalhost=false, a public HTTPS
	// URL should pass as long as it resolves to a public IP.
	// We use a URL that is known to resolve publicly — but since unit tests
	// should not make network calls, we only test the scheme/structure check
	// by using an IP-based URL.
	// 93.184.216.34 is example.com (publicly routable, not blocked).
	err := p.ValidateURL("https://93.184.216.34/path")
	if err != nil {
		t.Errorf("expected no error for public IP HTTPS URL, got: %v", err)
	}
}

func TestPolicy_ValidateURL_BlocksHTTP_MetadataService(t *testing.T) {
	p := DefaultPolicy()
	err := p.ValidateURL("http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Error("expected error for metadata service IP, got nil")
	}
}

func TestPolicy_ValidateURL_BlocksLoopbackByDefault(t *testing.T) {
	p := DefaultPolicy()
	err := p.ValidateURL("http://127.0.0.1/admin")
	if err == nil {
		t.Error("expected error for loopback IP when AllowLocalhost=false")
	}
}

func TestPolicy_ValidateURL_AllowsLoopbackWhenEnabled(t *testing.T) {
	p := DefaultPolicy()
	p.AllowLocalhost = true
	err := p.ValidateURL("http://127.0.0.1/admin")
	if err != nil {
		t.Errorf("expected no error when AllowLocalhost=true, got: %v", err)
	}
}

func TestPolicy_ValidateURL_BlocksFTP(t *testing.T) {
	p := DefaultPolicy()
	err := p.ValidateURL("ftp://example.com/file")
	if err == nil {
		t.Error("expected error for ftp scheme")
	}
}

func TestPolicy_ValidateURL_BlocksEmptyScheme(t *testing.T) {
	p := DefaultPolicy()
	err := p.ValidateURL("example.com/path")
	if err == nil {
		t.Error("expected error for URL without scheme")
	}
}

func TestPolicy_ValidateURL_AllowlistEnforced(t *testing.T) {
	p := DefaultPolicy()
	p.AllowedHosts = []string{"api.allowed.com"}

	// Allowed host — uses an IP address that is public.
	// We test the allowlist logic (not actual DNS resolution) by using a
	// hostname that is NOT in the allowlist.
	err := p.ValidateURL("https://api.blocked.com/v1")
	if err == nil {
		t.Error("expected error for host not in allowlist")
	}
}

func TestPolicy_ValidateURL_BlocksPrivateRFC1918(t *testing.T) {
	p := DefaultPolicy()
	err := p.ValidateURL("http://192.168.1.100/api")
	if err == nil {
		t.Error("expected error for RFC-1918 private IP when AllowLocalhost=false")
	}
}

func TestPolicy_ValidateURL_BlocksLinkLocal(t *testing.T) {
	p := DefaultPolicy()
	err := p.ValidateURL("http://169.254.0.1/path")
	if err == nil {
		t.Error("expected error for link-local IP")
	}
}

func TestDefaultPolicy_Defaults(t *testing.T) {
	p := DefaultPolicy()
	if p.MaxSteps != 20 {
		t.Errorf("expected MaxSteps=20, got %d", p.MaxSteps)
	}
	if p.MaxRequestBodyBytes != 1<<20 {
		t.Errorf("expected MaxRequestBodyBytes=1MiB, got %d", p.MaxRequestBodyBytes)
	}
	if p.MaxResponseBodyBytes != 4<<20 {
		t.Errorf("expected MaxResponseBodyBytes=4MiB, got %d", p.MaxResponseBodyBytes)
	}
	if p.RequestTimeoutSecs != 10 {
		t.Errorf("expected RequestTimeoutSecs=10, got %d", p.RequestTimeoutSecs)
	}
	if p.MaxRedirects != 5 {
		t.Errorf("expected MaxRedirects=5, got %d", p.MaxRedirects)
	}
	if p.AllowLocalhost {
		t.Error("expected AllowLocalhost=false by default")
	}
}
