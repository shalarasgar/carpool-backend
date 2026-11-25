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
| GET | `/v1/users/{id}` | Public profile. Never contains `phone` or `locale`. `404` for a deleted account. |
| GET | `/v1/users/{id}/reviews` | Reviews received: `author, author_role (driver|passenger), rating, comment, created_at`. |
| DELETE | `/v1/me` | Deletes the caller's account and personal data → `204`. `409 TRIP_IN_PROGRESS` while a trip the caller drives has departed and is not completed. |

`profile_complete` is false until first and last name are set. Publishing and booking return
`422 PROFILE_INCOMPLETE` until then.

### Account deletion

`DELETE /v1/me` is immediate and cannot be undone. In one transaction it:

- cancels the trips the user drives that have not departed (passengers get `trip_cancelled`) and
  the user's pending bookings and approved bookings on trips that have not departed (drivers get
  `booking_cancelled_by_passenger`);
- deletes vehicles, photos, sessions, push tokens, the inbox, blocks, OTP rows and the reviews
  **about** the user;
- clears the texts the user wrote (trip description and conditions, meeting points, booking
  messages, review comments) and keeps the ratings the user gave to others;
- clears name, bio, photo and rating and releases the phone number, which can register again
  as a new account.

Every access token of the account then gets `401 UNAUTHORIZED`. Past trips and bookings stay in
the history of the other participants; the embedded user object there has `"deleted": true` and
empty names, and clients show a placeholder such as "Deleted user". A deleted user cannot be
reviewed (`422 REVIEW_NOT_ALLOWED`).

A driver whose trip is under way must complete it first (`POST /v1/trips/{id}/complete`), then
delete. A passenger on a trip that has departed can delete; that booking completes with the trip.

An account that an admin blocked keeps its phone number after deletion, so the ban holds.

## Blocks and reports

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/users/{id}/block` | Blocks a user → `204`. Idempotent. `404` unknown or deleted user, `422` when `id` is the caller. |
| DELETE | `/v1/users/{id}/block` | Removes the block → `204`. Idempotent. |
| GET | `/v1/me/blocks` | `{items: [user…]}`, the users the caller blocked, newest first (at most 200). |
| POST | `/v1/reports` | `{reported_user_id, trip_id?, reason, details?}` → `201 {id}`. |

A block works in both directions: neither user sees the other's trips in search, and a booking
between them is refused with `403 USER_UNAVAILABLE`. Bookings made before the block are not
changed; either side can cancel or reject them as usual.

`reason` is one of `spam, fraud, abuse, no_show, unsafe, fake_profile, inappropriate_content,
other`. `details` is free text up to 1000 characters and is required for `other`. A user cannot
report themselves. Reports go to the admin queue; the reported user is not notified.

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

`id, status, driver{id, first_name, last_name, photo_url, rating_avg, rating_count, completed_trips, deleted?},
vehicle{brand, model, color, plate?, year, vehicle_type, passenger_seats, photo_url},
stops[{order, location, meeting_point, planned_at, stop_type}], departs_at, arrives_at, seats_total,
free_seats_by_segment[], price, price_negotiable, currency, description, conditions, amenities,
created_at, updated_at, match?{pickup_order, dropoff_order, free_seats}, viewer?{is_driver,
booking_id, booking_status}, pending_requests?, approved_seats?`

- `free_seats_by_segment[i]` is free seats between stop `i` and stop `i+1`.
- `vehicle.plate` is present only for the driver and for a passenger whose booking is approved or completed.
- `pending_requests` and `approved_seats` are present only for the driver.
- Every embedded user (`driver`, `passenger`, `author`, `actor`) has `deleted: true` when the
  account was deleted; its names and photo are then empty.

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

## Admin: staff accounts and roles

Every `/admin/v1` route except sign-in needs `Authorization: Bearer <token>`, where the token is
either a staff session token (below) or the static `ADMIN_TOKEN`. The static token acts as an
admin without an account: use it to create the first accounts, for scripts and in emergencies;
the audit log names it `ADMIN_TOKEN`. Anything else gets `401 UNAUTHORIZED`.

Two roles. **admin** can do everything. **moderator** gets the overview, reports (list, resolve),
users (search, card, tabs, block, unblock) and analytics; account deletion, SMS gateways, staff
management and the audit log answer `403 FORBIDDEN`.

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/admin/v1/auth/login` | No token. `{login, password}` → `{token, expires_at, staff}`. The session lasts 12 h. Wrong login, wrong password and a disabled account all give `401 INVALID_CREDENTIALS`. At most 10 attempts per client address per 15 min (`429 RATE_LIMITED` with `Retry-After`). Logins are case-insensitive. |
| POST | `/admin/v1/auth/logout` | Ends the session in use → `204`. |
| GET | `/admin/v1/me` | The signed-in account (staff object). With the static token: `{name: "ADMIN_TOKEN", role: "admin", via_token: true}`. |
| POST | `/admin/v1/me/password` | `{current_password, new_password}` → `204`. Ends the account's other sessions. `401 INVALID_CREDENTIALS` for a wrong current password, `403` with the static token. |
| GET | `/admin/v1/staff` | Admin. `{items: [staff]}`, active accounts first. |
| POST | `/admin/v1/staff` | Admin. `{login, name, role, password}` → `201` staff. `login`: 3–32 of `a–z 0–9 . _ -`, starting with a letter or digit, stored lowercase; `name` 1–80 characters; `role` `admin|moderator`; `password` 10–72 bytes. `422 VALIDATION_ERROR` with `field`, `409 LOGIN_TAKEN`. |
| PATCH | `/admin/v1/staff/{id}` | Admin. `{name?, role?, disabled?}` → staff. Disabling ends the account's sessions. `409 SELF_CHANGE` for one's own role or `disabled`; `409 LAST_ADMIN` when no active admin would remain. |
| POST | `/admin/v1/staff/{id}/password` | Admin. `{password}` → `204`. Sets a new password and ends every session of that account. |
| GET | `/admin/v1/audit?staff_id=&user_id=&action=&page=&limit=` | Admin. The audit log, newest first: `{items: [{id, staff_id?, staff_name, action, target_user_id?, target?, note?, created_at}], has_more}`. `user_id` filters entries about one app account. Actions: `staff.login`, `staff.create`, `staff.role`, `staff.disable`, `staff.enable`, `staff.update` (rename), `staff.password_reset`, `staff.password_change`, `report.resolve` (target: report id), `user.block`, `user.unblock`, `user.delete` (note: masked phone), `gateway.create`, `gateway.revoke`. |

Staff object: `{id, login, name, role, created_at, last_login_at, disabled, disabled_at?}`.
Passwords are stored as bcrypt hashes and session tokens as SHA-256; neither is ever returned or
logged.

## Admin: moderation

Admin token or staff session (see above). Error messages follow `Accept-Language` like the
product API.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/admin/v1/reports?status=&reason=&reported_user_id=&reporter_id=&page=&limit=` | Report queue, oldest first. Every filter is optional: `status` `open|resolved`, `reason` one of the report reasons, the two ids UUIDs (`422` otherwise). Items: `id, reporter_id, reporter_name, reported_user_id, reported_name, reported_blocked, reported_deleted, trip_id?, reason, details?, status, created_at, resolved_at?`, plus the reported user's history across all reports: `reported_reports` (total), `reported_open_reports`, `reported_reporters` (distinct people). No phone numbers. |
| POST | `/admin/v1/reports/{id}/resolve` | Closes a report → `204`. Idempotent. |
| POST | `/admin/v1/users/{id}/block` | Bans an account → `204`. The user cannot sign in, every request gets `403 USER_BLOCKED`, sessions and push tokens are removed. |
| POST | `/admin/v1/users/{id}/unblock` | Lifts the ban → `204`. |
| POST | `/admin/v1/users/delete` | Admin. `{phone, user_id?}` → `204` (`user_id` only links the audit entry to the account). Deletes the account that owns the number, exactly like `DELETE /v1/me`. For deletion requests received outside the app (the website); check first that the requester owns the number. `404` when no account has it, `409 TRIP_IN_PROGRESS` as above. |

## Admin: panel

Read-only routes for the admin panel, open to both roles. Unlike the product API they return
phone numbers, because support needs them.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/admin/v1/overview` | Dashboard in one call: `{day_start, reports:{open}, users:{total, new_today, blocked}, trips:{upcoming, new_today}, bookings:{pending, new_today}, sms:{pending, failed_today, gateways_active, last_heartbeat_at}}`. "Today" starts at local midnight in `APP_TIMEZONE`; `day_start` is that instant. `users.total` excludes deleted accounts; `new_today` counts every registration of the day; `trips.upcoming` is `active`, `full` and `in_progress`; `last_heartbeat_at` is the newest heartbeat of an active gateway, `null` if none. |
| GET | `/admin/v1/users?q=&status=&page=&limit=` | Account search, newest first. `q` (≤ 100 characters) matches the full name, case-insensitive, when it has letters, and the phone when it has 3 or more digits (spaces, `+` and `-` are ignored, so `65 12` finds `+99365 12…`). Empty `q` lists every account. `status`: empty = not deleted, `active` = neither blocked nor deleted, `blocked` (banned, deleted ones included), `deleted`. Items: `id, phone?, first_name, last_name, photo_url?, rating_avg, rating_count, blocked, deleted, created_at, open_reports`. |
| GET | `/admin/v1/users/{id}` | Account card, deleted and banned accounts included. The list fields plus `bio?, locale, deleted_at?, vehicles` (vehicle objects, plate included) and `activity`: `trips_upcoming, trips_completed, trips_cancelled` (as driver), `driver_cancellations` (bookings on their trips that the driver cancelled, whole-trip cancellations included), `bookings_active, bookings_completed, passenger_cancellations` (as passenger), `reports_against, reports_against_open, reporters` (distinct people), `reports_filed, blocked_by` (users who blocked them). `404` for an unknown or malformed id. |
| GET | `/admin/v1/users/{id}/trips?scope=upcoming|history&page=&limit=` | Trips the account drives, exactly as `GET /v1/me/trips` returns them to that driver. |
| GET | `/admin/v1/users/{id}/bookings?scope=upcoming|history&page=&limit=` | The account's bookings as a passenger, exactly as `GET /v1/me/bookings` returns them to that passenger (the driver's phone appears on approved and completed bookings). |
| GET | `/admin/v1/users/{id}/reviews?page=&limit=` | Reviews the account received, as `GET /v1/users/{id}/reviews`. |

## Admin: analytics

When people use the app, for pricing ad slots by hour. Both roles. The API counts the
distinct signed-in users of every authenticated `/v1` request per local hour and per local day
(`APP_TIMEZONE`). The ids of the current hour and day live only in memory, to count each person
once; only the counts are written (`activity_hours`, `activity_days`), every minute and on
shutdown. A restart within an hour or day counts that bucket again from zero and the larger
count is kept, so figures around a deploy are slightly low, never high. The app's notification
badge asks the API every 45 s while the app is open, so an open app counts in every hour it is
open.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/admin/v1/analytics/activity?days=` | `days` 1–90 (default 28, `422` otherwise), today included. Returns `{timezone, from, to, tracking_since, days, hours, heatmap, peaks}`. `from` is local midnight of the first day, `to` now. `tracking_since` is the first counted hour (`null` before anyone was counted). `days`: `[{day: "YYYY-MM-DD", users}]`, every day from the first counted one in the period to today, zeros included. `hours`: the last 48 hours up to the current one, `[{hour_start, users}]`, zeros included. `heatmap`: 168 slots `{weekday (1 = Monday … 7 = Sunday), hour (0–23), avg, max}`: the mean distinct users of that hour of the week over the period (from `tracking_since` if later; quiet hours count as zero; the running hour is left out) and the busiest single one. `peaks`: up to five slots with the highest `avg`, busiest first. |

## App-store review login

Store reviewers cannot receive an SMS on a Turkmenistan number. When `OTP_REVIEW_PHONE` and
`OTP_REVIEW_CODE` are set, that one number always accepts that 6-digit code: `request-otp`
answers `202` as usual but queues no SMS, and `verify-otp` with the fixed code signs in. The
per-IP and per-device OTP limits still apply to it; the per-phone limits do not. Leave both
variables empty when no review is in progress.
