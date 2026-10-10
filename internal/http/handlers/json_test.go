package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		status int
		code   string
		detail map[string]string
	}{
		{"valid", `{"phone":"+99365123456","code":"123456","device_id":"d"}`, 0, "", nil},
		{"malformed", `{"phone":`, http.StatusBadRequest, "BAD_REQUEST", nil},
		{"unknown field", `{"phone":"+99365123456","code":"123456","device_id":"d","x":1}`, http.StatusBadRequest, "BAD_REQUEST", nil},
		{"trailing data", `{"phone":"+99365123456","code":"123456","device_id":"d"} {}`, http.StatusBadRequest, "BAD_REQUEST", nil},
		{"missing", `{}`, http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]string{"phone": "required", "code": "required", "device_id": "required"}},
		{"bad code", `{"phone":"+99365123456","code":"12a456","device_id":"d"}`, http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]string{"code": "numeric"}},
		{"too large", `{"phone":"` + strings.Repeat("1", maxBodyBytes+1) + `"}`, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			var dst dto.VerifyOTPRequest
			ok := decodeJSON(rec, req, &dst)
			if tc.status == 0 {
				require.True(t, ok)
				require.Equal(t, "+99365123456", dst.Phone)
				return
			}
			require.False(t, ok)
			require.Equal(t, tc.status, rec.Code)
			var env httperr.Envelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			require.Equal(t, tc.code, env.Error.Code)
			if tc.detail != nil {
				require.Equal(t, tc.detail, env.Error.Details)
			}
		})
	}
}

func TestWriteServiceError(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrInvalidPhone, http.StatusUnprocessableEntity, "INVALID_PHONE"},
		{service.ErrOTPInvalid, http.StatusUnauthorized, "OTP_INVALID"},
		{service.ErrOTPExpired, http.StatusUnauthorized, "OTP_EXPIRED"},
		{service.ErrOTPAttemptsExceeded, http.StatusUnauthorized, "OTP_ATTEMPTS_EXCEEDED"},
		{service.ErrUserBlocked, http.StatusForbidden, "USER_BLOCKED"},
		{service.ErrRefreshTokenInvalid, http.StatusUnauthorized, "REFRESH_TOKEN_INVALID"},
		{service.ErrRefreshTokenExpired, http.StatusUnauthorized, "REFRESH_TOKEN_EXPIRED"},
		{service.ErrSMSNotFound, http.StatusNotFound, "SMS_NOT_FOUND"},
		{service.ErrGatewayExists, http.StatusConflict, "GATEWAY_EXISTS"},
		{&service.RateLimitError{RetryAfter: 42 * time.Second}, http.StatusTooManyRequests, "RATE_LIMITED"},
		{errors.New("pg: connection reset"), http.StatusInternalServerError, "INTERNAL"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		writeServiceError(rec, req, logger, tc.err)
		require.Equal(t, tc.status, rec.Code, tc.code)
		var env httperr.Envelope
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
		require.Equal(t, tc.code, env.Error.Code)
		require.NotEmpty(t, env.Error.Message)
		if tc.code == "RATE_LIMITED" {
			require.Equal(t, "42", rec.Header().Get("Retry-After"))
		}
		if tc.code == "INTERNAL" {
			require.NotContains(t, rec.Body.String(), "connection reset", "internal details are not exposed")
		}
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.5:51234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	req.Header.Set("X-Real-IP", "203.0.113.8")

	require.Equal(t, "10.0.0.5", clientIP(req, false), "proxy headers ignored by default")
	require.Equal(t, "203.0.113.7", clientIP(req, true))

	req.Header.Del("X-Forwarded-For")
	require.Equal(t, "203.0.113.8", clientIP(req, true))

	req.Header.Set("X-Real-IP", "not-an-ip")
	require.Equal(t, "10.0.0.5", clientIP(req, true), "garbage headers fall back to the socket address")
}

func TestToSnake(t *testing.T) {
	t.Parallel()
	require.Equal(t, "device_id", toSnake("DeviceID"))
	require.Equal(t, "phone", toSnake("Phone"))
	require.Equal(t, "operator_prefixes", toSnake("OperatorPrefixes"))
	require.Equal(t, "refresh_token", toSnake("RefreshToken"))
}
