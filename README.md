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
  config/         env-based configuration + validation
  logger/         slog setup
  apperr/         transport-agnostic sentinel errors (apperr.ErrNotFound, ...)

migrations/       golang-migrate SQL migrations
deployments/      Dockerfile + docker-compose.yml
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

make docker-up
curl http://localhost:8080/healthz
```

This builds the API image and starts it alongside Postgres; migrations run
automatically on startup. Tear it down with `make docker-down`.

## Local development (without Docker)

Requires Go 1.24+ and a Postgres instance.

```bash
cp .env.example .env
# point DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME at your Postgres,
# and set JWT_SECRET

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

Everything is configured through environment variables (optionally loaded
from a local `.env` file - see `.env.example`). `Load()`
(`internal/config/config.go`) validates these at startup and refuses to
start with an invalid configuration rather than failing confusingly later.

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | Informational only |
| `SERVER_ADDRESS` | `:8080` | |
| `SERVER_READ_TIMEOUT` / `SERVER_WRITE_TIMEOUT` | `10s` | |
| `SERVER_SHUTDOWN_TIMEOUT` | `15s` | Grace period for in-flight requests on SIGTERM/SIGINT |
| `DB_HOST` / `DB_PORT` / `DB_NAME` / `DB_USER` / `DB_PASSWORD` | `localhost` / `5432` / `to_do` / `postgres` / *(required)* | |
| `DB_SSLMODE` | `disable` | Use `require` (or stricter) against a real deployment |
| `JWT_SECRET` | *(required, ≥32 chars)* | Signs and verifies access tokens |
| `JWT_TTL` | `1h` | Access token lifetime |
| `JWT_ISSUER` | `to-do-list` | `iss` claim |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD_HASH` | *(empty = disabled)* | Bootstrap admin login, see below. Generate the hash with `make gen-admin-hash pass='...'` |
| `CORS_ALLOW_ORIGINS` | `*` | Comma-separated origins |
| `RUN_MIGRATIONS` | `true` | Apply pending migrations on startup |
| `MIGRATIONS_PATH` | `migrations` | |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `LOG_FORMAT` | `json` | `json` or `text` |

### Bootstrap admin login

Before any real user exists, you can log in as an operator-configured admin:

```bash
make gen-admin-hash pass='a strong admin password'
# put the output in ADMIN_PASSWORD_HASH, and a username in ADMIN_USERNAME
```

Logging in with that username checks the password against
`ADMIN_PASSWORD_HASH` instead of the `users` table and issues a token with
`is_admin: true`. An admin token can list/read/update/delete *any* user
(`GET /api/v1/users`, etc.) but has no rows of its own in the `tasks` table.
Leave both variables empty to disable this entirely.

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
