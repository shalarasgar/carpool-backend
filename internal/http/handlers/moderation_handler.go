package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/ride"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Moderation serves user blocks and reports.
type Moderation struct {
	Logger  *slog.Logger
	Service *service.Moderation
}

// Block serves POST /v1/users/{id}/block.
func (h Moderation) Block(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.Block(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unblock serves DELETE /v1/users/{id}/block.
func (h Moderation) Unblock(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.Unblock(r.Context(), middleware.UserFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Blocked serves GET /v1/me/blocks: the users the caller blocked.
func (h Moderation) Blocked(w http.ResponseWriter, r *http.Request) {
	list, err := h.Service.Blocked(r.Context(), middleware.UserFrom(r.Context()))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[userSummaryDTO]{Items: mapList(list, toSummary)})
}

type userReportRequest struct {
	ReportedUserID string `json:"reported_user_id"`
	TripID         string `json:"trip_id"`
	Reason         string `json:"reason"`
	Details        string `json:"details"`
}

// Report serves POST /v1/reports.
func (h Moderation) Report(w http.ResponseWriter, r *http.Request) {
	var body userReportRequest
	if !decodeBody(w, r, &body) {
		return
	}
	id, err := h.Service.Report(r.Context(), ride.ReportInput{
		ReporterID:     middleware.UserFrom(r.Context()),
		ReportedUserID: body.ReportedUserID,
		TripID:         body.TripID,
		Reason:         ride.ReportReason(body.Reason),
		Details:        body.Details,
	})
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// AdminUsers serves the moderation queue, account bans, and operator-run account
// deletion. It is protected by the admin token.
type AdminUsers struct {
	Logger     *slog.Logger
	Moderation *service.Moderation
	Profiles   *service.Profiles
}

type reportDTO struct {
	ID              string     `json:"id"`
	ReporterID      string     `json:"reporter_id"`
	ReporterName    string     `json:"reporter_name"`
	ReportedUserID  string     `json:"reported_user_id"`
	ReportedName    string     `json:"reported_name"`
	ReportedBlocked bool       `json:"reported_blocked"`
	ReportedDeleted bool       `json:"reported_deleted"`
	TripID          string     `json:"trip_id,omitempty"`
	Reason          string     `json:"reason"`
	Details         string     `json:"details,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

func toReport(r ride.Report) reportDTO {
	return reportDTO{ID: r.ID, ReporterID: r.ReporterID, ReporterName: r.ReporterName,
		ReportedUserID: r.ReportedUserID, ReportedName: r.ReportedName, ReportedBlocked: r.ReportedBlocked,
		ReportedDeleted: r.ReportedDeleted, TripID: r.TripID, Reason: string(r.Reason), Details: r.Details,
		Status: string(r.Status), CreatedAt: r.CreatedAt, ResolvedAt: r.ResolvedAt}
}

// Reports serves GET /admin/v1/reports?status=open|resolved&page=&limit=. Without status
// it lists every report.
func (h AdminUsers) Reports(w http.ResponseWriter, r *http.Request) {
	list, more, err := h.Moderation.Reports(r.Context(), ride.ReportStatus(r.URL.Query().Get("status")),
		queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[reportDTO]{Items: mapList(list, toReport), HasMore: more})
}

// ResolveReport serves POST /admin/v1/reports/{id}/resolve.
func (h AdminUsers) ResolveReport(w http.ResponseWriter, r *http.Request) {
	if err := h.Moderation.Resolve(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BlockUser serves POST /admin/v1/users/{id}/block: the account can no longer sign in.
func (h AdminUsers) BlockUser(w http.ResponseWriter, r *http.Request) {
	h.setBlocked(w, r, true)
}

// UnblockUser serves POST /admin/v1/users/{id}/unblock.
func (h AdminUsers) UnblockUser(w http.ResponseWriter, r *http.Request) {
	h.setBlocked(w, r, false)
}

func (h AdminUsers) setBlocked(w http.ResponseWriter, r *http.Request, blocked bool) {
	if err := h.Moderation.SetBlocked(r.Context(), chi.URLParam(r, "id"), blocked); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type deleteAccountRequest struct {
	Phone string `json:"phone"`
}

// DeleteAccount serves POST /admin/v1/users/delete {phone}. An operator calls it for a
// deletion request received outside the app, after checking the requester owns the number.
// The phone travels in the body so it never reaches the request log.
func (h AdminUsers) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	var body deleteAccountRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if err := h.Profiles.DeleteAccountByPhone(r.Context(), body.Phone); err != nil {
		writeRideErr(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
