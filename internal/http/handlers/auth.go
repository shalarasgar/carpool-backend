package handlers

import (
	"log/slog"
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Auth serves /v1/auth/*.
type Auth struct {
	Logger     *slog.Logger
	Service    *service.Auth
	TrustProxy bool
}

// RequestOTP handles POST /v1/auth/request-otp.
func (h Auth) RequestOTP(w http.ResponseWriter, r *http.Request) {
	var req dto.RequestOTPRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.Service.RequestOTP(r.Context(), service.RequestOTPInput{
		Phone:    req.Phone,
		IP:       clientIP(r, h.TrustProxy),
		DeviceID: req.DeviceID,
		Locale:   i18n.FromContext(r.Context()),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.RequestOTPResponse{
		ExpiresIn:  int(res.ExpiresIn.Seconds()),
		RetryAfter: int(res.RetryAfter.Seconds()),
		DevCode:    res.DevCode,
	})
}

// VerifyOTP handles POST /v1/auth/verify-otp.
func (h Auth) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req dto.VerifyOTPRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.Service.VerifyOTP(r.Context(), service.VerifyOTPInput{
		Phone:    req.Phone,
		Code:     req.Code,
		DeviceID: req.DeviceID,
		IP:       clientIP(r, h.TrustProxy),
		Locale:   i18n.FromContext(r.Context()),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.LoginResponse{
		AccessToken:  res.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(res.AccessExpiresIn.Seconds()),
		RefreshToken: res.RefreshToken,
		User:         toUserDTO(res.User),
		IsNewUser:    res.IsNewUser,
	})
}

// Refresh handles POST /v1/auth/refresh.
func (h Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	var req dto.RefreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	tokens, err := h.Service.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.TokenResponse{
		AccessToken:  tokens.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(tokens.AccessExpiresIn.Seconds()),
		RefreshToken: tokens.RefreshToken,
	})
}

// Logout handles POST /v1/auth/logout. It needs a valid access token and
// revokes every refresh token of that token's device.
func (h Auth) Logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.Service.Logout(ctx, middleware.UserIDFrom(ctx), middleware.DeviceIDFrom(ctx)); err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toUserDTO(u service.User) dto.User {
	return dto.User{
		ID:          u.ID,
		Phone:       u.Phone,
		Name:        u.Name,
		PhotoURL:    u.PhotoURL,
		RatingAvg:   u.RatingAvg,
		RatingCount: u.RatingCount,
		Locale:      u.Locale,
		CreatedAt:   u.CreatedAt,
	}
}
