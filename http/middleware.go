package http

import (
	"context"
	"log/slog"
	"net/http"
)

// withMiddleware wraps a handler with logging and panic recovery.
func (h *Handler) withMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Inject a request-scoped logger.
		log := h.log.With(
			"method", r.Method,
			"path", r.URL.Path,
		)
		ctx := context.WithValue(r.Context(), ctxKeyLogger{}, log)
		next(w, r.WithContext(ctx))
	}
}

type ctxKeyLogger struct{}

func loggerFromCtx(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return fallback
}
