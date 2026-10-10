package dto

// PatchMeRequest is PATCH /v1/me. Omitted fields are left unchanged.
type PatchMeRequest struct {
	Name   *string `json:"name" validate:"omitempty,min=1,max=160"`
	Locale *string `json:"locale" validate:"omitempty,oneof=tk ru en"`
}

// PublicVehicle is the car shown on a public profile.
type PublicVehicle struct {
	ID    string `json:"id"`
	Brand string `json:"brand"`
	Model string `json:"model"`
	Color string `json:"color"`
	Plate string `json:"plate"`
}

// PublicProfile is GET /v1/users/{id}. The phone number is never included.
type PublicProfile struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	PhotoURL       *string        `json:"photo_url"`
	RatingAvg      float64        `json:"rating_avg"`
	RatingCount    int            `json:"rating_count"`
	Vehicle        *PublicVehicle `json:"vehicle"`
	CompletedTrips int            `json:"completed_trips"`
}
