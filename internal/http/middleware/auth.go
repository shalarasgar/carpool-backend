package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

const (
	userIDKey   contextKey = 2
	deviceIDKey contextKey = 3
	gatewayKey  contextKey = 4
)

// AccessTokenVerifier validates a bearer access token and rejects accounts
// that must not call authenticated routes (blocked or anonymized).
type AccessTokenVerifier interface {
	VerifyAccessToken(token string) (userID, deviceID string, err error)
	AccountStatus(ctx context.Context, userID string) error
}

// GatewayAuthenticator resolves a gateway API key.
type GatewayAuthenticator interface {
	Authenticate(ctx context.Context, apiKey string) (service.GatewayIdentity, error)
}

// RequireUser rejects requests without a valid "Authorization: Bearer <jwt>".
func RequireUser(verifier AccessTokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
				return
			}
			userID, deviceID, err := verifier.VerifyAccessToken(token)
			if err != nil {
				httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
				return
			}
			if err := verifier.AccountStatus(r.Context(), userID); err != nil {
				writeAccountError(w, r, err)
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, userID)
			ctx = context.WithValue(ctx, deviceIDKey, deviceID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireGateway rejects requests without a valid "Authorization: Bearer <api key>".
func RequireGateway(gateways GatewayAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := bearerToken(r)
			if !ok {
				httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
				return
			}
			gw, err := gateways.Authenticate(r.Context(), key)
			if err != nil {
				status := http.StatusUnauthorized
				if service.CodeOf(err) == "" {
					status = http.StatusInternalServerError
					httperr.WriteCode(w, r, status, "INTERNAL")
					return
				}
				httperr.WriteCode(w, r, status, "UNAUTHORIZED")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), gatewayKey, gw)))
		})
	}
}

// RequireAdmin rejects requests whose bearer token is not the static admin token.
func RequireAdmin(adminToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok || adminToken == "" || !auth.SecretsEqual(token, adminToken) {
				httperr.WriteCode(w, r, http.StatusForbidden, "FORBIDDEN")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UserIDFrom returns the authenticated user id, or "".
func UserIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// DeviceIDFrom returns the device id bound to the access token, or "".
func DeviceIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(deviceIDKey).(string)
	return id
}

// GatewayFrom returns the authenticated gateway.
func GatewayFrom(ctx context.Context) (service.GatewayIdentity, bool) {
	gw, ok := ctx.Value(gatewayKey).(service.GatewayIdentity)
	return gw, ok
}

func writeAccountError(w http.ResponseWriter, r *http.Request, err error) {
	switch service.CodeOf(err) {
	case "USER_BLOCKED":
		httperr.WriteCode(w, r, http.StatusForbidden, "USER_BLOCKED")
	case "":
		slog.Error("account status",
			"request_id", RequestIDFrom(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"err", err,
		)
		httperr.WriteCode(w, r, http.StatusInternalServerError, "INTERNAL")
	default:
		httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
	}
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}
