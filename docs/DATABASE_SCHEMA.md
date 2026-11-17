# Database schema

PostgreSQL 16. UUID keys, `timestamptz` everywhere, times stored in UTC. Migrations live in
`migrations/` (golang-migrate, embedded; `MIGRATE_ON_START=true` applies them).

| Migration | Content |
| --- | --- |
| 000001 | Core tables (users, vehicles, locations, trips, trip_stops, bookings, reviews, blocks, reports, OTP, refresh tokens, SMS queue, gateways, device tokens). |
| 000002 | Welayats and main cities. |
| 000003 | Placeholder for the official etrap list. |
| 000004 | SMS send tracking. |
| 000005 | Enum values: trip `in_progress`, `expired`; booking `completed`, `expired`; location `settlement`. Separate because a new enum value cannot be used in the transaction that adds it. |
| 000006 | Product schema (below). Reversible; down tested. |
| 000007 | `push_tokens` and `notifications.pushed_at` (push outbox). |
| 000008 | Account deletion (`users.deleted_at`, nullable `users.phone`) and report handling (`reports.resolved_at`, CHECKed `status`, `reason`, `details` length). The down migration fails while deleted accounts exist: the old schema cannot hold a user without a phone. |

## Entities

**users** — one account for driving and travelling. Added `first_name`, `last_name` (required by
the service before trips/bookings), `bio`. The legacy `name` column is kept in sync.
`rating_avg`/`rating_count` are recomputed from `reviews` in the review transaction.
`is_blocked` is an admin ban. `deleted_at` marks a deleted account: the row stays because other
users' trips, bookings and reviews reference it, but name, bio, photo and rating are empty and
`phone` is NULL (released for a new registration). A deleted row keeps its `phone` only while
`is_blocked` is true, so deletion does not lift a ban. Code that reads `phone` must expect NULL.

**user_blocks** — `(blocker_id, blocked_id)`. Checked in both directions by search and booking.

**reports** — `reporter_id`, `reported_user_id`, optional `trip_id`, `reason` (CHECKed list:
spam, fraud, abuse, no_show, unsafe, fake_profile, inappropriate_content, other), `details`
(≤ 1000), `status` (`open` | `resolved`), `resolved_at`. Reports about a deleted account are
removed with it unless it is banned; reports a deleted user filed stay.

**vehicles** — `user_id` owner (many per user), `brand, model, color, plate`, optional
`year, vehicle_type, passenger_seats (1–8, without the driver), photo_url`, `deleted_at` (soft delete).

**locations** — self-referencing hierarchy (`welayat` → `city|etrap|settlement`), names in tk/ru/en,
`is_active`. Managed in the database, read by the app at runtime.

**trips** — `driver_id`, `vehicle_id`, `from_location_id`/`to_location_id` (= first/last stop),
`departs_at`, `arrives_at`, `seats_total (1–8)`, `suggested_price`, `price_negotiable`, structured
amenity columns with CHECKed values, `note` (description), `conditions`, `status`,
`cancelled_at`, `completed_at`. `seats_left` was **dropped**: availability is computed (see
BOOKING_AND_SEAT_MANAGEMENT.md).
Constraint `trips_driver_no_overlap`: `EXCLUDE USING gist (driver_id WITH =, tstzrange(departs_at, arrives_at) WITH &&) WHERE status IN ('active','full','in_progress')` (btree_gist).

**trip_stops** — the whole route: `stop_order` 0 = departure … N = destination,
`location_id`, `meeting_point`, `planned_at`, `stop_type (pickup|dropoff|both)`.
`UNIQUE (trip_id, stop_order)`, `UNIQUE (trip_id, location_id)`.

**bookings** — `trip_id`, `passenger_id`, `seats (1–8)`, `pickup_order < dropoff_order`,
`luggage`, `message`, `status`, `decided_at`, `cancelled_at`, `completed_at`.
Composite foreign keys `(trip_id, pickup_order)` and `(trip_id, dropoff_order)` →
`trip_stops (trip_id, stop_order)`, so a booking can only name stops of its own trip.
Partial unique index: one pending/approved booking per passenger per trip.

**reviews** — `booking_id` (required), `trip_id`, `from_user_id`, `to_user_id`, `rating 1–5`,
`comment`. `UNIQUE (booking_id, from_user_id)`, `UNIQUE (trip_id, from_user_id, to_user_id)`,
`CHECK (from_user_id <> to_user_id)`.

**notifications** — inbox: `user_id`, `type` (CHECKed list), `trip_id`, `booking_id`, `actor_id`,
`dedupe_key`, `read_at`. `UNIQUE (user_id, dedupe_key)` makes event delivery idempotent.

**device_tokens** — present for a future push channel; unused today.

## Lifecycle values

- Trip: `active` (published) → `full` ↔ `active` → `in_progress` → `completed` | `expired`; `cancelled` from active/full.
- Booking: `pending` → `approved | rejected | cancelled_by_* | expired`; `approved` → `cancelled_by_* | completed`.

History is never deleted: cancelled, rejected, expired and completed rows stay for history and reviews.
Account deletion is the exception for personal data: it removes or clears what identifies the
user (see `DeleteAccount` in `internal/store/account_db.go`) and leaves the anonymous trip and
booking rows for the other participants.
