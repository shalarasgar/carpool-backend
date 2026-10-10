# Carpool backend

Backend for the Turkmenistan intercity carpool API. Phase 1 (spec phases A–F) is implemented:

- Phase A: configuration, PostgreSQL, migrations, structured logging, health checks.
- Phase B: phone + OTP authentication, JWT access tokens with rotating refresh tokens, the SMS queue drained by an Android gateway phone, gateway and admin endpoints, background maintenance, localized errors (tk/ru/en).
- Phase C: the caller's profile and photo, vehicles, public profiles, and the location directory.
- Phase D: trips, search, and bookings, including the seat lock that prevents overselling.
- Phase E: reviews, user blocks, reports, the in-app notification inbox, and the rest of the admin API.
- Phase F: OpenAPI kept in sync with the router, an `api.http` example for every route, production Compose, and a database backup script.

Push notifications (FCM), chat, and payments are not implemented.

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

The image runs as uid 65532 and stores photos on the `photos` volume. A production-shaped stack (`DEV_MODE=false`, no published database port) is in [Production](#production).

## Environment

| Variable | Purpose |
| --- | --- |
| `APP_NAME` | Service name, JWT issuer and the prefix of the OTP SMS. Default `carpool`. |
| `PORT` | HTTP port. Default `8080`. |
| `DEV_MODE` | When `true`, `request-otp` returns the code as `dev_code`. Never enable outside local development. Default `false`. |
| `DATABASE_URL` | PostgreSQL URL (`postgres://` or `postgresql://`). |
| `JWT_SECRET` | HS256 signing secret for access tokens. Minimum 16 characters. |
| `OTP_HMAC_SECRET` | HMAC-SHA256 key for stored OTP hashes. Minimum 16 characters. |
| `ADMIN_TOKEN` | Static bearer token for `/admin/v1/*`. Minimum 16 characters. |
| `MIGRATE_ON_START` | Apply embedded migrations before serving. Default `false`. |
| `ALLOWED_PHONE_PREFIXES` | Comma-separated digit prefixes allowed right after `+993`. Default `6,7`. |
| `OTP_TTL` | Code lifetime. Default `5m`. |
| `OTP_MAX_ATTEMPTS` | Wrong codes before the code is invalidated. Default `5`. |
| `OTP_PHONE_COOLDOWN` | Minimum gap between codes for one phone. Default `60s`. |
| `OTP_PHONE_DAILY_LIMIT` | Codes per phone per 24 hours. Default `5`. |
| `OTP_IP_LIMIT` / `OTP_IP_WINDOW` | Codes per client IP per window. Default `30` / `1h`. |
| `OTP_DEVICE_LIMIT` / `OTP_DEVICE_WINDOW` | Codes per `device_id` per window. Default `10` / `1h`. |
| `RATE_LIMITER` | Limiter backend. Only `memory` exists; the interface allows Redis later. |
| `ACCESS_TOKEN_TTL` | JWT lifetime. Default `15m`. |
| `REFRESH_TOKEN_TTL` | Refresh token lifetime. Default `720h` (30 days). |
| `SMS_PROVIDER` | `queue` (write to `sms_queue` for the gateway phone) or `noop` (drop; pair with `DEV_MODE=true`). Default `queue`. |
| `SMS_MAX_ATTEMPTS` | Delivery attempts before a message is marked `failed`. Default `3`. |
| `SMS_LOCK_DURATION` | How long a claimed message stays locked for the gateway. Default `60s`. |
| `SMS_GATEWAY_HEALTHY_WINDOW` | A gateway is healthy if it heartbeated within this window. Default `30s`. |
| `SMS_GATEWAY_MAX_BATCH` | Upper bound for `?limit=` on `/gateway/v1/pending`. Default `50`. |
| `JOBS_INTERVAL` | Background maintenance period. Default `30s`. |
| `NOTIFICATION_READ_RETENTION` | Delete read inbox rows older than this. Unread rows are kept. Default `720h` (30 days). |
| `REVIEW_WINDOW` | How long after `departs_at` a participant may leave a review. Default `336h` (14 days). |
| `REPORT_USER_LIMIT` / `REPORT_USER_WINDOW` | Reports one user may file per window. Default `10` / `24h`. |
| `PHOTO_MAX_BYTES` | Maximum profile photo size. Default `2097152` (2 MiB). |
| `PHOTO_ALLOWED_TYPES` | Comma-separated sniffed types. Default `image/jpeg,image/png,image/webp`. |
| `PHOTO_DIR` | Directory for the local photo store. Default `data/photos`. The container image uses `/data/photos`. |
| `PHOTO_URL_PREFIX` | Public path prefix stored in `photo_url`. Default `/v1/media/photos`. |
| `MAX_VEHICLES_PER_USER` | Vehicles one account may keep. Default `3`. |
| `TRUST_PROXY_HEADERS` | Use `X-Forwarded-For` / `X-Real-IP` for the client IP. Enable only behind a proxy that overwrites them. Default `false`. |
| `DB_MAX_CONNS` | Pool max connections. Default `10`. |
| `DB_MIN_CONNS` | Pool min connections. Default `2`. |
| `DB_MAX_CONN_LIFETIME` | Max connection age. Default `30m`. |
| `DB_MAX_CONN_IDLE_TIME` | Max idle time. Default `5m`. |
| `DB_HEALTH_CHECK_PERIOD` | Pool health-check interval. Default `1m`. |

`JWT_SECRET`, `OTP_HMAC_SECRET`, `ADMIN_TOKEN`, gateway API keys, OTP codes, refresh tokens and the database password are never written to logs. Phone numbers are logged masked (`+99365***456`).

## Make targets

| Target | Action |
| --- | --- |
| `make run` | Run the API. |
| `make build` | Build `bin/api`. |
| `make test` | `go test ./...` (unit tests; integration tests skip without `TEST_DATABASE_URL`). |
| `make test-integration` | Same, with `TEST_DATABASE_URL` pointing at the docker-compose Postgres. |
| `make migrate-up` | Apply all pending migrations with the CLI. |
| `make migrate-down` | Roll back the latest migration. |
| `make sqlc` | Regenerate `internal/store` from `db/queries`. |
| `make lint` | `golangci-lint run ./...` (config in `.golangci.yml`; install the binary separately). |
| `make openapi-check` | Fail if a route registered in the router is missing from `openapi.yaml`, or the spec lists a route the router does not serve. Also checks error codes, trip status `expired`, and notification types. |

`api.http` has one example request per endpoint for the VS Code REST Client and the JetBrains HTTP Client. Set `@baseUrl`, `@accessToken`, `@adminToken`, and `@gatewayKey` at the top of the file.

`make migrate-up` and `make migrate-down` read `DATABASE_URL` from the environment or `.env`.

Integration tests (`internal/integration`) create a throwaway database per test on the server named by `TEST_DATABASE_URL`, apply the migrations, and drop it afterwards. The development database is never touched. They cover the full OTP login flow, refresh-token rotation and reuse detection, blocked users, attempt limits, rate limits, parallel `SKIP LOCKED` claiming, gateway retries, operator-prefix routing, the maintenance job, profile update and anonymized re-registration, photo upload, vehicle ownership, and location filtering with ETag. Later files cover trip search, booking approval under parallel load, reviews, blocks, reports, the inbox, and admin moderation.

## Authentication flow

1. `POST /v1/auth/request-otp {"phone"}` normalizes the number to E.164 (`+99365123456`, `99365123456`, `865123456`, `65123456`, with spaces/dashes), checks the rate limits, stores an HMAC-SHA256 of a 6-digit `crypto/rand` code, and hands the SMS to the `SmsProvider`. The response never says whether the phone belongs to a user.
2. The gateway phone polls `GET /gateway/v1/pending`, sends the SMS, and reports back.
3. `POST /v1/auth/verify-otp {"phone","code","device_id"}` checks the code in constant time, consumes it, creates the user on first login, revokes older refresh tokens of that `device_id`, and returns `access_token` (JWT, 15 min), `refresh_token` (opaque, 30 days, stored hashed), `user` and `is_new_user`.
4. `POST /v1/auth/refresh {"refresh_token"}` rotates the pair. Presenting a token that was already rotated revokes every token of that device.
5. `POST /v1/auth/logout` with `Authorization: Bearer <access_token>` revokes every refresh token of the device in the token.

Blocked users (`users.is_blocked`) get `403 USER_BLOCKED` from `verify-otp`, `refresh`, and every JWT route (`RequireUser` loads the account on each request). `request-otp` behaves exactly as for anyone else. An anonymized account's access token returns `401`.

### Error envelope and codes

Every error is `{"error":{"code":"...","message":"<localized>"}}`. `message` follows `Accept-Language` (`tk` default, `ru`, `en`); the response carries `Content-Language`. `VALIDATION_ERROR` adds `details` mapping field to failed rule.

| Status | Code | When |
| --- | --- | --- |
| 400 | `BAD_REQUEST` | Malformed JSON or unknown field |
| 401 | `OTP_INVALID` | Wrong code, no active code, or already used |
| 401 | `OTP_EXPIRED` | Code older than `OTP_TTL` |
| 401 | `OTP_ATTEMPTS_EXCEEDED` | `OTP_MAX_ATTEMPTS` wrong codes already consumed |
| 401 | `REFRESH_TOKEN_INVALID` / `REFRESH_TOKEN_EXPIRED` | Refresh failures |
| 401 | `UNAUTHORIZED` | Missing or invalid bearer token / gateway key |
| 403 | `USER_BLOCKED` | Blocked account |
| 403 | `FORBIDDEN` | Wrong admin token, booking your own trip, a blocked pair, or a trip/booking action that is not yours |
| 404 | `NOT_FOUND` | Unknown user, vehicle, or photo. Another user's vehicle is also `404` |
| 404 | `TRIP_NOT_FOUND` | Unknown trip id, or a trip hidden because of a block |
| 404 | `BOOKING_NOT_FOUND` | Unknown booking id |
| 409 | `SEAT_UNAVAILABLE` | Not enough seats, or the trip is full |
| 409 | `BOOKING_EXISTS` | This passenger already has a pending or approved booking on the trip |
| 409 | `REVIEW_EXISTS` | This reviewer already reviewed that person on this trip |
| 409 | `REVIEW_WINDOW_CLOSED` | `departs_at` is older than `REVIEW_WINDOW` |
| 409 | `INVALID_STATE` | The trip or booking is not in a state that allows the action |
| 404 | `SMS_NOT_FOUND` | Report for an id this gateway does not hold |
| 409 | `GATEWAY_EXISTS` | Duplicate gateway name |
| 413 | `PAYLOAD_TOO_LARGE` | JSON body over 64 KiB |
| 413 | `PHOTO_TOO_LARGE` | Profile photo over `PHOTO_MAX_BYTES` |
| 422 | `INVALID_PHONE` / `VALIDATION_ERROR` | Input validation |
| 422 | `PHOTO_TYPE_UNSUPPORTED` | Bytes are not an allowed image type |
| 422 | `VEHICLE_LIMIT` | `MAX_VEHICLES_PER_USER` reached |
| 429 | `RATE_LIMITED` | `Retry-After` header holds the wait in seconds |
| 500 | `INTERNAL` | Unexpected failure; see logs by `X-Request-ID` |

## SMS gateway contract (Android app)

The server never connects to the phone. The Android app polls the server. Register each phone once and keep the returned key in the app:

```bash
curl -s -X POST localhost:8080/admin/v1/gateways \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"android-phone-1","operator_prefixes":[]}'
# -> {"id":"...","name":"android-phone-1","api_key":"gw_...","operator_prefixes":[],"created_at":"..."}
```

The `api_key` is shown once; only its SHA-256 is stored. `operator_prefixes` are national-number prefixes (digits after `+993`, e.g. `["61","62","63","64","65"]`). A gateway with prefixes is preferred for matching numbers and skipped for others; a gateway without prefixes serves any number.

All gateway calls send `Authorization: Bearer <api_key>`.

| Call | Purpose |
| --- | --- |
| `GET /gateway/v1/pending?limit=10` | Claims up to `limit` pending messages (`FOR UPDATE SKIP LOCKED`), marks them `processing`, locks them for `SMS_LOCK_DURATION`, and records a heartbeat. Response: `{"messages":[{"id","phone","message","attempts","locked_until","created_at"}]}`. |
| `POST /gateway/v1/report {"id","status":"sent"\|"failed","error"}` | Reports the outcome. `failed` re-queues with `attempts+1`; after `SMS_MAX_ATTEMPTS` the message becomes `failed`. Response: `{"id","status","attempts"}`. |
| `POST /gateway/v1/heartbeat` | Keeps the gateway healthy while idle. |

Loop the app should run:

1. Every 3-5 seconds call `GET /pending`. Send each message with `SmsManager`, then `POST /report` with `sent` or `failed` plus the exception text.
2. Report before `locked_until` (60 s by default). If the app misses it, the server returns the message to `pending` and it will be delivered again, so report promptly even on failure.
3. When `/pending` returned nothing for 20 s, call `POST /heartbeat` so the server keeps considering the phone healthy (`SMS_GATEWAY_HEALTHY_WINDOW`, 30 s). `/pending` and `/report` also count as heartbeats.
4. On `401` stop and alert the operator: the key was revoked or the gateway was deactivated.

Routing: when the server enqueues, it assigns the message to a healthy gateway (heartbeat within 30 s, prefix match first). A gateway claims messages assigned to it plus unassigned ones. If no gateway is healthy the message is queued unassigned, a warning is logged, and `/readyz` reports `"sms_gateway_healthy": false`. Messages assigned to a gateway that later goes silent are unassigned by the background job so another phone can take them.

### Background maintenance

Every `JOBS_INTERVAL` (and once at startup) the process:

- returns `processing` messages whose `locked_until` passed to `pending`;
- unassigns `pending` messages routed to gateways that stopped heartbeating or were deactivated;
- deletes OTP codes expired for more than 1 hour;
- deletes refresh tokens that expired, or were revoked more than 7 days ago;
- marks past `active` and `full` trips `completed` when they have at least one approved booking and `seats_left` equals `seats_total` minus those seats;
- marks past `active` and `full` trips `expired` when they have no approved booking;
- sets still-`pending` bookings on any trip whose `departs_at` has passed to `cancelled_by_driver` and writes a `booking_cancelled` inbox row in that same statement. They are not `rejected`, because the driver never decided. Future pending bookings and approved bookings are left alone;
- deletes read inbox rows older than `NOTIFICATION_READ_RETENTION`. Unread rows are kept.

A past trip that has approved bookings but a `seats_left` that does not match those seats is left as it is, and the count is logged. Approving a booking after departure returns `INVALID_STATE`.

## Profiles, vehicles and locations

`GET /v1/me` and `PATCH /v1/me` use the caller's JWT. `PATCH` accepts `name` and/or `locale` (`tk`, `ru`, `en`). `GET /v1/users/{id}` is also authenticated, but the JSON is a public profile only: `name`, `photo_url`, `rating_avg`, `rating_count`, the newest `vehicle`, and `completed_trips`. There is no `phone` field. `completed_trips` is completed trips the user drove plus completed trips they rode with an approved booking.

`POST /v1/me/photo` is `multipart/form-data` with a part named `photo`. The part's `Content-Type` is ignored. The server sniffs the bytes (`jpeg`, `png`, `webp`), rejects anything else, and re-encodes the image so EXIF and other metadata are dropped. WebP is stored as JPEG, or as PNG when the picture has transparency. Files are random 32-hex names under `PHOTO_DIR` and are served at `GET /v1/media/photos/{name}` with no directory listing. `photo_url` in JSON is that path (`/v1/media/photos/...`), not an absolute URL. The storage type is an interface with a local-disk implementation so an S3 backend can replace it later. In the container image the directory is `/data/photos`. Compose mounts the `photos` volume there. The process runs as uid 65532, and `photos-init` chowns that volume before the API starts. A host bind mount has to be writable by uid 65532.

`DELETE /v1/me` anonymizes the account in one transaction. It cancels the user's `active` and `full` trips and the pending or approved bookings on them (`cancelled_by_driver`), cancels the user's own pending or approved bookings on other people's `active` or `full` trips (`cancelled_by_passenger`, restoring seats and re-opening a `full` trip), deletes their vehicles, clears the name and photo, and revokes every refresh token. `users.phone` becomes `anon:` + HMAC-SHA256 of the number (keyed with `OTP_HMAC_SECRET`) + the user id. The cleartext number is not kept. That frees the unique E.164 value, so the next OTP login with the same number creates a new user with an empty profile (`is_new_user: true`). The old row stays, with its ratings, so history is not rewritten onto the new account. Completed, cancelled and expired trips are not rewritten. If restoring seats would push `seats_left` above `seats_total`, the booking is still cancelled and the trip's seat counters are left unchanged. The previous access token then gets `401`.

Vehicles: `GET` and `POST /v1/me/vehicles`, `PATCH` and `DELETE /v1/me/vehicles/{id}`. Brand, model, color and plate are validated in the service. The plate is stored uppercased and must contain 4–10 letters and digits. Ownership is checked in the service; a vehicle that belongs to someone else is `404`, the same as a missing id. The default cap is 3 (`MAX_VEHICLES_PER_USER`). Creating a vehicle locks the user row so two parallel requests cannot both pass the cap.

`GET /v1/locations` is public. Filters: `type` (`welayat`, `city`, `etrap`), `parent_id`, `q`, `active_only` (default `true`). `name` follows `Accept-Language`. `q` is a Unicode case-insensitive substring of the Turkmen, Russian and English names, applied in the service so it does not depend on the database locale. The directory is small, so the query reads the table and filters in memory. The response sets `ETag` (SHA-256 of the JSON) and `Cache-Control: public, max-age=300`. Repeat the call with `If-None-Match` to get `304` and an empty body. `Accept-Encoding: gzip` compresses the `200` body. `Vary` is `Accept-Language, Accept-Encoding`.

## Trips and bookings

All of these routes require a JWT. Trip JSON never contains a phone number. A booking contains `driver_phone` only for the passenger and only after approval, and `passenger.phone` only for the driver and only after approval.

`POST /v1/trips` creates a trip. `departs_at` must be in the future, `seats_total` is 1..8, the two locations must differ and be active, and `vehicle_id`, when sent, must belong to the driver. `suggested_price` is optional (a decimal string or a JSON number) and is informational. Currency defaults to `TMT`.

`GET /v1/trips` searches bookable trips. Filters: `from`, `to`, `date` (`YYYY-MM-DD` in Asia/Ashgabat, UTC+5), `seats` (minimum `seats_left`), `page` (default 1), `page_size` (default 20, max 50). A welayat id includes that location when it is active and every active descendant, including descendants reached through an inactive parent. The recursive walk stops at depth 8. Cancelled, full, completed, expired and past trips are excluded, as are trips with no seats left, trips of a deleted or admin-blocked driver, and trips whose driver is blocked with the caller in either direction. `GET /v1/trips/{id}` returns any status. A blocked viewer who is not the driver gets `404 TRIP_NOT_FOUND`.

`PATCH /v1/trips/{id}` is the driver only, and only while the trip is `active` or `full`. `seats_total` cannot fall below the seats already approved (`409 INVALID_STATE`). `seats_left` is adjusted in the same update: zero seats left makes the trip `full`, and freeing a seat on a full trip makes it `active` again. Another user gets `403 FORBIDDEN`.

`POST /v1/trips/{id}/cancel` is the driver only. In one transaction the trip becomes `cancelled` and every pending or approved booking becomes `cancelled_by_driver`. Calling it again is a no-op. `POST /v1/trips/{id}/complete` is the driver only, at or after `departs_at`. It does not change bookings. `GET /v1/me/trips?status=` lists the driver's trips.

`POST /v1/trips/{id}/bookings {"seats":1}` is the passenger. Booking your own trip, or a user blocked in either direction, is `403 FORBIDDEN`. The trip must be `active` and in the future with enough `seats_left`. Pending requests do not reserve seats; approval does. A second live request by the same passenger is `409 BOOKING_EXISTS` (the partial unique index). `GET /v1/me/bookings` is the passenger's list and includes the trip. `GET /v1/trips/{id}/bookings` is the driver's list.

`POST /v1/bookings/{id}/approve` runs in one database transaction: lock the acting user, lock the trip with `SELECT ... FOR UPDATE`, lock the booking, check status and `seats_left`, decrement `seats_left`, set the trip to `full` at zero, then set the booking to `approved`. Approving the same booking again does not take the seats a second time. Not enough seats is `409 SEAT_UNAVAILABLE`. `POST /v1/bookings/{id}/reject` is the driver and only for a pending booking. `POST /v1/bookings/{id}/cancel` is the passenger or the driver. Cancelling an approved booking restores the seats and re-opens a `full` trip in that same locked transaction.

Those decisions insert an inbox row in the same transaction: `booking_requested`, `booking_approved`, `booking_rejected`, `booking_cancelled`, and `trip_cancelled` (one event per passenger when a trip is cancelled, not a second booking event). If the insert fails, the booking or trip change rolls back with it. Account deletion and an admin block do the same for the events they emit. The stored row is the event type plus `actor_id`, `trip_id`, and `booking_id`. Phone numbers are not stored. `title` and `body` are chosen from the recipient's saved `locale` when the inbox is read, not from `Accept-Language`.

**Lock order** is user, then trip, then booking. Mutating requests lock the acting user before the trip, and the trip before the booking. Account deletion uses that same order. A review locks the reviewed user and, when the reviewer row is locked too, locks the two user rows in id order before the trip and the booking, so two mutual reviews do not deadlock.

## Reviews, blocks, reports, and the inbox

`POST /v1/trips/{id}/reviews` accepts `to_user_id`, `rating` (1..5), and an optional `comment` (500 characters). It is allowed only on a `completed` trip, only between the driver and a passenger who has an `approved` booking, and only once in each direction. Two passengers cannot review each other. Reviewing yourself is `403 FORBIDDEN`. A second review of the same person on the same trip is `409 REVIEW_EXISTS`. A trip that is not `completed` is `409 INVALID_STATE`. A review after `REVIEW_WINDOW` (default 14 days from `departs_at`, not from the moment the trip was marked completed) is `409 REVIEW_WINDOW_CLOSED`. The recipient row is locked, then the trip, then the approved booking. When the reviewer is locked too, the two user rows are locked in id order so mutual reviews cannot deadlock. `rating_count` and `rating_avg` are recomputed from that user's reviews in the same transaction, so parallel reviews of one person cannot lose an update. Review JSON has no phone number.

`GET /v1/users/{id}/reviews` is page-based (`page`, `page_size`), newest first. A missing or deleted user is `404`.

`POST /v1/users/{id}/block` is idempotent (`201` the first time, `200` if the block already exists). `DELETE` is idempotent and returns `204`. `GET /v1/me/blocks` lists who the caller blocked. A user-to-user block does not cancel an approved booking and does not change seats. It hides each side's trips from the other's search, rejects a new booking, and rejects approval of a request that is still pending (`403 FORBIDDEN`). `GET /v1/trips/{id}` for the other party is `404 TRIP_NOT_FOUND`, which was already the rule in Phase D. Blocking yourself is `403`.

`POST /v1/reports` takes `reported_user_id`, optional `trip_id`, `reason` (`spam`, `harassment`, `scam`, `unsafe_driving`, `no_show`, `other`), and optional `details` (1000 characters). Reporting yourself is `403`. Each user may file `REPORT_USER_LIMIT` reports per `REPORT_USER_WINDOW` (default 10 per 24 hours); the limiter is the same in-memory limiter as OTP, so it resets on restart and is not shared between processes. A new report has status `open`.

`GET /v1/me/notifications?limit=&cursor=` is cursor-paginated, newest first, and includes `unread_count` for the whole inbox. `POST /v1/me/notifications/read` takes `{"ids":[...]}` or `{"all":true}`. Ids that are not the caller's unread rows are ignored.

## Admin

`/admin/v1` uses `Authorization: Bearer $ADMIN_TOKEN`. The comparison is constant-time. A missing or wrong token is `403`, including a normal user JWT.

| Call | Purpose |
| --- | --- |
| `POST /admin/v1/gateways` | Register a gateway. Returns the API key once. |
| `GET /admin/v1/gateways` | List gateways with `healthy` (active and a heartbeat inside `SMS_GATEWAY_HEALTHY_WINDOW`). No API key. |
| `GET /admin/v1/sms/stats` | `{pending, processing, sent, failed}` counts. |
| `GET /admin/v1/reports?status=` | Page of reports. `status` is optional. |
| `PATCH /admin/v1/reports/{id}` | Set status to `open`, `reviewing`, `resolved`, or `dismissed`. |
| `POST /admin/v1/users/{id}/block` | Set `is_blocked`, revoke every refresh token, and run the account-deletion cascade without anonymizing. Vehicles are deleted because that cascade deletes them. The name, photo, and phone stay. |
| `DELETE /admin/v1/users/{id}/block` | Clear `is_blocked`. Trips, bookings, vehicles, and refresh tokens are not restored. The existing access token works again until it expires; the refresh token does not. |
| `PATCH /admin/v1/locations/{id}` | `{"is_active": false}`. Existing trips are not changed. New trips cannot use an inactive location. Search already skips inactive endpoints. |

## Verify

With Postgres running, migrations applied and `DEV_MODE=true` in `.env` (the `.env.example` default):

```bash
ADMIN_TOKEN=replace-with-a-long-random-admin-token   # the value from .env
BASE=http://localhost:8080

curl -s $BASE/healthz
curl -s $BASE/readyz
# {"status":"ok","database":"up","sms_gateway_healthy":false}

# 1. Register the gateway phone (do this once) and keep the key.
GW=$(curl -s -X POST $BASE/admin/v1/gateways \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"android-phone-1"}')
echo "$GW"
GW_KEY=$(echo "$GW" | sed -E 's/.*"api_key":"([^"]+)".*/\1/')

# 2. Ask for a code.
curl -s -i -X POST $BASE/v1/auth/request-otp \
  -H 'Content-Type: application/json' -H 'Accept-Language: ru' \
  -d '{"phone":"+993 65 12 34 56"}'
# 200 {"expires_in":300,"retry_after":60,"dev_code":"482913"}

# Immediately again -> 429 with Retry-After.
curl -s -i -X POST $BASE/v1/auth/request-otp -H 'Content-Type: application/json' -d '{"phone":"+99365123456"}'

# 3. Simulate the gateway phone: claim the SMS...
curl -s "$BASE/gateway/v1/pending?limit=10" -H "Authorization: Bearer $GW_KEY"
# {"messages":[{"id":"<SMS_ID>","phone":"+99365123456","message":"carpool: kod 482913. Nikomu ne soobshchaite ego.",...}]}

# ...and report it sent.
curl -s -X POST $BASE/gateway/v1/report -H "Authorization: Bearer $GW_KEY" \
  -H 'Content-Type: application/json' -d '{"id":"<SMS_ID>","status":"sent"}'

curl -s $BASE/readyz
# {"status":"ok","database":"up","sms_gateway_healthy":true}

# 4. Sign in with the code (use dev_code from step 2).
curl -s -X POST $BASE/v1/auth/verify-otp -H 'Content-Type: application/json' \
  -d '{"phone":"+99365123456","code":"482913","device_id":"curl-device"}'
# {"access_token":"...","token_type":"Bearer","expires_in":900,"refresh_token":"...","user":{...},"is_new_user":true}

# 5. Rotate tokens.
curl -s -X POST $BASE/v1/auth/refresh -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<REFRESH_TOKEN>"}'

# 6. Log out (204), then the refresh token no longer works (401 REFRESH_TOKEN_INVALID).
curl -s -i -X POST $BASE/v1/auth/logout -H "Authorization: Bearer <ACCESS_TOKEN>"

# Phase C. TOKEN is the access_token from verify-otp.

# Own profile, then rename it.
curl -s $BASE/v1/me -H "Authorization: Bearer $TOKEN"
curl -s -X PATCH $BASE/v1/me -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Aýna Amanowa","locale":"ru"}'

# Public profile. The JSON has no phone field.
curl -s $BASE/v1/users/<USER_ID> -H "Authorization: Bearer $TOKEN"

# Photo. The part Content-Type is ignored; the bytes are sniffed and re-encoded.
curl -s -X POST $BASE/v1/me/photo -H "Authorization: Bearer $TOKEN" \
  -F "photo=@face.jpg;type=image/jpeg"
curl -s -o face-out.jpg $BASE/v1/media/photos/<FILE>

# Vehicles. A second user calling PATCH or DELETE on this id gets 404.
curl -s $BASE/v1/me/vehicles -H "Authorization: Bearer $TOKEN"
curl -s -X POST $BASE/v1/me/vehicles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"brand":"Toyota","model":"Camry","color":"ak","plate":"AG 1234 AB"}'
curl -s -X PATCH $BASE/v1/me/vehicles/<VEHICLE_ID> -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"color":"gara"}'
curl -s -i -X DELETE $BASE/v1/me/vehicles/<VEHICLE_ID> -H "Authorization: Bearer $TOKEN"

# Locations. Public. The second call returns 304.
ETAG=$(curl -s -D - -o /dev/null -H 'Accept-Language: ru' "$BASE/v1/locations?type=city" | awk 'tolower($1)=="etag:" {print $2}' | tr -d '\r')
curl -s -D - -o /dev/null -H 'Accept-Language: ru' -H "If-None-Match: $ETAG" "$BASE/v1/locations?type=city"
curl -s -H 'Accept-Encoding: gzip' -H 'Accept-Language: en' --compressed \
  "$BASE/v1/locations?q=mary&active_only=true"

# Phase D. Two users. ASH and MARY are location ids from GET /v1/locations
# (Ashgabat city c2000000-0000-4000-8000-000000000001, Mary city c2000000-0000-4000-8000-000000000008).
# Sign the passenger in the same way as step 4, with a second phone, and keep PASSENGER_TOKEN.
# DEPARTS must be in the future.

curl -s -X POST $BASE/v1/trips -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"from_location_id":"c2000000-0000-4000-8000-000000000001","to_location_id":"c2000000-0000-4000-8000-000000000008","departs_at":"2026-12-01T08:00:00Z","seats_total":2,"suggested_price":"25.50"}'
# Search from the passenger. A welayat id expands to its active cities. The JSON has no phone.
curl -s "$BASE/v1/trips?from=c2000000-0000-4000-8000-000000000001&to=a1000000-0000-4000-8000-000000000005&seats=1" \
  -H "Authorization: Bearer $PASSENGER_TOKEN"
curl -s -X POST $BASE/v1/trips/<TRIP_ID>/bookings -H "Authorization: Bearer $PASSENGER_TOKEN" \
  -H 'Content-Type: application/json' -d '{"seats":1}'
# Before approval neither booking list contains a phone number.
curl -s $BASE/v1/me/bookings -H "Authorization: Bearer $PASSENGER_TOKEN"
curl -s $BASE/v1/trips/<TRIP_ID>/bookings -H "Authorization: Bearer $TOKEN"
curl -s -X POST $BASE/v1/bookings/<BOOKING_ID>/approve -H "Authorization: Bearer $TOKEN"
# After approval the passenger sees driver_phone and the driver sees passenger.phone.
curl -s $BASE/v1/me/bookings -H "Authorization: Bearer $PASSENGER_TOKEN"
curl -s $BASE/v1/trips/<TRIP_ID>/bookings -H "Authorization: Bearer $TOKEN"
curl -s -X POST $BASE/v1/bookings/<BOOKING_ID>/cancel -H "Authorization: Bearer $PASSENGER_TOKEN"
# Cancelling the approved booking re-opens a full trip. The driver can also reject, or:
curl -s -X POST $BASE/v1/trips/<TRIP_ID>/cancel -H "Authorization: Bearer $TOKEN"

# Phase E. Complete the trip after departs_at, then review. The other direction is a separate review.
curl -s -X POST $BASE/v1/trips/<TRIP_ID>/complete -H "Authorization: Bearer $TOKEN"
curl -s -X POST $BASE/v1/trips/<TRIP_ID>/reviews -H "Authorization: Bearer $PASSENGER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"to_user_id":"<DRIVER_USER_ID>","rating":5,"comment":"on time"}'
curl -s "$BASE/v1/users/<DRIVER_USER_ID>/reviews" -H "Authorization: Bearer $PASSENGER_TOKEN"

# Block. 201 the first time, 200 if repeated. Approved bookings stay approved.
curl -s -X POST $BASE/v1/users/<DRIVER_USER_ID>/block -H "Authorization: Bearer $PASSENGER_TOKEN"
curl -s $BASE/v1/me/blocks -H "Authorization: Bearer $PASSENGER_TOKEN"
curl -s -i -X DELETE $BASE/v1/users/<DRIVER_USER_ID>/block -H "Authorization: Bearer $PASSENGER_TOKEN"

# Report. The sixth-or-so call in a fresh process is 429 once the per-user limit is hit.
curl -s -X POST $BASE/v1/reports -H "Authorization: Bearer $PASSENGER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"reported_user_id":"<DRIVER_USER_ID>","trip_id":"<TRIP_ID>","reason":"no_show","details":"did not arrive"}'

# Inbox. title/body follow the recipient's saved locale. Mark one id, or all.
curl -s "$BASE/v1/me/notifications?limit=20" -H "Authorization: Bearer $TOKEN"
curl -s -X POST $BASE/v1/me/notifications/read -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"all":true}'

# Admin. ADMIN_TOKEN is the value from .env.
curl -s "$BASE/admin/v1/reports?status=open" -H "Authorization: Bearer $ADMIN_TOKEN"
curl -s -X PATCH $BASE/admin/v1/reports/<REPORT_ID> -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' -d '{"status":"resolved"}'
curl -s -X POST $BASE/admin/v1/users/<USER_ID>/block -H "Authorization: Bearer $ADMIN_TOKEN"
curl -s -X DELETE $BASE/admin/v1/users/<USER_ID>/block -H "Authorization: Bearer $ADMIN_TOKEN"
curl -s -X PATCH $BASE/admin/v1/locations/<LOCATION_ID> -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' -d '{"is_active":false}'
curl -s $BASE/admin/v1/gateways -H "Authorization: Bearer $ADMIN_TOKEN"
curl -s $BASE/admin/v1/sms/stats -H "Authorization: Bearer $ADMIN_TOKEN"

# Anonymize. The same phone can verify-otp again and gets a new empty user.
# This also cancels the caller's active trips and passenger bookings and deletes their vehicles.
curl -s -i -X DELETE $BASE/v1/me -H "Authorization: Bearer $TOKEN"
```

A panic returns `500 INTERNAL`. Unknown paths return `404 NOT_FOUND`. Every response includes `X-Request-ID`. Stop the server with Ctrl-C: it drains open requests, stops the maintenance loop, then closes the database pool.

## Production

The API image is multi-stage and ends on `gcr.io/distroless/static-debian12:nonroot` (uid 65532). It has no shell. `docker-compose.yml` is the local stack (`DEV_MODE=true`, Postgres published on `5432`, the example secrets). `docker-compose.prod.yml` is the production-shaped example:

- `DEV_MODE=false`. Startup refuses the placeholder secrets from `.env.example` and `docker-compose.yml`, any secret containing `replace-with` or `change-me`, a secret with fewer than 8 distinct characters, and a process where `JWT_SECRET`, `OTP_HMAC_SECRET`, and `ADMIN_TOKEN` are not all different. `DEV_MODE=true` still accepts the local examples.
- Postgres is not published on the host. Set `POSTGRES_PASSWORD` to something other than the local `carpool` password.
- Profile photos live on the `photos` volume at `/data/photos`. `photos-init` runs `chown -R 65532:65532` on that volume, then exits, so the non-root process can write it.
- `TRUST_PROXY_HEADERS` defaults to `false`. Turn it on only behind a reverse proxy that discards the incoming `X-Forwarded-For` and `X-Real-IP` and sets them from the connecting client. When the flag is true, the per-IP OTP limit uses that address. A client who can supply the header can rotate it and avoid the limit.

```bash
POSTGRES_PASSWORD='long-random-db-password' \
JWT_SECRET='long-random-jwt-secret-value' \
OTP_HMAC_SECRET='long-random-otp-hmac-value' \
ADMIN_TOKEN='long-random-admin-token-value' \
  docker compose -f docker-compose.prod.yml up --build -d
```

Compose also reads a project `.env` for interpolation. If that file still has the example secrets, pass `--env-file` pointing at a file that does not. Inside the Compose network the API talks to Postgres with `sslmode=disable`. Use TLS when Postgres is not on that private network.

## Backup and restore

`scripts/backup-db.sh` writes a custom-format `pg_dump` (`-Fc`, no owners, no ACLs) to `backups/carpool-<UTC timestamp>.dump` with mode `0600`. The dump contains phone numbers. The script takes a directory lock so a second nightly run exits 0 instead of writing a second file, and it deletes dumps older than `BACKUP_RETENTION_DAYS` (default 14). `pg_dump` uses a snapshot, so it can run while the API is serving.

```bash
# Host client. DATABASE_URL comes from the environment or .env.
./scripts/backup-db.sh

# Client inside the Compose Postgres 16 image, so the versions match.
./scripts/backup-db.sh --docker
# COMPOSE_FILE=docker-compose.prod.yml ./scripts/backup-db.sh --docker
```

A nightly cron entry, from the repo root:

```bash
0 3 * * * cd /opt/carpool && ./scripts/backup-db.sh --docker >> /var/log/carpool-backup.log 2>&1
```

Stop the API before restoring. `scripts/restore-db.sh` requires `--yes` and replaces objects in the target database (`pg_restore --clean --if-exists --single-transaction`):

```bash
./scripts/restore-db.sh backups/carpool-20261010T030000Z.dump --yes
./scripts/restore-db.sh --docker backups/carpool-20261010T030000Z.dump --yes
```

The script does not print `DATABASE_URL`. After a restore, start the API again. `MIGRATE_ON_START` applies any migration newer than the dump.

## Phase 2

Not implemented. The schema already has the seams:

- **Etraps.** `locations.type` includes `etrap`, and search already expands a welayat to its active descendants. Add the official list in `migrations/000003_seed_etraps.up.sql` and mark the rows `is_active`. Do not invent etrap names. Trips between those rows then work without a code change.
- **`trip_stops`.** The table exists and Phase 1 does not read or write it. Phase 2 can attach intermediate stops to a trip and include them in search.
- **FCM.** `device_tokens` exists and nothing writes it. `notify.Notifier` is the delivery seam; Phase 1's implementation only inserts the in-app inbox, which the Flutter app polls. An FCM sender can sit beside that insert later. SMS fallback for notifications is the same seam.
- **Ratings.** `rating_avg` is one average over every review of the user. A later change can split driver and passenger ratings without changing the review request shape until the client needs it.

## Assumptions

Phase A:

- Ashgabat is a city with no parent welayat. It is administered separately from Ahal, Balkan, Dashoguz, Lebap, and Mary.
- The location seed contains those five welayats and these cities: Ashgabat, Anau (Änew), Tejen, Balkanabat, Turkmenbashi, Dashoguz, Turkmenabat, Mary, and Bayramaly. Welayats and those cities are `is_active = true`.
- Etrap rows are left for `migrations/000003_seed_etraps.up.sql`, which is empty until an official list is added.
- `reports.status` is text and defaults to `open`. The spec does not define a status enum.
- Active phone numbers stored in the database must match `+993` followed by 8 digits. Anonymized accounts store a tombstone instead; see Phase C.
- A user cannot block or review themselves. Those checks are database constraints.
- Startup fails when the database ping fails, so a misconfigured `DATABASE_URL` is visible immediately.
- `make lint` runs `golangci-lint` with `.golangci.yml`. Generated sqlc code under `internal/store` is excluded. `go vet` is one of the enabled linters.

Phase B:

- The rate limiter is in-memory and per process. Counters reset on restart and are not shared between replicas. It sits behind `ratelimit.Limiter` so a Redis or PostgreSQL implementation can replace it without touching the auth service. All rules of one request are checked atomically; a rejected request consumes no quota.
- `request-otp` accepts an optional `device_id` for the per-device limit (the spec only lists `phone`). The per-IP limit uses the socket address unless `TRUST_PROXY_HEADERS=true`.
- `request-otp` does not read the `users` table at all, so blocked users receive a code like everyone else and are refused at `verify-otp` / `refresh` with `403 USER_BLOCKED`. This keeps timing and responses identical for every phone.
- Requesting a new code invalidates the previous one for that phone; one active code per phone.
- The OTP HMAC covers phone and code, so a code cannot be replayed against another number. Refresh tokens and gateway API keys are random 256-bit values stored as plain SHA-256.
- Verification failures (`OTP_INVALID`, `OTP_EXPIRED`, `OTP_ATTEMPTS_EXCEEDED`) are `401`; validation failures (`INVALID_PHONE`, `VALIDATION_ERROR`) are `422`.
- `logout` requires a valid access token and revokes the whole device (the `device_id` carried in the JWT) rather than taking a refresh token in the body. Refresh first if the access token has expired.
- The SMS language follows `Accept-Language` of the `request-otp` call; the SMS text is ASCII-only to stay within one GSM-7 segment.
- A gateway claims messages assigned to it or unassigned, regardless of its `operator_prefixes`; prefixes only steer the initial assignment.
- The admin API in Phase B contained only `POST /admin/v1/gateways`, which is needed to bootstrap a gateway. The rest of `/admin/v1` arrived in Phase E.
- `golang.org/x/*` and `validator` are pinned to versions that still build with Go 1.23 so the Dockerfile base image keeps working.

Phase C:

- Deleting an account hashes the phone instead of keeping it reserved. A reserved cleartext number would block `users.phone` (unique, and constrained to `+993` plus 8 digits), so the same person could not register again. The tombstone is `anon:` + HMAC-SHA256(`OTP_HMAC_SECRET`, phone) + `:` + user id, and `deleted_at` is set. The hash is not reversible. The user id suffix keeps a second deletion of the same number from colliding. Ratings stay on the old row; the new login is a new user.
- `GET /v1/users/{id}` requires a JWT. "Public" means the payload, not an anonymous route. The phone is omitted. The vehicle shown is the most recently created one.
- Another user's vehicle id returns `404`, not `403`, so the API does not reveal which ids exist.
- Images wider or taller than 2048 px are rejected. Clients should resize before upload.
- Location search runs in the service after one ordered read of the `locations` table. That keeps Turkmen and Russian case-folding working on any database locale. The table is welayats, cities and, later, etraps.
- Profile photos in Docker live on the `photos` volume mounted at `/data/photos`. `photos-init` chowns the volume to uid 65532 before the API, which has no shell, starts. A bind mount still has to be writable by that uid.

Phase D:

- `date` on trip search is a calendar day in Asia/Ashgabat (UTC+5, no daylight saving), not UTC.
- Pagination is page-based. `page` defaults to 1, `page_size` to 20, and `page_size` cannot exceed 50. The response is `{trips|bookings, page, page_size, has_more}` and does not include a total count.
- A welayat search includes the welayat itself when it is active, plus every active descendant. The walk goes through inactive parents and stops at depth 8.
- Pending bookings do not reserve seats. Two passengers can both request the last seat; only the approval that still sees `seats_left` succeeds.
- `suggested_price` is a decimal string in responses (`"25.50"`). Requests may send that string or a JSON number. Currency is any 3 letters and defaults to `TMT`.
- Trip search also hides trips whose driver is deleted or `is_blocked`. Direct `GET /v1/trips/{id}` still returns those trips unless a `user_blocks` row exists in either direction.
- Completing a trip does not change its bookings. `expired` was added to `trip_status` for past trips that nobody was approved onto.
- Cancelling a trip notifies passengers with `trip_cancelled` only. It does not also emit `booking_cancelled`.
- Inbox rows for booking and trip changes are written inside the same database transaction. `notify.Nop` remains for tests that do not use the inbox.
- Account deletion restores seats only on `active` and `full` trips. A restore that would exceed `seats_total` still cancels the booking and leaves the trip counters alone, so a corrupted seat total cannot block deletion.
- Old sent SMS rows are not deleted yet. The job already removes expired OTP codes and refresh tokens.

Phase E:

- The review window is `REVIEW_WINDOW` after `departs_at`, default 14 days. It is not measured from the time the trip was marked `completed`.
- A review is one direction of a driver/passenger pair. The passenger reviewing the driver and the driver reviewing the passenger are two rows. Passenger-to-passenger is `403`.
- `rating_avg` is `round(avg(rating), 2)` of that user's reviews, recomputed under a lock on the reviewed user, so parallel reviews do not depend on commit order.
- A user-to-user block does not cancel approved bookings. It does stop a new booking and the approval of a pending one, and it hides search results in both directions.
- Admin `is_blocked` reuses the account-deletion cascade, including vehicle deletion, and does not anonymize. Unblock does not restore trips, bookings, vehicles, or refresh tokens.
- Deactivating a location does not rewrite trips that already use it. Search continues to skip inactive locations, which it already did.
- Report reasons are `spam`, `harassment`, `scam`, `unsafe_driving`, `no_show`, and `other`. Statuses are `open`, `reviewing`, `resolved`, and `dismissed`. The spec left both as text; the checks were added in migration `000006`.
- The report rate limit is in-memory and per process, keyed by user id.
- Inbox text is localized from `users.locale` at read time. `Accept-Language` does not change it. Pagination is a cursor (`limit`, `cursor`, `next_cursor`) plus `unread_count`. Reviews, blocks, and admin reports stay page-based.
- The maintenance job turns pending bookings on trips that have already departed into `cancelled_by_driver` and inserts `booking_cancelled` in the same statement. It does not use `rejected`.
- Read notifications older than 30 days are deleted. Unread notifications are kept.
- `POST /v1/users/{id}/block` returns `201` or `200`. `DELETE` returns `204` even when there was nothing to delete.

Phase F:

- `DEV_MODE=false` refuses the secrets published in `.env.example` and `docker-compose.yml`, secrets that contain `replace-with` or `change-me`, secrets with fewer than 8 distinct characters, and a process where `JWT_SECRET`, `OTP_HMAC_SECRET`, and `ADMIN_TOKEN` are not all different. The error text does not include the secret. `DEV_MODE=true` still accepts the local examples, which is what `docker-compose.yml` uses.
- The database password in `docker-compose.yml` stays `carpool` for local use. `docker-compose.prod.yml` requires `POSTGRES_PASSWORD` and does not publish `5432`.
- `make openapi-check` compares chi routes with `openapi.yaml`. It does not start a server. The same checks run as part of `go test ./...`.
- Backups are logical `pg_dump -Fc` files, safe to take while the API is running. Restore is manual, requires `--yes`, and expects the API to be stopped.
- `TRUST_PROXY_HEADERS` stays false unless a reverse proxy overwrites the forwarding headers. The per-IP OTP limit is the only caller.
