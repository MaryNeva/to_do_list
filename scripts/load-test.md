# Isolated load test

Run from the repository root:

```sh
python3 scripts/load-test.py --users 1000 --concurrency 20 --duration 120
```

Requires Docker Compose with `up --wait`, Python 3 and the repository's Go toolchain. Build the local app image first with `make observability-build` if needed. The script starts a separate Compose project named `todo-loadtest`. It uses the existing app image, configuration and dashboard files; rebuild first after source changes.

Services: API http://localhost:8081, Prometheus http://localhost:9091, Grafana http://localhost:3001. No PostgreSQL host port is published. The ordinary stack and its volumes are untouched. The isolated stack uses its own database, random database/JWT secrets and named volumes. Grafana login is admin; its random password is stored in `/tmp/todo-loadtest/private-state.json` (owner-only file inside an owner-only directory). Do not share this file or `compose.json`: they contain test secrets. Reusing the directory preserves the Grafana password and database.

## Workload

1. Start stack and run migrations via normal app startup.
2. Seed 1,000 user rows and one initial task per user using SQL. This is fixture preparation, not registration performance testing. Users share a bcrypt hash of one random run-specific password; the real configured bcrypt cost is retained.
3. Authenticate **all 1,000 users** through HTTP login with four login workers. On a laptop this can take several minutes because bcrypt intentionally consumes CPU. This phase is included in Grafana's time range.
4. Run a closed-loop workload for 120 seconds with 20 workers. Each worker selects users round-robin, lists tasks, then reads identity/task, creates/updates/deletes a task, or rotates refresh. Per-user locks prevent concurrent refresh rotation of one session. Wait 250 ms per iteration by default.
5. Log out all created sessions and wait for final scrapes.
6. Save redacted per-phase and per-operation summaries, status distribution and client p50/p95/p99. Print dashboard links with the exact run timestamps.

The auth limiter is raised to 100,000/min **only in this isolated app**. The normal app's configuration is unchanged. This permits a single-machine test of 1,000 logins; it is not a test of default rate-limit behavior. No fake JWTs or bypassed login checks are used.

`--users` controls dataset/session population; `--concurrency` controls concurrent clients. Actual traffic is limited by response time and think time. Client latency includes transport and differs from server histogram estimates. This runner does not determine a production capacity limit and does not guarantee a fixed arrival rate. CPU/memory results include contention with the normal stack on the same computer.

On a successful default run, check `seeded_users=1000`, `authenticated_users=1000`, `active_users=1000`, zero unexpected statuses and `completed=true` in the report. A very short workload may not visit all users; `active_users` reports the measured count. Transport errors are status 0 and make the run fail. No retries mask failures.

The database is preserved for inspection. Each repeat adds a new set of users; it does not reset existing test data. Use a new `--project todo-loadtest-...` and a fresh `--output /tmp/...` for an independent database, plus different ports if another test stack is running. Reports refer to users from that run, not total historical population.

Stop without deleting data:

```sh
python3 scripts/load-test.py --down
```

For a later stronger run, change concurrency and duration explicitly, e.g. `--concurrency 50 --duration 180`. Observe errors, CPU, pool waits and latency before increasing further. The script does not delete volumes or change production settings. Hourly cleanup, expiry, forced outages, attacks and cross-user authorization edge cases belong to separate tests.
