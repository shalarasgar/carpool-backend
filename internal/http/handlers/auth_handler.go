package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// OTPService is the authentication flow used by the mobile client.
type OTPService interface {
	RequestOTP(ctx context.Context, phone, ip, deviceID, locale string) (string, error)
	VerifyOTP(ctx context.Context, phone, code, deviceID, locale string) (service.Session, error)
	Refresh(ctx context.Context, refreshToken, deviceID string) (service.Session, error)
	Logout(ctx context.Context, refreshToken string) error
}

// Auth serves phone OTP login.
type Auth struct {
	Logger  *slog.Logger
	Service OTPService
	DevMode bool
}

type otpRequest struct {
	Phone    string `json:"phone"`
	DeviceID string `json:"device_id"`
}

type verifyRequest struct {
	Phone    string `json:"phone"`
	Code     string `json:"code"`
	DeviceID string `json:"device_id"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

type sessionResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int          `json:"expires_in"`
	IsNewUser    bool         `json:"is_new_user"`
	User         userResponse `json:"user"`
}

type userResponse struct {
	ID     string `json:"id"`
	Phone  string `json:"phone"`
	Name   string `json:"name"`
	Locale string `json:"locale"`
}

// RequestOTP accepts a phone number and queues an SMS. The code is omitted unless DevMode is on.
func (h Auth) RequestOTP(w http.ResponseWriter, r *http.Request) {
	var body otpRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	code, err := h.Service.RequestOTP(r.Context(), body.Phone, clientIP(r), body.DeviceID, auth.Locale(r.Header.Get("Accept-Language")))
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	resp := map[string]string{"status": "accepted"}
	if h.DevMode && code != "" {
		resp["dev_code"] = code
	}
	httperr.WriteJSON(w, http.StatusAccepted, resp)
}

// VerifyOTP exchanges a code for a session.
func (h Auth) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var body verifyRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	session, err := h.Service.VerifyOTP(r.Context(), body.Phone, body.Code, body.DeviceID, auth.Locale(r.Header.Get("Accept-Language")))
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	writeSession(w, http.StatusOK, session)
}

// Refresh rotates a refresh token.
func (h Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	session, err := h.Service.Refresh(r.Context(), body.RefreshToken, body.DeviceID)
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	writeSession(w, http.StatusOK, session)
}

// Logout revokes a refresh token.
func (h Auth) Logout(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.Service.Logout(r.Context(), body.RefreshToken); err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeSession(w http.ResponseWriter, status int, session service.Session) {
	httperr.WriteJSON(w, status, sessionResponse{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresIn:    session.ExpiresIn,
		IsNewUser:    session.User.IsNew,
		User: userResponse{
			ID:     session.User.ID,
			Phone:  session.User.Phone,
			Name:   session.User.Name,
			Locale: session.User.Locale,
		},
	})
}
