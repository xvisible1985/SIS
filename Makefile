.PHONY: up down migrate build-ingester run-ingester test test-integration

up:
	docker compose up -d

down:
	docker compose down

migrate:
	go run ./cmd/migrate/

build-ingester:
	go build -o bin/ingester ./services/ingester/

run-ingester:
	go run ./services/ingester/

test:
	go test ./...

test-integration:
	go test ./tests/integration/... -v -tags=integration
