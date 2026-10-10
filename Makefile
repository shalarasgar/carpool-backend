.PHONY: run build test test-integration migrate-up migrate-down sqlc lint openapi-check

GOLANGCI ?= golangci-lint

SQLC ?= sqlc
MIGRATE ?= migrate
# Server used by integration tests. Each test creates and drops its own database here.
TEST_DATABASE_URL ?= postgres://carpool:carpool@localhost:5432/carpool?sslmode=disable

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./...

test-integration:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./... -count=1

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
	$(GOLANGCI) run ./...

openapi-check:
	go test ./internal/http -run TestOpenAPI -count=1
