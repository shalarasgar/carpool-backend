package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

type gatewayKey int

const gatewayContextKey gatewayKey = 2

// GatewayAuthenticator resolves a bearer API key.
type GatewayAuthenticator interface {
	Authenticate(ctx context.Context, rawKey string) (smsqueue.Gateway, error)
}

// Bearer returns the token from an Authorization header.
func Bearer(r *http.Request) string {
	value := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}

// GatewayAuth requires an active gateway API key.
func GatewayAuth(authn GatewayAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gw, err := authn.Authenticate(r.Context(), Bearer(r))
			if err != nil {
				var limited *smsqueue.RateLimited
				if errors.As(err, &limited) {
					seconds := int(limited.RetryAfter.Round(time.Second) / time.Second)
					if seconds < 1 {
						seconds = 1
					}
					w.Header().Set("Retry-After", strconv.Itoa(seconds))
					httperr.Write(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests")
					return
				}
				httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
				return
			}
			ctx := context.WithValue(r.Context(), gatewayContextKey, gw)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GatewayFrom returns the gateway stored by GatewayAuth.
func GatewayFrom(ctx context.Context) smsqueue.Gateway {
	gw, _ := ctx.Value(gatewayContextKey).(smsqueue.Gateway)
	return gw
}

// AdminAuth compares the bearer token with the static admin token.
func AdminAuth(adminToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if adminToken == "" || !auth.SecretEqual(Bearer(r), adminToken) {
				httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
