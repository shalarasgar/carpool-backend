package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Vehicles serves /v1/me/vehicles.
type Vehicles struct {
	Logger  *slog.Logger
	Service *service.Vehicles
}

// List handles GET /v1/me/vehicles.
func (h Vehicles) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Service.List(r.Context(), middleware.UserIDFrom(r.Context()))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.VehicleList{Vehicles: make([]dto.Vehicle, 0, len(rows))}
	for _, row := range rows {
		out.Vehicles = append(out.Vehicles, toVehicleDTO(row))
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

// Create handles POST /v1/me/vehicles.
func (h Vehicles) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.VehicleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	row, err := h.Service.Create(r.Context(), middleware.UserIDFrom(r.Context()), service.VehicleInput{
		Brand: req.Brand,
		Model: req.Model,
		Color: req.Color,
		Plate: req.Plate,
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toVehicleDTO(row))
}

// Patch handles PATCH /v1/me/vehicles/{id}.
func (h Vehicles) Patch(w http.ResponseWriter, r *http.Request) {
	var req dto.PatchVehicleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	row, err := h.Service.Update(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), service.VehiclePatch{
		Brand: req.Brand,
		Model: req.Model,
		Color: req.Color,
		Plate: req.Plate,
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, toVehicleDTO(row))
}

// Delete handles DELETE /v1/me/vehicles/{id}.
func (h Vehicles) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.Delete(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toVehicleDTO(v service.Vehicle) dto.Vehicle {
	return dto.Vehicle{
		ID:        v.ID,
		Brand:     v.Brand,
		Model:     v.Model,
		Color:     v.Color,
		Plate:     v.Plate,
		CreatedAt: v.CreatedAt,
	}
}
