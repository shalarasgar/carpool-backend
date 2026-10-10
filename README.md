# Carpool backend

Backend for the Turkmenistan intercity carpool API. Done so far:

- Phase A: configuration, PostgreSQL, migrations, structured logging, health checks.
- Phase B: phone + OTP authentication, JWT access tokens with rotating refresh tokens, the SMS queue drained by an Android gateway phone, gateway and admin endpoints, background maintenance, localized errors (tk/ru/en).

Trips, bookings, users/vehicles and the rest of the core API are not implemented yet.

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
| `make lint` | `go vet ./...` |

`make migrate-up` and `make migrate-down` read `DATABASE_URL` from the environment or `.env`.

Integration tests (`internal/integration`) create a throwaway database per test on the server named by `TEST_DATABASE_URL`, apply the migrations, and drop it afterwards. The development database is never touched. They cover the full OTP login flow, refresh-token rotation and reuse detection, blocked users, attempt limits, rate limits, parallel `SKIP LOCKED` claiming, gateway retries, operator-prefix routing, and the maintenance job.

## Authentication flow

1. `POST /v1/auth/request-otp {"phone"}` normalizes the number to E.164 (`+99365123456`, `99365123456`, `865123456`, `65123456`, with spaces/dashes), checks the rate limits, stores an HMAC-SHA256 of a 6-digit `crypto/rand` code, and hands the SMS to the `SmsProvider`. The response never says whether the phone belongs to a user.
2. The gateway phone polls `GET /gateway/v1/pending`, sends the SMS, and reports back.
3. `POST /v1/auth/verify-otp {"phone","code","device_id"}` checks the code in constant time, consumes it, creates the user on first login, revokes older refresh tokens of that `device_id`, and returns `access_token` (JWT, 15 min), `refresh_token` (opaque, 30 days, stored hashed), `user` and `is_new_user`.
4. `POST /v1/auth/refresh {"refresh_token"}` rotates the pair. Presenting a token that was already rotated revokes every token of that device.
5. `POST /v1/auth/logout` with `Authorization: Bearer <access_token>` revokes every refresh token of the device in the token.

Blocked users (`users.is_blocked`) get `403 USER_BLOCKED` from `verify-otp` and `refresh`; `request-otp` behaves exactly as for anyone else.

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
| 403 | `FORBIDDEN` | Wrong admin token |
| 404 | `SMS_NOT_FOUND` | Report for an id this gateway does not hold |
| 409 | `GATEWAY_EXISTS` | Duplicate gateway name |
| 413 | `PAYLOAD_TOO_LARGE` | Body over 64 KiB |
| 422 | `INVALID_PHONE` / `VALIDATION_ERROR` | Input validation |
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
- deletes refresh tokens that expired, or were revoked more than 7 days ago.

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
```

A panic returns `500 INTERNAL`. Unknown paths return `404 NOT_FOUND`. Every response includes `X-Request-ID`. Stop the server with Ctrl-C: it drains open requests, stops the maintenance loop, then closes the database pool.

## Assumptions

Phase A:

- Ashgabat is a city with no parent welayat. It is administered separately from Ahal, Balkan, Dashoguz, Lebap, and Mary.
- The location seed contains those five welayats and these cities: Ashgabat, Anau (Änew), Tejen, Balkanabat, Turkmenbashi, Dashoguz, Turkmenabat, Mary, and Bayramaly. Welayats and those cities are `is_active = true`.
- Etrap rows are left for `migrations/000003_seed_etraps.up.sql`, which is empty until an official list is added.
- `reports.status` is text and defaults to `open`. The spec does not define a status enum.
- Phone numbers stored in the database must match `+993` followed by 8 digits.
- A user cannot block or review themselves. Those checks are database constraints.
- Startup fails when the database ping fails, so a misconfigured `DATABASE_URL` is visible immediately.
- `make lint` runs `go vet`. Install `golangci-lint` separately if you want a stricter lint later.

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
- The admin API in this phase contains only `POST /admin/v1/gateways`, which is needed to bootstrap a gateway. Listing gateways, SMS stats and user blocking arrive with the rest of the admin API in Phase E.
- `golang.org/x/*` and `validator` are pinned to versions that still build with Go 1.23 so the Dockerfile base image keeps working.
