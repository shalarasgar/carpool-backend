package middleware

import "net/http"

// ActivityRecorder counts a request by a signed-in user. It must be cheap and must not
// keep the id anywhere but memory.
type ActivityRecorder interface {
	Seen(userID string)
}

// TrackActivity records the user that UserAuth put in the context. Mount it after
// UserAuth; requests without a user pass through uncounted.
func TrackActivity(rec ActivityRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id := UserFrom(r.Context()); id != "" {
				rec.Seen(id)
			}
			next.ServeHTTP(w, r)
		})
	}
}
