package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

const defaultPendingLimit = 10

// Gateway serves /gateway/v1/* for the Android SMS sender and
// /admin/v1/gateways for operators.
type Gateway struct {
	Logger  *slog.Logger
	Service *service.Gateway
}

// Pending handles GET /gateway/v1/pending?limit=10.
func (h Gateway) Pending(w http.ResponseWriter, r *http.Request) {
	gw, ok := middleware.GatewayFrom(r.Context())
	if !ok {
		httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	limit := defaultPendingLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			httperr.WriteCodeDetails(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]string{"limit": "min"})
			return
		}
		limit = n
	}
	messages, err := h.Service.ClaimPending(r.Context(), gw, limit)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.GatewayPendingResponse{Messages: make([]dto.GatewayMessage, 0, len(messages))}
	for _, m := range messages {
		out.Messages = append(out.Messages, dto.GatewayMessage{
			ID:          m.ID,
			Phone:       m.Phone,
			Message:     m.Message,
			Attempts:    m.Attempts,
			LockedUntil: m.LockedUntil,
			CreatedAt:   m.CreatedAt,
		})
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

// Report handles POST /gateway/v1/report.
func (h Gateway) Report(w http.ResponseWriter, r *http.Request) {
	gw, ok := middleware.GatewayFrom(r.Context())
	if !ok {
		httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	var req dto.GatewayReportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.Service.Report(r.Context(), gw, req.ID, service.ReportStatus(req.Status), req.Error)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.GatewayReportResponse{ID: req.ID, Status: res.Status, Attempts: res.Attempts})
}

// Heartbeat handles POST /gateway/v1/heartbeat.
func (h Gateway) Heartbeat(w http.ResponseWriter, r *http.Request) {
	gw, ok := middleware.GatewayFrom(r.Context())
	if !ok {
		httperr.WriteCode(w, r, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	if err := h.Service.Heartbeat(r.Context(), gw); err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.GatewayHeartbeatResponse{Gateway: gw.Name, ServerTime: time.Now().UTC()})
}

// Create handles POST /admin/v1/gateways.
func (h Gateway) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateGatewayRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := h.Service.Create(r.Context(), req.Name, req.OperatorPrefixes)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, dto.CreateGatewayResponse{
		ID:               created.ID,
		Name:             created.Name,
		APIKey:           created.APIKey,
		OperatorPrefixes: created.OperatorPrefixes,
		CreatedAt:        created.CreatedAt,
	})
}
