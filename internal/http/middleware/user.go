package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
)

type userKey int

const userContextKey userKey = 3

// UserChecker rejects access tokens of users that were removed or blocked after issue.
type UserChecker interface {
	ActiveUser(ctx context.Context, id string) error
}

// UserAuth requires a valid access token and an active user.
func UserAuth(secret string, users UserChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locale := auth.Locale(r.Header.Get("Accept-Language"))
			id, err := auth.ParseAccess(secret, Bearer(r), time.Now())
			if err == nil {
				err = users.ActiveUser(r.Context(), id)
			}
			switch {
			case err == nil:
			case errors.Is(err, auth.ErrBlocked):
				httperr.Write(w, http.StatusForbidden, "USER_BLOCKED", i18n.Message(locale, "USER_BLOCKED"))
				return
			case errors.Is(err, auth.ErrUnauthorized):
				httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Message(locale, "UNAUTHORIZED"))
				return
			default:
				httperr.Write(w, http.StatusInternalServerError, "INTERNAL", i18n.Message(locale, "INTERNAL"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, id)))
		})
	}
}

// UserFrom returns the user id stored by UserAuth.
func UserFrom(ctx context.Context) string {
	id, _ := ctx.Value(userContextKey).(string)
	return id
}

// Limiter decides whether a key may make another request now.
// The in-memory implementation can be replaced by a shared store (for example Redis).
type Limiter interface {
	Allow(key string, now time.Time) (bool, time.Duration)
}

// WindowLimiter allows Max requests per key in each fixed Window.
type WindowLimiter struct {
	Max    int
	Window time.Duration

	mu      sync.Mutex
	buckets map[string]windowBucket
}

type windowBucket struct {
	start time.Time
	count int
}

// Allow counts one request for key.
func (l *WindowLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]windowBucket{}
	}
	if len(l.buckets) > 10000 {
		for k, b := range l.buckets {
			if now.Sub(b.start) >= l.Window {
				delete(l.buckets, k)
			}
		}
	}
	b := l.buckets[key]
	if now.Sub(b.start) >= l.Window {
		b = windowBucket{start: now}
	}
	if b.count >= l.Max {
		return false, b.start.Add(l.Window).Sub(now)
	}
	b.count++
	l.buckets[key] = b
	return true, 0
}

// RateLimit limits requests per signed-in user. Requests without a user are not limited
// here; OTP endpoints have their own limits in the database.
func RateLimit(l Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id := UserFrom(r.Context()); id != "" {
				if ok, wait := l.Allow(id, time.Now()); !ok {
					seconds := int(wait.Round(time.Second) / time.Second)
					if seconds < 1 {
						seconds = 1
					}
					w.Header().Set("Retry-After", strconv.Itoa(seconds))
					locale := auth.Locale(r.Header.Get("Accept-Language"))
					httperr.Write(w, http.StatusTooManyRequests, "RATE_LIMITED", i18n.Message(locale, "RATE_LIMITED"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
