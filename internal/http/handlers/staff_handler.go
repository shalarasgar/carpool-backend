package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/staff"
)

// StaffHandler serves panel sign-in, the staff list and the audit log.
type StaffHandler struct {
	Logger  *slog.Logger
	Service *service.Staff
}

type staffDTO struct {
	ID          string     `json:"id,omitempty"`
	Login       string     `json:"login,omitempty"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	LastLoginAt *time.Time `json:"last_login_at"`
	Disabled    bool       `json:"disabled"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
	// ViaToken marks the static ADMIN_TOKEN, which has no account.
	ViaToken bool `json:"via_token,omitempty"`
}

func toStaff(m staff.Member) staffDTO {
	d := staffDTO{ID: m.ID, Login: m.Login, Name: m.Name, Role: string(m.Role), LastLoginAt: m.LastLoginAt,
		Disabled: m.DisabledAt != nil, DisabledAt: m.DisabledAt}
	if !m.CreatedAt.IsZero() {
		created := m.CreatedAt
		d.CreatedAt = &created
	}
	return d
}

func writeStaffErr(w http.ResponseWriter, logger *slog.Logger, err error) {
	var field *staff.FieldError
	switch {
	case errors.As(err, &field):
		httperr.WriteJSON(w, http.StatusUnprocessableEntity, httperr.Envelope{Error: httperr.Body{
			Code: "VALIDATION_ERROR", Message: "Invalid " + field.Field,
			Field: field.Field, Details: map[string]string{"reason": field.Reason},
		}})
	case errors.Is(err, staff.ErrInvalidCredentials):
		httperr.Write(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Wrong login or password")
	case errors.Is(err, staff.ErrUnauthorized):
		httperr.Write(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
	case errors.Is(err, staff.ErrForbidden):
		httperr.Write(w, http.StatusForbidden, "FORBIDDEN", "This action needs the admin role")
	case errors.Is(err, staff.ErrNotFound):
		httperr.Write(w, http.StatusNotFound, "NOT_FOUND", "Resource not found")
	case errors.Is(err, staff.ErrLoginTaken):
		httperr.Write(w, http.StatusConflict, "LOGIN_TAKEN", "This login is taken")
	case errors.Is(err, staff.ErrLastAdmin):
		httperr.Write(w, http.StatusConflict, "LAST_ADMIN", "At least one active admin must remain")
	case errors.Is(err, staff.ErrSelf):
		httperr.Write(w, http.StatusConflict, "SELF_CHANGE", "You cannot change your own role or access")
	default:
		if logger != nil {
			logger.Error("staff request failed", "err", err)
		}
		httperr.Write(w, http.StatusInternalServerError, "INTERNAL", "Internal server error")
	}
}

// Login opens a panel session: {login, password} → {token, expires_at, staff}.
func (h StaffHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s, err := h.Service.Login(r.Context(), body.Login, body.Password)
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]any{
		"token": s.Token, "expires_at": s.ExpiresAt, "staff": toStaff(s.Member),
	})
}

// Logout ends the session in use. With the static token there is nothing to end.
func (h StaffHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if middleware.StaffFrom(r.Context()).ID != "" {
		if err := h.Service.Logout(r.Context(), middleware.Bearer(r)); err != nil {
			writeStaffErr(w, h.Logger, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the signed-in account.
func (h StaffHandler) Me(w http.ResponseWriter, r *http.Request) {
	a := middleware.StaffFrom(r.Context())
	m, err := h.Service.Me(r.Context(), a)
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	d := toStaff(m)
	d.ViaToken = a.ID == ""
	httperr.WriteJSON(w, http.StatusOK, d)
}

// ChangePassword: {current_password, new_password} → 204. Other sessions end.
func (h StaffHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	err := h.Service.ChangeOwnPassword(r.Context(), middleware.StaffFrom(r.Context()), middleware.Bearer(r),
		body.Current, body.New)
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List returns every staff account.
func (h StaffHandler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.Service.List(r.Context(), middleware.StaffFrom(r.Context()))
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	out := make([]staffDTO, 0, len(list))
	for _, m := range list {
		out = append(out, toStaff(m))
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Create: {login, name, role, password} → 201 staff.
func (h StaffHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login    string `json:"login"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	m, err := h.Service.Create(r.Context(), middleware.StaffFrom(r.Context()), service.NewMember{
		Login: body.Login, Name: body.Name, Role: staff.Role(body.Role), Password: body.Password,
	})
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toStaff(m))
}

// Update: {name?, role?, disabled?} → staff.
func (h StaffHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     *string `json:"name"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p := staff.Update{Name: body.Name, Disabled: body.Disabled}
	if body.Role != nil {
		role := staff.Role(*body.Role)
		p.Role = &role
	}
	m, err := h.Service.Update(r.Context(), middleware.StaffFrom(r.Context()), chi.URLParam(r, "id"), p)
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toStaff(m))
}

// ResetPassword: {password} → 204. The member's sessions end.
func (h StaffHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	err := h.Service.ResetPassword(r.Context(), middleware.StaffFrom(r.Context()), chi.URLParam(r, "id"), body.Password)
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type auditDTO struct {
	ID           int64     `json:"id"`
	StaffID      string    `json:"staff_id,omitempty"`
	StaffName    string    `json:"staff_name"`
	Action       string    `json:"action"`
	TargetUserID string    `json:"target_user_id,omitempty"`
	Target       string    `json:"target,omitempty"`
	Note         string    `json:"note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Audit lists the log, newest first: ?staff_id=&user_id=&action=&page=&limit=.
func (h StaffHandler) Audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, more, err := h.Service.AuditLog(r.Context(), middleware.StaffFrom(r.Context()), staff.AuditFilter{
		StaffID: q.Get("staff_id"), TargetUserID: q.Get("user_id"), Action: q.Get("action"),
	}, queryInt(r, "page", 1), queryInt(r, "limit", 20))
	if err != nil {
		writeStaffErr(w, h.Logger, err)
		return
	}
	out := make([]auditDTO, 0, len(list))
	for _, e := range list {
		out = append(out, auditDTO{ID: e.ID, StaffID: e.StaffID, StaffName: e.StaffName, Action: e.Action,
			TargetUserID: e.TargetUserID, Target: e.Target, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	httperr.WriteJSON(w, http.StatusOK, listResponse[auditDTO]{Items: out, HasMore: more})
}

// AuditRecorder writes the admin audit log. service.Staff implements it.
type AuditRecorder interface {
	Record(ctx context.Context, a staff.Actor, e staff.Entry)
}

// audit records an admin action by the request's actor. A nil recorder records nothing.
func audit(rec AuditRecorder, r *http.Request, e staff.Entry) {
	if rec != nil {
		rec.Record(r.Context(), middleware.StaffFrom(r.Context()), e)
	}
}
