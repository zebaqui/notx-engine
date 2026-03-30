// Package relay implements the gRPC-first HTTP relay execution engine.
// All execution logic lives here; the HTTP adapter layer is a thin translator.
package relay

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Policy — security constraints for the relay engine
// ─────────────────────────────────────────────────────────────────────────────

// Policy holds the security constraints that govern every outbound request.
// All validation is performed inside the gRPC layer before any network I/O.
type Policy struct {
	// AllowedHosts is an explicit allowlist of hostnames (without port) that
	// the relay may contact.  When empty every host is permitted (subject to
	// the block-list below).  In production this should always be set.
	AllowedHosts []string

	// AllowLocalhost permits connections to 127.x.x.x / ::1 / localhost.
	// Should only be true in development/test environments.
	AllowLocalhost bool

	// MaxSteps is the maximum number of steps allowed in a single flow.
	// Default: 20.
	MaxSteps int

	// MaxRequestBodyBytes caps the request body size accepted by the engine.
	// Default: 1 MiB.
	MaxRequestBodyBytes int64

	// MaxResponseBodyBytes caps how many bytes are read from an upstream
	// response body.  Default: 4 MiB.
	MaxResponseBodyBytes int64

	// RequestTimeout is the per-request deadline passed to context.WithTimeout.
	// Default: 10 seconds.
	RequestTimeoutSecs int

	// MaxRedirects is the maximum number of HTTP redirects the engine will
	// follow for a single request.  Default: 5.
	MaxRedirects int
}

// DefaultPolicy returns a Policy configured with production-safe defaults.
func DefaultPolicy() Policy {
	return Policy{
		AllowLocalhost:       false,
		MaxSteps:             20,
		MaxRequestBodyBytes:  1 << 20, // 1 MiB
		MaxResponseBodyBytes: 4 << 20, // 4 MiB
		RequestTimeoutSecs:   10,
		MaxRedirects:         5,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Blocked network ranges (SSRF / metadata service protection)
// ─────────────────────────────────────────────────────────────────────────────

// blockedCIDRs is a hard-coded list of IP ranges that must never be reachable
// regardless of allowlist configuration.  These cover:
//   - AWS/GCP/Azure instance metadata services (169.254.169.254)
//   - Link-local addresses
//   - RFC-1918 private ranges (blocked unless AllowLocalhost is set)
//   - Loopback (blocked unless AllowLocalhost is set)
var blockedCIDRs = mustParseCIDRs([]string{
	"169.254.0.0/16",  // link-local / metadata (AWS, GCP, Azure, etc.)
	"100.64.0.0/10",   // CGNAT shared address space
	"192.0.0.0/24",    // IANA special-purpose
	"192.0.2.0/24",    // TEST-NET-1
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"240.0.0.0/4",     // reserved (class E)
	"::1/128",         // IPv6 loopback
	"fc00::/7",        // IPv6 unique local
	"fe80::/10",       // IPv6 link-local
})

// localhostCIDRs are only blocked when AllowLocalhost is false.
var localhostCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",    // IPv4 loopback
	"10.0.0.0/8",     // RFC-1918 class A
	"172.16.0.0/12",  // RFC-1918 class B
	"192.168.0.0/16", // RFC-1918 class C
})

// ─────────────────────────────────────────────────────────────────────────────
// ValidateURL checks a raw URL against the policy.
// ─────────────────────────────────────────────────────────────────────────────

// ValidateURL returns a non-nil error when the URL violates the policy.
// It checks:
//  1. Scheme must be http or https.
//  2. Host must appear in AllowedHosts (when non-empty).
//  3. Resolved IP must not be in a blocked CIDR.
//  4. Loopback / RFC-1918 ranges are blocked unless AllowLocalhost is true.
func (p *Policy) ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}

	// 1. Scheme check.
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme %q is not allowed: only http and https are permitted", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	// 2. Allowlist check (when configured).
	if len(p.AllowedHosts) > 0 {
		if !p.hostAllowed(host) {
			return fmt.Errorf("host %q is not in the relay allowlist", host)
		}
	}

	// 3 & 4. IP-based block checks (resolve hostname to IPs).
	if err := p.checkHost(host); err != nil {
		return err
	}

	return nil
}

// hostAllowed returns true when host (or its parent domain) is in p.AllowedHosts.
func (p *Policy) hostAllowed(host string) bool {
	for _, allowed := range p.AllowedHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
		// Also allow subdomains: *.allowed.com
		if strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(allowed)) {
			return true
		}
	}
	return false
}

// checkHost resolves host to IPs and validates each one.
func (p *Policy) checkHost(host string) error {
	// If the host is already a raw IP, check it directly.
	if ip := net.ParseIP(host); ip != nil {
		return p.checkIP(ip)
	}

	// DNS resolution.
	addrs, err := net.LookupHost(host)
	if err != nil {
		// If we cannot resolve, we cannot be sure it is safe — deny.
		return fmt.Errorf("could not resolve host %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if err := p.checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// checkIP returns a non-nil error if the IP falls in a blocked range.
func (p *Policy) checkIP(ip net.IP) error {
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return fmt.Errorf("host resolves to blocked IP range %s", cidr)
		}
	}
	if !p.AllowLocalhost {
		for _, cidr := range localhostCIDRs {
			if cidr.Contains(ip) {
				return fmt.Errorf("host resolves to private/loopback IP %s (localhost access is disabled)", ip)
			}
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("relay: invalid built-in CIDR " + c + ": " + err.Error())
		}
		nets = append(nets, n)
	}
	return nets
}
