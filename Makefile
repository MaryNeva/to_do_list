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

# Every Compose call goes through envexec, so .env is parsed like the service
# parses it (bcrypt hashes keep their $) and never by Compose itself.
COMPOSE := $(ENVEXEC) docker compose --env-file /dev/null

# Upper bound, in seconds, for the stack to report healthy after it starts.
COMPOSE_WAIT_TIMEOUT ?= 120

# Image metadata. BUILD_DATE above is the wall clock, so it would change the
# build arguments on every run and relink the binary; images are stamped
# with the commit time instead, which only moves when HEAD does. Override
# IMAGE_BUILD_DATE to stamp the real build time.
IMAGE_BUILD_DATE ?= $(shell TZ=UTC git log -1 --date=format-local:%Y-%m-%dT%H:%M:%SZ --format=%cd 2>/dev/null || echo unknown)
IMAGE_METADATA = VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(IMAGE_BUILD_DATE)
MIGRATIONS_DIR := migrations
MIGRATE_VERSION := v4.19.1
STATICCHECK_VERSION := v0.6.1
GOVULNCHECK_VERSION := v1.1.4

# Without -tags postgres the migrate CLI fails with "unknown driver postgres".
MIGRATE := go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

# Empty means: build the DSN with ./cmd/dsn, which escapes the password the
# same way the service does. Set it to target another database.
DATABASE_URL ?=
export DATABASE_URL

.PHONY: run build verify test test-integration test-contract cover lint fmt fmt-check vet tidy \
        staticcheck vulncheck cover-total ci \
        migrate-up migrate-down migrate-create migrate-verify \
        docker-up docker-start docker-stop docker-down docker-logs docker-diagnose \
        observability-build observability-up observability-down compose-check \
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

# Reports unformatted files without rewriting them.
fmt-check: ## Fail if anything is not gofmt'd (does not touch any file)
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not gofmt'd:"; \
		echo "$$unformatted"; \
		echo "run 'make fmt' to fix them"; \
		exit 1; \
	fi

# Tool versions are pinned; @latest may require a newer Go toolchain.
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

# Recipes use @ so the DSN (which contains the password) is not echoed.
migrate-up: ## Apply all pending migrations
	@$(ENVEXEC) sh -c '$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$${DATABASE_URL:-$$(go run ./cmd/dsn)}" up'

migrate-down: ## Roll back the most recent migration
	@$(ENVEXEC) sh -c '$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$${DATABASE_URL:-$$(go run ./cmd/dsn)}" down 1'

migrate-create: ## Create a new migration pair: make migrate-create name=add_foo
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq $(name)

migrate-verify: ## Run up -> down -> up against MIGRATE_VERIFY_URL (a throwaway database)
	@$(ENVEXEC) ./scripts/migrate-verify.sh

# "up --wait --wait-timeout" needs a recent Compose v2; check it up front so an
# old version fails with a clear message instead of "unknown flag".
compose-check:
	@docker compose up --help 2>/dev/null | grep -q -- '--wait-timeout' || { \
		echo "this needs a docker compose whose 'up' supports --wait and --wait-timeout; found: $$(docker compose version 2>&1 | head -1)" >&2; \
		exit 1; \
	}

# Shell snippets shared by the start targets. $(1) is the extra -f flags.
# The exit code of a failed "up" is kept; diagnostics never print the
# resolved Compose config, which contains the secrets.
define compose_up_or_diagnose
@$(COMPOSE) $(1) up -d $(2) --wait --wait-timeout $(COMPOSE_WAIT_TIMEOUT) || { \
		rc=$$?; \
		$(MAKE) --no-print-directory docker-diagnose COMPOSE_FILES='$(1)' >&2; \
		exit $$rc; \
	}
endef

# Tag of the image docker-up builds; must match app.image in docker-compose.yml.
APP_IMAGE := to-do-list-app:local

# Fails with a hint instead of letting "up --no-build" try to pull a local image.
define require_app_image
@docker image inspect $(APP_IMAGE) >/dev/null 2>&1 || { \
		echo "image $(APP_IMAGE) has not been built yet; run '$(1)' first" >&2; \
		exit 1; \
	}
endef

docker-up: compose-check ## Build the current code and start app + Postgres, waiting until both are healthy
	@$(IMAGE_METADATA) $(COMPOSE) build app
	$(call compose_up_or_diagnose,,--no-build)

docker-start: compose-check ## Start the already-built image without building, waiting until healthy
	$(call require_app_image,make docker-up)
	$(call compose_up_or_diagnose,,--no-build)

docker-stop: ## Stop the containers, keeping them and the database volume
	$(COMPOSE) stop

docker-down: ## Stop and remove the docker compose stack (the database volume is kept)
	$(COMPOSE) down

docker-logs: ## Tail the app container's logs
	$(COMPOSE) logs -f app

COMPOSE_FILES ?=
docker-diagnose: ## Show container state, health-check output and recent logs of an unready stack
	@echo "== containers ==" ; \
	$(COMPOSE) $(COMPOSE_FILES) ps -a || true; \
	echo; echo "== health checks (Docker keeps the last few results) =="; \
	for c in todo_db todo_app; do \
		docker inspect --format '{{.Name}}: {{.State.Status}}{{if .State.Health}}, health={{.State.Health.Status}}, failing streak={{.State.Health.FailingStreak}}{{range .State.Health.Log}}{{println}}    exit {{.ExitCode}}: {{.Output}}{{end}}{{end}}' "$$c" 2>/dev/null | grep -v '^[[:space:]]*$$' || echo "/$$c: no such container"; \
	done; \
	echo; echo "== last 50 log lines: db, app =="; \
	$(COMPOSE) $(COMPOSE_FILES) logs --no-color --tail=50 db app || true

# A separate file, not a profile: Compose interpolates the whole file, so a
# profile would make GRAFANA_PASSWORD required for the API stack too.
observability-build: compose-check ## Build the app image for the monitoring stack (same image as docker-up)
	@$(IMAGE_METADATA) $(COMPOSE) $(COMPOSE_OBSERVABILITY) build app

observability-up: compose-check ## Start the stack with Prometheus and Grafana without building (needs GRAFANA_PASSWORD)
	$(call require_app_image,make observability-build)
	$(call compose_up_or_diagnose,$(COMPOSE_OBSERVABILITY),--no-build)
	@$(ENVEXEC) sh -c 'echo "Grafana:    http://localhost:$${GRAFANA_PORT:-3000}  (dashboard: to-do-list service)"'
	@$(ENVEXEC) sh -c 'echo "Prometheus: http://localhost:$${PROMETHEUS_PORT:-9090}"'

observability-down: ## Stop the stack including Prometheus and Grafana
	$(COMPOSE) $(COMPOSE_OBSERVABILITY) down

# With METRICS_ADDRESS set, the metrics port is not published, so fetch from
# inside the container; otherwise, or if that fails, use the local API port.
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

# The password is read with echo off and piped to hashpw, so it never appears
# in the command line, ps output or shell history.
gen-admin-hash: ## Hash a password for ADMIN_PASSWORD_HASH (prompts, does not echo)
	@printf 'Password to hash: ' >&2; \
	stty -echo 2>/dev/null || true; \
	IFS= read -r secret; \
	stty echo 2>/dev/null || true; \
	printf '\n' >&2; \
	printf '%s\n' "$$secret" | $(ENVEXEC) go run ./cmd/hashpw

# Same metadata as docker-up, so the two do not keep relinking each other's image.
smoke-test: ## Start the full stack (db + migrations + app) and verify register/login/task CRUD against real Postgres
	@$(IMAGE_METADATA) ./scripts/smoke-test.sh