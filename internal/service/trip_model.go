package service

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

const maxNoteRunes = 500

var pricePattern = regexp.MustCompile(`^[0-9]{1,8}(\.[0-9]{1,2})?$`)

// errInconsistentSeats means seats_left does not match the approved bookings.
// Callers either abort the request or, for account deletion, skip the seat
// update and still cancel the booking.
var errInconsistentSeats = errors.New("inconsistent seats")

// TripDriver is the public driver card on a trip. It has no phone number.
type TripDriver struct {
	ID          string
	Name        string
	PhotoURL    *string
	RatingAvg   float64
	RatingCount int
}

// TripVehicle is the car attached to a trip, when the driver set one.
type TripVehicle struct {
	ID    string
	Brand string
	Model string
	Color string
	Plate string
}

// TripPlace is an endpoint with the name already localized.
type TripPlace struct {
	ID   string
	Type string
	Name string
}

// Trip is a ride. Phone numbers are never part of this value.
type Trip struct {
	ID             string
	Driver         TripDriver
	Vehicle        *TripVehicle
	From           TripPlace
	To             TripPlace
	DepartsAt      time.Time
	SeatsTotal     int
	SeatsLeft      int
	SuggestedPrice *string
	Currency       string
	PickupNote     *string
	DropoffNote    *string
	Note           *string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TripPage is one page of trips.
type TripPage struct {
	Trips    []Trip
	Page     int
	PageSize int
	HasMore  bool
}

// TripInput is POST /v1/trips.
type TripInput struct {
	VehicleID      *string
	FromLocationID string
	ToLocationID   string
	DepartsAt      time.Time
	SeatsTotal     int
	SuggestedPrice *string
	Currency       string
	PickupNote     *string
	DropoffNote    *string
	Note           *string
}

// OptionalID is a JSON field that was omitted, set to null, or set to an id.
type OptionalID struct {
	Set   bool
	Value *string
}

// OptionalPrice is a JSON money field. Null clears it.
type OptionalPrice struct {
	Set   bool
	Value *string
}

// TripPatch is PATCH /v1/trips/{id}. Nil pointers are left unchanged.
type TripPatch struct {
	Vehicle        OptionalID
	FromLocationID *string
	ToLocationID   *string
	DepartsAt      *time.Time
	SeatsTotal     *int
	SuggestedPrice OptionalPrice
	Currency       *string
	PickupNote     *string
	DropoffNote    *string
	Note           *string
}

func (p TripPatch) empty() bool {
	return !p.Vehicle.Set && p.FromLocationID == nil && p.ToLocationID == nil &&
		p.DepartsAt == nil && p.SeatsTotal == nil && !p.SuggestedPrice.Set &&
		p.Currency == nil && p.PickupNote == nil && p.DropoffNote == nil && p.Note == nil
}

// TripSearch is GET /v1/trips.
type TripSearch struct {
	From     string
	To       string
	Date     string
	Seats    string
	Page     string
	PageSize string
	Locale   string
}

// TripList is GET /v1/me/trips.
type TripList struct {
	Status   string
	Page     string
	PageSize string
	Locale   string
}

// BookingPassenger is the person who requested seats. Phone is set only when
// the viewer is the driver and the booking is approved.
type BookingPassenger struct {
	ID       string
	Name     string
	PhotoURL *string
	Phone    *string
}

// Booking is a seat request. DriverPhone is set only when the viewer is the
// passenger and the booking is approved.
type Booking struct {
	ID          string
	TripID      string
	Seats       int
	Status      string
	Passenger   BookingPassenger
	DriverPhone *string
	Trip        *Trip
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BookingPage is one page of bookings.
type BookingPage struct {
	Bookings []Booking
	Page     int
	PageSize int
	HasMore  bool
}

// PageQuery is the shared page and page_size input.
type PageQuery struct {
	Page     string
	PageSize string
	Locale   string
}

type tripSource struct {
	ID                pgtype.UUID
	DriverID          pgtype.UUID
	VehicleID         pgtype.UUID
	FromLocationID    pgtype.UUID
	ToLocationID      pgtype.UUID
	DepartsAt         pgtype.Timestamptz
	SeatsTotal        int32
	SeatsLeft         int32
	SuggestedPrice    pgtype.Numeric
	Currency          string
	PickupNote        pgtype.Text
	DropoffNote       pgtype.Text
	Note              pgtype.Text
	Status            store.TripStatus
	CreatedAt         pgtype.Timestamptz
	UpdatedAt         pgtype.Timestamptz
	DriverName        string
	DriverPhotoURL    pgtype.Text
	DriverRatingAvg   pgtype.Numeric
	DriverRatingCount int32
	VehicleBrand      pgtype.Text
	VehicleModel      pgtype.Text
	VehicleColor      pgtype.Text
	VehiclePlate      pgtype.Text
	FromType          store.LocationType
	FromNameTK        string
	FromNameRU        string
	FromNameEN        string
	ToType            store.LocationType
	ToNameTK          string
	ToNameRU          string
	ToNameEN          string
}

func tripFromSource(src tripSource, locale string) (Trip, error) {
	price, err := formatPrice(src.SuggestedPrice)
	if err != nil {
		return Trip{}, err
	}
	trip := Trip{
		ID: src.ID.String(),
		Driver: TripDriver{
			ID:          src.DriverID.String(),
			Name:        src.DriverName,
			PhotoURL:    textPtr(src.DriverPhotoURL),
			RatingAvg:   numericFloat(src.DriverRatingAvg),
			RatingCount: int(src.DriverRatingCount),
		},
		From: TripPlace{
			ID:   src.FromLocationID.String(),
			Type: string(src.FromType),
			Name: localizedPlace(src.FromNameTK, src.FromNameRU, src.FromNameEN, locale),
		},
		To: TripPlace{
			ID:   src.ToLocationID.String(),
			Type: string(src.ToType),
			Name: localizedPlace(src.ToNameTK, src.ToNameRU, src.ToNameEN, locale),
		},
		DepartsAt:      src.DepartsAt.Time,
		SeatsTotal:     int(src.SeatsTotal),
		SeatsLeft:      int(src.SeatsLeft),
		SuggestedPrice: price,
		Currency:       src.Currency,
		PickupNote:     textPtr(src.PickupNote),
		DropoffNote:    textPtr(src.DropoffNote),
		Note:           textPtr(src.Note),
		Status:         string(src.Status),
		CreatedAt:      src.CreatedAt.Time,
		UpdatedAt:      src.UpdatedAt.Time,
	}
	if src.VehicleID.Valid {
		trip.Vehicle = &TripVehicle{
			ID:    src.VehicleID.String(),
			Brand: src.VehicleBrand.String,
			Model: src.VehicleModel.String,
			Color: src.VehicleColor.String,
			Plate: src.VehiclePlate.String,
		}
	}
	return trip, nil
}

func sourceFromView(row store.GetTripViewRow) tripSource {
	return tripSource{
		ID: row.ID, DriverID: row.DriverID, VehicleID: row.VehicleID,
		FromLocationID: row.FromLocationID, ToLocationID: row.ToLocationID,
		DepartsAt: row.DepartsAt, SeatsTotal: row.SeatsTotal, SeatsLeft: row.SeatsLeft,
		SuggestedPrice: row.SuggestedPrice, Currency: row.Currency,
		PickupNote: row.PickupNote, DropoffNote: row.DropoffNote, Note: row.Note,
		Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		DriverName: row.DriverName, DriverPhotoURL: row.DriverPhotoUrl,
		DriverRatingAvg: row.DriverRatingAvg, DriverRatingCount: row.DriverRatingCount,
		VehicleBrand: row.VehicleBrand, VehicleModel: row.VehicleModel,
		VehicleColor: row.VehicleColor, VehiclePlate: row.VehiclePlate,
		FromType: row.FromType, FromNameTK: row.FromNameTk, FromNameRU: row.FromNameRu, FromNameEN: row.FromNameEn,
		ToType: row.ToType, ToNameTK: row.ToNameTk, ToNameRU: row.ToNameRu, ToNameEN: row.ToNameEn,
	}
}

func sourceFromSearch(row store.SearchTripsRow) tripSource {
	return sourceFromView(store.GetTripViewRow(row))
}

func sourceFromDriver(row store.ListDriverTripsRow) tripSource {
	return sourceFromSearch(store.SearchTripsRow(row))
}

func tripFromPassengerBooking(row store.ListBookingsByPassengerRow, locale string) (Trip, error) {
	return tripFromSource(tripSource{
		ID: row.TripID, DriverID: row.DriverID, VehicleID: row.VehicleID,
		FromLocationID: row.FromLocationID, ToLocationID: row.ToLocationID,
		DepartsAt: row.DepartsAt, SeatsTotal: row.SeatsTotal, SeatsLeft: row.SeatsLeft,
		SuggestedPrice: row.SuggestedPrice, Currency: row.Currency,
		PickupNote: row.PickupNote, DropoffNote: row.DropoffNote, Note: row.Note,
		Status: row.TripStatus, CreatedAt: row.TripCreatedAt, UpdatedAt: row.TripUpdatedAt,
		DriverName: row.DriverName, DriverPhotoURL: row.DriverPhotoUrl,
		DriverRatingAvg: row.DriverRatingAvg, DriverRatingCount: row.DriverRatingCount,
		VehicleBrand: row.VehicleBrand, VehicleModel: row.VehicleModel,
		VehicleColor: row.VehicleColor, VehiclePlate: row.VehiclePlate,
		FromType: row.FromType, FromNameTK: row.FromNameTk, FromNameRU: row.FromNameRu, FromNameEN: row.FromNameEn,
		ToType: row.ToType, ToNameTK: row.ToNameTk, ToNameRU: row.ToNameRu, ToNameEN: row.ToNameEn,
	}, locale)
}

func textPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func numericFloat(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func formatPrice(n pgtype.Numeric) (*string, error) {
	if !n.Valid {
		return nil, nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil, err
	}
	s := strconv.FormatFloat(f.Float64, 'f', 2, 64)
	return &s, nil
}

func parsePrice(raw string) (pgtype.Numeric, error) {
	raw = strings.TrimSpace(raw)
	if !pricePattern.MatchString(raw) {
		return pgtype.Numeric{}, validationError(map[string]string{"suggested_price": "numeric"})
	}
	var n pgtype.Numeric
	if err := n.Scan(raw); err != nil {
		return pgtype.Numeric{}, validationError(map[string]string{"suggested_price": "numeric"})
	}
	return n, nil
}

func normalizeCurrency(raw string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return "TMT", nil
	}
	if len(s) != 3 || strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return "", validationError(map[string]string{"currency": "len"})
	}
	return s, nil
}

func optionalNote(raw *string, field string) (pgtype.Text, error) {
	if raw == nil {
		return pgtype.Text{}, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return pgtype.Text{}, nil
	}
	if utf8.RuneCountInString(s) > maxNoteRunes {
		return pgtype.Text{}, validationError(map[string]string{field: "max"})
	}
	return pgtype.Text{String: s, Valid: true}, nil
}

func localizedPlace(tk, ru, en, locale string) string {
	switch locale {
	case i18n.LocaleRU:
		return ru
	case i18n.LocaleEN:
		return en
	default:
		return tk
	}
}

func localeOrDefault(locale string) string {
	if !i18n.Supported(locale) {
		return i18n.DefaultLocale
	}
	return locale
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

func sameID(a, b string) bool {
	left, okA := canonicalUUID(a)
	right, okB := canonicalUUID(b)
	return okA && okB && left == right
}
