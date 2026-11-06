# Booking and seat management

## One source of truth

Seats are never stored as a counter. Free seats on a segment are computed from **approved
bookings** inside the transaction that holds the trip's row lock. Consequences:

- there is no second number to keep in sync;
- leaving the `approved` state frees the seats by construction, so a repeated cancellation
  cannot restore seats twice;
- `trips.status` `full` is only a derived flag, recomputed in the same transaction.

## Segments

A route has stops `0..N`. Segment `i` runs from stop `i` to stop `i+1`. A booking with
`pickup_order = p`, `dropoff_order = d` occupies segments `p … d-1`.

```
load(i)       = Σ seats of approved bookings with pickup_order ≤ i < dropoff_order
free(p, d)    = seats_total − max(load(i) for i in p … d-1)
fits(p, d, n) = n ≥ 1 and free(p, d) ≥ n
full          = every segment has load(i) = seats_total
```

Example, four seats, Ashgabat(0) → Tejen(1) → Mary(2):
A Ashgabat→Tejen ×2 and B Tejen→Mary ×3 both fit (loads 2 and 3). C Ashgabat→Mary ×3 does not
(max load 3, only 1 free). The pure functions live in `internal/ride/route.go` and are unit
tested; the store uses them under the lock.

## Acceptance (`DecideBooking`)

1. `BEGIN` (READ COMMITTED).
2. `SELECT … FROM trips WHERE id = $trip FOR UPDATE` — every seat-changing transaction takes this lock first.
3. Lock the booking row.
4. Check the caller is the driver, the booking is `pending` (an already-approved booking returns
   unchanged: idempotent), the trip is `active|full` and has not departed.
5. Re-read approved bookings of the trip (after the lock, so all earlier commits are visible).
6. `fits(pickup, dropoff, seats)` or roll back with `INSUFFICIENT_SEATS`.
7. Set `approved`, `decided_at`; recompute `active/full`; insert the notification.
8. `COMMIT`.

Two concurrent approvals serialize on the trip row; the second re-reads and sees the first.
Pending requests never hold seats, but a request that already cannot fit is refused at creation.

## Cancellation

Passenger or driver, same lock order (trip, then booking). Pending → cancelled at any time;
approved → cancelled only before departure. An already-cancelled booking returns unchanged
(no second notification, no seat effect). Cancelling a trip cancels all pending and approved
bookings in the same transaction.

## Driver schedule

`CreateTrip` and schedule edits lock the driver's `users` row, then look for live trips whose
`[departs_at − buffer, arrives_at + buffer)` overlaps the new one (`TRIP_OVERLAP_BUFFER`,
default 30 min). The exclusion constraint `trips_driver_no_overlap` rejects any overlap of the
raw intervals even if the check were bypassed. Cancelled, completed and expired trips do not block.
Lock order users → trips never conflicts with booking transactions, which lock only trips.

## Lifecycle job

Every minute (`internal/jobs` runner + `Trips.Maintain`), with `FOR UPDATE SKIP LOCKED`:
departed bookable trips → `in_progress` and their pending bookings → `expired`;
`in_progress` trips past `arrives_at + TRIP_COMPLETE_GRACE` → `completed` (approved → completed)
or `expired` when nobody was approved; one reminder `TRIP_REMIND_BEFORE` departure.

## Tests

`internal/store/integration_test.go` (real PostgreSQL): 2+3 seats on 4 approved concurrently
(10 rounds, exactly one succeeds), 12 parallel one-seat approvals fill exactly 4, disjoint and
overlapping segments, five concurrent cancellations restore once, duplicate approval, ten
concurrent overlapping publications create one trip, and the exclusion constraint itself.
