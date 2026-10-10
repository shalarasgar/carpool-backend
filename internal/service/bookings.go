package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Bookings implements seat requests and the driver's approve / reject decisions.
type Bookings struct {
	rideDB
}

// NewBookings wires the booking service. notifier may be nil, which uses a no-op.
func NewBookings(pool *pgxpool.Pool, notifier notify.Notifier, logger *slog.Logger) *Bookings {
	return &Bookings{rideDB: newRideDB(pool, notifier, logger)}
}

// Create requests seats. The passenger cannot book their own trip or a trip
// of someone blocked in either direction. Pending requests do not reserve
// seats; approval does. A second pending or approved request on the same trip
// is BOOKING_EXISTS.
func (s *Bookings) Create(ctx context.Context, passengerID, tripID string, seats int) (Booking, error) {
	if seats < minTripSeats || seats > maxTripSeats {
		return Booking{}, validationError(map[string]string{"seats": "min"})
	}
	passenger, err := parseUserID(passengerID)
	if err != nil {
		return Booking{}, err
	}
	id, err := parseUUID(tripID)
	if err != nil {
		return Booking{}, ErrTripNotFound
	}
	var created store.Booking
	var driver pgtype.UUID
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, passenger); err != nil {
			return err
		}
		trip, err := q.GetTripForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTripNotFound
		}
		if err != nil {
			return fmt.Errorf("lock trip: %w", err)
		}
		if sameID(trip.DriverID.String(), passenger.String()) {
			return ErrForbidden
		}
		blocked, err := blockedEitherWay(ctx, q, passenger, trip.DriverID)
		if err != nil {
			return err
		}
		if blocked {
			return ErrForbidden
		}
		if err := bookable(trip, seats, s.clock()); err != nil {
			return err
		}
		created, err = q.CreateBooking(ctx, store.CreateBookingParams{
			TripID:      id,
			PassengerID: passenger,
			Seats:       int32(seats),
		})
		if isUniqueViolation(err) {
			return ErrBookingExists
		}
		if err != nil {
			return fmt.Errorf("create booking: %w", err)
		}
		driver = trip.DriverID
		return nil
	})
	if err != nil {
		return Booking{}, err
	}
	s.notify(ctx, []notify.Event{{
		Kind:        notify.KindBookingRequested,
		RecipientID: driver.String(),
		ActorID:     passenger.String(),
		TripID:      id.String(),
		BookingID:   created.ID.String(),
	}})
	s.logger.Info("booking requested", "user_id", passengerID, "trip_id", tripID, "booking_id", created.ID.String())
	return s.present(ctx, created.ID, passengerID)
}

// ListMine returns the caller's bookings as a passenger, newest first.
// The nested trip has no phone number. driver_phone is set only after approval.
func (s *Bookings) ListMine(ctx context.Context, passengerID string, q PageQuery) (BookingPage, error) {
	passenger, err := parseUserID(passengerID)
	if err != nil {
		return BookingPage{}, err
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return BookingPage{}, err
	}
	rows, err := s.q.ListBookingsByPassenger(ctx, store.ListBookingsByPassengerParams{
		PassengerID: passenger,
		PageOffset:  int32(offset),
		PageLimit:   int32(limit),
	})
	if err != nil {
		return BookingPage{}, fmt.Errorf("list bookings: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	locale := localeOrDefault(q.Locale)
	out := make([]Booking, 0, len(trimmed))
	for _, row := range trimmed {
		booking, err := s.decorate(ctx, passengerID, bookingCore{
			ID: row.ID, TripID: row.TripID, PassengerID: row.PassengerID,
			Seats: row.Seats, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			PassengerName: row.PassengerName, PassengerPhoto: row.PassengerPhotoUrl, DriverID: row.DriverID,
		})
		if err != nil {
			return BookingPage{}, err
		}
		trip, err := tripFromPassengerBooking(row, locale)
		if err != nil {
			return BookingPage{}, err
		}
		booking.Trip = &trip
		out = append(out, booking)
	}
	return BookingPage{Bookings: out, Page: page, PageSize: size, HasMore: more}, nil
}

// ListForTrip returns every request on the driver's trip. The passenger phone
// is included only after approval.
func (s *Bookings) ListForTrip(ctx context.Context, driverID, tripID string, q PageQuery) (BookingPage, error) {
	driver, err := parseUserID(driverID)
	if err != nil {
		return BookingPage{}, err
	}
	id, err := parseUUID(tripID)
	if err != nil {
		return BookingPage{}, ErrTripNotFound
	}
	owner, err := s.q.GetTripView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return BookingPage{}, ErrTripNotFound
	}
	if err != nil {
		return BookingPage{}, fmt.Errorf("load trip: %w", err)
	}
	if !sameID(owner.DriverID.String(), driver.String()) {
		return BookingPage{}, ErrForbidden
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return BookingPage{}, err
	}
	rows, err := s.q.ListBookingsByTrip(ctx, store.ListBookingsByTripParams{
		TripID:     id,
		PageOffset: int32(offset),
		PageLimit:  int32(limit),
	})
	if err != nil {
		return BookingPage{}, fmt.Errorf("list trip bookings: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	out := make([]Booking, 0, len(trimmed))
	for _, row := range trimmed {
		booking, err := s.decorate(ctx, driverID, bookingCore{
			ID: row.ID, TripID: row.TripID, PassengerID: row.PassengerID,
			Seats: row.Seats, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			PassengerName: row.PassengerName, PassengerPhoto: row.PassengerPhotoUrl, DriverID: row.DriverID,
		})
		if err != nil {
			return BookingPage{}, err
		}
		out = append(out, booking)
	}
	return BookingPage{Bookings: out, Page: page, PageSize: size, HasMore: more}, nil
}

// Approve accepts a pending request. The trip row is locked first, then the
// booking. seats_left is decremented and the trip becomes full at zero.
// A repeated approve of the same booking does not take the seats again.
func (s *Bookings) Approve(ctx context.Context, driverID, bookingID string) (Booking, error) {
	return s.decide(ctx, driverID, bookingID, decisionApprove)
}

// Reject declines a pending request. An approved request must be cancelled.
func (s *Bookings) Reject(ctx context.Context, driverID, bookingID string) (Booking, error) {
	return s.decide(ctx, driverID, bookingID, decisionReject)
}

// Cancel withdraws a pending or approved booking. The passenger and the driver
// may both call it. Cancelling an approved booking restores seats and re-opens
// a full trip, in the same transaction that locks the trip row.
func (s *Bookings) Cancel(ctx context.Context, callerID, bookingID string) (Booking, error) {
	return s.decide(ctx, callerID, bookingID, decisionCancel)
}

type decision string

const (
	decisionApprove decision = "approve"
	decisionReject  decision = "reject"
	decisionCancel  decision = "cancel"
)

func (s *Bookings) decide(ctx context.Context, callerID, bookingID string, kind decision) (Booking, error) {
	caller, err := parseUserID(callerID)
	if err != nil {
		return Booking{}, err
	}
	id, err := parseUUID(bookingID)
	if err != nil {
		return Booking{}, ErrBookingNotFound
	}
	var events []notify.Event
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, caller); err != nil {
			return err
		}
		preview, err := q.GetBookingView(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBookingNotFound
		}
		if err != nil {
			return fmt.Errorf("load booking: %w", err)
		}
		isDriver := sameID(preview.DriverID.String(), caller.String())
		isPassenger := sameID(preview.PassengerID.String(), caller.String())
		if kind == decisionCancel {
			if !isDriver && !isPassenger {
				return ErrForbidden
			}
		} else if !isDriver {
			return ErrForbidden
		}
		trip, err := q.GetTripForUpdate(ctx, preview.TripID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTripNotFound
		}
		if err != nil {
			return fmt.Errorf("lock trip: %w", err)
		}
		booking, err := q.GetBookingForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBookingNotFound
		}
		if err != nil {
			return fmt.Errorf("lock booking: %w", err)
		}
		switch kind {
		case decisionApprove:
			blocked, err := blockedEitherWay(ctx, q, caller, booking.PassengerID)
			if err != nil {
				return err
			}
			if blocked {
				return ErrForbidden
			}
			change, err := approveSeats(string(trip.Status), int(trip.SeatsLeft), int(booking.Seats), string(booking.Status), trip.DepartsAt.Time, s.clock())
			if err != nil || !change.apply {
				return err
			}
			if _, err := q.UpdateTripSeats(ctx, store.UpdateTripSeatsParams{
				ID:        trip.ID,
				SeatsLeft: int32(change.seatsLeft),
				Status:    store.TripStatus(change.status),
			}); err != nil {
				return fmt.Errorf("update seats: %w", err)
			}
			if _, err := q.UpdateBookingStatus(ctx, store.UpdateBookingStatusParams{
				ID:     booking.ID,
				Status: store.BookingStatusApproved,
			}); err != nil {
				return fmt.Errorf("approve booking: %w", err)
			}
			events = []notify.Event{{
				Kind:        notify.KindBookingApproved,
				RecipientID: booking.PassengerID.String(),
				ActorID:     caller.String(),
				TripID:      trip.ID.String(),
				BookingID:   booking.ID.String(),
			}}
		case decisionReject:
			changed, err := rejectBooking(string(booking.Status))
			if err != nil || !changed {
				return err
			}
			if _, err := q.UpdateBookingStatus(ctx, store.UpdateBookingStatusParams{
				ID:     booking.ID,
				Status: store.BookingStatusRejected,
			}); err != nil {
				return fmt.Errorf("reject booking: %w", err)
			}
			events = []notify.Event{{
				Kind:        notify.KindBookingRejected,
				RecipientID: booking.PassengerID.String(),
				ActorID:     caller.String(),
				TripID:      trip.ID.String(),
				BookingID:   booking.ID.String(),
			}}
		case decisionCancel:
			if booking.Status == store.BookingStatusApproved && trip.Status != store.TripStatusActive && trip.Status != store.TripStatusFull {
				return ErrInvalidState
			}
			next, already, err := cancelBookingStatus(isDriver && !isPassenger, string(booking.Status))
			if err != nil || already {
				return err
			}
			if booking.Status == store.BookingStatusApproved {
				change, err := restoreApprovedSeats(string(trip.Status), int(trip.SeatsLeft), int(trip.SeatsTotal), int(booking.Seats))
				if errors.Is(err, errInconsistentSeats) {
					return ErrInvalidState
				}
				if err != nil {
					return err
				}
				if change.apply {
					if _, err := q.UpdateTripSeats(ctx, store.UpdateTripSeatsParams{
						ID:        trip.ID,
						SeatsLeft: int32(change.seatsLeft),
						Status:    store.TripStatus(change.status),
					}); err != nil {
						return fmt.Errorf("restore seats: %w", err)
					}
				}
			}
			if _, err := q.UpdateBookingStatus(ctx, store.UpdateBookingStatusParams{
				ID:     booking.ID,
				Status: store.BookingStatus(next),
			}); err != nil {
				return fmt.Errorf("cancel booking: %w", err)
			}
			recipient := trip.DriverID.String()
			if isDriver && !isPassenger {
				recipient = booking.PassengerID.String()
			}
			events = []notify.Event{{
				Kind:        notify.KindBookingCancelled,
				RecipientID: recipient,
				ActorID:     caller.String(),
				TripID:      trip.ID.String(),
				BookingID:   booking.ID.String(),
			}}
		}
		return nil
	})
	if err != nil {
		return Booking{}, err
	}
	s.notify(ctx, events)
	s.logger.Info("booking decision", "user_id", callerID, "booking_id", bookingID, "decision", string(kind))
	return s.present(ctx, id, callerID)
}

type bookingCore struct {
	ID             pgtype.UUID
	TripID         pgtype.UUID
	PassengerID    pgtype.UUID
	Seats          int32
	Status         store.BookingStatus
	CreatedAt      pgtype.Timestamptz
	UpdatedAt      pgtype.Timestamptz
	PassengerName  string
	PassengerPhoto pgtype.Text
	DriverID       pgtype.UUID
}

func (s *Bookings) present(ctx context.Context, id pgtype.UUID, viewerID string) (Booking, error) {
	row, err := s.q.GetBookingView(ctx, id)
	if err != nil {
		return Booking{}, fmt.Errorf("load booking: %w", err)
	}
	return s.decorate(ctx, viewerID, bookingCore{
		ID: row.ID, TripID: row.TripID, PassengerID: row.PassengerID,
		Seats: row.Seats, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		PassengerName: row.PassengerName, PassengerPhoto: row.PassengerPhotoUrl, DriverID: row.DriverID,
	})
}

// decorate copies a booking and attaches a phone only when the spec allows it:
// the driver's number to the passenger after approval, the passenger's number
// to the driver after approval.
func (s *Bookings) decorate(ctx context.Context, viewerID string, row bookingCore) (Booking, error) {
	booking := Booking{
		ID:     row.ID.String(),
		TripID: row.TripID.String(),
		Seats:  int(row.Seats),
		Status: string(row.Status),
		Passenger: BookingPassenger{
			ID:       row.PassengerID.String(),
			Name:     row.PassengerName,
			PhotoURL: textPtr(row.PassengerPhoto),
		},
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}
	if booking.Status != bookingApproved {
		return booking, nil
	}
	viewerIsDriver := sameID(viewerID, row.DriverID.String())
	viewerIsPassenger := sameID(viewerID, row.PassengerID.String())
	var phoneOf pgtype.UUID
	var into **string
	switch {
	case viewerIsDriver && !viewerIsPassenger:
		phoneOf = row.PassengerID
		into = &booking.Passenger.Phone
	case viewerIsPassenger && !viewerIsDriver:
		phoneOf = row.DriverID
		into = &booking.DriverPhone
	default:
		return booking, nil
	}
	phone, err := s.q.GetActiveUserPhone(ctx, phoneOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return booking, nil
	}
	if err != nil {
		return Booking{}, fmt.Errorf("load phone: %w", err)
	}
	*into = &phone
	return booking, nil
}

func bookable(trip store.Trip, seats int, now time.Time) error {
	if !trip.DepartsAt.Time.After(now) {
		return ErrInvalidState
	}
	if trip.Status == store.TripStatusFull || int(trip.SeatsLeft) < seats {
		return ErrSeatUnavailable
	}
	if trip.Status != store.TripStatusActive {
		return ErrInvalidState
	}
	return nil
}
