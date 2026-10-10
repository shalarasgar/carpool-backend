package handlers

import (
	"log/slog"
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Reports serves POST /v1/reports.
type Reports struct {
	Logger  *slog.Logger
	Service *service.Reports
}

// Create handles POST /v1/reports.
func (h Reports) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateReportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	tripID := ""
	if req.TripID != nil {
		tripID = *req.TripID
	}
	report, err := h.Service.Create(r.Context(), middleware.UserIDFrom(r.Context()), service.ReportInput{
		ReportedUserID: req.ReportedUserID,
		TripID:         tripID,
		Reason:         req.Reason,
		Details:        req.Details,
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toReportDTO(report))
}

func toReportDTO(report service.Report) dto.Report {
	return dto.Report{
		ID:             report.ID,
		ReporterID:     report.ReporterID,
		ReportedUserID: report.ReportedUserID,
		TripID:         report.TripID,
		Reason:         report.Reason,
		Details:        report.Details,
		Status:         report.Status,
		CreatedAt:      report.CreatedAt,
	}
}
