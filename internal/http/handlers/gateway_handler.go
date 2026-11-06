package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/smsqueue"
)

// GatewayService is the phone-facing queue.
type GatewayService interface {
	Heartbeat(ctx context.Context, gatewayID string) error
	Claim(ctx context.Context, claimer smsqueue.Gateway, limit int) ([]smsqueue.Job, error)
	MarkSending(ctx context.Context, gatewayID, jobID string) error
	Report(ctx context.Context, gatewayID string, in smsqueue.Report) error
	CreateGateway(ctx context.Context, name string, prefixes []string) (smsqueue.Gateway, string, error)
	Gateways(ctx context.Context) ([]smsqueue.Gateway, error)
	RevokeGateway(ctx context.Context, id string) error
	Stats(ctx context.Context) (map[string]int, []smsqueue.Gateway, error)
}

// Gateway serves the authenticated SMS phone API.
type Gateway struct {
	Logger  *slog.Logger
	Service GatewayService
}

type reportRequest struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Error        string `json:"error"`
	NotSubmitted bool   `json:"not_submitted"`
}

type sendingRequest struct {
	ID string `json:"id"`
}

type pendingResponse struct {
	Messages []pendingMessage `json:"messages"`
}

type pendingMessage struct {
	ID        string    `json:"id"`
	Phone     string    `json:"phone"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Pending claims jobs for the authenticated gateway.
func (h Gateway) Pending(w http.ResponseWriter, r *http.Request) {
	limit := 1
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 10 {
			httperr.Write(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "limit must be between 1 and 10")
			return
		}
		limit = n
	}
	jobs, err := h.Service.Claim(r.Context(), middleware.GatewayFrom(r.Context()), limit)
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	resp := pendingResponse{Messages: []pendingMessage{}}
	for _, job := range jobs {
		resp.Messages = append(resp.Messages, pendingMessage{
			ID:        job.ID,
			Phone:     job.Phone,
			Message:   job.Message,
			CreatedAt: job.CreatedAt,
		})
	}
	httperr.WriteJSON(w, http.StatusOK, resp)
}

// Sending records that the phone is about to hand the message to the radio.
func (h Gateway) Sending(w http.ResponseWriter, r *http.Request) {
	var body sendingRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID == "" {
		httperr.Write(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "id is required")
		return
	}
	gw := middleware.GatewayFrom(r.Context())
	if err := h.Service.MarkSending(r.Context(), gw.ID, body.ID); err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Report stores the telephony or carrier result. sent is not a carrier delivery receipt.
func (h Gateway) Report(w http.ResponseWriter, r *http.Request) {
	var body reportRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID == "" || body.Status == "" {
		httperr.Write(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "id and status are required")
		return
	}
	gw := middleware.GatewayFrom(r.Context())
	err := h.Service.Report(r.Context(), gw.ID, smsqueue.Report{
		ID:           body.ID,
		Status:       body.Status,
		Error:        body.Error,
		NotSubmitted: body.NotSubmitted,
	})
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Heartbeat refreshes last_heartbeat_at without claiming a job.
func (h Gateway) Heartbeat(w http.ResponseWriter, r *http.Request) {
	gw := middleware.GatewayFrom(r.Context())
	if err := h.Service.Heartbeat(r.Context(), gw.ID); err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type gatewayRequest struct {
	Name     string   `json:"name"`
	Prefixes []string `json:"operator_prefixes"`
}

type gatewayResponse struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Prefixes      []string   `json:"operator_prefixes"`
	Active        bool       `json:"is_active"`
	LastHeartbeat *time.Time `json:"last_heartbeat_at"`
	CreatedAt     time.Time  `json:"created_at"`
	APIKey        string     `json:"api_key,omitempty"`
}

// Admin serves gateway registration. It is protected by the admin token.
type Admin struct {
	Logger  *slog.Logger
	Service GatewayService
}

// CreateGateway returns a new API key once.
func (h Admin) CreateGateway(w http.ResponseWriter, r *http.Request) {
	var body gatewayRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	gw, raw, err := h.Service.CreateGateway(r.Context(), body.Name, body.Prefixes)
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	resp := toGateway(gw)
	resp.APIKey = raw
	httperr.WriteJSON(w, http.StatusCreated, resp)
}

// ListGateways returns gateway health without API keys.
func (h Admin) ListGateways(w http.ResponseWriter, r *http.Request) {
	list, err := h.Service.Gateways(r.Context())
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	out := make([]gatewayResponse, 0, len(list))
	for _, gw := range list {
		out = append(out, toGateway(gw))
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]any{"gateways": out})
}

// RevokeGateway disables a phone.
func (h Admin) RevokeGateway(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.RevokeGateway(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Stats returns queue counts and gateway rows.
func (h Admin) Stats(w http.ResponseWriter, r *http.Request) {
	counts, list, err := h.Service.Stats(r.Context())
	if err != nil {
		writeErr(w, h.Logger, err)
		return
	}
	out := make([]gatewayResponse, 0, len(list))
	for _, gw := range list {
		out = append(out, toGateway(gw))
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]any{"counts": counts, "gateways": out})
}

func toGateway(gw smsqueue.Gateway) gatewayResponse {
	resp := gatewayResponse{
		ID:        gw.ID,
		Name:      gw.Name,
		Prefixes:  gw.Prefixes,
		Active:    gw.Active,
		CreatedAt: gw.CreatedAt,
	}
	if resp.Prefixes == nil {
		resp.Prefixes = []string{}
	}
	if !gw.LastHeartbeat.IsZero() {
		beat := gw.LastHeartbeat
		resp.LastHeartbeat = &beat
	}
	return resp
}
