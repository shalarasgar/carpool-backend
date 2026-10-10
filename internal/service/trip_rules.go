package service

import (
	"strconv"
	"time"
)

const (
	minTripSeats       = 1
	maxTripSeats       = 8
	defaultPageSize    = 20
	maxPageSize        = 50
	maxPage            = 10000
	tripActive         = "active"
	tripFull           = "full"
	tripCancelled      = "cancelled"
	tripCompleted      = "completed"
	tripExpired        = "expired"
	bookingPending     = "pending"
	bookingApproved    = "approved"
	bookingRejected    = "rejected"
	bookingByPassenger = "cancelled_by_passenger"
	bookingByDriver    = "cancelled_by_driver"
)

// ashgabat is Turkmenistan time (UTC+5, no daylight saving). Search dates are
// calendar days in this zone.
var ashgabat = time.FixedZone("Asia/Ashgabat", 5*60*60)

// seatUpdate is the trip row change that follows a booking decision.
// apply is false when the trip row must stay as it is.
type seatUpdate struct {
	seatsLeft int
	status    string
	apply     bool
}

// approveSeats is the approval transition. The trip row must already be locked.
// An already-approved booking is a no-op so a retried request does not take
// the seats twice.
func approveSeats(tripStatus string, seatsLeft, bookingSeats int, bookingStatus string, departsAt, now time.Time) (seatUpdate, error) {
	if bookingStatus == bookingApproved {
		return seatUpdate{seatsLeft: seatsLeft, status: tripStatus}, nil
	}
	if bookingStatus != bookingPending {
		return seatUpdate{}, ErrInvalidState
	}
	if !departsAt.After(now) {
		return seatUpdate{}, ErrInvalidState
	}
	if tripStatus == tripFull || seatsLeft < bookingSeats || bookingSeats < 1 {
		return seatUpdate{}, ErrSeatUnavailable
	}
	if tripStatus != tripActive {
		return seatUpdate{}, ErrInvalidState
	}
	left := seatsLeft - bookingSeats
	status := tripActive
	if left == 0 {
		status = tripFull
	}
	return seatUpdate{seatsLeft: left, status: status, apply: true}, nil
}

// restoreApprovedSeats gives seats back after an approved booking is cancelled
// and re-opens a full trip. Completed, cancelled and expired trips are left
// unchanged. errInconsistentSeats means the stored seats_left cannot accept
// the seats without passing seats_total; the caller decides whether to abort.
func restoreApprovedSeats(tripStatus string, seatsLeft, seatsTotal, bookingSeats int) (seatUpdate, error) {
	if tripStatus != tripActive && tripStatus != tripFull {
		return seatUpdate{seatsLeft: seatsLeft, status: tripStatus}, nil
	}
	if bookingSeats < 1 || seatsLeft+bookingSeats > seatsTotal {
		return seatUpdate{}, errInconsistentSeats
	}
	left := seatsLeft + bookingSeats
	status := tripStatus
	if tripStatus == tripFull && left > 0 {
		status = tripActive
	}
	return seatUpdate{seatsLeft: left, status: status, apply: true}, nil
}

// resizeTrip changes seats_total. approved seats are seats_total - seats_left,
// which is true while every approval and cancellation updated the counter.
// newTotal below that number is INVALID_STATE. Zero seats left makes the trip
// full; seats coming back onto a full trip make it active again.
func resizeTrip(status string, seatsTotal, seatsLeft, newTotal int) (int, int, string, error) {
	if status != tripActive && status != tripFull {
		return 0, 0, "", ErrInvalidState
	}
	if newTotal < minTripSeats || newTotal > maxTripSeats {
		return 0, 0, "", validationError(map[string]string{"seats_total": "min"})
	}
	if seatsLeft < 0 || seatsLeft > seatsTotal {
		return 0, 0, "", errInconsistentSeats
	}
	approved := seatsTotal - seatsLeft
	if newTotal < approved {
		return 0, 0, "", ErrInvalidState
	}
	left := newTotal - approved
	next := status
	if left == 0 {
		next = tripFull
	} else if status == tripFull {
		next = tripActive
	}
	return newTotal, left, next, nil
}

// completeTrip moves an active or full trip to completed once departs_at has
// been reached. Repeating it is a no-op. Bookings are not modified.
func completeTrip(status string, departsAt, now time.Time) (string, bool, error) {
	switch status {
	case tripCompleted:
		return tripCompleted, false, nil
	case tripActive, tripFull:
		if departsAt.After(now) {
			return "", false, ErrInvalidState
		}
		return tripCompleted, true, nil
	default:
		return "", false, ErrInvalidState
	}
}

// cancelTrip reports whether an active or full trip should become cancelled.
// Cancelling twice is a no-op and must not cancel bookings again.
func cancelTrip(status string) (bool, error) {
	switch status {
	case tripCancelled:
		return false, nil
	case tripActive, tripFull:
		return true, nil
	default:
		return false, ErrInvalidState
	}
}

// cancelBookingStatus picks the terminal status for a passenger or a driver.
// An already-cancelled booking is returned unchanged.
func cancelBookingStatus(byDriver bool, current string) (string, bool, error) {
	switch current {
	case bookingPending, bookingApproved:
		if byDriver {
			return bookingByDriver, false, nil
		}
		return bookingByPassenger, false, nil
	case bookingByPassenger, bookingByDriver:
		return current, true, nil
	default:
		return "", false, ErrInvalidState
	}
}

// rejectBooking accepts a pending booking. Rejecting twice is a no-op.
// Approved bookings must be cancelled instead.
func rejectBooking(current string) (bool, error) {
	switch current {
	case bookingPending:
		return true, nil
	case bookingRejected:
		return false, nil
	default:
		return false, ErrInvalidState
	}
}

func parsePage(pageRaw, sizeRaw string) (page, size, limit, offset int, err error) {
	page = 1
	size = defaultPageSize
	if pageRaw != "" {
		n, convErr := strconv.Atoi(pageRaw)
		if convErr != nil || n < 1 || n > maxPage {
			return 0, 0, 0, 0, validationError(map[string]string{"page": "min"})
		}
		page = n
	}
	if sizeRaw != "" {
		n, convErr := strconv.Atoi(sizeRaw)
		if convErr != nil || n < 1 || n > maxPageSize {
			return 0, 0, 0, 0, validationError(map[string]string{"page_size": "max"})
		}
		size = n
	}
	return page, size, size + 1, (page - 1) * size, nil
}

func parseMinSeats(raw string) (int, error) {
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minTripSeats || n > maxTripSeats {
		return 0, validationError(map[string]string{"seats": "min"})
	}
	return n, nil
}

// searchDay returns [start, end) for a YYYY-MM-DD calendar day in Turkmenistan.
func searchDay(raw string) (time.Time, time.Time, error) {
	if raw == "" {
		return time.Time{}, time.Time{}, nil
	}
	start, err := time.ParseInLocation("2006-01-02", raw, ashgabat)
	if err != nil {
		return time.Time{}, time.Time{}, validationError(map[string]string{"date": "date"})
	}
	return start, start.AddDate(0, 0, 1), nil
}

func trimPage[T any](rows []T, size int) ([]T, bool) {
	if rows == nil {
		return []T{}, false
	}
	if len(rows) > size {
		return rows[:size], true
	}
	return rows, false
}
