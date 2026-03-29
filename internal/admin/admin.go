package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// ui holds the compiled admin SPA produced by `npm run build`.
// The `ui/` subdirectory is populated by scripts/build.sh before `go build`
// runs, so it is never committed to source control.
//
//go:embed ui
var ui embed.FS

// apiPrefixes are the URL prefixes that should be forwarded to the notx API
// server rather than served from the embedded FS.
var apiPrefixes = []string{
	"/v1/",
	"/healthz",
	"/readyz",
}

func isAPIPath(path string) bool {
	for _, p := range apiPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// Handler returns an http.Handler that:
//
//  1. Forwards /v1/*, /healthz, /readyz to the notx API server at apiBase.
//  2. Serves real embedded files (JS/CSS/favicon…) verbatim.
//  3. Falls back to index.html for every other path so the React client-side
//     router can handle deep links.
func Handler(apiBase string) http.Handler {
	// ── Reverse proxy to the notx API server ─────────────────────────────────
	target, err := url.Parse(apiBase)
	if err != nil {
		panic("admin: invalid API base URL " + apiBase + ": " + err.Error())
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Rewrite the Host header so the upstream sees its own host, not the
	// admin server's host.
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
	}

	// ── Embedded SPA file server ──────────────────────────────────────────────
	sub, err := fs.Sub(ui, "ui")
	if err != nil {
		panic("admin: failed to sub embed FS: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. API paths → reverse-proxy to the notx server.
		if isAPIPath(r.URL.Path) {
			proxy.ServeHTTP(w, r)
			return
		}

		// 2. Known static file → serve from embedded FS.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := sub.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// 3. Fallback → index.html (client-side router takes over).
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
