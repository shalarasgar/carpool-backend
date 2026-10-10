package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Admin serves /admin/v1 besides gateway registration.
type Admin struct {
	Logger  *slog.Logger
	Service *service.Admin
}

// Reports handles GET /admin/v1/reports.
func (h Admin) Reports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.ListReports(r.Context(), q.Get("status"), service.PageQuery{
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.ReportPage{
		Reports:  make([]dto.Report, 0, len(page.Reports)),
		Page:     page.Page,
		PageSize: page.PageSize,
		HasMore:  page.HasMore,
	}
	for _, report := range page.Reports {
		out.Reports = append(out.Reports, toReportDTO(report))
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

// UpdateReport handles PATCH /admin/v1/reports/{id}.
func (h Admin) UpdateReport(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateReportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	report, err := h.Service.UpdateReportStatus(r.Context(), chi.URLParam(r, "id"), req.Status)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toReportDTO(report))
}

// BlockUser handles POST /admin/v1/users/{id}/block.
func (h Admin) BlockUser(w http.ResponseWriter, r *http.Request) {
	h.setBlocked(w, r, true)
}

// UnblockUser handles DELETE /admin/v1/users/{id}/block.
func (h Admin) UnblockUser(w http.ResponseWriter, r *http.Request) {
	h.setBlocked(w, r, false)
}

func (h Admin) setBlocked(w http.ResponseWriter, r *http.Request, blocked bool) {
	var (
		state service.UserModeration
		err   error
	)
	if blocked {
		state, err = h.Service.BlockUser(r.Context(), chi.URLParam(r, "id"))
	} else {
		state, err = h.Service.UnblockUser(r.Context(), chi.URLParam(r, "id"))
	}
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.UserModeration{ID: state.ID, IsBlocked: state.IsBlocked})
}

// SetLocation handles PATCH /admin/v1/locations/{id}.
func (h Admin) SetLocation(w http.ResponseWriter, r *http.Request) {
	var req dto.SetLocationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	state, err := h.Service.SetLocationActive(r.Context(), chi.URLParam(r, "id"), *req.IsActive)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.LocationState{ID: state.ID, Type: state.Type, IsActive: state.IsActive})
}

// Gateways handles GET /admin/v1/gateways.
func (h Admin) Gateways(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Service.Gateways(r.Context())
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.GatewayList{Gateways: make([]dto.GatewayHealth, 0, len(rows))}
	for _, row := range rows {
		out.Gateways = append(out.Gateways, dto.GatewayHealth{
			ID:               row.ID,
			Name:             row.Name,
			IsActive:         row.IsActive,
			Healthy:          row.Healthy,
			LastHeartbeatAt:  row.LastHeartbeatAt,
			OperatorPrefixes: row.OperatorPrefixes,
			CreatedAt:        row.CreatedAt,
		})
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

// SMSStats handles GET /admin/v1/sms/stats.
func (h Admin) SMSStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.Service.SMSStats(r.Context())
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.SMSStats{
		Pending:    stats.Pending,
		Processing: stats.Processing,
		Sent:       stats.Sent,
		Failed:     stats.Failed,
	})
}
