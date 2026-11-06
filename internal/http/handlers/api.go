package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		httperr.Write(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON body")
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeErr(w http.ResponseWriter, logger *slog.Logger, err error) {
	var limited *smsqueue.RateLimited
	switch {
	case errors.As(err, &limited):
		seconds := int(limited.RetryAfter.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		httperr.Write(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests")
	case errors.Is(err, auth.ErrInvalidPhone):
		httperr.Write(w, http.StatusUnprocessableEntity, "INVALID_PHONE", "Phone number is not a supported Turkmenistan mobile number")
	case errors.Is(err, auth.ErrInvalidCode):
		httperr.Write(w, http.StatusUnauthorized, "INVALID_CODE", "Invalid code")
	case errors.Is(err, auth.ErrBlocked):
		httperr.Write(w, http.StatusForbidden, "USER_BLOCKED", "This account cannot sign in")
	case errors.Is(err, auth.ErrUnauthorized), errors.Is(err, smsqueue.ErrUnauthorized):
		httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
	case errors.Is(err, auth.ErrValidation):
		httperr.Write(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Invalid request")
	case errors.Is(err, smsqueue.ErrNotFound):
		httperr.Write(w, http.StatusNotFound, "NOT_FOUND", "Resource not found")
	case errors.Is(err, smsqueue.ErrAlreadySent):
		httperr.Write(w, http.StatusConflict, "ALREADY_SENT", "The message was already accepted by the phone")
	case errors.Is(err, smsqueue.ErrConflict):
		httperr.Write(w, http.StatusConflict, "CONFLICT", "The message is not in a state that accepts this update")
	default:
		if logger != nil {
			logger.Error("request failed", "err_type", fmt.Sprintf("%T", err))
		}
		httperr.Write(w, http.StatusInternalServerError, "INTERNAL", "Internal server error")
	}
}
