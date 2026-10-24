# Grafana demo runner

Start the monitoring stack before running the script:

```sh
cd /Users/mariianevodova/Documents/to-do
make observability-up
python3 scripts/grafana-demo.py
```

Python 3 standard library only. Default: three cycles, 3 seconds between repeated reads, at least 4 seconds between auth calls, and 30 seconds for scrapes before/after the run. Allow several minutes. If SERVER_PORT is customized, use `--base-url http://localhost:PORT` or pass it via the environment.

For a shorter verification:

```sh
python3 scripts/grafana-demo.py --cycles 1 --report /tmp/grafana-demo-report.json
```

Optional bootstrap administrator checks (credentials are prompted, never stored):

```sh
python3 scripts/grafana-demo.py --admin
```

Coverage: readiness, registration, login, wrong password, identity, unauthenticated access, forbidden user listing, task create/list/read/update/status toggle/delete, invalid input, missing task, refresh rotation, token reuse, revocation of replacement, logout, password-change session revocation, profile password update, and self-deletion. Admin option adds administrator identity and user listing.

Each cycle creates a unique account with a random password. The script deletes only its own account and tasks. Failure cleanup is best-effort and reports the demo user ID if it cannot complete. Tokens, password hashes, passwords and response bodies are never written to the report. Existing accounts are not modified. The local API must support the repository's current contract: access token is returned as `token`, not `access_token`.

The runner exits nonzero on any failed status or data assertion. It does not retry refresh rotation: replaying a consumed token has security consequences. Auth calls are paced for the default 20/min shared limiter; competing clients or stricter configured limits can still produce a failure.

Open the timestamped dashboard URLs printed at the end: they select the exact run interval. Expected results: successful CRUD HTTP codes, 400/401/403/404 negative checks, successful refresh rotation, reuse detection, revoked sessions with logout/password_change/token_reuse reasons. Short spikes in CPU or pool occupancy may fall between scrapes. No-data is not zero. Failure/rejection metrics are intentionally exercised.

Not an exhaustive integration or load test: it does not deliberately break the database, generate 5xx, exhaust rate limits, wait for token expiry, run hourly cleanup, or test every authorization/concurrency edge case. Existing repository tests cover more invariants. These exclusions are also written to the JSON report.
