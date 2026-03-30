package relay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Executor — performs outbound HTTP requests respecting the Policy
// ─────────────────────────────────────────────────────────────────────────────

// RequestSpec is the resolved (post-interpolation) description of an outbound
// HTTP request.  All {{variable}} placeholders must have been expanded before
// constructing a RequestSpec.
type RequestSpec struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// ResponseResult holds the outcome of executing a RequestSpec.
type ResponseResult struct {
	Status     int
	Headers    map[string]string
	Body       []byte
	DurationMS int64
}

// Executor executes outbound HTTP requests using the constraints in Policy.
type Executor struct {
	policy Policy
	client *http.Client
}

// NewExecutor creates an Executor wired to the provided policy.
// A dedicated http.Client is constructed with redirect and TLS settings
// derived from the policy.
func NewExecutor(p Policy) *Executor {
	redirectLimit := p.MaxRedirects

	client := &http.Client{
		// Redirect policy: honour the limit; never strip the Authorization
		// header across same-host redirects.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= redirectLimit {
				return fmt.Errorf("stopped after %d redirects", redirectLimit)
			}
			return nil
		},
		// Overall transport timeout is handled by the per-request context;
		// the transport itself has no idle-connection timeout set here so
		// that the executor remains stateless w.r.t. connection pools.
	}

	return &Executor{policy: p, client: client}
}

// Execute validates spec against the policy and performs the HTTP request.
// It returns a ResponseResult on success, or an error classified with
// enough context for the gRPC service to map to the correct status code.
//
// Callers are responsible for applying variable interpolation before calling
// Execute; this function treats all fields in spec as fully resolved.
func (e *Executor) Execute(ctx context.Context, spec RequestSpec) (*ResponseResult, error) {
	// ── 1. Policy validation ─────────────────────────────────────────────────
	if err := e.policy.ValidateURL(spec.URL); err != nil {
		return nil, &PolicyViolationError{Cause: err, Host: extractHost(spec.URL)}
	}

	if err := validateMethod(spec.Method); err != nil {
		return nil, err
	}

	if int64(len(spec.Body)) > e.policy.MaxRequestBodyBytes {
		return nil, fmt.Errorf("request body exceeds limit of %d bytes", e.policy.MaxRequestBodyBytes)
	}

	// ── 2. Build http.Request ────────────────────────────────────────────────
	var bodyReader io.Reader
	if len(spec.Body) > 0 {
		bodyReader = bytes.NewReader(spec.Body)
	}

	timeout := time.Duration(e.policy.RequestTimeoutSecs) * time.Second
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, strings.ToUpper(spec.Method), spec.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	for k, v := range spec.Headers {
		req.Header.Set(k, v)
	}

	// ── 3. Execute ───────────────────────────────────────────────────────────
	start := time.Now()
	resp, err := e.client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return nil, &TimeoutError{
				URL:        spec.URL,
				DurationMS: elapsed,
			}
		}
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// ── 4. Read response (size-limited) ─────────────────────────────────────
	limitedReader := io.LimitReader(resp.Body, e.policy.MaxResponseBodyBytes)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// ── 5. Normalise headers ─────────────────────────────────────────────────
	headers := make(map[string]string, len(resp.Header))
	for k, vs := range resp.Header {
		headers[strings.ToLower(k)] = strings.Join(vs, ", ")
	}

	return &ResponseResult{
		Status:     resp.StatusCode,
		Headers:    headers,
		Body:       body,
		DurationMS: elapsed,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Typed errors (used by the gRPC service for status code mapping)
// ─────────────────────────────────────────────────────────────────────────────

// PolicyViolationError is returned when a request is blocked by the security
// policy (maps to gRPC PERMISSION_DENIED).
type PolicyViolationError struct {
	Cause error
	Host  string
}

func (e *PolicyViolationError) Error() string {
	return fmt.Sprintf("policy violation for host %q: %v", e.Host, e.Cause)
}
func (e *PolicyViolationError) Unwrap() error { return e.Cause }

// TimeoutError is returned when the outbound request deadline is exceeded
// (maps to gRPC DEADLINE_EXCEEDED).
type TimeoutError struct {
	URL        string
	DurationMS int64
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("request to %q timed out after %dms", e.URL, e.DurationMS)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

var allowedMethods = map[string]struct{}{
	"GET":     {},
	"POST":    {},
	"PUT":     {},
	"PATCH":   {},
	"DELETE":  {},
	"HEAD":    {},
	"OPTIONS": {},
}

func validateMethod(method string) error {
	if _, ok := allowedMethods[strings.ToUpper(method)]; !ok {
		return fmt.Errorf("HTTP method %q is not allowed", method)
	}
	return nil
}

func extractHost(rawURL string) string {
	if idx := strings.Index(rawURL, "://"); idx >= 0 {
		rest := rawURL[idx+3:]
		if end := strings.IndexAny(rest, "/?#"); end >= 0 {
			return rest[:end]
		}
		return rest
	}
	return rawURL
}
