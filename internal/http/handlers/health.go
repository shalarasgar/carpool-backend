package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

const readinessTimeout = 2 * time.Second

// ReadyChecker is implemented by the health service.
type ReadyChecker interface {
	CheckReady(ctx context.Context) (service.Status, error)
}

// Health serves liveness and readiness probes.
type Health struct {
	Logger  *slog.Logger
	Checker ReadyChecker
}

type liveResponse struct {
	Status string `json:"status"`
}

type readyResponse struct {
	Status            string `json:"status"`
	Database          string `json:"database"`
	SMSGatewayHealthy bool   `json:"sms_gateway_healthy"`
}

// Live reports that the process is running. It does not touch the database.
func (h Health) Live(w http.ResponseWriter, _ *http.Request) {
	httperr.WriteJSON(w, http.StatusOK, liveResponse{Status: "ok"})
}

// Ready reports database connectivity. sms_gateway_healthy is informational
// and does not change the HTTP status.
func (h Health) Ready(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	status, err := h.Checker.CheckReady(ctx)
	if err != nil {
		logger.Warn("readiness check failed",
			"request_id", middleware.RequestIDFrom(r.Context()),
			"err", err,
		)
		httperr.WriteJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status:            "unavailable",
			Database:          "down",
			SMSGatewayHealthy: false,
		})
		return
	}

	httperr.WriteJSON(w, http.StatusOK, readyResponse{
		Status:            "ok",
		Database:          status.Database,
		SMSGatewayHealthy: status.SMSGatewayHealthy,
	})
}
