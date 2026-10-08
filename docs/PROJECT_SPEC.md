# Carpool Backend - Project Specification

You are a senior Go backend engineer. Build a complete, production-ready backend for a simplified ride-sharing (carpooling) mobile app for Turkmenistan, similar in concept to BlaBlaCar. The mobile client is Flutter. Work in phases, and at the end of each phase make sure the project compiles, tests pass, and the README is updated. Do not leave TODO stubs for core features.

## 1. Product summary
- Drivers publish intercity trips with free seats. Passengers search and request seats. The driver approves or rejects.
- NO in-app payments. Price is only an informational "suggested price" field; payment happens in cash outside the app.
- Login and registration are ONLY via phone number + OTP. No passwords.
- Phase 1 scope: intercity trips between major cities. The data model must already support Phase 2: trips between etraps (districts) and welayat (province) centers, without code changes (only data/config changes).
- Languages: Turkmen (tk), Russian (ru), English (en). API error messages must be localized via the Accept-Language header; default is tk.

## 2. Tech stack (strict)
- Go 1.22+, router: chi
- PostgreSQL 16, driver: pgx/v5 (pgxpool), query layer: sqlc
- Migrations: golang-migrate (SQL files, up/down)
- Config: environment variables (envconfig or similar), with a .env.example
- Logging: log/slog (structured JSON), request-ID middleware
- Auth: JWT access token (15 min) + rotating refresh token (30 days, stored hashed in DB, revocable per device)
- Validation: go-playground/validator
- Rate limiting: PostgreSQL- or in-memory-based (no mandatory Redis dependency); design it behind an interface so Redis can be added later
- Testing: standard testing + testify, integration tests with testcontainers-go or a docker-compose test DB
- OpenAPI 3 spec (openapi.yaml) kept in sync with the endpoints
- Dockerfile (multi-stage, distroless/alpine) + docker-compose.yml (app + postgres)
- Makefile targets: run, build, test, migrate-up, migrate-down, sqlc, lint
- Clean layered structure:

  cmd/api/main.go
  internal/config
  internal/http (router, middleware, handlers, dto, errors)
  internal/service (business logic, no HTTP types)
  internal/store (sqlc generated + transaction helpers)
  internal/auth (JWT, OTP)
  internal/sms (provider interface + implementations)
  internal/i18n
  internal/jobs (background workers)
  migrations/
  db/queries/ (sqlc queries)

## 3. Database schema (PostgreSQL)
Use UUID primary keys (gen_random_uuid()), timestamptz everywhere, and proper indexes and constraints.

- users(id, phone UNIQUE in E.164, name, photo_url, rating_avg numeric(3,2) default 0, rating_count int default 0, locale, is_blocked bool, created_at, updated_at)
- vehicles(id, user_id, brand, model, color, plate, created_at)
- locations(id, parent_id NULLABLE self-FK, type ENUM('welayat','city','etrap'), name_tk, name_ru, name_en, is_active bool, sort_order, created_at)
  - Hierarchy: welayat -> city/etrap. Provide a seed migration for Ashgabat city and the 5 welayats (Ahal, Balkan, Dashoguz, Lebap, Mary) and their main cities. Only major cities are is_active=true in Phase 1. Add a clearly marked seed file where the official, up-to-date etrap list can be added later; do not invent etrap data you are not sure about.
- trips(id, driver_id, vehicle_id NULLABLE, from_location_id, to_location_id, departs_at, seats_total, seats_left, suggested_price numeric(10,2) NULLABLE, currency default 'TMT', pickup_note, dropoff_note, note, status ENUM('active','full','cancelled','completed'), created_at, updated_at)
  - CHECK (seats_left >= 0 AND seats_left <= seats_total), CHECK (from_location_id <> to_location_id)
  - Indexes for search: (from_location_id, to_location_id, departs_at) and (status, departs_at)
- trip_stops(id, trip_id, location_id, stop_order, note) — create the table now and keep it unused in Phase 1 (Phase 2: intermediate stops)
- bookings(id, trip_id, passenger_id, seats, status ENUM('pending','approved','rejected','cancelled_by_passenger','cancelled_by_driver'), created_at, updated_at)
  - Partial unique index so a passenger has only one active (pending/approved) booking per trip
- reviews(id, trip_id, from_user_id, to_user_id, rating smallint 1..5, comment, created_at) — UNIQUE(trip_id, from_user_id, to_user_id)
- user_blocks(blocker_id, blocked_id, created_at)
- reports(id, reporter_id, reported_user_id, trip_id NULLABLE, reason, details, status, created_at)
- otp_codes(id, phone, code_hash, expires_at, attempts int, used bool, ip, device_id, created_at)
- refresh_tokens(id, user_id, token_hash, device_id, expires_at, revoked_at, created_at)
- sms_queue(id, phone, message, status ENUM('pending','processing','sent','failed'), attempts int, gateway_id NULLABLE, last_error, locked_until, created_at, sent_at)
- sms_gateways(id, name, api_key_hash, last_heartbeat_at, is_active, operator_prefixes text[], created_at)
- device_tokens(id, user_id, platform, token, created_at) — optional, for future push

## 4. Authentication and OTP flow
Endpoints:
- POST /v1/auth/request-otp {phone}
- POST /v1/auth/verify-otp {phone, code, device_id} -> {access_token, refresh_token, user, is_new_user}
- POST /v1/auth/refresh {refresh_token}
- POST /v1/auth/logout

OTP rules:
- 6-digit code from crypto/rand. Store only a hash (HMAC-SHA256 with a server secret). Never log the code or put it in API responses (except an explicit DEV_MODE=true flag that returns it for local testing).
- Valid for 5 minutes, max 5 wrong attempts, then the code is invalidated.
- Rate limits: 1 SMS per phone per 60 seconds, max 5 per phone per day (configurable), plus per-IP and per-device limits. Return 429 with Retry-After.
- Normalize and validate phone numbers to E.164 (+993 prefix, 8 digits after the country code; accept the +993 6x/7x mobile formats and make the allowed prefixes configurable).
- Use constant-time comparison. Response timing and messages must not reveal whether a user exists.
- SMS text: "<AppName>: kod 123456. Kimseyle paylasmanyn." (localized, ASCII-safe to keep it in a single SMS segment).

## 5. SMS gateway (Android phone acts as the SMS sender)
The server cannot reach the phone directly, so the phone POLLS the server.
- Define: type SmsProvider interface { Send(ctx, phone, text string) error }
- Implement QueueSmsProvider, which only inserts into sms_queue. The provider is selected via config so a real operator API can be plugged in later.
- Gateway endpoints (authenticated by a per-gateway API key in the Authorization header, hashed in DB):
  - GET /gateway/v1/pending?limit=10 — atomically claim pending messages using SELECT ... FOR UPDATE SKIP LOCKED, set status=processing and locked_until=now()+60s. Also acts as a heartbeat (updates last_heartbeat_at).
  - POST /gateway/v1/report {id, status: sent|failed, error} — mark result; on failure re-queue with attempts+1 up to a max (default 3), then mark failed.
  - POST /gateway/v1/heartbeat
- Messages stuck in processing past locked_until are returned to pending by a background job.
- Multi-gateway support: when enqueuing, choose an active gateway with a recent heartbeat (< 30s); optionally route by phone operator prefix via sms_gateways.operator_prefixes. If no healthy gateway exists, still enqueue, but log a warning and expose it in the health endpoint.
- Do NOT write the Android app now. Only provide a clear, documented API contract for it in the README/openapi.

## 6. Core API (all under /v1, JWT required unless noted, JSON, cursor- or page-based pagination)
Users:
- GET /me, PATCH /me (name, locale), POST /me/photo (multipart, size and type limits; store on local disk behind a Storage interface, S3-ready)
- GET /users/{id} (public profile: name, photo, rating, vehicle, number of completed trips)
- DELETE /me (soft delete / anonymize)
Vehicles:
- GET/POST /me/vehicles, PATCH/DELETE /me/vehicles/{id}
Locations:
- GET /locations?type=&parent_id=&q=&active_only=true (public, localized names by Accept-Language, cacheable with ETag)
Trips:
- POST /trips (driver creates; validate departs_at is in the future, seats 1..8, from != to, both locations active)
- GET /trips?from=&to=&date=&seats=&page= (search; if `from`/`to` is a welayat, include all active child locations recursively via recursive CTE; exclude cancelled, full, and past trips; exclude trips of users blocked in either direction)
- GET /trips/{id}
- PATCH /trips/{id} (only the driver, only while no approved bookings conflict, e.g. you cannot reduce seats below approved seats)
- POST /trips/{id}/cancel (driver; notify and cancel all bookings)
- POST /trips/{id}/complete (driver, only after departs_at)
- GET /me/trips (as driver), with status filter
Bookings:
- POST /trips/{id}/bookings {seats} (passenger requests; cannot book own trip; blocked-user check)
- GET /me/bookings (as passenger)
- GET /trips/{id}/bookings (driver sees requests)
- POST /bookings/{id}/approve and /reject (driver)
- POST /bookings/{id}/cancel (passenger or driver)
- The driver's phone number is returned in the booking response ONLY after approval; the passenger's phone is visible to the driver ONLY after approval. Never expose phones elsewhere.
Reviews:
- POST /trips/{id}/reviews {to_user_id, rating, comment} — only participants of a completed trip with an approved booking, once per pair; update rating_avg/rating_count transactionally.
- GET /users/{id}/reviews
Safety:
- POST /users/{id}/block, DELETE /users/{id}/block
- POST /reports {reported_user_id, trip_id?, reason, details}
Notifications (no FCM dependency in Phase 1):
- GET /me/notifications (in-app inbox table: booking requested/approved/rejected/cancelled, trip cancelled) + POST /me/notifications/read. The Flutter app will poll this. Design a Notifier interface so FCM or SMS fallback can be added later.

## 7. Concurrency and correctness (critical)
- Approving a booking must run in ONE DB transaction: lock the trip row (SELECT ... FOR UPDATE), verify seats_left >= booking.seats and trip status, decrement seats_left, set trip status to 'full' when it reaches 0, update the booking. Cancelling an approved booking restores seats and re-opens a 'full' trip.
- Write a concurrency integration test: N parallel approvals on a trip with fewer seats must never oversell.
- Use idempotent handlers where it makes sense, and consistent HTTP codes (400, 401, 403, 404, 409, 422, 429).
- A background job marks past active trips as completed/expired, and cleans expired OTPs, expired refresh tokens, and old sent SMS records.

## 8. Security and ops
- Standard error envelope: {"error": {"code": "SEAT_UNAVAILABLE", "message": "<localized>"}} with stable machine-readable codes the Flutter app can map.
- Middleware: request ID, structured logging, panic recovery, CORS (configurable), body size limit, timeouts, security headers, gzip.
- Authorization checks live in the service layer (ownership checks), not only in handlers.
- Blocked users (is_blocked) cannot authenticate.
- Admin endpoints under /admin/v1 protected by a separate static admin token (or an is_admin flag): list reports, block/unblock users, activate/deactivate locations, list gateways and their health, view SMS queue stats.
- GET /healthz (liveness) and GET /readyz (DB reachable, and at least one healthy SMS gateway reported as a non-fatal field).
- Graceful shutdown, DB connection pooling config, never log secrets, PII-aware logging (mask phone numbers).
- The service must work well on slow, unreliable mobile networks: small JSON payloads, pagination everywhere, gzip, ETag on static-ish data (locations).

## 9. Deliverables
1. Full source code following the structure above.
2. SQL migrations (up/down) and sqlc queries; generated code committed or generated via `make sqlc`.
3. openapi.yaml covering the public, gateway, and admin APIs.
4. Tests: unit tests for services (OTP, booking logic), integration tests for the main flows (OTP login, create trip, search with welayat expansion, booking approve with concurrency, review), and a test for SMS queue claiming with SKIP LOCKED.
5. README.md: setup, env variables, how to run locally with docker-compose, API overview, the gateway contract for the Android SMS app, and a Phase 2 roadmap (etrap-level trips, trip_stops, FCM, ratings improvements).
6. A Postman collection or a .http file with example requests for every endpoint.

## 10. Working method
- First output a short plan and the final folder tree, then implement phase by phase:
  Phase A: project skeleton, config, DB, migrations, health endpoints.
  Phase B: auth + OTP + SMS queue + gateway endpoints.
  Phase C: users, vehicles, locations.
  Phase D: trips, search, bookings (with transactions).
  Phase E: reviews, blocks, reports, notifications, admin.
  Phase F: tests, OpenAPI, README, Docker polish.
- After each phase, show how to run and verify it (curl examples).
- If a requirement is ambiguous, choose the simplest reasonable option, state the assumption in the README, and continue.
- Do not add features that are not listed (no payments, no chat, no maps/routing APIs).