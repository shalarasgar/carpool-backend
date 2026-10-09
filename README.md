# Carpool backend

Phase A skeleton for the Turkmenistan intercity carpool API: configuration, PostgreSQL, migrations, structured logging, and health checks. Auth, trips, and bookings are not implemented yet.

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
| `DATABASE_URL` | PostgreSQL URL (`postgres://` or `postgresql://`). |
| `JWT_SECRET` | Signing secret. Minimum 16 characters. Unused until auth is added. |
| `OTP_HMAC_SECRET` | OTP HMAC secret. Minimum 16 characters. Unused until auth is added. |
| `ADMIN_TOKEN` | Static admin token. Minimum 16 characters. Unused until admin routes are added. |
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

`sms_gateway_healthy` is informational. It is true only when an active SMS gateway heartbeated within the last 30 seconds. No gateway is configured in Phase A, so the field stays false and the status stays `200`.

If Postgres is down, `/readyz` returns `503`:

```json
{"status":"unavailable","database":"down","sms_gateway_healthy":false}
```

A panic returns `500` with `{"error":{"code":"INTERNAL","message":"Internal server error"}}`. Unknown paths return `404` with code `NOT_FOUND`. Every response includes `X-Request-ID`.

Stop the server with Ctrl-C. It drains open requests, then closes the database pool.

## Assumptions

- Ashgabat is a city with no parent welayat. It is administered separately from Ahal, Balkan, Dashoguz, Lebap, and Mary.
- The location seed contains those five welayats and these cities: Ashgabat, Anau (Änew), Tejen, Balkanabat, Turkmenbashi, Dashoguz, Turkmenabat, Mary, and Bayramaly. Welayats and those cities are `is_active = true`.
- Etrap rows are left for `migrations/000003_seed_etraps.up.sql`, which is empty until an official list is added.
- `reports.status` is text and defaults to `open`. The spec does not define a status enum.
- Phone numbers stored in the database must match `+993` followed by 8 digits.
- A user cannot block or review themselves. Those checks are database constraints.
- Phase A error messages are English. Localized text arrives with the i18n work in a later phase. Codes (`INTERNAL`, `NOT_FOUND`, `METHOD_NOT_ALLOWED`) are stable.
- Startup fails when the database ping fails, so a misconfigured `DATABASE_URL` is visible immediately. `/healthz` is only reachable once that ping has succeeded.
- `make lint` runs `go vet`. Install `golangci-lint` separately if you want a stricter lint later.
