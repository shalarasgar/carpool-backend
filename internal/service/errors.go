package service

import (
	"errors"
	"fmt"
	"time"
)

// Error is a business error with a stable machine-readable code. The HTTP
// layer maps codes to status lines and localized messages.
type Error struct {
	Code string
}

func (e *Error) Error() string { return e.Code }

// Sentinel business errors.
var (
	ErrInvalidPhone         = &Error{Code: "INVALID_PHONE"}
	ErrOTPInvalid           = &Error{Code: "OTP_INVALID"}
	ErrOTPExpired           = &Error{Code: "OTP_EXPIRED"}
	ErrOTPAttemptsExceeded  = &Error{Code: "OTP_ATTEMPTS_EXCEEDED"}
	ErrUserBlocked          = &Error{Code: "USER_BLOCKED"}
	ErrRefreshTokenInvalid  = &Error{Code: "REFRESH_TOKEN_INVALID"}
	ErrRefreshTokenExpired  = &Error{Code: "REFRESH_TOKEN_EXPIRED"}
	ErrUnauthorized         = &Error{Code: "UNAUTHORIZED"}
	ErrSMSNotFound          = &Error{Code: "SMS_NOT_FOUND"}
	ErrGatewayExists        = &Error{Code: "GATEWAY_EXISTS"}
	ErrValidation           = &Error{Code: "VALIDATION_ERROR"}
	errUserNotFoundInternal = errors.New("user not found")
)

// RateLimitError is returned when a limiter rejects a request.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
}

// CodeOf returns the business code of err, or an empty string for other errors.
func CodeOf(err error) string {
	var be *Error
	if errors.As(err, &be) {
		return be.Code
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return "RATE_LIMITED"
	}
	return ""
}
