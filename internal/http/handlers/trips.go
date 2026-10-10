package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Trips serves /v1/trips and /v1/me/trips.
type Trips struct {
	Logger  *slog.Logger
	Service *service.Trips
}

// Create handles POST /v1/trips.
func (h Trips) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateTripRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	trip, err := h.Service.Create(r.Context(), middleware.UserIDFrom(r.Context()), service.TripInput{
		VehicleID:      req.VehicleID,
		FromLocationID: req.FromLocationID,
		ToLocationID:   req.ToLocationID,
		DepartsAt:      req.DepartsAt,
		SeatsTotal:     req.SeatsTotal,
		SuggestedPrice: req.SuggestedPrice.Value,
		Currency:       req.Currency,
		PickupNote:     req.PickupNote,
		DropoffNote:    req.DropoffNote,
		Note:           req.Note,
	}, i18n.FromContext(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toTripDTO(trip))
}

// List handles GET /v1/trips.
func (h Trips) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.Search(r.Context(), middleware.UserIDFrom(r.Context()), service.TripSearch{
		From:     q.Get("from"),
		To:       q.Get("to"),
		Date:     q.Get("date"),
		Seats:    q.Get("seats"),
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
		Locale:   i18n.FromContext(r.Context()),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTripPage(page))
}

// Get handles GET /v1/trips/{id}.
func (h Trips) Get(w http.ResponseWriter, r *http.Request) {
	trip, err := h.Service.Get(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), i18n.FromContext(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTripDTO(trip))
}

// Patch handles PATCH /v1/trips/{id}.
func (h Trips) Patch(w http.ResponseWriter, r *http.Request) {
	var req dto.PatchTripRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	trip, err := h.Service.Update(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), service.TripPatch{
		Vehicle:        service.OptionalID{Set: req.VehicleID.Set, Value: req.VehicleID.Value},
		FromLocationID: req.FromLocationID,
		ToLocationID:   req.ToLocationID,
		DepartsAt:      req.DepartsAt,
		SeatsTotal:     req.SeatsTotal,
		SuggestedPrice: service.OptionalPrice{Set: req.SuggestedPrice.Set, Value: req.SuggestedPrice.Value},
		Currency:       req.Currency,
		PickupNote:     req.PickupNote,
		DropoffNote:    req.DropoffNote,
		Note:           req.Note,
	}, i18n.FromContext(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTripDTO(trip))
}

// Cancel handles POST /v1/trips/{id}/cancel.
func (h Trips) Cancel(w http.ResponseWriter, r *http.Request) {
	trip, err := h.Service.Cancel(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), i18n.FromContext(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTripDTO(trip))
}

// Complete handles POST /v1/trips/{id}/complete.
func (h Trips) Complete(w http.ResponseWriter, r *http.Request) {
	trip, err := h.Service.Complete(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), i18n.FromContext(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTripDTO(trip))
}

// Mine handles GET /v1/me/trips.
func (h Trips) Mine(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.ListMine(r.Context(), middleware.UserIDFrom(r.Context()), service.TripList{
		Status:   q.Get("status"),
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
		Locale:   i18n.FromContext(r.Context()),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toTripPage(page))
}

func toTripPage(page service.TripPage) dto.TripPage {
	out := dto.TripPage{
		Trips:    make([]dto.Trip, 0, len(page.Trips)),
		Page:     page.Page,
		PageSize: page.PageSize,
		HasMore:  page.HasMore,
	}
	for _, trip := range page.Trips {
		out.Trips = append(out.Trips, toTripDTO(trip))
	}
	return out
}

func toTripDTO(trip service.Trip) dto.Trip {
	out := dto.Trip{
		ID: trip.ID,
		Driver: dto.TripDriver{
			ID:          trip.Driver.ID,
			Name:        trip.Driver.Name,
			PhotoURL:    trip.Driver.PhotoURL,
			RatingAvg:   trip.Driver.RatingAvg,
			RatingCount: trip.Driver.RatingCount,
		},
		From:           dto.TripPlace{ID: trip.From.ID, Type: trip.From.Type, Name: trip.From.Name},
		To:             dto.TripPlace{ID: trip.To.ID, Type: trip.To.Type, Name: trip.To.Name},
		DepartsAt:      trip.DepartsAt,
		SeatsTotal:     trip.SeatsTotal,
		SeatsLeft:      trip.SeatsLeft,
		SuggestedPrice: trip.SuggestedPrice,
		Currency:       trip.Currency,
		PickupNote:     trip.PickupNote,
		DropoffNote:    trip.DropoffNote,
		Note:           trip.Note,
		Status:         trip.Status,
		CreatedAt:      trip.CreatedAt,
		UpdatedAt:      trip.UpdatedAt,
	}
	if trip.Vehicle != nil {
		out.Vehicle = &dto.TripVehicle{
			ID:    trip.Vehicle.ID,
			Brand: trip.Vehicle.Brand,
			Model: trip.Vehicle.Model,
			Color: trip.Vehicle.Color,
			Plate: trip.Vehicle.Plate,
		}
	}
	return out
}
