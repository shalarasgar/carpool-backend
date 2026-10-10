package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// ReleaseUserActivity runs inside the account-deletion transaction, which has
// already locked the user row. It cancels the user's active and full trips
// together with their pending and approved bookings, cancels the user's own
// pending and approved bookings on other active or full trips (restoring seats
// and re-opening a full trip), and deletes the user's vehicles.
//
// Trips are locked in id order before any booking row. Seat totals that would
// exceed seats_total are left unchanged; the booking is still cancelled so
// deletion can finish. Completed, cancelled and expired trips are not rewritten.
func ReleaseUserActivity(ctx context.Context, q *store.Queries, userID pgtype.UUID) ([]notify.Event, error) {
	passengerBookings, err := q.ListLiveBookingsByPassenger(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list passenger bookings: %w", err)
	}
	driverTrips, err := q.ListLiveTripIDsByDriver(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list driver trips: %w", err)
	}

	ids := make([]pgtype.UUID, 0, len(passengerBookings)+len(driverTrips))
	seen := make(map[string]struct{}, len(passengerBookings)+len(driverTrips))
	add := func(id pgtype.UUID) {
		key := id.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		ids = append(ids, id)
	}
	for _, booking := range passengerBookings {
		add(booking.TripID)
	}
	for _, id := range driverTrips {
		add(id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	locked := make(map[string]store.Trip, len(ids))
	for _, id := range ids {
		trip, err := q.GetTripForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("lock trip: %w", err)
		}
		locked[id.String()] = trip
	}

	var events []notify.Event
	for _, booking := range passengerBookings {
		trip, ok := locked[booking.TripID.String()]
		if !ok || (trip.Status != store.TripStatusActive && trip.Status != store.TripStatusFull) {
			continue
		}
		row, err := q.GetBookingForUpdate(ctx, booking.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("lock booking: %w", err)
		}
		if row.Status != store.BookingStatusPending && row.Status != store.BookingStatusApproved {
			continue
		}
		if row.Status == store.BookingStatusApproved {
			change, err := restoreApprovedSeats(string(trip.Status), int(trip.SeatsLeft), int(trip.SeatsTotal), int(row.Seats))
			if err != nil && !errors.Is(err, errInconsistentSeats) {
				return nil, err
			}
			if err == nil && change.apply {
				updated, err := q.UpdateTripSeats(ctx, store.UpdateTripSeatsParams{
					ID:        trip.ID,
					SeatsLeft: int32(change.seatsLeft),
					Status:    store.TripStatus(change.status),
				})
				if err != nil {
					return nil, fmt.Errorf("restore seats: %w", err)
				}
				locked[trip.ID.String()] = updated
				trip = updated
			}
		}
		if _, err := q.UpdateBookingStatus(ctx, store.UpdateBookingStatusParams{
			ID:     row.ID,
			Status: store.BookingStatusCancelledByPassenger,
		}); err != nil {
			return nil, fmt.Errorf("cancel passenger booking: %w", err)
		}
		if trip.DriverID.String() != userID.String() {
			events = append(events, notify.Event{
				Kind:        notify.KindBookingCancelled,
				RecipientID: trip.DriverID.String(),
				ActorID:     userID.String(),
				TripID:      trip.ID.String(),
				BookingID:   row.ID.String(),
			})
		}
	}

	for _, id := range driverTrips {
		trip, ok := locked[id.String()]
		if !ok || (trip.Status != store.TripStatusActive && trip.Status != store.TripStatusFull) {
			continue
		}
		cancelled, err := q.CancelLiveBookingsForTrip(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("cancel trip bookings: %w", err)
		}
		n, err := q.SetTripStatus(ctx, store.SetTripStatusParams{ID: id, Status: store.TripStatusCancelled})
		if err != nil {
			return nil, fmt.Errorf("cancel trip: %w", err)
		}
		if n == 0 {
			continue
		}
		trip.Status = store.TripStatusCancelled
		locked[id.String()] = trip
		for _, booking := range cancelled {
			if booking.PassengerID.String() == userID.String() {
				continue
			}
			events = append(events, notify.Event{
				Kind:        notify.KindTripCancelled,
				RecipientID: booking.PassengerID.String(),
				ActorID:     userID.String(),
				TripID:      id.String(),
				BookingID:   booking.ID.String(),
			})
		}
	}

	if err := q.DeleteVehiclesByUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("delete vehicles: %w", err)
	}
	return events, nil
}
