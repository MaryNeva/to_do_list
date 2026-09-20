ENVEXEC := go run ./cmd/envexec

BINARY := bin/server
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X to-do-list/internal/buildinfo.version=$(VERSION) \
	-X to-do-list/internal/buildinfo.commit=$(COMMIT) \
	-X to-do-list/internal/buildinfo.date=$(BUILD_DATE)
COMPOSE_OBSERVABILITY := -f docker-compose.yml -f docker-compose.observability.yml
MIGRATIONS_DIR := migrations
MIGRATE_VERSION := v4.19.1
STATICCHECK_VERSION := v0.6.1
GOVULNCHECK_VERSION := v1.1.4

# The migrate CLI registers its database drivers behind build tags: without
# -tags postgres it starts and then fails with "unknown driver postgres".
MIGRATE := go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

# Where the migration commands connect. Left empty, it is resolved at run time
# by the service's own configuration code rather than pasted together here, so
# a password containing @ : or / is escaped identically in both. Set
# DATABASE_URL to point them at a different database.
DATABASE_URL ?=
export DATABASE_URL

.PHONY: run build verify test test-integration test-contract cover lint fmt fmt-check vet tidy \
        staticcheck vulncheck cover-total ci \
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
	$(ENVEXEC) go test -mod=readonly -race -tags=integration ./internal/repository/postgres/...

test-contract: ## Check real HTTP responses against docs/openapi.yaml
	go test -mod=readonly -count=1 ./internal/transport/httpserver/ -run TestOpenAPI -v

cover: ## Generate and open an HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

vet: ## Run go vet
	go vet -mod=readonly ./...

fmt: ## Format the codebase in place
	gofmt -w .

# Reports, never rewrites: a check that fixes the thing it is checking can
# always pass, and then CI is the first to see the unformatted commit.
fmt-check: ## Fail if anything is not gofmt'd (does not touch any file)
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not gofmt'd:"; \
		echo "$$unformatted"; \
		echo "run 'make fmt' to fix them"; \
		exit 1; \
	fi

# Pinned, and pinned to versions this Go toolchain can build: @latest tracks
# a module that already requires a newer Go than this project targets.
staticcheck: ## Static analysis beyond go vet (downloads the tool on first run)
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

vulncheck: ## Report known vulnerabilities this code actually reaches
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

lint: fmt-check vet staticcheck ## Check formatting, vet and static analysis without changing anything

cover-total: ## Print unit-test coverage measured by this run, nothing cached
	@go test -race -covermode=atomic -coverprofile=coverage.out ./... >/dev/null
	@go tool cover -func=coverage.out | awk '/^total:/ {print "unit-test coverage: " $$3}'

ci: lint test vulncheck test-contract ## Everything CI runs that needs no database or docker
	@echo "the checks that need neither Postgres nor docker have passed"

tidy: ## Tidy go.mod/go.sum (needs network access)
	go mod tidy

# The recipes are silenced with @ so the resolved DSN, which carries the
# database password, is never echoed into a build log.
migrate-up: ## Apply all pending migrations
	@$(ENVEXEC) sh -c '$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$${DATABASE_URL:-$$(go run ./cmd/dsn)}" up'

migrate-down: ## Roll back the most recent migration
	@$(ENVEXEC) sh -c '$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$${DATABASE_URL:-$$(go run ./cmd/dsn)}" down 1'

migrate-create: ## Create a new migration pair: make migrate-create name=add_foo
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq $(name)

migrate-verify: ## Run up -> down -> up against MIGRATE_VERIFY_URL (a throwaway database)
	@$(ENVEXEC) ./scripts/migrate-verify.sh

docker-up: ## Build and start the app + Postgres via docker compose
	$(ENVEXEC) docker compose --env-file /dev/null up --build -d

docker-down: ## Stop and remove the docker compose stack
	$(ENVEXEC) docker compose --env-file /dev/null down

docker-logs: ## Tail the app container's logs
	$(ENVEXEC) docker compose --env-file /dev/null logs -f app

# Two files rather than a profile: Compose interpolates every variable in a
# file whatever profile is selected, so Grafana's mandatory password would
# otherwise be required to start the API on its own.
observability-up: ## Start the stack together with Prometheus and Grafana (needs GRAFANA_PASSWORD)
	VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) \
		$(ENVEXEC) docker compose --env-file /dev/null $(COMPOSE_OBSERVABILITY) up --build -d
	@$(ENVEXEC) sh -c 'echo "Grafana:    http://localhost:$${GRAFANA_PORT:-3000}  (dashboard: to-do-list service)"'
	@$(ENVEXEC) sh -c 'echo "Prometheus: http://localhost:$${PROMETHEUS_PORT:-9090}"'

observability-down: ## Stop the stack including Prometheus and Grafana
	$(ENVEXEC) docker compose --env-file /dev/null $(COMPOSE_OBSERVABILITY) down

# Every part of the address is configurable, so none of it is written here:
# METRICS_ADDRESS decides whether /metrics has a listener of its own (in
# compose it does, and that port is deliberately not published, so this reads
# it from inside the container), METRICS_PATH where it is served and
# METRICS_NAMESPACE how the series are prefixed.
metrics: ## Print the metrics the running service currently exposes
	@$(ENVEXEC) sh -c ' \
		path="$${METRICS_PATH:-/metrics}"; \
		prefix="$${METRICS_NAMESPACE:-todo}_"; \
		port="$${METRICS_ADDRESS##*:}"; \
		if [ -n "$$port" ]; then \
			body="$$(docker compose exec -T app wget -qO- "http://127.0.0.1:$${port}$${path}" 2>/dev/null)"; \
		fi; \
		[ -n "$$body" ] || body="$$(curl -s "http://localhost:$${SERVER_PORT:-8080}$${path}")"; \
		printf "%s\n" "$$body" | grep -E "^$${prefix}" | grep -v "_bucket{"'

# The password is prompted for, with echo off, and reaches the program on a
# pipe. As a make variable it would be pasted into a shell command line: the
# shell would interpret quotes, $$ and backticks before the program saw it,
# and the password would sit in ps output and in shell history.
gen-admin-hash: ## Hash a password for ADMIN_PASSWORD_HASH (prompts, does not echo)
	@printf 'Password to hash: ' >&2; \
	stty -echo 2>/dev/null || true; \
	IFS= read -r secret; \
	stty echo 2>/dev/null || true; \
	printf '\n' >&2; \
	printf '%s\n' "$$secret" | $(ENVEXEC) go run ./cmd/hashpw

smoke-test: ## Start the full stack (db + migrations + app) and verify register/login/task CRUD against real Postgres
	@./scripts/smoke-test.sh
