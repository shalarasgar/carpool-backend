package middleware

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/staff"
)

type staffContextKey struct{}

// StaffAuthenticator resolves a panel session token.
type StaffAuthenticator interface {
	Authenticate(ctx context.Context, token string) (staff.Member, error)
}

// StaffAuth admits the static admin token (as staff.TokenActor) or a staff session
// token, and puts the actor in the context. Anything else gets 401.
func StaffAuth(adminToken string, sessions StaffAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := Bearer(r)
			var actor staff.Actor
			switch {
			case token == "":
				httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
				return
			case adminToken != "" && auth.SecretEqual(token, adminToken):
				actor = staff.TokenActor
			case sessions == nil:
				httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
				return
			default:
				m, err := sessions.Authenticate(r.Context(), token)
				switch {
				case errors.Is(err, staff.ErrUnauthorized):
					httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
					return
				case err != nil:
					httperr.Write(w, http.StatusInternalServerError, "INTERNAL", "Internal server error")
					return
				}
				actor = staff.Actor{ID: m.ID, Name: m.Name, Role: m.Role}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), staffContextKey{}, actor)))
		})
	}
}

// StaffFrom returns the actor stored by StaffAuth.
func StaffFrom(ctx context.Context) staff.Actor {
	a, _ := ctx.Value(staffContextKey{}).(staff.Actor)
	return a
}

// RequireAdmin answers 403 FORBIDDEN to a moderator. Mount it after StaffAuth.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !StaffFrom(r.Context()).IsAdmin() {
			httperr.Write(w, http.StatusForbidden, "FORBIDDEN", "This action needs the admin role")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LimitByIP lets each client address through l (sign-in attempts). Mount it after the
// proxy-address middleware so the address is the real client.
func LimitByIP(l Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if ok, wait := l.Allow("ip:"+host, time.Now()); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(wait.Round(time.Second)/time.Second))))
				httperr.Write(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
