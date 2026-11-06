# API documentation

Base URL: the server root (`http://127.0.0.1:8081` in local development). All bodies are JSON
unless stated. Every response carries `X-Request-ID`.

## Conventions

- **Auth.** Product endpoints under `/v1` (except `/v1/auth/*` and `/v1/locations`) need
  `Authorization: Bearer <access_token>`. Access tokens live 15 minutes; rotate them with
  `/v1/auth/refresh`. A blocked or unknown user gets `401 UNAUTHORIZED` / `403 USER_BLOCKED`.
- **Language.** `Accept-Language: tk|ru|en` (default `tk`) selects the language of the error
  `message` and of the OTP SMS. Clients should switch on `code`, never on `message`.
- **Errors.** `{"error": {"code": "...", "message": "...", "field": "...", "details": {...}}}`.
  `field` and `details` appear only when useful (validation, overlap).
- **Lists.** `{"items": [...], "has_more": bool}`; paginate with `page` (1-based) and `limit` (≤ 50).
- **Times.** RFC 3339 in UTC. Search `date`, `time_from`, `time_to` are local to `APP_TIMEZONE`.
- **IDs.** UUIDs. A malformed id in a path is `404 NOT_FOUND`.
- **Rate limits.** OTP limits are per phone/IP/device (database backed). Signed-in users are
  limited to `API_RATE_PER_MIN` requests per minute. `429 RATE_LIMITED` carries `Retry-After`.

## Existing endpoints (unchanged)

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/auth/request-otp` | `{phone, device_id}` → `202 {"status":"accepted"}` (+ `dev_code` with `DEV_MODE=true`). |
| POST | `/v1/auth/verify-otp` | `{phone, code, device_id}` → `{access_token, refresh_token, expires_in, is_new_user, user}`. |
| POST | `/v1/auth/refresh` | `{refresh_token, device_id}` → new session (rotation, reuse detection). |
| POST | `/v1/auth/logout` | `{refresh_token}` → `204`. |
| * | `/gateway/v1/*`, `/admin/v1/*` | SMS gateway and admin API, see the README. |
| GET | `/healthz`, `/readyz` | Liveness and readiness. |

## Locations

`GET /v1/locations` (public) — active welayats, cities, etraps and settlements, each with
`id, parent_id?, type, name_tk, name_ru, name_en`. Sends an `ETag`; `If-None-Match` gets `304`.

## Profile

| Method | Path | Body / result |
| --- | --- | --- |
| GET | `/v1/me` | Own profile: `id, phone, first_name, last_name, bio, photo_url, locale, rating_avg, rating_count, trips_as_driver, trips_as_passenger, member_since, profile_complete`. |
| PATCH | `/v1/me` | Any of `first_name, last_name, bio, locale`. Names: 1–50 chars after trimming. |
| POST | `/v1/me/photo` | Multipart field `photo`, JPEG/PNG/WebP up to 5 MB (content sniffed). |
| DELETE | `/v1/me/photo` | Removes the photo. |
| POST | `/v1/me/phone/request-otp` | `{phone, device_id}`. Sends an OTP to the **new** number through the SMS queue. `409 PHONE_TAKEN` if used. |
| POST | `/v1/me/phone/verify` | `{phone, code}`. Moves the account to the new number. |
| GET | `/v1/users/{id}` | Public profile. Never contains `phone` or `locale`. |
| GET | `/v1/users/{id}/reviews` | Reviews received: `author, author_role (driver|passenger), rating, comment, created_at`. |

`profile_complete` is false until first and last name are set. Publishing and booking return
`422 PROFILE_INCOMPLETE` until then.

## Vehicles

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/me/vehicles` | Own vehicles (with plate). |
| POST | `/v1/me/vehicles` | `{brand, model, color, plate, year?, vehicle_type?, passenger_seats?}`. `vehicle_type`: sedan, hatchback, wagon, suv, minivan, minibus, other. `passenger_seats` 1–8, excluding the driver. Plate is upper-cased. |
| PUT | `/v1/me/vehicles/{id}` | Same body. Seats cannot drop below a live trip's seats (`409 SEATS_BELOW_BOOKED`). |
| DELETE | `/v1/me/vehicles/{id}` | Soft delete; refused while a live trip uses it (`409 TRIP_HAS_BOOKINGS`). |
| POST | `/v1/me/vehicles/{id}/photo` | Multipart field `photo`. |

## Trips

### Publish — `POST /v1/trips`

```json
{
  "vehicle_id": "uuid",
  "departs_at": "2026-11-15T03:00:00Z",
  "arrives_at": "2026-11-15T09:00:00Z",
  "seats_total": 3,
  "price": 150, "price_negotiable": false,
  "description": "…", "conditions": "…",
  "amenities": {"air_conditioning": "yes", "audio_system": "unspecified", "music_policy": "driver_decides",
                "smoking_policy": "prohibited", "pets_policy": "not_allowed", "luggage_size": "medium",
                "child_seat": "no", "conversation": "friendly"},
  "stops": [
    {"location_id": "…", "meeting_point": "Teke bazar", "stop_type": "pickup"},
    {"location_id": "…", "planned_at": "2026-11-15T05:00:00Z", "stop_type": "both"},
    {"location_id": "…", "stop_type": "dropoff"}
  ]
}
```

Rules: 2–8 distinct active stops; the first allows pickup, the last drop-off; intermediate
`planned_at` optional and non-decreasing inside the schedule; departure in the future and at most
90 days ahead; duration 15 min–48 h; seats 1–8 and not above the vehicle's passenger seats;
price 0–100000 or null. Amenity values not listed are `unspecified`.
Errors: `422 VALIDATION_ERROR` (with `field`), `422 LOCATION_INACTIVE`, `409 TRIP_OVERLAP`
(with `details.trip_id` when known).

### Trip object

`id, status, driver{id, first_name, last_name, photo_url, rating_avg, rating_count, completed_trips},
vehicle{brand, model, color, plate?, year, vehicle_type, passenger_seats, photo_url},
stops[{order, location, meeting_point, planned_at, stop_type}], departs_at, arrives_at, seats_total,
free_seats_by_segment[], price, price_negotiable, currency, description, conditions, amenities,
created_at, updated_at, match?{pickup_order, dropoff_order, free_seats}, viewer?{is_driver,
booking_id, booking_status}, pending_requests?, approved_seats?`

- `free_seats_by_segment[i]` is free seats between stop `i` and stop `i+1`.
- `vehicle.plate` is present only for the driver and for a passenger whose booking is approved or completed.
- `pending_requests` and `approved_seats` are present only for the driver.

### Other trip endpoints

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/trips` | Search (below). |
| GET | `/v1/trips/{id}` | Trip for the viewer. |
| PATCH | `/v1/trips/{id}` | Driver only, before departure. Any of `vehicle_id, seats_total, price, clear_price, price_negotiable, description, conditions, amenities, departs_at+arrives_at, meeting_points{"order": "text"}`. Seats ≥ highest accepted segment load. Schedule moves only without pending/approved bookings (`409 TRIP_HAS_BOOKINGS`) and re-checks overlap. Accepted passengers are notified. The route itself cannot change. |
| POST | `/v1/trips/{id}/cancel` | Driver; cancels every pending and approved booking; idempotent. |
| POST | `/v1/trips/{id}/complete` | Driver, after departure; approved → completed, pending → expired; idempotent. |
| GET | `/v1/me/trips?scope=upcoming|history` | Driver's trips. |
| GET | `/v1/trips/{id}/bookings` | Driver's view of requests. |

### Search — `GET /v1/trips`

| Param | Meaning |
| --- | --- |
| `from`, `to` | Location ids. A welayat matches all its active places. Omitted = anywhere. |
| `date` | `YYYY-MM-DD`, local day of the pickup stop. |
| `seats` | Seats needed on every segment between pickup and drop-off (default 1). |
| `time_from`, `time_to` | `HH:MM` local window of the pickup time. |
| `price_min`, `price_max` | Price per seat. |
| `air_conditioning=yes`, `pets=allowed`, `smoking=prohibited|allowed`, `child_seat=yes` | Comfort filters. |
| `luggage=small|medium|large` | Minimum luggage size. |
| `min_rating` | Only drivers with reviews and at least this average. |
| `sort` | `departure` (default), `price_asc`, `price_desc`, `rating`. |

Only `active`/`full` trips that have not departed, not the viewer's own, not from blocked users.
Each item has `match` with the chosen pickup/drop-off and the free seats on that part.

## Bookings

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/trips/{id}/bookings` | `{pickup_order, dropoff_order, seats, luggage?, message?}`. Errors: `INVALID_SEGMENT`, `INSUFFICIENT_SEATS`, `BOOKING_EXISTS`, `OWN_TRIP`, `TRIP_NOT_ACTIVE`, `TRIP_ALREADY_STARTED`, `PROFILE_INCOMPLETE`, `USER_UNAVAILABLE`. |
| GET | `/v1/bookings/{id}` | Participants only (others `404`). |
| POST | `/v1/bookings/{id}/approve` | Driver. Transactional seat check; idempotent. `409 INSUFFICIENT_SEATS`, `409 BOOKING_NOT_PENDING`. |
| POST | `/v1/bookings/{id}/reject` | Driver; idempotent. |
| POST | `/v1/bookings/{id}/cancel` | Passenger or driver; approved bookings only before departure; idempotent. |
| GET | `/v1/bookings/{id}/contact` | `{phone}` of the other participant, only for approved/completed bookings (`403 CONTACT_UNAVAILABLE`). |
| POST | `/v1/bookings/{id}/review` | `{rating 1–5, comment?}`. Completed bookings only; one per author. |
| GET | `/v1/me/bookings?scope=upcoming|history` | Passenger's bookings with their trips. |

Booking object: `id, trip_id, status, seats, pickup_order, dropoff_order, luggage, message,
created_at, decided_at, cancelled_at, completed_at, passenger{…}, trip?, contact_phone?, reviewed`.
`contact_phone` is filled by the server only for a participant of an approved or completed booking.

## Notifications

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/me/notifications?before=RFC3339&limit=` | `type, trip_id, booking_id, actor{…}, from, to, departs_at, read, created_at`. |
| GET | `/v1/me/notifications/unread-count` | `{unread}`. |
| POST | `/v1/me/notifications/read` | `{ids: []}`; empty marks all. |

Types: `booking_requested, booking_approved, booking_rejected, booking_cancelled_by_passenger,
booking_cancelled_by_driver, booking_expired, trip_cancelled, trip_updated, trip_reminder,
trip_completed, review_received`. Clients render the text from the type; no phone numbers or
codes are stored.

## Media

`GET /media/{file}` serves uploaded photos (public, cacheable).
