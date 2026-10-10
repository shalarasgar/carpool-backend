package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApproveSeats(t *testing.T) {
	t.Parallel()
	depart := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	now := depart.Add(-time.Hour)

	got, err := approveSeats(tripActive, 3, 1, bookingPending, depart, now)
	require.NoError(t, err)
	require.Equal(t, seatUpdate{seatsLeft: 2, status: tripActive, apply: true}, got)

	full, err := approveSeats(tripActive, 2, 2, bookingPending, depart, now)
	require.NoError(t, err)
	require.Equal(t, seatUpdate{seatsLeft: 0, status: tripFull, apply: true}, full)

	_, err = approveSeats(tripActive, 1, 2, bookingPending, depart, now)
	require.ErrorIs(t, err, ErrSeatUnavailable)

	_, err = approveSeats(tripFull, 0, 1, bookingPending, depart, now)
	require.ErrorIs(t, err, ErrSeatUnavailable)

	_, err = approveSeats(tripCancelled, 2, 1, bookingPending, depart, now)
	require.ErrorIs(t, err, ErrInvalidState)

	_, err = approveSeats(tripActive, 2, 1, bookingRejected, depart, now)
	require.ErrorIs(t, err, ErrInvalidState)

	_, err = approveSeats(tripActive, 2, 1, bookingPending, depart, depart)
	require.ErrorIs(t, err, ErrInvalidState)

	again, err := approveSeats(tripFull, 0, 1, bookingApproved, depart, now)
	require.NoError(t, err)
	require.False(t, again.apply)
	require.Equal(t, 0, again.seatsLeft)
	require.Equal(t, tripFull, again.status)
}

func TestRestoreApprovedSeats(t *testing.T) {
	t.Parallel()

	got, err := restoreApprovedSeats(tripFull, 0, 3, 1)
	require.NoError(t, err)
	require.Equal(t, seatUpdate{seatsLeft: 1, status: tripActive, apply: true}, got)

	active, err := restoreApprovedSeats(tripActive, 1, 3, 1)
	require.NoError(t, err)
	require.Equal(t, seatUpdate{seatsLeft: 2, status: tripActive, apply: true}, active)

	done, err := restoreApprovedSeats(tripCompleted, 0, 3, 1)
	require.NoError(t, err)
	require.False(t, done.apply)
	require.Equal(t, tripCompleted, done.status)

	_, err = restoreApprovedSeats(tripActive, 3, 3, 1)
	require.ErrorIs(t, err, errInconsistentSeats)
}

func TestResizeTrip(t *testing.T) {
	t.Parallel()

	total, left, status, err := resizeTrip(tripActive, 4, 1, 3)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Equal(t, 0, left)
	require.Equal(t, tripFull, status)

	_, _, _, err = resizeTrip(tripActive, 4, 1, 2)
	require.ErrorIs(t, err, ErrInvalidState)

	total, left, status, err = resizeTrip(tripFull, 2, 0, 4)
	require.NoError(t, err)
	require.Equal(t, 4, total)
	require.Equal(t, 2, left)
	require.Equal(t, tripActive, status)

	_, _, _, err = resizeTrip(tripActive, 4, 4, 0)
	require.ErrorIs(t, err, ErrValidation)
	_, _, _, err = resizeTrip(tripCancelled, 4, 4, 3)
	require.ErrorIs(t, err, ErrInvalidState)
	_, _, _, err = resizeTrip(tripActive, 4, 5, 4)
	require.ErrorIs(t, err, errInconsistentSeats)
}

func TestCompleteAndCancelTrip(t *testing.T) {
	t.Parallel()
	depart := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

	status, changed, err := completeTrip(tripActive, depart, depart)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, tripCompleted, status)

	_, _, err = completeTrip(tripFull, depart, depart.Add(-time.Second))
	require.ErrorIs(t, err, ErrInvalidState)

	status, changed, err = completeTrip(tripCompleted, depart, depart.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, tripCompleted, status)

	_, _, err = completeTrip(tripCancelled, depart, depart.Add(time.Hour))
	require.ErrorIs(t, err, ErrInvalidState)

	ok, err := cancelTrip(tripActive)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cancelTrip(tripFull)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cancelTrip(tripCancelled)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = cancelTrip(tripCompleted)
	require.ErrorIs(t, err, ErrInvalidState)
	_, err = cancelTrip(tripExpired)
	require.ErrorIs(t, err, ErrInvalidState)
}

func TestBookingStatusTransitions(t *testing.T) {
	t.Parallel()

	status, already, err := cancelBookingStatus(false, bookingPending)
	require.NoError(t, err)
	require.False(t, already)
	require.Equal(t, bookingByPassenger, status)

	status, already, err = cancelBookingStatus(true, bookingApproved)
	require.NoError(t, err)
	require.False(t, already)
	require.Equal(t, bookingByDriver, status)

	status, already, err = cancelBookingStatus(true, bookingByPassenger)
	require.NoError(t, err)
	require.True(t, already)
	require.Equal(t, bookingByPassenger, status)

	_, _, err = cancelBookingStatus(false, bookingRejected)
	require.ErrorIs(t, err, ErrInvalidState)

	changed, err := rejectBooking(bookingPending)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = rejectBooking(bookingRejected)
	require.NoError(t, err)
	require.False(t, changed)
	_, err = rejectBooking(bookingApproved)
	require.ErrorIs(t, err, ErrInvalidState)
}

func TestParsePageSeatsAndDate(t *testing.T) {
	t.Parallel()

	page, size, limit, offset, err := parsePage("", "")
	require.NoError(t, err)
	require.Equal(t, 1, page)
	require.Equal(t, 20, size)
	require.Equal(t, 21, limit)
	require.Equal(t, 0, offset)

	_, _, _, _, err = parsePage("0", "")
	require.ErrorIs(t, err, ErrValidation)
	_, _, _, _, err = parsePage("1", "51")
	require.ErrorIs(t, err, ErrValidation)

	seats, err := parseMinSeats("")
	require.NoError(t, err)
	require.Equal(t, 1, seats)
	_, err = parseMinSeats("9")
	require.ErrorIs(t, err, ErrValidation)

	start, end, err := searchDay("2026-10-12")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 10, 11, 19, 0, 0, 0, time.UTC), start.UTC())
	_, offsetSec := start.Zone()
	require.Equal(t, 5*3600, offsetSec)
	require.Equal(t, 24*time.Hour, end.Sub(start))
	_, _, err = searchDay("12-10-2026")
	require.ErrorIs(t, err, ErrValidation)

	rows, more := trimPage([]int{1, 2, 3}, 2)
	require.Equal(t, []int{1, 2}, rows)
	require.True(t, more)
	rows, more = trimPage[int](nil, 2)
	require.Empty(t, rows)
	require.False(t, more)
}
