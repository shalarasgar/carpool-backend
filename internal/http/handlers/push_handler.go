package handlers

import (
	"log/slog"
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Push serves device registration for push notifications.
type Push struct {
	Logger *slog.Logger
	Tokens *service.PushTokens
}

type pushTokenRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// Register serves PUT /v1/me/push-token {token, platform}.
func (h Push) Register(w http.ResponseWriter, r *http.Request) {
	var body pushTokenRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if err := h.Tokens.Register(r.Context(), middleware.UserFrom(r.Context()), body.Token, body.Platform); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unregister serves DELETE /v1/me/push-token {token}.
func (h Push) Unregister(w http.ResponseWriter, r *http.Request) {
	var body pushTokenRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if err := h.Tokens.Unregister(r.Context(), middleware.UserFrom(r.Context()), body.Token); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
