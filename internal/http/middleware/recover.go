package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/shalarasgar/carpool-backend/internal/http/errors"
)

// Recover converts a panic into a 500 error envelope and a structured log line.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				logger.Error("panic recovered",
					"request_id", RequestIDFrom(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"err", rec,
					"stack", string(debug.Stack()),
				)
				httperr.WriteCode(w, r, http.StatusInternalServerError, "INTERNAL")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
