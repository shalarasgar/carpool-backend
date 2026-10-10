package dto

import "time"

// VehicleRequest is POST /v1/me/vehicles.
type VehicleRequest struct {
	Brand string `json:"brand" validate:"required,min=1,max=80"`
	Model string `json:"model" validate:"required,min=1,max=80"`
	Color string `json:"color" validate:"required,min=1,max=48"`
	Plate string `json:"plate" validate:"required,min=1,max=32"`
}

// PatchVehicleRequest is PATCH /v1/me/vehicles/{id}. Omitted fields stay as they are.
type PatchVehicleRequest struct {
	Brand *string `json:"brand" validate:"omitempty,min=1,max=80"`
	Model *string `json:"model" validate:"omitempty,min=1,max=80"`
	Color *string `json:"color" validate:"omitempty,min=1,max=48"`
	Plate *string `json:"plate" validate:"omitempty,min=1,max=32"`
}

// Vehicle is a car owned by the caller.
type Vehicle struct {
	ID        string    `json:"id"`
	Brand     string    `json:"brand"`
	Model     string    `json:"model"`
	Color     string    `json:"color"`
	Plate     string    `json:"plate"`
	CreatedAt time.Time `json:"created_at"`
}

// VehicleList is GET /v1/me/vehicles.
type VehicleList struct {
	Vehicles []Vehicle `json:"vehicles"`
}
