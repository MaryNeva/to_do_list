# to-do-list

[![CI](https://github.com/MaryNeva/to_do_list/actions/workflows/ci.yml/badge.svg)](https://github.com/MaryNeva/to_do_list/actions/workflows/ci.yml)

A small task-management REST API written in Go: JWT authentication, tasks
scoped to their owner, and profile management. Built with
[Fiber](https://gofiber.io), [pgx](https://github.com/jackc/pgx) and
Postgres, following a straightforward layered (clean) architecture.

See [Notable design decisions](#notable-design-decisions) for a rundown of
the security and correctness choices behind the auth and ownership checks.

> This repository is published for reading, not for reuse: it is
> source-available under an all-rights-reserved [licence](LICENSE), not open
> source.

## Features

- JWT authentication (HS256) with algorithm-confusion protection, expiry,
  and a minimum-length signing secret enforced at startup.
- Refresh tokens with rotation and revocation: `POST /auth/refresh` swaps a
  session for a new pair, `POST /auth/logout` ends it, and presenting a token
  that was already consumed revokes every session that user has. Only a
  SHA-256 hash of each token is stored, so a database dump cannot be replayed.
  The exchange is a single transaction taken under a row lock on the owning
  user, so a token can be spent at most once even if several requests present
  it at the same moment, and a failed rotation leaves the old token usable.
- Changing a password ends every refresh session of that account in the same
  transaction as the password write, and a `credentials_version` counter stops
  a login that verified the old password from storing its session afterwards -
  see "Session lifetime" below for what that does and does not invalidate.
- Expired refresh tokens are swept by a background janitor that stops with
  the server; rows are kept for a retention period after they lapse so a
  replayed token is still recognisable as reuse rather than as unknown.
- Paginated list endpoints (`limit`/`offset` with a configured default and
  ceiling) returning `{items, total, limit, offset}`; tasks can additionally
  be filtered by `status` and sorted by `created_at`, `updated_at`, `title`
  or `status` in either direction.
- Usernames are unique and matched case-insensitively (a unique index on
  `lower(username)`), so "Alice" and "alice" cannot be two accounts.
- Tasks are always scoped to their creator; the ownership check lives in one
  place (the use-case layer) and is exercised by tests.
- An optional bootstrap admin login (via env vars, not a database row) that
  can list/manage every user.
- Structured JSON logging (`log/slog`). The request ID is stamped onto every
  line written while a request is in flight, including lines written deep in
  a use case, so an access log entry and the warning it caused can be found
  by one identifier.
- Prometheus metrics on `/metrics`: RED metrics per route template, plus
  login/registration outcomes, refresh-token rotations (success, reuse,
  unknown, expired), sessions ended by reason, connection-pool state and the
  janitor's work. Label values come from closed sets, so no request can grow
  the registry.
- `GET /healthz` reports liveness and the build identity; `GET /readyz`
  probes every dependency and names the one that is unavailable. The binary's
  version and revision are also exposed as `todo_build_info`.
- An optional Prometheus + Grafana stack in a compose file of its own, with
  a provisioned dashboard. Everything the stack publishes is
  bound to `127.0.0.1`, Grafana has no default password, and `/metrics` is
  served on a listener the compose file never publishes.
- A central HTTP error handler: every domain error maps to a specific status
  code (404/409/400/401/403) and to a stable machine-readable `code`, and only
  that error's message ever reaches the client - anything unexpected is logged
  in full server-side and returned to the client as a bare `500`.
- `PATCH /tasks/{id}` and `PATCH /users/{id}` are true partial updates: an
  absent property keeps its value, a present one is written, and a task's
  status can be set outright, so a retried request does not move the task a
  second time.
- `context`-based timeouts on every database call, independent of whatever
  deadline the incoming request's context already carries.
- Explicit resource ceilings: request-body size, connection-pool size and
  connection lifetime, per-call timeouts, and a bound on how many bcrypt
  hashes may run at once. See [Limits](#limits).
- Database migrations (`golang-migrate`), run automatically on startup.
- Unit tests for every use case, the JWT/password packages, HTTP handlers
  and middleware (fakes, no test-only network calls); a separate
  build-tag-gated integration suite against real Postgres.
- Docker Compose stack (API + Postgres, optionally Prometheus + Grafana) for
  a one-command local deployment.

## Architecture

```
cmd/
  server/     entry point: config -> logger -> DB -> use cases -> HTTP server -> graceful shutdown
  hashpw/     CLI to bcrypt-hash a password for ADMIN_PASSWORD_HASH
  dsn/        prints the database URL the service would use, so tooling
              (the migration targets) connects exactly the way it does

internal/
  domain/         entities and value types (no framework imports)
  usecase/        business rules: validation, ownership checks, context timeouts
  repository/
    postgres/     pgx-backed implementations of consumer-owned repository interfaces
  auth/
    password/     bcrypt hashing/verification
    token/        JWT issuing/verification
  transport/
    http/         response/error helpers, central error handler, health checks
      handler/    thin controllers (HTTP <-> use case)
      middleware/ JWT auth, request logging, metrics, request-ID propagation
      dto/        request/response JSON shapes (never expose PasswordHash)
    httpserver/   assembles the fiber app + routes (kept separate from
                  transport/http to avoid an import cycle with handler)
  observability/  Prometheus registry, collectors, /metrics exposition
  buildinfo/      version/commit/build date, from -ldflags or the VCS stamps
  config/         configuration loading (config.yaml -> .env -> env vars) + validation
  logger/         slog setup + request-ID context plumbing
  apperr/         transport-agnostic sentinel errors (apperr.ErrNotFound, ...)

config.yaml       non-secret settings, safe to commit (see Configuration below)
.env.example      secrets template - copy to .env, which is git-ignored
docker-compose.yml  Postgres + the app
docker-compose.observability.yml  Prometheus + Grafana, merged in on request
migrations/       golang-migrate SQL migrations
.github/workflows/ci.yml  build with -mod=readonly, tests, OpenAPI lint,
                  contract test, migration check, Docker build from a clean
                  checkout
.dockerignore     keeps .env, .git and local drafts out of the build context
deployments/      Dockerfile, Prometheus scrape config, provisioned Grafana
                  datasource and dashboard
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

Requires Docker Compose and Go 1.25+ (the development command wrapper loads
`.env` with the same semantics as the server).

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

Requires Go 1.25+ and a Postgres instance.

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

### Editing a task

`PATCH /api/v1/tasks/{id}` writes only the properties the body contains:

```bash
# renames, and leaves the description exactly as it was
curl -X PATCH localhost:8080/api/v1/tasks/1 -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' -d '{"title":"New title"}'

# clears the description; an empty title would be rejected instead
curl -X PATCH localhost:8080/api/v1/tasks/1 -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' -d '{"description":""}'

# sets the status outright, so sending it twice changes nothing the second time
curl -X PATCH localhost:8080/api/v1/tasks/1 -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' -d '{"status":"completed"}'
```

An absent property keeps its value, a present one is written, and `null`
counts as absent. The update is one statement with no read first, so two
clients editing different properties of the same task both keep their change.
`POST /api/v1/tasks/{id}/toggle-status` still cycles
`created -> in_progress -> completed -> created`, but it is relative: a retry
after a lost response moves the task again. Prefer `PATCH` with an explicit
status wherever a request may be retried.

### Editing a profile

`PATCH /api/v1/users/{id}` follows the same rule, with one difference: no
profile field can be cleared, so a property that is present but empty is a
mistake to report rather than an absence to ignore.

```bash
# changes the email, leaves the username and password alone
curl -X PATCH localhost:8080/api/v1/users/1 -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' -d '{"email":"new@example.com"}'

# 400: the username was sent, and "" is not a username
curl -X PATCH localhost:8080/api/v1/users/1 -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' -d '{"username":""}'

# 400: nothing to change
curl -X PATCH localhost:8080/api/v1/users/1 -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' -d '{}'
```

`null` counts as absent here too. Surrounding whitespace is trimmed from the
username and the email; a password is stored exactly as sent, spaces
included. Changing the password revokes every refresh token that user holds.

### Error responses

Every failure - including the rate limiter's `429` and an unknown route -
answers with the same envelope:

```json
{
  "code": "validation_error",
  "message": "request body is invalid",
  "request_id": "0f1c...",
  "fields": [{ "field": "title", "message": "failed 'required' validation" }]
}
```

`code` is the contract and is safe to branch on: `validation_error`,
`bad_request`, `not_found`, `conflict`, `unauthorized`, `invalid_credentials`,
`forbidden`, `method_not_allowed`, `rate_limited`, `unavailable`,
`internal_error`. `message` is written for people and may be reworded at any
time. `fields` appears only when individual fields were rejected.
`request_id` matches the `X-Request-ID` header and the server's log line for
the same request.

Two of those are worth telling apart. `internal_error` (`500`) means
something went wrong that should not have, and the request is not worth
retrying as it stands. `unavailable` (`503`, with `Retry-After`) means the
request was fine but did not finish in time - a database call hit
`db.call_timeout`, or the server was shutting down and abandoned the work -
and retrying is the right response. Answering a timeout with `500` would
tell a client to give up on a request that will very likely succeed, and
would bury a slow dependency at the same log level as a crash; timeouts are
logged as warnings, with the detail the client is not given.

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
| `server.max_body_bytes` | `1048576` | `SERVER_MAX_BODY_BYTES` |
| `server.trusted_proxies` / `server.proxy_header` | empty / empty | `SERVER_TRUSTED_PROXIES` / `SERVER_PROXY_HEADER` |
| `db.host` / `db.port` / `db.name` / `db.user` | `localhost` / `5432` / `to_do` / `postgres` | `DB_HOST` / `DB_PORT` / `DB_NAME` / `DB_USER` |
| `db.sslmode` | `disable` | `DB_SSLMODE` |
| `db.connect_timeout` | `5s` | `DB_CONNECT_TIMEOUT` |
| `db.call_timeout` | `5s` | `DB_CALL_TIMEOUT` |
| `db.max_connections` / `db.min_connections` | `10` / `2` | `DB_MAX_CONNECTIONS` / `DB_MIN_CONNECTIONS` |
| `db.max_conn_lifetime` / `db.max_conn_idle_time` | `1h` / `30m` | `DB_MAX_CONN_LIFETIME` / `DB_MAX_CONN_IDLE_TIME` |
| `jwt.ttl` | `1h` | `JWT_TTL` |
| `jwt.refresh_ttl` | `720h` | `JWT_REFRESH_TTL` |
| `jwt.issuer` | `to-do-list` | `JWT_ISSUER` |
| `jwt.min_secret_length` | `32` | `JWT_MIN_SECRET_LENGTH` |
| `password.bcrypt_cost` | `12` | `PASSWORD_BCRYPT_COST` |
| `password.min_length` | `8` | `PASSWORD_MIN_LENGTH` |
| `password.max_concurrent_hashes` | `4` | `PASSWORD_MAX_CONCURRENT_HASHES` |
| `user.min_username_length` / `user.max_username_length` | `3` / `50` | `USER_MIN_USERNAME_LENGTH` / `USER_MAX_USERNAME_LENGTH` |
| `task.max_title_length` | `200` | `TASK_MAX_TITLE_LENGTH` |
| `task.max_description_length` | `4000` | `TASK_MAX_DESCRIPTION_LENGTH` |
| `pagination.default_page_size` | `20` | `PAGINATION_DEFAULT_PAGE_SIZE` |
| `pagination.max_page_size` | `100` | `PAGINATION_MAX_PAGE_SIZE` |
| `cleanup.interval` | `1h` | `CLEANUP_INTERVAL` |
| `cleanup.refresh_token_retention` | `720h` | `CLEANUP_REFRESH_TOKEN_RETENTION` |
| `ratelimit.auth_max_requests` / `ratelimit.auth_window` | `20` / `1m` | `RATELIMIT_AUTH_MAX_REQUESTS` / `RATELIMIT_AUTH_WINDOW` |
| `cors.allow_origins` / `allow_methods` / `allow_headers` | `*` / methods / headers | `CORS_ALLOW_ORIGINS` / `CORS_ALLOW_METHODS` / `CORS_ALLOW_HEADERS` |
| `health.ready_timeout` | `2s` | `HEALTH_READY_TIMEOUT` |
| `observability.metrics_enabled` | `true` | `METRICS_ENABLED` |
| `observability.metrics_path` | `/metrics` | `METRICS_PATH` |
| `observability.metrics_namespace` | `todo` | `METRICS_NAMESPACE` |
| `observability.metrics_address` | empty | `METRICS_ADDRESS` |
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

### Session lifetime

Two different things expire at two different speeds, and it is worth being
explicit about which is which:

| | Lives until | Ended by logout or a password change? |
|---|---|---|
| Access token (JWT) | `jwt.ttl` after it was issued (default 1h) | **No** - it is stateless and is not checked against the database on each request |
| Refresh token | `jwt.refresh_ttl` (default 30d), or its first use | Yes, immediately |

So `POST /auth/logout` and a password change both take effect on the next
refresh, not on the next API call: an access token already in a client's
hands keeps working until it expires. That is the price of not querying the
database on every request, and the reason `jwt.ttl` is kept short. A
deployment that needs revocation to bite sooner can lower `jwt.ttl`, which
shortens the window rather than closing it; nothing short of checking the
database on every request makes it immediate. What logout and a password
change do guarantee is that no *new* access token can be minted.

The access token is also verified strictly: only HS256 is accepted, the
issuer must match `jwt.issuer`, an `exp` claim is required (a token without
one is rejected rather than treated as eternal), and the identity has to make
sense - a regular account needs a positive user id, and id 0 is reserved for
the bootstrap admin.

#### A login that races a password change

"A password change ends every session" has an awkward edge: a login that has
already read and verified the *old* hash, but has not yet stored its session,
would otherwise slip its session in after the revocation had run - and the
revocation cannot retroactively touch a row that did not exist when it ran.

The fix is a counter, `users.credentials_version`, bumped whenever the
password is written. A login carries the version it verified against, and the
session is only stored while that version is still current:

```
login                              password change
-----                              ---------------
read hash + version v                        |
verify password                              |
                                   write new hash, version -> v+1
                                   revoke every existing session
store session (expects v) ---------> refused: version is v+1
```

The check runs inside the insert's transaction, under a shared lock on the
user row that a password change takes exclusively, so the two cannot overlap
and read stale values of each other. A refused login answers `401`, the same
as a wrong password: from the client's point of view the credentials it used
are no longer valid, which is exactly true.

#### If the response to a refresh is lost

Rotation is atomic, but atomicity stops at the process boundary. If the
client never receives the reply - connection reset, timeout, a proxy giving
up - the exchange still happened: the old token is consumed and a new one
exists that the client does not have. Retrying with the old token is then
indistinguishable from a replay, and is treated as one: every session of that
account is ended and the user has to log in again.

No transaction can fix this, because the problem is that the result never
arrived. Making it invisible needs either an idempotency key the client
repeats on a retry (so the server can return the same pair twice) or a short
grace window in which the immediately preceding token is still accepted from
the same client. Both trade away some of the reuse detection this service is
built around, so neither is implemented here; the deliberate choice is to
fail safe and make the client log in again.

### Superadmin / bootstrap admin login

Before any real user exists, you can log in as an operator-configured
superadmin - a login that isn't a row in the `users` table at all:

```bash
make gen-admin-hash        # prompts for the password without echoing it
# put the output in ADMIN_PASSWORD_HASH, and a username in ADMIN_USERNAME,
# both in .env (or as real environment variables, e.g. in docker-compose.yml)
```

The password is typed at a prompt and reaches the program on a pipe; it is
never a command-line argument. As one it would be visible in `ps` output and
in shell history, and the shell would interpret its quotes, `$` and backticks
first - so a password could quietly be hashed as something other than what
was typed. `cmd/hashpw` refuses an argument for the same reason.

The service checks `ADMIN_PASSWORD_HASH` at startup and refuses to start if
it is not a bcrypt hash, rather than rejecting every admin login later.

Logging in with that username checks the password against
`ADMIN_PASSWORD_HASH` instead of the `users` table and issues a token with
`is_admin: true`. An admin token can list/read/update/delete *any* user
(`GET /api/v1/users`, etc.) but has no rows of its own in the `tasks` table.
Leave both variables empty (the default) to disable this entirely.

## Testing

```bash
make verify             # build, vet and test with -mod=readonly: proves a clone can build
make test               # unit tests: use cases, JWT, passwords, handlers, middleware, config
make test-integration   # needs a real Postgres reachable at TEST_DATABASE_URL
make test-contract      # real HTTP responses checked against docs/openapi.yaml
make migrate-verify     # up -> down -> up, and an upgrade over existing rows
make smoke-test         # the whole stack through the real HTTP API
```

`make lint` reports and never rewrites: it runs `gofmt -l`, `go vet` and
`staticcheck` and fails on a finding, leaving the files alone. `make fmt` is
the one that edits. A check that silently fixes what it is checking always
passes, and then the unformatted commit is CI's problem instead.

`make verify` is the one to run before pushing. It builds with `-mod=readonly`,
so it fails if `go.mod` or `go.sum` would have to change - which is exactly
what a fresh clone cannot do for itself. The same check runs in CI, next to a
`go mod tidy` that must produce no diff.

The contract test (`internal/transport/httpserver/openapi_test.go`) starts the
assembled application, calls every endpoint and validates each response
against the schema in `docs/openapi.yaml`, including rejecting fields the
document does not describe. It is what stops the specification drifting away
from the service - the list endpoints were once documented as bare arrays
while the handlers returned a page envelope, and a generated client would
have been wrong on both.

`make migrate-verify` creates a throwaway database, migrates it up, rolls it
all the way back, migrates up again and compares the schema, then rolls back
one step, writes rows the way an older release would have, and migrates
forward over them to check nothing is lost and new columns are backfilled.

Unit tests use hand-written in-memory fakes (see `fakeTaskRepo`,
`fakeUserRepo`, `fakeTokenService`, `fakeAuthValidator`, ...) rather than a
mocking library, so `go mod tidy` doesn't need to fetch one just for tests.

Integration tests live in
`internal/repository/postgres/integration_test.go`, gated behind the
`integration` build tag so a plain `go test ./...` never needs a database:

These tests `TRUNCATE` every table between cases, so they need a database of
their own - never the one `make run` uses. Create it once:

```bash
make docker-up   # brings up Postgres (and the app)

# Through the project's .env loader: Compose interpolates the whole file for
# every command it is given, so a bare "docker compose exec" stops at
# DB_PASSWORD before it ever reaches psql.
go run ./cmd/envexec docker compose --env-file /dev/null exec -T db \
  psql -U postgres -c 'CREATE DATABASE to_do_test'

TEST_DATABASE_URL="postgres://postgres:<password>@localhost:5432/to_do_test?sslmode=disable" \
  make test-integration
```

The suite refuses to start unless the database name ends in `_test`
(`TEST_DATABASE_ALLOW_ANY_NAME=1` overrides that, deliberately awkwardly), so
a typo in the URL costs a failed test run rather than the contents of the
development database. In CI the database is a service container created as
`to_do_test` and thrown away with the job.

Two failure modes are treated as failures rather than as silence:

- With `TEST_DATABASE_URL` unset the suite skips - except when `CI` is set,
  where it fails instead. An integration job that skipped every test looks
  exactly like one that passed.
- The CI job additionally greps its own output for `--- SKIP` and fails on a
  match, which catches a service container that never came up.

For a full black-box check against the running Docker stack - HTTP API in,
Postgres rows out - see `make smoke-test` under
[Quick start](#quick-start-docker).

## Observability

Three things, in the order you reach for them when something is wrong: a
dashboard says *something* is wrong, metrics say *what*, logs say *which
request*.

### Metrics

`GET /metrics` serves the Prometheus text exposition format. Turn it off with
`observability.metrics_enabled: false`; rename the prefix with
`observability.metrics_namespace`.

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `todo_http_requests_total` | counter | `method`, `route`, `status` | Throughput and error rate per endpoint |
| `todo_http_request_duration_seconds` | histogram | `method`, `route` | Latency quantiles per endpoint |
| `todo_http_requests_in_flight` | gauge | - | Whether requests are queueing |
| `todo_auth_attempts_total` | counter | `operation`, `outcome` | Failed-login rate, registration conflicts |
| `todo_auth_refresh_rotations_total` | counter | `outcome` | Token exchanges, and replays of consumed tokens |
| `todo_auth_sessions_revoked_total` | counter | `reason` | Logouts, password changes, reuse-triggered revocations. Counts sessions actually ended, so one replay that kills three sessions moves it by three, and a revocation that failed moves it not at all |
| `todo_cleanup_runs_total` | counter | `outcome` | Whether the janitor is running and succeeding |
| `todo_cleanup_refresh_tokens_removed_total` | counter | - | How much it deletes |
| `todo_cleanup_duration_seconds` | histogram | - | How long a sweep takes |
| `todo_db_pool_*` | gauges + counters | - | Pool saturation and waiting |
| `todo_build_info` | gauge (always 1) | `version`, `commit`, `built_at`, `go_version` | Which revision produced a sample |

Plus the standard Go runtime and process collectors.

Two label decisions are worth stating, because getting them wrong is how a
registry ends up with a million series and an out-of-memory kill:

- `route` is the **route template** (`/api/v1/tasks/:id`), never the concrete
  path. A request that matched no route is labelled `unmatched`, so a scanner
  walking random URLs adds nothing.
- `method` is mapped onto a fixed set of constants, and anything else becomes
  `other`. This also fixes a real aliasing bug: Fiber returns a method string
  that points into a buffer fasthttp reuses, and the registry keeps label
  values for the life of the process, so a `GET` recorded now would later read
  back as `GETE`. `TestMetrics_MethodLabelIsACopyFromAClosedSet` pins it.

`outcome` separates a caller's mistake from the service's: a wrong password or
a taken username is `rejected` and normal, while `failure` means the service
itself broke. Alert on `failure`, not on `rejected`.

`todo_auth_refresh_rotations_total{outcome="reuse"}` is the one counter worth
paging on. Any increment means a refresh token was presented after it had
already been consumed - a replay, or a client bug - and every session of that
account was ended in response.

The endpoint is unauthenticated, like most Prometheus endpoints. Keep it on an
internal network, or drop `/metrics` at the ingress and scrape the pod
directly.

### Health

| Endpoint | Purpose | Body |
|---|---|---|
| `GET /healthz` | Liveness | `{"status":"ok","build":{"version","commit","built_at","go_version"}}` |
| `GET /readyz` | Readiness | `{"status":"ready","checks":{"postgres":{"status":"ok","duration_ms":0.4}}}` |

`/healthz` deliberately touches nothing: restarting the process because
Postgres blinked turns a short outage into a longer one. `/readyz` probes each
dependency under `health.ready_timeout` and reports them separately, so an
unready instance says *which* dependency is at fault rather than only that
something is. It answers `503` if any check fails.

What it does **not** publish is *why*. A connection error names the host, the
port, the database and the user the service was trying to reach, and neither
endpoint asks for credentials:

```
# the client sees
{"status":"unavailable","checks":{"postgres":{"status":"unavailable","duration_ms":0.3}}}

# the log line, findable by the same request_id
{"level":"ERROR","msg":"readiness check failed","check":"postgres",
 "error":"failed to connect to `user=todo_user database=to_do_prod`: db.internal:5432: connection refused",
 "request_id":"..."}
```

Whoever is paged can read the log; a stranger polling `/readyz` learns only
that a dependency called `postgres` is unhappy.

The version and revision come from `-ldflags` (`make build` and the Dockerfile
both set them) and fall back to the VCS stamps Go embeds, so an un-stamped
build still reports its commit.

### Log correlation

`requestid` assigns an identifier, one middleware copies it into the request
context, and the logger's handler stamps it onto every record whose context
carries it. Nothing below the transport layer knows about request IDs, yet
their log lines carry them:

```json
{"level":"WARN","msg":"http_request","method":"POST","path":"/api/v1/auth/refresh","status":401,"request_id":"d60d752a-..."}
{"level":"WARN","msg":"refresh token reused after it was consumed","user_id":41,"request_id":"d60d752a-..."}
```

The same identifier is returned in the `X-Request-Id` header and in the
`request_id` field of every error response, so a user can quote it from a
failed request and it can be found in the logs.

Two subtleties worth knowing if you extend this. Fiber runs the error handler
*after* every middleware has returned, so at the moment the access-log and
metrics middleware run, the response still says `200`. Both derive the real
code from the error through one shared function (`httpapi.StatusFor`), which
is also what the error handler uses - otherwise every 404 would be logged and
counted as a success.

And the middleware order is deliberate: `recover` is mounted *inside* the
metrics and logging middleware, not outside them. A panic unwinds the stack of
everything it passes through, so anything mounted outside `recover` never runs
its post-`c.Next()` code - the request would vanish from both the metrics and
the access log while the client still got a `500`. With `recover` inside, the
panic reaches them as an ordinary error and is counted and logged like any
other 500 (`TestApp_PanicIsAccountedAsA500` pins this on the fully assembled
app).

### Prometheus and Grafana

Both live in a second compose file, `docker-compose.observability.yml`, which
is overlaid on the base one. A profile would not do: Compose interpolates
every variable in a file whatever profile is selected, so Grafana's mandatory
`GRAFANA_PASSWORD` would become a requirement for starting the API on its
own. With two files, `make docker-up` starts Postgres and the API and never
reads it.

```bash
# GRAFANA_PASSWORD must be set in .env first - there is no default.
make observability-up     # db + app + Prometheus + Grafana
# Grafana:    http://127.0.0.1:3000   (log in as admin with GRAFANA_PASSWORD)
# Prometheus: http://127.0.0.1:9090
make observability-down
```

Three things about this stack are deliberate, because the usual local-demo
shortcuts are also the usual ways a laptop ends up serving its database to a
café:

- **Nothing is published beyond this machine.** Every port in
  `docker-compose.yml` is bound through `${BIND_HOST:-127.0.0.1}`. Setting
  `BIND_HOST=0.0.0.0` in `.env` opens the stack to the network, which is for
  a demo on a network you trust and nothing more.
- **Grafana has no default password and no anonymous access.** The stack
  refuses to start without `GRAFANA_PASSWORD`, and opening the dashboards
  without a login is opt-in with `GRAFANA_ANONYMOUS=true`.
- **`/metrics` is not on the API port.** `METRICS_ADDRESS` moves it to a
  listener of its own (`:9101` in compose), which the compose file does not
  publish; Prometheus reaches it over the internal network. The endpoint has
  no authentication, which is precisely why it is not where the internet
  would find it. Leaving `observability.metrics_address` empty puts it back
  on the API port, which is a convenience for `make run`.

None of this makes the stack fit for a public deployment. That needs TLS in
front of the service, real credentials for Postgres and Grafana, neither of
them on a published port at all, and `server.trusted_proxies` set so the rate
limiter sees the real client address.

`deployments/compose_test.go` checks these properties on every `go test ./...`,
since they are exactly what gets loosened during an evening of debugging and
then committed.

The datasource and the dashboard are provisioned from
`deployments/grafana/provisioning/`, so the **to-do-list service** dashboard is
there on first load, with RPS and latency per route, the status-code mix, auth
and refresh outcomes, pool saturation and the janitor's activity.

To look at the raw numbers instead:

```bash
make metrics              # the todo_* series the running service exposes
```

## Continuous integration

Every push and every pull request runs
[`.github/workflows/ci.yml`](.github/workflows/ci.yml). Six jobs run in
parallel and a seventh waits on all of them, so one green tick means all of
this held:

| Job | What it proves |
|---|---|
| Formatting, vet and static analysis | `gofmt -l`, `go vet` and `staticcheck` all report nothing; the build works with the checked-in `go.mod`/`go.sum` and `go mod tidy` produces no diff |
| Unit tests | `go test -race ./...` across every package, with coverage measured by that same run |
| Postgres integration tests | The SQL, the row locks and the migrations against a real Postgres 17, plus `up -> down -> up` and an upgrade over existing rows |
| OpenAPI contract | `docs/openapi.yaml` is valid OpenAPI 3, and the assembled service answers the way it says |
| Known vulnerabilities | `govulncheck` finds no advisory whose vulnerable code this binary actually reaches |
| Docker build and smoke test | The image builds from a clean checkout, and the whole stack answers the real HTTP API with rows landing in Postgres |

Some deliberate choices in there:

- **The pipeline runs on every branch**, not only the default one. A check
  that first runs after a merge cannot stop anything from being merged.
- **Nothing in CI rewrites the code.** The formatting job reports and fails;
  `make fmt` is what edits.
- **A skipped integration suite is a failure.** The tests skip when
  `TEST_DATABASE_URL` is unset, which is right on a laptop and wrong in CI,
  so they fail instead when `CI` is set - and the job greps its own output
  for `--- SKIP` as a second line of defence. A green run that touched no
  database is the failure mode worth engineering against, because it reads
  as success everywhere it is summarised.
- **Coverage is only ever quoted from the run that produced it.** The
  percentage is computed from that job's profile and written to the run
  summary; the profile is kept as an artifact. There is no coverage number
  hard-coded in this README, because a number in a README is a number nobody
  re-measures.

Locally, `make ci` runs everything that needs neither Postgres nor docker.

## Limits

Every ceiling the service runs under is a setting in `config.yaml`, not a
number buried in the code, and each has a test that fails if it stops being
enforced.

| What it bounds | Setting | Default | What happens at the edge |
|---|---|---|---|
| Request body | `server.max_body_bytes` | 1 MiB | `413` with `code: payload_too_large`, refused before any handler allocates the body |
| Concurrent database work | `db.max_connections` | 10 | Further queries wait for a free connection instead of piling onto Postgres |
| Idle capacity | `db.min_connections` | 2 | A quiet period is not followed by a burst of handshakes |
| Connection age | `db.max_conn_lifetime` / `db.max_conn_idle_time` | 1h / 30m | Connections are recycled even when healthy, so the pool cannot sit on the far side of a restarted database |
| Any single query | `db.call_timeout` | 5s | The use case's context expires and the request fails rather than hanging |
| Opening a connection | `db.connect_timeout` | 5s | Dialling has its own budget, so a connection being opened cannot outlive the caller |
| Simultaneous bcrypt work | `password.max_concurrent_hashes` | 4 | Logins and registrations queue; a caller that waits out its timeout is refused, and that refusal is **not** reported as a wrong password |
| Auth request rate | `ratelimit.auth_max_requests` / `ratelimit.auth_window` | 20 / 1m | `429` with `code: rate_limited`, per client address |

Two of these are worth a paragraph.

**bcrypt is the expensive one.** At cost 12 a single hash costs a core about
a quarter of a second - that is the entire point of bcrypt - so without a
ceiling, enough simultaneous logins take all the CPU and every other request
starves. `password.max_concurrent_hashes` bounds how many hashes and
verifications run at once; set it to roughly the number of cores the process
has. Callers beyond that wait, and a caller that waits longer than
`db.call_timeout` is turned away with an error that is deliberately
distinguishable from `invalid_credentials`: an overloaded process must not
tell an honest user their password is wrong
(`TestAuthUseCase_Login_AFullHashingQueueIsNotABadPassword`).

**The rate limiter counts per client address, and behind a proxy there is
only one.** Fiber takes the address from the connection, which behind a
reverse proxy is always the proxy: every caller would then share one bucket.
The fix is to read the real address from a forwarding header - but that header
is written by the client, so believing it unconditionally lets anyone mint a
fresh bucket per request. Both halves are therefore configured together:
`server.proxy_header` names the header and `server.trusted_proxies` names the
peers whose version of it is believed. With `trusted_proxies` empty (the
default) the header is ignored entirely, and the configuration loader refuses
to start if `proxy_header` is set without it.
`TestApp_ForwardedAddressIsOnlyBelievedFromATrustedProxy` pins both directions.

### What the rate limiter is not

It counts in this process's memory. Two replicas mean two independent
counters, so the effective limit is `auth_max_requests x replicas`, and a
restart forgets everything. That is honest for a single-instance deployment
and for slowing down online password guessing, which is what it is for. It is
not a defence against a distributed attack, and it is not a quota anyone
should bill against. A shared counter (Redis or the like) would fix the
arithmetic; for this project the added dependency buys less than it costs, so
the limitation is documented rather than engineered around.

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
- Shutdown runs on two contexts, and the difference is the whole of it.
  `signal.NotifyContext` is cancelled the instant `SIGTERM` arrives and only
  decides *when* to stop; requests hang off a separate context that survives
  the signal. On the signal the server stops accepting, both listeners drain
  at once out of one `server.shutdown_timeout` budget, and only when that
  budget is spent is the remaining work cancelled. Handing the signal context
  to the requests - the obvious wiring - cancels every in-flight query at the
  moment the log says it is draining them (`TestDrain_LetsAnInFlightRequestFinish`
  and `TestDrain_CancelsWorkThatOutlivesTheGracePeriod` pin both halves).
  A listener that dies on its own takes the same path: the other server is
  drained on the same budget and the pool is closed only afterwards, rather
  than being closed under requests that are still running
  (`TestAwaitStop_DrainsEvenWhenTheExitIsAFailure`).
- Migrations (`golang-migrate`) run automatically on startup, toggleable
  with `RUN_MIGRATIONS`.
- Observability points inward like everything else. `internal/usecase`
  declares the `MetricsRecorder` interface it needs and knows nothing about
  Prometheus; `internal/observability` implements it. The recorder is passed
  as a functional option (`usecase.WithMetrics`), so a use case built without
  one gets a no-op and every existing construction site - including all the
  tests - compiles unchanged.
- Single Go module with package boundaries (`internal/domain`,
  `internal/usecase`, `internal/repository`, `internal/transport`) rather
  than separate modules, keeping `go build ./...` / `go test ./...` simple.

## Roadmap / possible next steps

Left out deliberately to keep this a reasonably-sized to-do app rather than
a distributed system, but worth knowing about:

- OpenTelemetry tracing. Logs and metrics answer "what happened" and "how
  often"; a trace would answer "where did this one slow request spend its
  time", which neither can.
- Alerting rules shipped with the Prometheus config, rather than a dashboard
  someone has to be looking at.
- Exemplars linking a latency bucket to a specific trace, once tracing
  exists.
- golangci-lint alongside `staticcheck`, once its ruleset is worth the
  configuration it needs.

## License

**Source-available, not open source.** Copyright (c) 2026 MaryNeva, all
rights reserved - see [LICENSE](LICENSE).

The code is here to be read: by employers, by colleagues, by anyone curious
about how it is put together. It is not licensed for use, copying,
modification or redistribution in any project. If you want to use any part of
it, ask - [github.com/MaryNeva](https://github.com/MaryNeva).

## Application boundaries and configuration semantics

User profile operations receive authenticated `domain.Claims` explicitly.
`UserUseCase` enforces self-or-admin access and admin-only listing before
validation or repository access. Claims must come from a trusted authenticator,
never from request JSON. HTTP handlers only extract claims, parse input and
format results. Repository contracts live with their consumers in `usecase`;
HTTP service contracts live in `handler`. Authentication reads only the user
methods it needs; cleanup depends only on expired-session deletion.

Handlers and authentication middleware pass `c.UserContext()` to application
code. Parent values, deadlines and cancellation are preserved when use cases
add their own timeout. This does not promise cancellation on a disconnected
Fiber client.

YAML is decoded into a typed schema with `yaml.v3` and strict known-field
checking. Unknown/duplicate fields, invalid types and extra YAML documents
are rejected. Effective settings retain the priority **YAML < .env < non-empty
process environment**; empty environment overrides retain the lower-priority
value for compatibility with Compose. Secrets are not YAML fields.

`.env` uses literal `KEY=value` entries, one per line. Matching outer single
or double quotes are removed; `$`, backticks, backslashes, spaces inside quotes,
and `#` inside values are literal. There is no interpolation or shell execution.
Only whole-line comments are supported. Duplicate keys, invalid assignments
and unmatched opening quotes are errors. Use environment variables for multiline
values. Put comments on separate lines and do not use `export KEY=...` syntax.

Make's Docker commands and the smoke script use `go run ./cmd/envexec` to load
this same format; neither Make nor Bash sources `.env`. Compose is passed
`--env-file /dev/null` so it consumes the resolved process environment instead
of parsing `.env` again. Use the Make targets, or prefix a direct Compose call
with `go run ./cmd/envexec docker compose --env-file /dev/null ...`.
The smoke script therefore also requires Go 1.25+.
