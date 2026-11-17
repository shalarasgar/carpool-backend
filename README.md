# Carpool backend

Go API for the Turkmenistan intercity carpool app (Flutter client: `../yolugra`). It provides phone OTP login through the Android SMS gateway, profiles, vehicles, locations, trips with intermediate stops, segment-based bookings with transactional seat control, contact privacy, reviews, and an in-app notification inbox.

| Document | Content |
| --- | --- |
| [docs/API_DOCUMENTATION.md](docs/API_DOCUMENTATION.md) | Every endpoint, request and response, error codes. |
| [docs/DATABASE_SCHEMA.md](docs/DATABASE_SCHEMA.md) | Tables, constraints, migrations. |
| [docs/BOOKING_AND_SEAT_MANAGEMENT.md](docs/BOOKING_AND_SEAT_MANAGEMENT.md) | Segment capacity, locking, cancellation, driver overlap rule. |
| [../yolugra/docs](../yolugra/docs) | Business rules, architecture, localization, setup, testing of the whole product. |

## Prerequisites

- Go 1.23 or newer
- Docker with Compose
- [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html)
- [golang-migrate](https://github.com/golang-migrate/migrate)

`sqlc` and `migrate` are required for `make sqlc`, `make migrate-up`, and `make migrate-down`. Install them and put their directories on `PATH`:

```bash
# macOS
brew install sqlc golang-migrate

# or with Go
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

The `postgres` build tag is required so the `migrate` CLI can talk to PostgreSQL. The API process uses the golang-migrate library directly and does not need the CLI.

## Local setup

```bash
cp .env.example .env
docker compose up -d postgres
make migrate-up
make run
```

The server listens on `:8080`. `make run` loads `.env` and does not apply migrations unless `MIGRATE_ON_START=true`.

Run the API and Postgres together (the container applies migrations on startup):

```bash
docker compose up --build
```

## Environment

| Variable | Purpose |
| --- | --- |
| `APP_NAME` | Service name. Default `carpool`. |
| `PORT` | HTTP port. Default `8080`. |
| `DEV_MODE` | Local development flag. Default `false`. |
| `TRUST_PROXY` | Take the client IP from the last `X-Forwarded-For` entry. Default `false`. Enable only behind a reverse proxy that is the only route to the API (see [deploy/](deploy/README.md)). |
| `DATABASE_URL` | PostgreSQL URL (`postgres://` or `postgresql://`). |
| `JWT_SECRET` | Signing secret. Minimum 16 characters. Unused until auth is added. |
| `OTP_HMAC_SECRET` | OTP HMAC secret. Minimum 16 characters. Unused until auth is added. |
| `ADMIN_TOKEN` | Static admin token for `/admin/v1`. Minimum 16 characters. |
| `MIGRATE_ON_START` | Apply embedded migrations before serving. Default `false`. |
| `DB_MAX_CONNS` | Pool max connections. Default `10`. |
| `DB_MIN_CONNS` | Pool min connections. Default `2`. |
| `DB_MAX_CONN_LIFETIME` | Max connection age. Default `30m`. |
| `DB_MAX_CONN_IDLE_TIME` | Max idle time. Default `5m`. |
| `DB_HEALTH_CHECK_PERIOD` | Pool health-check interval. Default `1m`. |

`JWT_SECRET`, `OTP_HMAC_SECRET`, `ADMIN_TOKEN`, and the database password are never written to logs.

## Make targets

| Target | Action |
| --- | --- |
| `make run` | Run the API. |
| `make build` | Build `bin/api`. |
| `make test` | `go test ./...` |
| `make migrate-up` | Apply all pending migrations with the CLI. |
| `make migrate-down` | Roll back the latest migration. |
| `make sqlc` | Regenerate `internal/store` from `db/queries`. |
| `make lint` | `go vet ./...` |

`make migrate-up` and `make migrate-down` read `DATABASE_URL` from the environment or `.env`.

## Verify

With Postgres running and migrations applied:

```bash
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

`/healthz` is liveness and returns `200`:

```json
{"status":"ok"}
```

`/readyz` is readiness. It returns `200` when the database answers:

```json
{"status":"ok","database":"up","sms_gateway_healthy":false}
```

`sms_gateway_healthy` is informational. It is true only when an active SMS gateway heartbeated within the last 30 seconds. No gateway is configured until an admin creates one, so the field stays false and the status stays `200`.

## SMS gateway and OTP

Phone login and the Android SMS gateway are unchanged by the product API; the gateway endpoints and their contract stay as below.

The backend generates the OTP and the SMS text. The phone sends that text unchanged. `sent` means Android's telephony stack accepted the message. A carrier delivery receipt is a separate `delivered` report.

| Method | Path | Auth |
| --- | --- | --- |
| POST | `/v1/auth/request-otp` | Public. Body `{phone, device_id}`. `202 {"status":"accepted"}`. `DEV_MODE=true` also returns `dev_code`. |
| POST | `/v1/auth/verify-otp` | Public. Body `{phone, code, device_id}`. Returns access and refresh tokens. |
| POST | `/v1/auth/refresh` | Public. Body `{refresh_token, device_id}`. Rotates the refresh token. |
| POST | `/v1/auth/logout` | Public. Body `{refresh_token}`. |
| GET | `/gateway/v1/pending?limit=1` | Gateway bearer key. Claims jobs with `FOR UPDATE SKIP LOCKED`. |
| POST | `/gateway/v1/sending` | Gateway bearer key. Body `{id}`. Call this before `SmsManager`. |
| POST | `/gateway/v1/report` | Gateway bearer key. Body `{id, status, error, not_submitted}`. `status` is `sent`, `failed`, `unknown`, `delivered`, or `delivery_failed`. |
| POST | `/gateway/v1/heartbeat` | Gateway bearer key. |
| POST | `/admin/v1/gateways` | Admin bearer token. Returns `api_key` once. |
| GET | `/admin/v1/gateways` | Admin bearer token. |
| POST | `/admin/v1/gateways/{id}/revoke` | Admin bearer token. |
| GET | `/admin/v1/sms/stats` | Admin bearer token. |
| GET | `/admin/v1/reports` | Admin bearer token. Report queue. |
| POST | `/admin/v1/reports/{id}/resolve` | Admin bearer token. |
| POST | `/admin/v1/users/{id}/block`, `/unblock` | Admin bearer token. Ban or reinstate an account. |
| POST | `/admin/v1/users/delete` | Admin bearer token. Body `{phone}`. Deletes an account on request. |

Create a gateway, then paste the key into the Android app:

```bash
curl -s -X POST http://localhost:8080/admin/v1/gateways \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"pixel\",\"operator_prefixes\":[\"61\",\"62\",\"71\"]}"
```

A job whose send has started is not returned to the pending pool when its lease expires. That avoids a second SMS when the phone crashed after calling `SmsManager` and before reporting. A lease that expires before `/gateway/v1/sending` is claimed again. Messages older than `OTP_TTL` that were never handed to the radio are failed as `expired`.

Phone numbers must be Turkmenistan mobiles: `+993` and 8 digits, starting with a prefix in `OTP_MOBILE_PREFIXES` (default `6,7`). The SMS text fits in one segment (at most 70 characters, since the Turkmen text is sent as UCS-2), for example `Ýolugra: kod 123456. Kody hiçkim bilen paýlaşmaň!`

If Postgres is down, `/readyz` returns `503`:

```json
{"status":"unavailable","database":"down","sms_gateway_healthy":false}
```

A panic returns `500` with `{"error":{"code":"INTERNAL","message":"Internal server error"}}`. Unknown paths return `404` with code `NOT_FOUND`. Every response includes `X-Request-ID`.

Stop the server with Ctrl-C. It drains open requests, then closes the database pool.

## Product API

Summary; details in [docs/API_DOCUMENTATION.md](docs/API_DOCUMENTATION.md). All need a Bearer access token except `/v1/locations`.

| Area | Endpoints |
| --- | --- |
| Profile | `GET/PATCH/DELETE /v1/me`, `POST/DELETE /v1/me/photo`, `POST /v1/me/phone/request-otp`, `POST /v1/me/phone/verify`, `GET /v1/users/{id}`, `GET /v1/users/{id}/reviews` |
| Blocks and reports | `POST/DELETE /v1/users/{id}/block`, `GET /v1/me/blocks`, `POST /v1/reports` |
| Vehicles | `GET/POST /v1/me/vehicles`, `PUT/DELETE /v1/me/vehicles/{id}`, `POST /v1/me/vehicles/{id}/photo` |
| Locations | `GET /v1/locations` (public, ETag) |
| Trips | `POST/GET /v1/trips`, `GET/PATCH /v1/trips/{id}`, `POST /v1/trips/{id}/cancel`, `POST /v1/trips/{id}/complete`, `GET /v1/me/trips` |
| Bookings | `POST/GET /v1/trips/{id}/bookings`, `GET /v1/bookings/{id}`, `POST /v1/bookings/{id}/approve|reject|cancel`, `GET /v1/bookings/{id}/contact`, `GET /v1/me/bookings` |
| Reviews | `POST /v1/bookings/{id}/review` |
| Notifications | `GET /v1/me/notifications`, `GET /v1/me/notifications/unread-count`, `POST /v1/me/notifications/read` |

New environment variables: `APP_TIMEZONE` (default `Asia/Ashgabat`), `TRIP_OVERLAP_BUFFER` (`30m`), `TRIP_COMPLETE_GRACE` (`6h`), `TRIP_REMIND_BEFORE` (`2h`), `MEDIA_DIR` (`./media`), `API_RATE_PER_MIN` (`120`), `OTP_REVIEW_PHONE` and `OTP_REVIEW_CODE` (empty; see below).

Integration tests need a disposable database: `make test-integration` (defaults to `carpool_test` on the compose Postgres).

## Account deletion, moderation, and the store review login

These exist because Google Play requires them of an app with accounts and user content.

- **Account deletion.** `DELETE /v1/me` from the app, or `POST /admin/v1/users/delete {phone}` by an operator for a request that arrived through the website. Both erase the personal data at once; what is removed and what stays is listed in [docs/API_DOCUMENTATION.md](docs/API_DOCUMENTATION.md#account-deletion).
- **Blocks and reports.** Users block and report each other. Reports land in `GET /admin/v1/reports`; an operator resolves them and can ban an account.
- **Review login.** `OTP_REVIEW_PHONE` and `OTP_REVIEW_CODE` make one number accept one fixed 6-digit code without an SMS, so a reviewer outside Turkmenistan can sign in. Give the pair to the store in the "App access" form. Use a number you own, keep nothing real in that account, and clear both variables when the review is over.

```bash
# Delete an account for a request received by email
curl -s -X POST http://localhost:8080/admin/v1/users/delete \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"phone\":\"+99361234567\"}"

# Open reports, then ban the reported account
curl -s "http://localhost:8080/admin/v1/reports?status=open" -H "Authorization: Bearer $ADMIN_TOKEN"
curl -s -X POST http://localhost:8080/admin/v1/users/<user-id>/block -H "Authorization: Bearer $ADMIN_TOKEN"
```

## Assumptions

- Trip routes cannot change after publication (bookings reference the stops); drivers cancel and republish instead. Schedule, seats, price, comfort and meeting points can change.
- Bookings are closed once a trip departs, including pickups at later stops.
- Contact numbers stay visible for completed bookings, so participants can reach each other about the finished trip.
- Push notifications are not integrated: there is no Firebase project. The inbox plus client polling covers the first version; `device_tokens` and the event table are ready for FCM.
- Ashgabat is a city with no parent welayat. It is administered separately from Ahal, Balkan, Dashoguz, Lebap, and Mary.
- The location seed contains those five welayats and these cities: Ashgabat, Anau (Änew), Tejen, Balkanabat, Turkmenbashi, Dashoguz, Turkmenabat, Mary, and Bayramaly. Welayats and those cities are `is_active = true`.
- Etrap rows are left for `migrations/000003_seed_etraps.up.sql`, which is empty until an official list is added.
- `reports.status` is `open` or `resolved`; the spec does not define a status enum. `reports.reason` is a closed list so the client can translate it.
- A deleted account keeps an empty `users` row, because other users' trips, bookings and reviews reference it. The spec does not cover deletion; erasing the personal data and keeping anonymous history is the simplest option that leaves other users' history intact.
- Deleting an account is refused while a trip the user drives is under way. The driver completes the trip first; the alternative, completing it silently, would change a trip's outcome as a side effect.
- A user-to-user block does not cancel bookings made before it. Either side can cancel or reject them.
- An account banned by an admin keeps its phone number after deletion, so deleting and signing up again does not lift a ban.
- The spec's admin list also names "activate/deactivate locations"; that is still done in the database.
- Phone numbers stored in the database must match `+993` followed by 8 digits.
- A user cannot block or review themselves. Those checks are database constraints.
- Phase A error messages are English. Localized text arrives with the i18n work in a later phase. Codes (`INTERNAL`, `NOT_FOUND`, `METHOD_NOT_ALLOWED`) are stable.
- Startup fails when the database ping fails, so a misconfigured `DATABASE_URL` is visible immediately. `/healthz` is only reachable once that ping has succeeded.
- `make lint` runs `go vet`. Install `golangci-lint` separately if you want a stricter lint later.
