-include .env
export

BINARY := bin/server
MIGRATIONS_DIR := migrations
MIGRATE_VERSION := v4.19.1
DATABASE_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable

.PHONY: run build test test-integration cover lint fmt vet tidy \
        migrate-up migrate-down migrate-create \
        docker-up docker-down docker-logs gen-admin-hash

run: ## Run the API locally (reads .env)
	go run ./cmd/server

build: ## Build the server binary into bin/server
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/server

test: ## Run unit tests (no database required)
	go test -race -cover ./...

test-integration: ## Run Postgres integration tests (needs TEST_DATABASE_URL)
	go test -race -tags=integration ./internal/repository/postgres/...

cover: ## Generate and open an HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

vet: ## Run go vet
	go vet ./...

fmt: ## Format the codebase
	gofmt -w .

lint: fmt vet ## Format + vet (a stand-in for golangci-lint if it isn't installed)

tidy: ## Tidy go.mod/go.sum (needs network access)
	go mod tidy

migrate-up: ## Apply all pending migrations
	go run github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION) -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

migrate-down: ## Roll back the most recent migration
	go run github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION) -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

migrate-create: ## Create a new migration pair: make migrate-create name=add_foo
	go run github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION) create -ext sql -dir $(MIGRATIONS_DIR) -seq $(name)

docker-up: ## Build and start the app + Postgres via docker compose
	docker compose -f deployments/docker-compose.yml --env-file .env up --build -d

docker-down: ## Stop and remove the docker compose stack
	docker compose -f deployments/docker-compose.yml --env-file .env down

docker-logs: ## Tail the app container's logs
	docker compose -f deployments/docker-compose.yml logs -f app

gen-admin-hash: ## Hash a password for ADMIN_PASSWORD_HASH: make gen-admin-hash pass='...'
	@go run ./cmd/hashpw $(pass)
