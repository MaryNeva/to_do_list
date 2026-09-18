-include .env
export

BINARY := bin/server
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X to-do-list/internal/buildinfo.version=$(VERSION) \
	-X to-do-list/internal/buildinfo.commit=$(COMMIT) \
	-X to-do-list/internal/buildinfo.date=$(BUILD_DATE)
MIGRATIONS_DIR := migrations
MIGRATE_VERSION := v4.19.1

# The migrate CLI registers its database drivers behind build tags: without
# -tags postgres it starts and then fails with "unknown driver postgres".
MIGRATE := go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

# Where the migration commands connect. Left empty, it is resolved at run time
# by the service's own configuration code rather than pasted together here, so
# a password containing @ : or / is escaped identically in both. Set
# DATABASE_URL to point them at a different database.
DATABASE_URL ?=
DSN = $(if $(DATABASE_URL),$(DATABASE_URL),$$(go run ./cmd/dsn))

.PHONY: run build verify test test-integration test-contract cover lint fmt vet tidy \
        migrate-up migrate-down migrate-create migrate-verify \
        docker-up docker-down docker-logs observability-up observability-down \
        metrics gen-admin-hash smoke-test

run: ## Run the API locally (reads .env)
	go run ./cmd/server

build: ## Build the server binary into bin/server (stamped with version/commit)
	CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/server

verify: ## Prove the checked-in go.mod/go.sum are complete and consistent
	go build -mod=readonly ./...
	go vet -mod=readonly ./...
	go test -mod=readonly -count=1 ./...
	@echo "go.mod and go.sum describe the build exactly"

test: ## Run unit tests (no database required)
	go test -mod=readonly -race -cover ./...

test-integration: ## Run Postgres integration tests (needs TEST_DATABASE_URL)
	go test -mod=readonly -race -tags=integration ./internal/repository/postgres/...

test-contract: ## Check real HTTP responses against docs/openapi.yaml
	go test -mod=readonly -count=1 ./internal/transport/httpserver/ -run TestOpenAPI -v

cover: ## Generate and open an HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

vet: ## Run go vet
	go vet -mod=readonly ./...

fmt: ## Format the codebase
	gofmt -w .

lint: fmt vet ## Format + vet (a stand-in for golangci-lint if it isn't installed)

tidy: ## Tidy go.mod/go.sum (needs network access)
	go mod tidy

# The recipes are silenced with @ so the resolved DSN, which carries the
# database password, is never echoed into a build log.
migrate-up: ## Apply all pending migrations
	@$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DSN)" up

migrate-down: ## Roll back the most recent migration
	@$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DSN)" down 1

migrate-create: ## Create a new migration pair: make migrate-create name=add_foo
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq $(name)

migrate-verify: ## Run up -> down -> up against MIGRATE_VERIFY_URL (a throwaway database)
	@./scripts/migrate-verify.sh

docker-up: ## Build and start the app + Postgres via docker compose
	docker compose up --build -d

docker-down: ## Stop and remove the docker compose stack
	docker compose down

docker-logs: ## Tail the app container's logs
	docker compose logs -f app

observability-up: ## Start the stack together with Prometheus and Grafana
	VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) \
		docker compose --profile observability up --build -d
	@echo "Grafana:    http://localhost:$${GRAFANA_PORT:-3000}  (dashboard: to-do-list service)"
	@echo "Prometheus: http://localhost:$${PROMETHEUS_PORT:-9090}"

observability-down: ## Stop the stack including Prometheus and Grafana
	docker compose --profile observability down

metrics: ## Print the metrics the running service currently exposes
	@curl -s http://localhost:$${SERVER_PORT:-8080}/metrics | grep -E '^todo_' | grep -v '_bucket{'

gen-admin-hash: ## Hash a password for ADMIN_PASSWORD_HASH: make gen-admin-hash pass='...'
	@go run ./cmd/hashpw $(pass)

smoke-test: ## Start the full stack (db + migrations + app) and verify register/login/task CRUD against real Postgres
	@./scripts/smoke-test.sh
