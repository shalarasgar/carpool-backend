.PHONY: run build test migrate-up migrate-down sqlc lint

SQLC ?= sqlc
MIGRATE ?= migrate

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./...

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
