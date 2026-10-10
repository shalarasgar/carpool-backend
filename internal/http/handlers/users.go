package handlers

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Users serves the caller's profile and public profiles.
type Users struct {
	Logger   *slog.Logger
	Service  *service.Users
	MaxPhoto int64
}

// Get handles GET /v1/me.
func (h Users) Get(w http.ResponseWriter, r *http.Request) {
	user, err := h.Service.Get(r.Context(), middleware.UserIDFrom(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toUserDTO(user))
}

// Patch handles PATCH /v1/me.
func (h Users) Patch(w http.ResponseWriter, r *http.Request) {
	var req dto.PatchMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := h.Service.Update(r.Context(), middleware.UserIDFrom(r.Context()), service.ProfileUpdate{
		Name:   req.Name,
		Locale: req.Locale,
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toUserDTO(user))
}

// Delete handles DELETE /v1/me.
func (h Users) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.Anonymize(r.Context(), middleware.UserIDFrom(r.Context())); err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Photo handles POST /v1/me/photo. The part name is "photo".
// The part's Content-Type is ignored; the service sniffs the bytes.
func (h Users) Photo(w http.ResponseWriter, r *http.Request) {
	limit := h.MaxPhoto + 16<<10
	if limit < h.MaxPhoto {
		limit = h.MaxPhoto
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseMultipartForm(h.MaxPhoto); err != nil {
		if photoTooLarge(err) {
			httperr.WriteCode(w, r, http.StatusRequestEntityTooLarge, "PHOTO_TOO_LARGE")
			return
		}
		httperr.WriteCode(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		httperr.WriteCodeDetails(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", map[string]string{"photo": "required"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, h.MaxPhoto+1))
	if err != nil {
		httperr.WriteCode(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	user, err := h.Service.SetPhoto(r.Context(), middleware.UserIDFrom(r.Context()), data)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toUserDTO(user))
}

func photoTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return true
	}
	return strings.Contains(err.Error(), "request body too large")
}

// Public handles GET /v1/users/{id}.
func (h Users) Public(w http.ResponseWriter, r *http.Request) {
	profile, err := h.Service.PublicProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.PublicProfile{
		ID:             profile.ID,
		Name:           profile.Name,
		PhotoURL:       profile.PhotoURL,
		RatingAvg:      profile.RatingAvg,
		RatingCount:    profile.RatingCount,
		CompletedTrips: profile.CompletedTrips,
	}
	if profile.Vehicle != nil {
		out.Vehicle = &dto.PublicVehicle{
			ID:    profile.Vehicle.ID,
			Brand: profile.Vehicle.Brand,
			Model: profile.Vehicle.Model,
			Color: profile.Vehicle.Color,
			Plate: profile.Vehicle.Plate,
		}
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}
