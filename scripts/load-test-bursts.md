# 1,000-user burst profile (15 minutes)

```bash
cd /Users/mariianevodova/Documents/to-do
python3 scripts/load-test-bursts.py
```

Requirements: Python 3 with aiohttp, Docker Compose, Go, and an already built `to-do-list-app:local` image. If aiohttp is missing, install it in a Python virtual environment. The runner checks the process open-file limit for 1,000 sockets.

## Isolation and timing

Uses the same separate `todo-loadtest` stack as `load-test.py`, API :8081, Prometheus :9091, Grafana :3001. The normal stack/database is untouched. A new set of 1,000 users is seeded directly into the test DB on each run and authenticated via the real API with four login workers. The configured bcrypt cost stays unchanged. The test auth limiter is increased only inside the isolated app.

Preparation, baseline scrapes, login, logout, draining requests and the final scrape wait are additional to the **900-second workload**. Data and test stack are kept after completion. Repeated runs add users rather than dropping the database. Stop with `python3 scripts/load-test.py --down`.

## Virtual users and activity waves

Every virtual user has its own async coroutine, token pair and sequential state. There is no hidden 20-client worker limit. At most one request per user is sent at a time; HTTP connection limit is the full user count. The first requests are staggered over five seconds. Stage boundaries wake sleeping users, so spikes are visible even after quiet phases. Requests already in flight are allowed up to 20 seconds to finish after the final deadline; no further workload requests start.

| Workload time | Stage | Mean pause between actions |
|---|---|---:|
| 0–2 min | baseline | 3 s |
| 2–3 min | spike-1 | 0.05 s |
| 3–5 min | recovery-1 | 5 s |
| 5–7 min | busy-period | 0.5 s |
| 7–8 min | spike-2 | 0 s |
| 8–10 min | recovery-2 | 6 s |
| 10–12 min | high-activity | 0.25 s |
| 12–13 min | spike-3 | 0.02 s |
| 13–15 min | cooldown | 8 s |

Pause is randomized between half and one-and-a-half times the mean. All 1,000 user coroutines remain present throughout the workload, including think time. **This does not promise 1,000 HTTP requests in flight continuously.** Actual concurrency is measured and reported as `max_requests_in_flight`. The scenario is closed-loop: a slow server slows arrivals, rather than building an unlimited queue. 1,000 workers or requests/s alone do not prove production capacity.

Actions: approximately 45% task listing, 15% individual task/profile reads, 10% task creation, 10% edits, 6% status toggles, 5% deletes, 7% refresh, 2% profile reads. If the user has no current task, those actions fall back to profile/refresh. Every user operates on its own data. A transport/HTTP error is recorded; an uncertain refresh exchange is followed by a new login, never a retry of the old refresh token. Logout is a separate final phase. Requests use keep-alive and bounded timeouts.

## Output and interpretation

Every ten seconds: elapsed time, stage, users started, completed requests, unexpected responses, actual requests in flight, peak in flight, measured RPS, monitor scheduling delay. Progress is also saved as `burst-progress.json`.

Final outputs in `/tmp/todo-loadtest`:

- `burst-report-TIMESTAMP.json`: global, per-stage and per-operation status counts, p50/p95/p99, actual active users, actual concurrency, transport failures, logout status counts and timestamped Grafana links.
- `burst-timeline-TIMESTAMP.csv`: ten-second progress samples.
- `private-state.json`: test Grafana credentials; `prepared-sessions.json`: test bearer tokens/password. These private files must not be shared; they are owner-only and are not included in reports.

Latency uses a bounded integer-millisecond histogram; raw request bodies/tokens are never logged. Stage is assigned at request start; requests finishing after a boundary remain in the originating stage. RPS is completed requests per sampling interval. Quantiles include failures/timeouts. Server histogram estimates and client quantiles measure different things.

First open API overview, then runtime/pool. At spikes compare RPS, p95, 5xx, CPU, pool occupancy and connection cancellations. Recovery should reduce latency and resource pressure; high RPS alone is not success. All clients share the computer with Docker: rising monitor delay may indicate the generator itself is saturated. Watch raw status counts and transport failures. Zero sampled pool occupancy does not rule out short-lived contention.

The script exits nonzero for unexpected responses, failed logout, missing active users, or an interrupted/failed run. It records overload rather than suppressing it. The profile intentionally exercises positive user workflows; the separate grafana-demo.py covers negative/security checks. Hourly cleanup and expiry are not forced during the 15-minute test.

For a short functional verification, compress the stage boundaries:

```bash
python3 scripts/load-test-bursts.py --users 1000 --duration 30
```

Short compressed phases cannot be reliably compared on 15-second Prometheus scrapes. Use the default 900 seconds for visual analysis. Full preparation still occurs. The script does not run itself on a schedule.
