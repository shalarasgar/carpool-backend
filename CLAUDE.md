# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Go API for Ýolugra, an intercity carpool app for Turkmenistan (Flutter client in `../yolugra`, product docs in `../yolugra/docs`). Phone + OTP login through an Android SMS gateway phone, profiles, vehicles, trips with intermediate stops, segment-based bookings, reviews, an in-app notification inbox, and FCM push.

`docs/PROJECT_SPEC.md` is the original brief. Read the relevant section before implementing a feature, and do not add features it does not list (no payments, chat, or maps APIs). Where the spec, the README, and the code disagree, the code wins: parts of the README predate auth and push.

## Commands

The Makefile wraps plain Go commands. `make` is often missing on Windows, so the direct form is given.

| Task | Command |
| --- | --- |
| Run the API (loads `.env`) | `go run ./cmd/api` (`make run`) |
| Build | `go build ./...` (`make build` writes `bin/api`) |
| Lint | `go vet ./...` (`make lint`) |
| Unit tests | `go test ./...` (`make test`) |
| One package | `go test ./internal/ride/` |
| One test | `go test ./internal/ride/ -run TestName -v` |
| Postgres only | `docker compose up -d postgres` |
| API + Postgres | `docker compose up --build` (migrates on start) |
| Migrations (needs `migrate` CLI) | `make migrate-up`, `make migrate-down` |
| Regenerate sqlc (needs `sqlc` CLI) | `make sqlc` |

Run `go build ./...`, `go vet ./...`, and `go test ./...` before finishing any change.

Integration and end-to-end tests (`internal/store`, `internal/http`) talk to a real PostgreSQL and skip themselves unless `CARPOOL_TEST_DATABASE_URL` is set. They apply migrations themselves and create their own users, so the database is reusable, but it must exist (`carpool_test` on the compose Postgres):

```bash
# bash / make test-integration
CARPOOL_TEST_DATABASE_URL='postgres://carpool:carpool@localhost:5432/carpool_test?sslmode=disable' go test -count=1 ./internal/store/ ./internal/http/
```

```powershell
$env:CARPOOL_TEST_DATABASE_URL='postgres://carpool:carpool@localhost:5432/carpool_test?sslmode=disable'; go test -count=1 ./internal/store/ ./internal/http/
```

A passing `go test ./...` without that variable says nothing about SQL, locking, or the router against a real database. Run the integration tests after touching `internal/store` or a migration.

The server does not apply migrations unless `MIGRATE_ON_START=true`. They are embedded in the binary (`migrations/embed.go`), so the API itself never needs the `migrate` CLI.

## Architecture

### Dependency rule

```
cmd/api/main.go            composition root: the only place that wires concrete types
internal/http              router, middleware, handlers (transport)
internal/service           use cases; no HTTP types, no SQL
internal/store             PostgreSQL (pgx); implements the service interfaces
internal/ride | auth | smsqueue    domain: types, rules, sentinel errors; no HTTP, no SQL
```

Dependencies point inward: `http → service → domain ← store`. Keep it that way:

- **Domain packages are pure.** `ride` (trips, stops, bookings, seat math, status transitions), `auth` (OTP, phone format, JWT), and `smsqueue` (SMS job state machine) import neither `net/http` nor `pgx`. A rule that can be a pure function belongs there, with a unit test.
- **Interfaces are declared by the consumer.** Each service file starts with the store interface it needs (`TripStore`, `BookingStore`, `ProfileStore`, …); `push` and `jobs` do the same (`push.Store`, `push.Sender`, `jobs.Maintainer`). One concrete `store.AppDB` satisfies all of them. A new persistence need means a method on the consumer's interface plus an `AppDB` method, not a service importing `store`.
- **Handlers translate, nothing else.** Decode the body into an unexported request struct, build a `ride` input, call one service method, map the result with a `toX` function into a DTO. Request and response structs live next to their handlers (`internal/http/dto` and `internal/sms` are empty placeholders).
- **Services orchestrate.** They normalize input (`in.Normalize()`), reject malformed ids with `ride.ValidID` → `ride.ErrNotFound`, pass `s.now()` to the store, and re-read the resource after a mutation so every write returns the fresh state.

### Store

Despite what the spec says, almost nothing is sqlc. Only `db.go`, `models.go`, and `health.sql.go` are generated (from `db/queries/health.sql` and the `migrations/` schema); never edit those by hand. Everything else is hand-written SQL in `internal/store/*_db.go` as methods on `AppDB`, returning `ride`/`auth`/`smsqueue` types rather than row structs.

Invariants that need a lock are enforced inside store transactions (`db.inTx`), which call the pure `ride` functions under the lock and return `ride` sentinel errors. The rules, detailed in `docs/BOOKING_AND_SEAT_MANAGEMENT.md`:

- Seats are never a stored counter. Free seats per segment are computed from approved bookings (`ride.Fits`, `ride.SegmentLoad`); `trips.status = full` is derived in the same transaction.
- Lock order is fixed: trip row first (`lockTrip`), then booking (`lockBookingWithTrip`). Driver schedule checks lock `users` then `trips`. Any new seat-changing transaction must follow the same order.
- Repeating an already-applied decision or cancellation returns unchanged; keep new state changes idempotent the same way.
- Legal status changes are the transition tables in `internal/ride/ride.go`.

### Notifications and push

A state change inserts its inbox rows in the same transaction (`insertEvents`, deduplicated by `dedupe_key`). The inbox table doubles as the push outbox: `push.Dispatcher` claims unsent rows before sending (at-most-once: a crash loses a push, never repeats it), builds the text in the user's locale, and sends through FCM, deleting tokens FCM rejects. The sentences in `push/text.go` mirror the Flutter app's inbox strings (`note*` keys in its ARB files); change both together. The FCM client is hand-written on `net/http` (service-account JWT → OAuth token → HTTP v1 send); there is no Firebase SDK. Push is off unless `FCM_CREDENTIALS_FILE` is set (key kept in gitignored `./secrets/`).

### Background jobs

`jobs.Reaper` is a generic ticker over anything with `Maintain(ctx) error`. `main.go` starts three: SMS lease reaping (`service.Gateway`, 15s), trip lifecycle and reminders (`service.Trips`, 1m), and push dispatch (`PUSH_INTERVAL`). A new periodic task implements `Maintain` and is started the same way.

### OTP and SMS gateway

The backend generates the OTP and the SMS text and queues a job; an Android phone polls `/gateway/v1/*` with its gateway key, claims jobs (`FOR UPDATE SKIP LOCKED`), reports `sending` before handing the text to the radio, then reports the result. A job whose send has started is never returned to the pending pool, so a phone crash cannot cause a second SMS. Gateways are created through `/admin/v1` with the static `ADMIN_TOKEN`.

### Errors and localization

Failures are sentinel errors in the domain packages (`ride.ErrX`, plus `*ride.FieldError{Field, Reason}` for validation and `*ride.OverlapError`). Handlers map them to the envelope `{"error":{"code","message","field?","details?"}}`:

- `writeRideErr` (product API): table `rideErrors` in `handlers/ride_common.go`, message localized from `Accept-Language` through `internal/i18n` (tk default, ru, en).
- `writeErr` (auth, gateway, admin): English messages, `handlers/api.go`.

Adding a product error means: sentinel in `ride`, row in `rideErrors`, three translations in `i18n`, entry in `docs/API_DOCUMENTATION.md`. Clients switch on the code; codes are stable.

### Router and config

`server.NewRouter` mounts a route group only when its dependency in `Deps` is non-nil, which is how tests build partial routers. `/v1/*` sits behind `UserAuth` (JWT plus an active-user check) and a per-user rate limiter; the caller's id comes from `middleware.UserFrom(ctx)`.

`config.Config` is loaded with envconfig, validated in `validate()`, and implements `slog.LogValuer` so secrets never reach logs. A new variable goes into `config.go`, `validate()`, `.env.example`, and the README table.

## Conventions

- Stack is fixed: chi, pgx/v5, sqlc, golang-migrate, envconfig, slog, and the standard library. No ORM, no extra frameworks. Tests use only `testing` (the spec mentions testify and validator; the code deliberately uses neither).
- Time is injected: services and the dispatcher hold `now func() time.Time`, and store methods take `now` as a parameter. Do not call `time.Now()` inside business logic; tests replace the clock.
- Unit tests use in-memory fakes of the consumer interfaces (`memDB` in `service/flow_test.go`, `fakeStore`/`fakeSender` in `push`), not mocks of the database.
- Never log OTP codes, tokens, push tokens, message texts, or full phone numbers. Store errors go through `dbErr(op, err)`, which drops the PostgreSQL `Detail` because it can contain row values.
- Constructors are `NewX(store, cfg, logger)` and tolerate a nil logger. Every exported identifier has a doc comment; comments state the invariant or the reason (lock order, idempotency), not the mechanics.
- Lists return `{items, has_more}`; ids are UUID strings everywhere.
- Schema changes are a new numbered `up`/`down` pair in `migrations/`, with `docs/DATABASE_SCHEMA.md` updated; endpoint changes update `docs/API_DOCUMENTATION.md`.
- When the spec is ambiguous, take the simplest option and record it under "Assumptions" in the README.
- A deleted account is an empty `users` row with `deleted_at` set and `phone` NULL (see `store/account_db.go`). New queries that read `phone` must tolerate NULL, and queries that find a live user must filter `deleted_at IS NULL`. New tables holding personal data need a step in `eraseUserData`.
- The public site in `../website` (privacy policy, account deletion page) states what this backend collects, who sees it, how long it is kept, and what deletion removes. A change to any of that changes those pages too, in all three languages.
