# to-do-list

A small task-management REST API written in Go: JWT authentication, tasks
scoped to their owner, and profile management. Built with
[Fiber](https://gofiber.io), [pgx](https://github.com/jackc/pgx) and
Postgres, following a straightforward layered (clean) architecture.

See [Notable design decisions](#notable-design-decisions) for a rundown of
the security and correctness choices behind the auth and ownership checks.

## Features

- JWT authentication (HS256) with algorithm-confusion protection, expiry,
  and a minimum-length signing secret enforced at startup.
- Tasks are always scoped to their creator; the ownership check lives in one
  place (the use-case layer) and is exercised by tests.
- An optional bootstrap admin login (via env vars, not a database row) that
  can list/manage every user.
- Structured JSON logging (`log/slog`) with a request ID on every log line
  and response header.
- A central HTTP error handler: every domain error maps to a specific status
  code (404/409/400/401/403), and only that error's message ever reaches the
  client - anything unexpected is logged in full server-side and returned to
  the client as a bare `500`.
- `context`-based timeouts on every database call, independent of whatever
  deadline the incoming request's context already carries.
- Database migrations (`golang-migrate`), run automatically on startup.
- Unit tests for every use case, the JWT/password packages, HTTP handlers
  and middleware (fakes, no test-only network calls); a separate
  build-tag-gated integration suite against real Postgres.
- Docker Compose stack (API + Postgres) for a one-command local deployment.

## Architecture

```
cmd/
  server/     entry point: config -> logger -> DB -> use cases -> HTTP server -> graceful shutdown
  hashpw/     CLI to bcrypt-hash a password for ADMIN_PASSWORD_HASH

internal/
  domain/         entities + repository/service interfaces (no framework imports)
  usecase/        business rules: validation, ownership checks, context timeouts
  repository/
    postgres/     pgx-backed implementations of the domain repository interfaces
  auth/
    password/     bcrypt hashing/verification
    token/        JWT issuing/verification
  transport/
    http/         response/error helpers, central error handler, health checks
      handler/    thin controllers (HTTP <-> use case)
      middleware/ JWT auth middleware, request logging
      dto/        request/response JSON shapes (never expose PasswordHash)
    httpserver/   assembles the fiber app + routes (kept separate from
                  transport/http to avoid an import cycle with handler)
  config/         configuration loading (config.yaml -> .env -> env vars) + validation
  logger/         slog setup
  apperr/         transport-agnostic sentinel errors (apperr.ErrNotFound, ...)

config.yaml       non-secret settings, safe to commit (see Configuration below)
.env.example      secrets template - copy to .env, which is git-ignored
docker-compose.yml  Postgres + the app, for local/demo deployment
migrations/       golang-migrate SQL migrations
deployments/      Dockerfile
scripts/          smoke-test.sh: end-to-end check against the running stack
docs/openapi.yaml OpenAPI 3 spec
api/*.http        example requests (IntelliJ HTTP Client / VS Code REST Client)
```

Dependency direction is inward: `domain` depends on nothing in this project;
`usecase` depends only on `domain`; `repository/postgres` and
`transport/http` both depend on `domain` and are wired together in
`cmd/server/main.go`. This is what makes it possible to unit test the use
cases and handlers with in-memory fakes instead of a real database (see the
`*_test.go` files throughout).

## Quick start (Docker)

```bash
cp .env.example .env
# edit .env: set DB_PASSWORD and JWT_SECRET at minimum
#   openssl rand -base64 48   # good way to generate JWT_SECRET
# everything else (ports, timeouts, log level, ...) already has a sensible
# default in config.yaml - see Configuration below.

make docker-up
curl http://localhost:8080/healthz
```

This builds the API image and starts it alongside Postgres; migrations run
automatically on startup. Tear it down with `make docker-down`.

To verify the whole stack actually works end to end - not just that the
containers start, but that registration, login and task CRUD really hit
Postgres - run:

```bash
make smoke-test
```

`scripts/smoke-test.sh` starts the stack (skip with `--no-up` if it's
already running), waits for `/readyz`, then drives the real HTTP API:
registers a user, logs in, rejects a wrong password and an unauthenticated
request, creates a task, checks the row exists in `tasks` via `psql` inside
the `db` container (joined to the right user by `creator_id`, not just
trusted from the API response), toggles its status, deletes it, and checks
it is gone from both the API and the database. It exits non-zero and prints
the app/db logs if anything fails. Add `--down` to stop the stack
afterwards.

## Local development (without Docker)

Requires Go 1.24+ and a Postgres instance.

```bash
cp .env.example .env
# set DB_PASSWORD and JWT_SECRET; if your Postgres is not on
# localhost:5432, either edit db.host/db.port in config.yaml or override
# DB_HOST/DB_PORT in .env

go mod tidy   # fetches dependencies - needs network access
make run      # or: go run ./cmd/server
```

`RUN_MIGRATIONS=true` (the default) applies pending migrations
automatically on startup. To manage them by hand instead:

```bash
make migrate-up
make migrate-down
make migrate-create name=add_something
```

### Trying the API

Use the `.http` files in `api/` with the IntelliJ HTTP Client or the VS Code
REST Client extension, or plain `curl`:

```bash
curl -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","email":"alice@example.com","password":"s3cret-password"}'

curl -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"s3cret-password"}'
# -> {"token": "...", "token_type": "Bearer", "expires_at": "..."}

curl localhost:8080/api/v1/tasks \
  -H 'Authorization: Bearer <token from above>'
```

Full request/response shapes are in [`docs/openapi.yaml`](docs/openapi.yaml).

## Configuration

Every setting lives in **`config.yaml`** at the project root. There are no
defaults compiled into the Go code: if a key is missing there and no
environment variable supplies it, the service refuses to start and names the
key. That keeps one file as the answer to "what does this service actually
run with".

Values are resolved from three sources, lowest priority first:

1. **`config.yaml`** - every non-secret setting. Committed to git.
2. **`.env`** - secrets and per-machine overrides. Git-ignored (copy
   `.env.example`).
3. **Real environment variables** - always win. This is how
   `docker-compose.yml` points the container at `DB_HOST=db`.

An empty environment variable counts as "not set", so
`docker-compose.yml` can pass optional overrides through as
`LOG_LEVEL: ${LOG_LEVEL:-}` without blanking what `config.yaml` says.

### Secrets

These four are **never** read from `config.yaml`, only from `.env` or the
environment, so the committed file cannot leak one:

| Variable | Notes |
|---|---|
| `DB_PASSWORD` | Required |
| `JWT_SECRET` | Required, at least `jwt.min_secret_length` characters |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD_HASH` | Optional superadmin login, see below |

### Settings in `config.yaml`

Each one can be overridden by the environment variable in the right-hand
column.

| `config.yaml` key | Value | Environment override |
|---|---|---|
| `app.name` | `to-do-list` | `APP_NAME` |
| `app.env` | `development` | `APP_ENV` |
| `server.address` | `:8080` | `SERVER_ADDRESS` |
| `server.read_timeout` / `server.write_timeout` | `10s` | `SERVER_READ_TIMEOUT` / `SERVER_WRITE_TIMEOUT` |
| `server.shutdown_timeout` | `15s` | `SERVER_SHUTDOWN_TIMEOUT` |
| `db.host` / `db.port` / `db.name` / `db.user` | `localhost` / `5432` / `to_do` / `postgres` | `DB_HOST` / `DB_PORT` / `DB_NAME` / `DB_USER` |
| `db.sslmode` | `disable` | `DB_SSLMODE` |
| `db.connect_timeout` | `5s` | `DB_CONNECT_TIMEOUT` |
| `db.call_timeout` | `5s` | `DB_CALL_TIMEOUT` |
| `jwt.ttl` | `1h` | `JWT_TTL` |
| `jwt.issuer` | `to-do-list` | `JWT_ISSUER` |
| `jwt.min_secret_length` | `32` | `JWT_MIN_SECRET_LENGTH` |
| `password.bcrypt_cost` | `12` | `PASSWORD_BCRYPT_COST` |
| `password.min_length` | `8` | `PASSWORD_MIN_LENGTH` |
| `user.min_username_length` / `user.max_username_length` | `3` / `50` | `USER_MIN_USERNAME_LENGTH` / `USER_MAX_USERNAME_LENGTH` |
| `task.max_title_length` | `200` | `TASK_MAX_TITLE_LENGTH` |
| `task.max_description_length` | `4000` | `TASK_MAX_DESCRIPTION_LENGTH` |
| `ratelimit.auth_max_requests` / `ratelimit.auth_window` | `20` / `1m` | `RATELIMIT_AUTH_MAX_REQUESTS` / `RATELIMIT_AUTH_WINDOW` |
| `cors.allow_origins` / `allow_methods` / `allow_headers` | `*` / methods / headers | `CORS_ALLOW_ORIGINS` / `CORS_ALLOW_METHODS` / `CORS_ALLOW_HEADERS` |
| `health.ready_timeout` | `2s` | `HEALTH_READY_TIMEOUT` |
| `log.level` / `log.format` | `info` / `json` | `LOG_LEVEL` / `LOG_FORMAT` |
| `run_migrations` | `true` | `RUN_MIGRATIONS` |
| `migrations_path` | `migrations` | `MIGRATIONS_PATH` |

`config.yaml` is copied into the Docker image (see `deployments/Dockerfile`),
because without it the service has no settings at all.

### What stays in the code

A few values are properties of an algorithm or protocol rather than choices a
deployment gets to make, so they are constants, not settings: bcrypt's
72-byte password limit (`password.MaxLength`), Postgres' `23505`
unique-violation code, and the `Bearer ` authorization prefix. Making those
configurable would only let a deployment configure itself into a bug.

Policy that *is* configurable but cannot live in a struct tag - username and
password lengths, task title/description limits - is enforced in the
use-case layer instead, because `validate:"max=200"` tags are compile-time
constants and cannot read `config.yaml`.

### Superadmin / bootstrap admin login

Before any real user exists, you can log in as an operator-configured
superadmin - a login that isn't a row in the `users` table at all:

```bash
make gen-admin-hash pass='a strong admin password'
# put the output in ADMIN_PASSWORD_HASH, and a username in ADMIN_USERNAME,
# both in .env (or as real environment variables, e.g. in docker-compose.yml)
```

Logging in with that username checks the password against
`ADMIN_PASSWORD_HASH` instead of the `users` table and issues a token with
`is_admin: true`. An admin token can list/read/update/delete *any* user
(`GET /api/v1/users`, etc.) but has no rows of its own in the `tasks` table.
Leave both variables empty (the default) to disable this entirely.

## Testing

```bash
make test               # unit tests: use cases, JWT, passwords, handlers, middleware, config
make test-integration   # needs a real Postgres reachable at TEST_DATABASE_URL
```

Unit tests use hand-written in-memory fakes (see `fakeTaskRepo`,
`fakeUserRepo`, `fakeTokenService`, `fakeAuthValidator`, ...) rather than a
mocking library, so `go mod tidy` doesn't need to fetch one just for tests.

Integration tests live in
`internal/repository/postgres/integration_test.go`, gated behind the
`integration` build tag so a plain `go test ./...` never needs a database:

```bash
make docker-up   # brings up Postgres (and the app)
TEST_DATABASE_URL="postgres://postgres:<password>@localhost:5432/to_do?sslmode=disable" \
  make test-integration
```

For a full black-box check against the running Docker stack - HTTP API in,
Postgres rows out - see `make smoke-test` under
[Quick start](#quick-start-docker).

## Notable design decisions

- Tasks and user profiles are scoped by ownership: a task is only visible
  to its creator, and a missing-permission read returns `404` rather than
  `403` so an endpoint never confirms another user's resource even exists.
  Profile endpoints require the caller to be the target user or an admin;
  listing all users is admin-only.
- The JWT verifier pins the signing algorithm to HMAC explicitly
  (`TestParse_RejectsAlgNone` covers this), since accepting `"alg": "none"`
  or an attacker-chosen algorithm is a well-known bypass.
  `internal/config` also refuses to start if `JWT_SECRET` is unset or
  shorter than 32 characters.
- `dto.UserResponse` has no password field, so a hash can never leak
  through a user-returning endpoint regardless of how the domain model
  changes later (`TestUserHandler_Get_NeverLeaksPasswordHash`).
  Passwords are only re-hashed when a caller actually supplies a new one.
- A single central error handler (`internal/transport/http/errors.go`)
  maps `apperr` sentinels to HTTP status codes, so handlers just return an
  error and never write ad-hoc response bodies. `recover.New()` sits first
  in the middleware chain.
- The server listens in a goroutine and shuts down via
  `app.ShutdownWithContext` on `SIGINT`/`SIGTERM`, draining in-flight
  requests and closing the database pool before exiting.
- Migrations (`golang-migrate`) run automatically on startup, toggleable
  with `RUN_MIGRATIONS`.
- Single Go module with package boundaries (`internal/domain`,
  `internal/usecase`, `internal/repository`, `internal/transport`) rather
  than separate modules, keeping `go build ./...` / `go test ./...` simple.

## Roadmap / possible next steps

Left out deliberately to keep this a reasonably-sized to-do app rather than
a distributed system, but worth knowing about:

- Refresh tokens / token revocation (current tokens are short-lived and
  stateless; there is no logout-side blacklist).
- Pagination on `GET /tasks` and `GET /users`.
- OpenTelemetry tracing in addition to the structured logs.
- golangci-lint in CI (a `Makefile` `lint` target using `gofmt`+`go vet` is
  included as a lightweight stand-in).

## License

MIT - see [LICENSE](LICENSE).
