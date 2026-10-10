// Package dto holds request and response bodies of the HTTP API.
package dto

import "time"

// RequestOTPRequest is POST /v1/auth/request-otp. DeviceID is optional and
// only used for per-device rate limiting.
type RequestOTPRequest struct {
	Phone    string `json:"phone" validate:"required,max=32"`
	DeviceID string `json:"device_id,omitempty" validate:"omitempty,min=1,max=128"`
}

// RequestOTPResponse tells the client how long the code is valid and when it
// may request another one. DevCode is present only with DEV_MODE=true.
type RequestOTPResponse struct {
	ExpiresIn  int    `json:"expires_in"`
	RetryAfter int    `json:"retry_after"`
	DevCode    string `json:"dev_code,omitempty"`
}

// VerifyOTPRequest is POST /v1/auth/verify-otp.
type VerifyOTPRequest struct {
	Phone    string `json:"phone" validate:"required,max=32"`
	Code     string `json:"code" validate:"required,len=6,numeric"`
	DeviceID string `json:"device_id" validate:"required,min=1,max=128"`
}

// User is the caller's own profile.
type User struct {
	ID          string    `json:"id"`
	Phone       string    `json:"phone"`
	Name        string    `json:"name"`
	PhotoURL    *string   `json:"photo_url"`
	RatingAvg   float64   `json:"rating_avg"`
	RatingCount int       `json:"rating_count"`
	Locale      string    `json:"locale"`
	CreatedAt   time.Time `json:"created_at"`
}

// LoginResponse is the verify-otp result.
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	User         User   `json:"user"`
	IsNewUser    bool   `json:"is_new_user"`
}

// RefreshRequest is POST /v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required,max=256"`
}

// TokenResponse is the refresh result.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}
