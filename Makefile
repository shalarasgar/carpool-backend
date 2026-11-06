.PHONY: run build test test-integration migrate-up migrate-down sqlc lint

SQLC ?= sqlc
MIGRATE ?= migrate

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./...

# Runs the PostgreSQL integration tests against a disposable database (default: carpool_test).
test-integration:
	CARPOOL_TEST_DATABASE_URL=$${CARPOOL_TEST_DATABASE_URL:-postgres://carpool:carpool@localhost:5432/carpool_test?sslmode=disable} go test -count=1 ./internal/store/ ./internal/http/

migrate-up:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	if [ -z "$$DATABASE_URL" ]; then echo "DATABASE_URL is required"; exit 1; fi; \
	$(MIGRATE) -path migrations -database "$$DATABASE_URL" up

migrate-down:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	if [ -z "$$DATABASE_URL" ]; then echo "DATABASE_URL is required"; exit 1; fi; \
	$(MIGRATE) -path migrations -database "$$DATABASE_URL" down 1

sqlc:
	$(SQLC) generate

lint:
	go vet ./...
