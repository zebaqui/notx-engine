package pairing

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// ipLimiterEntry holds a token-bucket rate limiter for a single remote IP and
// the last time it was seen (for sweeping stale entries).
type ipLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// BootstrapRateLimiter is a gRPC UnaryServerInterceptor that enforces:
//   - A per-IP token-bucket rate limit (perIPRate events/sec, perIPBurst burst).
//   - A global token-bucket rate limit across all IPs (globalRate/globalBurst).
//
// Requests that exceed either limit receive codes.ResourceExhausted.
// Stale per-IP entries are swept on a background goroutine every sweepInterval.
//
// Call Stop() to release the background goroutine when the server shuts down.
type BootstrapRateLimiter struct {
	perIPRate  rate.Limit
	perIPBurst int
	global     *rate.Limiter

	mu       sync.Mutex
	limiters map[string]*ipLimiterEntry

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewBootstrapRateLimiter creates a BootstrapRateLimiter and starts the
// background sweep goroutine.
//
//   - perIPRate:     token refill rate per second per IP (e.g. 5.0/60 for 5/min)
//   - perIPBurst:    max burst per IP
//   - globalRate:    token refill rate per second across all IPs
//   - globalBurst:   max global burst
//   - sweepInterval: how often to remove stale IP entries (e.g. 5*time.Minute)
func NewBootstrapRateLimiter(
	perIPRate rate.Limit,
	perIPBurst int,
	globalRate rate.Limit,
	globalBurst int,
	sweepInterval time.Duration,
) *BootstrapRateLimiter {
	rl := &BootstrapRateLimiter{
		perIPRate:  perIPRate,
		perIPBurst: perIPBurst,
		global:     rate.NewLimiter(globalRate, globalBurst),
		limiters:   make(map[string]*ipLimiterEntry),
		stopCh:     make(chan struct{}),
	}
	go rl.sweepLoop(sweepInterval)
	return rl
}

// Stop signals the background sweep goroutine to exit.
// It is safe to call Stop multiple times; only the first call has any effect.
func (rl *BootstrapRateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.stopCh) })
}

// Interceptor returns a gRPC UnaryServerInterceptor that enforces rate limits.
func (rl *BootstrapRateLimiter) Interceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		// Global limit first.
		if !rl.global.Allow() {
			return nil, status.Errorf(codes.ResourceExhausted,
				"server is temporarily overloaded; please retry later")
		}

		// Per-IP limit.
		ip := remoteIP(ctx)
		if ip != "" {
			limiter := rl.getOrCreate(ip)
			if !limiter.Allow() {
				return nil, status.Errorf(codes.ResourceExhausted,
					"too many requests from this address; please retry later")
			}
		}

		return handler(ctx, req)
	}
}

func (rl *BootstrapRateLimiter) getOrCreate(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	entry, ok := rl.limiters[ip]
	if !ok {
		entry = &ipLimiterEntry{
			limiter: rate.NewLimiter(rl.perIPRate, rl.perIPBurst),
		}
		rl.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

func (rl *BootstrapRateLimiter) sweepLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.sweep()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *BootstrapRateLimiter) sweep() {
	cutoff := time.Now().Add(-10 * time.Minute)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, entry := range rl.limiters {
		if entry.lastSeen.Before(cutoff) {
			delete(rl.limiters, ip)
		}
	}
}

// remoteIP extracts the remote IP (without port) from a gRPC context.
func remoteIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}

// isLoopback reports whether addr is a loopback address (IPv4 or IPv6).
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
