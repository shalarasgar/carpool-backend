package service

import (
	"errors"
	"fmt"
	"time"
)

// Error is a business error with a stable machine-readable code. The HTTP
// layer maps codes to status lines and localized messages. Details carries
// field-level validation hints (field name to failed rule).
type Error struct {
	Code    string
	Details map[string]string
}

func (e *Error) Error() string { return e.Code }

// Is matches business errors by code so errors.Is works for sentinels.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other != nil && e.Code == other.Code
}

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
	ErrNotFound             = &Error{Code: "NOT_FOUND"}
	ErrVehicleLimit         = &Error{Code: "VEHICLE_LIMIT"}
	ErrPhotoTooLarge        = &Error{Code: "PHOTO_TOO_LARGE"}
	ErrPhotoType            = &Error{Code: "PHOTO_TYPE_UNSUPPORTED"}
	ErrForbidden            = &Error{Code: "FORBIDDEN"}
	ErrSeatUnavailable      = &Error{Code: "SEAT_UNAVAILABLE"}
	ErrTripNotFound         = &Error{Code: "TRIP_NOT_FOUND"}
	ErrBookingNotFound      = &Error{Code: "BOOKING_NOT_FOUND"}
	ErrInvalidState         = &Error{Code: "INVALID_STATE"}
	ErrBookingExists        = &Error{Code: "BOOKING_EXISTS"}
	ErrReviewExists         = &Error{Code: "REVIEW_EXISTS"}
	ErrReviewWindowClosed   = &Error{Code: "REVIEW_WINDOW_CLOSED"}
	errUserNotFoundInternal = errors.New("user not found")
)

func validationError(details map[string]string) *Error {
	return &Error{Code: "VALIDATION_ERROR", Details: details}
}

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
