package middleware

import (
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/i18n"
)

// Locale resolves Accept-Language into a supported locale on the context and
// echoes it in Content-Language.
func Locale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.Resolve(r.Header.Get("Accept-Language"))
		w.Header().Set("Content-Language", locale)
		next.ServeHTTP(w, r.WithContext(i18n.WithLocale(r.Context(), locale)))
	})
}
