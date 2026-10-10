package handlers

import (
	"log/slog"
	"net/http"

	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Notifications serves the in-app inbox.
type Notifications struct {
	Logger  *slog.Logger
	Service *service.Notifications
}

// List handles GET /v1/me/notifications.
func (h Notifications) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.List(r.Context(), middleware.UserIDFrom(r.Context()), q.Get("cursor"), q.Get("limit"))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.NotificationPage{
		Notifications: make([]dto.Notification, 0, len(page.Notifications)),
		UnreadCount:   page.UnreadCount,
		NextCursor:    page.NextCursor,
	}
	for _, item := range page.Notifications {
		out.Notifications = append(out.Notifications, dto.Notification{
			ID:        item.ID,
			Type:      item.Type,
			Title:     item.Title,
			Body:      item.Body,
			Payload:   item.Payload,
			ReadAt:    item.ReadAt,
			CreatedAt: item.CreatedAt,
		})
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

// Read handles POST /v1/me/notifications/read.
func (h Notifications) Read(w http.ResponseWriter, r *http.Request) {
	var req dto.ReadNotificationsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	unread, err := h.Service.MarkRead(r.Context(), middleware.UserIDFrom(r.Context()), service.MarkRead{
		IDs: req.IDs,
		All: req.All,
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, dto.UnreadCount{UnreadCount: unread})
}
