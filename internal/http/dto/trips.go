package dto

import (
	"bytes"
	"encoding/json"
	"time"
)

// Decimal accepts a JSON string or number and is used for suggested_price.
// Null and omission both leave Value nil. Set is true when the field was present.
type Decimal struct {
	Set   bool
	Value *string
}

// UnmarshalJSON accepts null, a string, or a number.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	d.Set = true
	if string(b) == "null" {
		d.Value = nil
		return nil
	}
	if len(b) > 0 && b[0] != '"' {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var num json.Number
		if err := dec.Decode(&num); err != nil {
			return err
		}
		s := num.String()
		d.Value = &s
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	d.Value = &s
	return nil
}

// OptionalString is a JSON string that may be omitted or null.
// Set is false when the field was omitted. Null sets Value to nil.
type OptionalString struct {
	Set   bool
	Value *string
}

// UnmarshalJSON accepts null or a string.
func (o *OptionalString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Value = &s
	return nil
}

// CreateTripRequest is POST /v1/trips.
type CreateTripRequest struct {
	VehicleID      *string   `json:"vehicle_id" validate:"omitempty,uuid"`
	FromLocationID string    `json:"from_location_id" validate:"required,uuid"`
	ToLocationID   string    `json:"to_location_id" validate:"required,uuid"`
	DepartsAt      time.Time `json:"departs_at" validate:"required"`
	SeatsTotal     int       `json:"seats_total" validate:"required,min=1,max=8"`
	SuggestedPrice Decimal   `json:"suggested_price"`
	Currency       string    `json:"currency" validate:"omitempty,len=3"`
	PickupNote     *string   `json:"pickup_note" validate:"omitempty,max=500"`
	DropoffNote    *string   `json:"dropoff_note" validate:"omitempty,max=500"`
	Note           *string   `json:"note" validate:"omitempty,max=500"`
}

// PatchTripRequest is PATCH /v1/trips/{id}. Omitted fields stay as they are.
// vehicle_id and suggested_price accept null to clear them.
type PatchTripRequest struct {
	VehicleID      OptionalString `json:"vehicle_id"`
	FromLocationID *string        `json:"from_location_id" validate:"omitempty,uuid"`
	ToLocationID   *string        `json:"to_location_id" validate:"omitempty,uuid"`
	DepartsAt      *time.Time     `json:"departs_at"`
	SeatsTotal     *int           `json:"seats_total" validate:"omitempty,min=1,max=8"`
	SuggestedPrice Decimal        `json:"suggested_price"`
	Currency       *string        `json:"currency" validate:"omitempty,len=3"`
	PickupNote     *string        `json:"pickup_note" validate:"omitempty,max=500"`
	DropoffNote    *string        `json:"dropoff_note" validate:"omitempty,max=500"`
	Note           *string        `json:"note" validate:"omitempty,max=500"`
}

// TripDriver is the public card on a trip. There is no phone field.
type TripDriver struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	PhotoURL    *string `json:"photo_url"`
	RatingAvg   float64 `json:"rating_avg"`
	RatingCount int     `json:"rating_count"`
}

// TripVehicle is the car attached to a trip.
type TripVehicle struct {
	ID    string `json:"id"`
	Brand string `json:"brand"`
	Model string `json:"model"`
	Color string `json:"color"`
	Plate string `json:"plate"`
}

// TripPlace is a localized endpoint.
type TripPlace struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// Trip is a ride. Phone numbers are never included.
type Trip struct {
	ID             string       `json:"id"`
	Driver         TripDriver   `json:"driver"`
	Vehicle        *TripVehicle `json:"vehicle"`
	From           TripPlace    `json:"from"`
	To             TripPlace    `json:"to"`
	DepartsAt      time.Time    `json:"departs_at"`
	SeatsTotal     int          `json:"seats_total"`
	SeatsLeft      int          `json:"seats_left"`
	SuggestedPrice *string      `json:"suggested_price"`
	Currency       string       `json:"currency"`
	PickupNote     *string      `json:"pickup_note"`
	DropoffNote    *string      `json:"dropoff_note"`
	Note           *string      `json:"note"`
	Status         string       `json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// TripPage is a page of trips.
type TripPage struct {
	Trips    []Trip `json:"trips"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	HasMore  bool   `json:"has_more"`
}

// CreateBookingRequest is POST /v1/trips/{id}/bookings.
type CreateBookingRequest struct {
	Seats int `json:"seats" validate:"required,min=1,max=8"`
}

// BookingPassenger is the person who requested seats.
// Phone is present only for the driver, and only after approval.
type BookingPassenger struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	PhotoURL *string `json:"photo_url"`
	Phone    *string `json:"phone,omitempty"`
}

// Booking is a seat request.
// DriverPhone is present only for the passenger, and only after approval.
// Trip is set on the passenger's own list and is omitted elsewhere.
type Booking struct {
	ID          string           `json:"id"`
	TripID      string           `json:"trip_id"`
	Seats       int              `json:"seats"`
	Status      string           `json:"status"`
	Passenger   BookingPassenger `json:"passenger"`
	DriverPhone *string          `json:"driver_phone,omitempty"`
	Trip        *Trip            `json:"trip,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// BookingPage is a page of bookings.
type BookingPage struct {
	Bookings []Booking `json:"bookings"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
	HasMore  bool      `json:"has_more"`
}
