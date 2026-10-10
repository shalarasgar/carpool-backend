package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// maxBodyBytes bounds JSON request bodies. Every Phase B body is tiny.
const maxBodyBytes = 64 << 10

var validate = validator.New(validator.WithRequiredStructEnabled())

// decodeJSON reads and validates a JSON body. It writes the error response
// itself and returns false when the caller should stop.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httperr.WriteCode(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE")
			return false
		}
		httperr.WriteCode(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		httperr.WriteCode(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return false
	}
	if err := validate.Struct(dst); err != nil {
		httperr.WriteCodeDetails(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", validationDetails(err))
		return false
	}
	return true
}

func validationDetails(err error) map[string]string {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return nil
	}
	details := make(map[string]string, len(verrs))
	for _, fe := range verrs {
		field := fe.Field()
		if ns := fe.Namespace(); strings.Contains(ns, ".") {
			field = ns[strings.Index(ns, ".")+1:]
		}
		details[toSnake(field)] = fe.Tag()
	}
	return details
}

// toSnake converts Go field names such as DeviceID to device_id.
func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && (s[i-1] < 'A' || s[i-1] > 'Z') {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// statusByCode maps business codes to HTTP statuses.
var statusByCode = map[string]int{
	"INVALID_PHONE":          http.StatusUnprocessableEntity,
	"VALIDATION_ERROR":       http.StatusUnprocessableEntity,
	"OTP_INVALID":            http.StatusUnauthorized,
	"OTP_EXPIRED":            http.StatusUnauthorized,
	"OTP_ATTEMPTS_EXCEEDED":  http.StatusUnauthorized,
	"REFRESH_TOKEN_INVALID":  http.StatusUnauthorized,
	"REFRESH_TOKEN_EXPIRED":  http.StatusUnauthorized,
	"UNAUTHORIZED":           http.StatusUnauthorized,
	"USER_BLOCKED":           http.StatusForbidden,
	"FORBIDDEN":              http.StatusForbidden,
	"SMS_NOT_FOUND":          http.StatusNotFound,
	"NOT_FOUND":              http.StatusNotFound,
	"GATEWAY_EXISTS":         http.StatusConflict,
	"RATE_LIMITED":           http.StatusTooManyRequests,
	"VEHICLE_LIMIT":          http.StatusUnprocessableEntity,
	"PHOTO_TOO_LARGE":        http.StatusRequestEntityTooLarge,
	"PHOTO_TYPE_UNSUPPORTED": http.StatusUnprocessableEntity,
	"SEAT_UNAVAILABLE":       http.StatusConflict,
	"TRIP_NOT_FOUND":         http.StatusNotFound,
	"BOOKING_NOT_FOUND":      http.StatusNotFound,
	"INVALID_STATE":          http.StatusConflict,
	"BOOKING_EXISTS":         http.StatusConflict,
	"REVIEW_EXISTS":          http.StatusConflict,
	"REVIEW_WINDOW_CLOSED":   http.StatusConflict,
}

// writeServiceError translates a service error into the envelope. Unknown
// errors become 500 and are logged with the request id.
func writeServiceError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	var rl *service.RateLimitError
	if errors.As(err, &rl) {
		secs := int(rl.RetryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		httperr.WriteCode(w, r, http.StatusTooManyRequests, "RATE_LIMITED")
		return
	}
	var be *service.Error
	if errors.As(err, &be) {
		if status, ok := statusByCode[be.Code]; ok {
			httperr.WriteCodeDetails(w, r, status, be.Code, be.Details)
			return
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("request failed",
		"request_id", middleware.RequestIDFrom(r.Context()),
		"method", r.Method,
		"path", r.URL.Path,
		"err", err,
	)
	httperr.WriteCode(w, r, http.StatusInternalServerError, "INTERNAL")
}

// clientIP returns the caller address. With trustProxy it honors
// X-Forwarded-For / X-Real-IP set by a reverse proxy.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip.String()
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			if ip := net.ParseIP(xri); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
