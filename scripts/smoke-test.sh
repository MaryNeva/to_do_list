#!/usr/bin/env bash
# End-to-end smoke test: brings up the docker compose stack (Postgres +
# migrations + the API), then drives the real HTTP API to check that
# registration, login and task CRUD actually persist to the database.
#
# Usage:
#   scripts/smoke-test.sh            # start the stack if needed, then verify
#   scripts/smoke-test.sh --down     # also stop the stack afterwards
#   scripts/smoke-test.sh --no-up    # skip `docker compose up`, just verify
#                                     # against whatever is already running
#
# Requires: Go 1.24+, docker compose, curl, python3 (used for JSON parsing so the
# script doesn't depend on jq being installed).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/docker-compose.yml"
ENV_FILE="$ROOT_DIR/.env"

DO_UP=1
DO_DOWN=0
for arg in "$@"; do
	case "$arg" in
	--no-up) DO_UP=0 ;;
	--down) DO_DOWN=1 ;;
	*)
		echo "unknown argument: $arg" >&2
		exit 2
		;;
	esac
done

if [[ ! -f "$ENV_FILE" ]]; then
	echo "missing $ENV_FILE - copy .env.example to .env and fill in DB_PASSWORD/JWT_SECRET first" >&2
	exit 1
fi

# Load once with the same literal parser as the application; never source secrets.
if [[ "${TODO_ENV_LOADED:-}" != "1" ]]; then
 cd "$ROOT_DIR"
 exec go run ./cmd/envexec env TODO_ENV_LOADED=1 bash "$ROOT_DIR/scripts/smoke-test.sh" "$@"
fi

SERVER_PORT="${SERVER_PORT:-8080}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-postgres}"
DB_NAME="${DB_NAME:-to_do}"
BASE_URL="http://localhost:${SERVER_PORT}"

# Every response body lands in a private directory that goes away with the
# run. Fixed paths under /tmp would leave bearer tokens readable by anyone on
# the machine and make two runs of this script overwrite each other's files.
WORK_DIR="$(mktemp -d)"

PASS=0
FAIL=0

pass() {
	PASS=$((PASS + 1))
	echo "  ok   - $1"
}

fail() {
	FAIL=$((FAIL + 1))
	echo "  FAIL - $1" >&2
}

step() {
	echo
	echo "== $1 =="
}

json_get() {
	# json_get '<json>' 'key' - tiny helper so the script doesn't need jq.
	python3 -c '
import json, sys
data = json.loads(sys.argv[1])
value = data
for part in sys.argv[2].split("."):
    value = value[part]
print(value)
' "$1" "$2"
}

compose() {
	docker compose -f "$COMPOSE_FILE" --env-file /dev/null "$@"
}

cleanup() {
	rm -rf "$WORK_DIR"
	if [[ "$DO_DOWN" -eq 1 ]]; then
		step "Stopping the stack (--down)"
		compose down
	fi
}
trap cleanup EXIT

if [[ "$DO_UP" -eq 1 ]]; then
	step "Starting Postgres + the API (docker compose up --build -d)"
	compose up --build -d
fi

step "Waiting for the app to report ready on ${BASE_URL}/readyz"
ready=0
for _ in $(seq 1 60); do
	if curl -fsS "${BASE_URL}/readyz" >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 2
done
if [[ "$ready" -eq 1 ]]; then
	pass "app is up and can reach the database (/readyz)"
else
	fail "app never became ready within 120s"
	echo "---- app logs ----"
	compose logs --tail=100 app || true
	echo "---- db logs ----"
	compose logs --tail=50 db || true
	exit 1
fi

RUN_ID="$(date +%s)-$$"
USERNAME="smoketest_${RUN_ID}"
EMAIL="smoketest_${RUN_ID}@example.com"
PASSWORD="smoke-test-password-123"
TASK_TITLE="Smoke test task ${RUN_ID}"

step "Registering a new user via POST /api/v1/auth/register"
register_body=$(curl -fsS -o "$WORK_DIR/register.json" -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/auth/register" \
	-H 'Content-Type: application/json' \
	-d "{\"username\":\"${USERNAME}\",\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\"}") || true
if [[ "$register_body" == "201" ]] && ! grep -qi password "$WORK_DIR/register.json"; then
	pass "register returned 201 and the response has no password field"
else
	fail "register returned HTTP ${register_body}: $(cat "$WORK_DIR/register.json" 2>/dev/null)"
fi

step "Logging in via POST /api/v1/auth/login"
login_status=$(curl -fsS -o "$WORK_DIR/login.json" -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/auth/login" \
	-H 'Content-Type: application/json' \
	-d "{\"username\":\"${USERNAME}\",\"password\":\"${PASSWORD}\"}") || true
if [[ "$login_status" == "200" ]]; then
	TOKEN="$(json_get "$(cat "$WORK_DIR/login.json")" token)"
	pass "login returned 200 with a bearer token"
else
	fail "login returned HTTP ${login_status}: $(cat "$WORK_DIR/login.json" 2>/dev/null)"
	echo "cannot continue without a token" >&2
	exit 1
fi

step "Rotating the session (POST /api/v1/auth/refresh)"
REFRESH_TOKEN="$(json_get "$(cat "$WORK_DIR/login.json")" refresh_token)"
refresh_status=$(curl -fsS -o "$WORK_DIR/refresh.json" -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/auth/refresh" \
	-H 'Content-Type: application/json' \
	-d "{\"refresh_token\":\"${REFRESH_TOKEN}\"}") || true
if [[ "$refresh_status" == "200" ]]; then
	NEW_REFRESH="$(json_get "$(cat "$WORK_DIR/refresh.json")" refresh_token)"
	if [[ "$NEW_REFRESH" != "$REFRESH_TOKEN" ]]; then
		pass "refresh returned a new token pair"
	else
		fail "refresh handed back the same refresh token instead of rotating it"
	fi
else
	fail "refresh returned HTTP ${refresh_status}: $(cat "$WORK_DIR/refresh.json" 2>/dev/null)"
fi

step "Refusing the rotated-out refresh token"
reused_status=$(curl -s -o /dev/null -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/auth/refresh" \
	-H 'Content-Type: application/json' \
	-d "{\"refresh_token\":\"${REFRESH_TOKEN}\"}")
if [[ "$reused_status" == "401" ]]; then
	pass "a refresh token cannot be used twice"
else
	fail "reusing a rotated refresh token returned HTTP ${reused_status}, want 401"
fi

step "Rejecting the wrong password (POST /api/v1/auth/login)"
wrong_status=$(curl -s -o /dev/null -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/auth/login" \
	-H 'Content-Type: application/json' \
	-d "{\"username\":\"${USERNAME}\",\"password\":\"wrong-password\"}")
if [[ "$wrong_status" == "401" ]]; then
	pass "wrong password correctly rejected with 401"
else
	fail "wrong password returned HTTP ${wrong_status}, want 401"
fi

step "Reading identity via GET /api/v1/auth/me"
me_status=$(curl -fsS -o "$WORK_DIR/me.json" -w '%{http_code}' "${BASE_URL}/api/v1/auth/me" \
	-H "Authorization: Bearer ${TOKEN}") || true
if [[ "$me_status" == "200" ]]; then
	USER_ID="$(json_get "$(cat "$WORK_DIR/me.json")" user_id)"
	pass "/auth/me returned user_id=${USER_ID} for username=${USERNAME}"
else
	fail "/auth/me returned HTTP ${me_status}"
fi

step "Rejecting an unauthenticated request (GET /api/v1/tasks/ with no token)"
noauth_status=$(curl -s -o /dev/null -w '%{http_code}' "${BASE_URL}/api/v1/tasks/")
if [[ "$noauth_status" == "401" ]]; then
	pass "request with no bearer token correctly rejected with 401"
else
	fail "unauthenticated request returned HTTP ${noauth_status}, want 401"
fi

step "Creating a task via POST /api/v1/tasks/"
create_status=$(curl -fsS -o "$WORK_DIR/create.json" -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/tasks/" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H 'Content-Type: application/json' \
	-d "{\"title\":\"${TASK_TITLE}\",\"description\":\"created by scripts/smoke-test.sh\"}") || true
if [[ "$create_status" == "201" ]]; then
	TASK_ID="$(json_get "$(cat "$WORK_DIR/create.json")" id)"
	CREATOR_ID="$(json_get "$(cat "$WORK_DIR/create.json")" creator_id)"
	if [[ "$CREATOR_ID" == "$USER_ID" ]]; then
		pass "task ${TASK_ID} created with creator_id=${CREATOR_ID} matching the logged-in user"
	else
		fail "task creator_id=${CREATOR_ID}, want ${USER_ID}"
	fi
else
	fail "create task returned HTTP ${create_status}: $(cat "$WORK_DIR/create.json" 2>/dev/null)"
	exit 1
fi

step "Confirming the task exists directly in Postgres (not just in the API response)"
db_row="$(compose exec -T db psql -U "$DB_USER" -d "$DB_NAME" -tA -c \
	"SELECT t.title FROM tasks t JOIN users u ON u.id = t.creator_id WHERE t.id = ${TASK_ID} AND u.username = '${USERNAME}';")"
if [[ "$(echo "$db_row" | tr -d '[:space:]')" == "$(echo "$TASK_TITLE" | tr -d '[:space:]')" ]]; then
	pass "row found in the tasks table, joined to the right user by creator_id"
else
	fail "task row not found in Postgres (got: '${db_row}')"
fi

step "Listing tasks via GET /api/v1/tasks/ and checking the new task is present"
list_status=$(curl -fsS -o "$WORK_DIR/list.json" -w '%{http_code}' "${BASE_URL}/api/v1/tasks/" \
	-H "Authorization: Bearer ${TOKEN}") || true
if [[ "$list_status" == "200" ]] && grep -q "$TASK_TITLE" "$WORK_DIR/list.json"; then
	pass "list includes the task just created"
else
	fail "list (HTTP ${list_status}) did not include the new task: $(cat "$WORK_DIR/list.json" 2>/dev/null)"
fi

step "Filtering and paginating GET /api/v1/tasks/"
filtered_status=$(curl -fsS -o "$WORK_DIR/filtered.json" -w '%{http_code}' \
	"${BASE_URL}/api/v1/tasks/?status=created&limit=1&offset=0" \
	-H "Authorization: Bearer ${TOKEN}") || true
if [[ "$filtered_status" == "200" ]]; then
	pass "filtered page returned (total: $(json_get "$(cat "$WORK_DIR/filtered.json")" total))"
else
	fail "filtered list returned HTTP ${filtered_status}"
fi

bad_status=$(curl -s -o /dev/null -w '%{http_code}' \
	"${BASE_URL}/api/v1/tasks/?status=archived" -H "Authorization: Bearer ${TOKEN}")
if [[ "$bad_status" == "400" ]]; then
	pass "an unknown status is rejected with 400"
else
	fail "an unknown status returned HTTP ${bad_status}, want 400"
fi

step "Editing the task via PATCH /api/v1/tasks/${TASK_ID}"
NEW_TITLE="${TASK_TITLE} (edited)"
update_status=$(curl -fsS -o "$WORK_DIR/update.json" -w '%{http_code}' \
	-X PATCH "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H 'Content-Type: application/json' \
	-d "{\"title\":\"${NEW_TITLE}\",\"description\":\"edited by scripts/smoke-test.sh\"}") || true
if [[ "$update_status" == "200" ]] && [[ "$(json_get "$(cat "$WORK_DIR/update.json")" title)" == "$NEW_TITLE" ]]; then
	pass "the response carries the edited title"
else
	fail "update returned HTTP ${update_status}: $(cat "$WORK_DIR/update.json" 2>/dev/null)"
fi

# The response echoing the new title is not proof it was stored: read it back
# and check the row itself.
reread_status=$(curl -fsS -o "$WORK_DIR/reread.json" -w '%{http_code}' \
	"${BASE_URL}/api/v1/tasks/${TASK_ID}" -H "Authorization: Bearer ${TOKEN}") || true
db_title="$(compose exec -T db psql -U "$DB_USER" -d "$DB_NAME" -tA -c \
	"SELECT title FROM tasks WHERE id = ${TASK_ID};")"
if [[ "$reread_status" == "200" ]] &&
	[[ "$(json_get "$(cat "$WORK_DIR/reread.json")" title)" == "$NEW_TITLE" ]] &&
	[[ "$(echo "$db_title" | tr -d '[:space:]')" == "$(echo "$NEW_TITLE" | tr -d '[:space:]')" ]]; then
	pass "the edit persisted: the API and the tasks row both show the new title"
else
	fail "the edit did not persist (API title: $(cat "$WORK_DIR/reread.json" 2>/dev/null), db title: '${db_title}')"
fi

step "A PATCH that sends only a title leaves the description alone"
# The bug this guards: a title-only edit used to blank the description,
# because the request struct always carried one.
kept_status=$(curl -fsS -o "$WORK_DIR/partial.json" -w '%{http_code}' \
	-X PATCH "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H 'Content-Type: application/json' \
	-d "{\"title\":\"${NEW_TITLE} (partial)\"}") || true
db_description="$(compose exec -T db psql -U "$DB_USER" -d "$DB_NAME" -tA -c \
	"SELECT description FROM tasks WHERE id = ${TASK_ID};")"
if [[ "$kept_status" == "200" ]] && [[ "$db_description" == "edited by scripts/smoke-test.sh" ]]; then
	pass "the description survived a title-only edit"
else
	fail "HTTP ${kept_status}, the stored description is now '${db_description}'"
fi

step "An empty description clears it, an empty title does not"
cleared_status=$(curl -fsS -o "$WORK_DIR/cleared.json" -w '%{http_code}' \
	-X PATCH "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H 'Content-Type: application/json' \
	-d '{"description":""}') || true
db_description="$(compose exec -T db psql -U "$DB_USER" -d "$DB_NAME" -tA -c \
	"SELECT description FROM tasks WHERE id = ${TASK_ID};")"
if [[ "$cleared_status" == "200" ]] && [[ -z "$db_description" ]]; then
	pass "an empty description cleared the column"
else
	fail "HTTP ${cleared_status}, the stored description is '${db_description}'"
fi

empty_title_status=$(curl -s -o "$WORK_DIR/empty-title.json" -w '%{http_code}' \
	-X PATCH "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H 'Content-Type: application/json' \
	-d '{"title":"   "}')
if [[ "$empty_title_status" == "400" ]] &&
	[[ "$(json_get "$(cat "$WORK_DIR/empty-title.json")" code)" == "validation_error" ]]; then
	pass "an empty title is rejected with code validation_error"
else
	fail "HTTP ${empty_title_status}: $(cat "$WORK_DIR/empty-title.json" 2>/dev/null)"
fi

step "Setting a status through PATCH is safe to repeat"
# Whatever a retry after a lost response would do, it must not move the task
# a second time - which is exactly what toggle-status would do.
for attempt in 1 2; do
	set_status=$(curl -fsS -o "$WORK_DIR/set-status.json" -w '%{http_code}' \
		-X PATCH "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
		-H "Authorization: Bearer ${TOKEN}" \
		-H 'Content-Type: application/json' \
		-d '{"status":"completed"}') || true
	if [[ "$set_status" != "200" ]]; then
		fail "attempt ${attempt} returned HTTP ${set_status}: $(cat "$WORK_DIR/set-status.json" 2>/dev/null)"
		break
	fi
done
db_task_status="$(compose exec -T db psql -U "$DB_USER" -d "$DB_NAME" -tA -c \
	"SELECT status FROM tasks WHERE id = ${TASK_ID};")"
if [[ "$db_task_status" == "completed" ]]; then
	pass "two identical requests left the task completed, not one step further"
else
	fail "after repeating the same request the status is '${db_task_status}', want completed"
fi

step "Toggling task status via POST /api/v1/tasks/${TASK_ID}/toggle-status"
toggle_status=$(curl -fsS -o "$WORK_DIR/toggle.json" -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/tasks/${TASK_ID}/toggle-status" \
	-H "Authorization: Bearer ${TOKEN}") || true
# The task was left completed by the step above, and the cycle wraps round.
if [[ "$toggle_status" == "200" ]] && grep -q '"status":"created"' "$WORK_DIR/toggle.json"; then
	pass "status moved completed -> created"
else
	fail "toggle-status returned HTTP ${toggle_status}: $(cat "$WORK_DIR/toggle.json" 2>/dev/null)"
fi

step "Deleting the task via DELETE /api/v1/tasks/${TASK_ID}"
delete_status=$(curl -s -o /dev/null -w '%{http_code}' \
	-X DELETE "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
	-H "Authorization: Bearer ${TOKEN}")
if [[ "$delete_status" == "204" ]]; then
	pass "delete returned 204"
else
	fail "delete returned HTTP ${delete_status}, want 204"
fi

step "Confirming the task is gone (GET /api/v1/tasks/${TASK_ID} -> 404) and removed from Postgres"
get_after_delete=$(curl -s -o /dev/null -w '%{http_code}' "${BASE_URL}/api/v1/tasks/${TASK_ID}" \
	-H "Authorization: Bearer ${TOKEN}")
db_row_after="$(compose exec -T db psql -U "$DB_USER" -d "$DB_NAME" -tA -c \
	"SELECT count(*) FROM tasks WHERE id = ${TASK_ID};")"
if [[ "$get_after_delete" == "404" ]] && [[ "$(echo "$db_row_after" | tr -d '[:space:]')" == "0" ]]; then
	pass "task no longer reachable via the API or present in Postgres"
else
	fail "task still visible after delete (API status=${get_after_delete}, db rows=${db_row_after})"
fi

step "Ending a session (POST /api/v1/auth/logout)"
# A fresh login, deliberately: the session opened at the start of this run was
# already destroyed by the reuse-detection check above, which revokes every
# session the account has. Reusing it here would let a completely broken
# logout still produce 204 followed by 401, and the check would prove nothing.
logout_login_status=$(curl -fsS -o "$WORK_DIR/logout-login.json" -w '%{http_code}' \
	-X POST "${BASE_URL}/api/v1/auth/login" \
	-H 'Content-Type: application/json' \
	-d "{\"username\":\"${USERNAME}\",\"password\":\"${PASSWORD}\"}") || true
if [[ "$logout_login_status" != "200" ]]; then
	fail "could not open a fresh session to test logout (HTTP ${logout_login_status})"
else
	LOGOUT_REFRESH="$(json_get "$(cat "$WORK_DIR/logout-login.json")" refresh_token)"

	# The session has to be usable before logout, otherwise the 401 afterwards
	# says nothing about whether logout did anything.
	before_logout=$(curl -s -o "$WORK_DIR/before-logout.json" -w '%{http_code}' \
		-X POST "${BASE_URL}/api/v1/auth/refresh" \
		-H 'Content-Type: application/json' \
		-d "{\"refresh_token\":\"${LOGOUT_REFRESH}\"}")
	if [[ "$before_logout" == "200" ]]; then
		pass "the fresh session works before logout"
		LOGOUT_REFRESH="$(json_get "$(cat "$WORK_DIR/before-logout.json")" refresh_token)"
	else
		fail "the fresh session was already unusable before logout (HTTP ${before_logout})"
	fi

	logout_status=$(curl -s -o /dev/null -w '%{http_code}' \
		-X POST "${BASE_URL}/api/v1/auth/logout" \
		-H 'Content-Type: application/json' \
		-d "{\"refresh_token\":\"${LOGOUT_REFRESH}\"}")
	after_logout=$(curl -s -o /dev/null -w '%{http_code}' \
		-X POST "${BASE_URL}/api/v1/auth/refresh" \
		-H 'Content-Type: application/json' \
		-d "{\"refresh_token\":\"${LOGOUT_REFRESH}\"}")

	if [[ "$logout_status" == "204" ]] && [[ "$after_logout" == "401" ]]; then
		pass "logout revoked a session that was working a moment earlier"
	else
		fail "logout returned HTTP ${logout_status} and a later refresh HTTP ${after_logout}, want 204 then 401"
	fi
fi

echo
echo "========================================"
echo "  passed: ${PASS}   failed: ${FAIL}"
echo "========================================"

if [[ "$FAIL" -gt 0 ]]; then
	exit 1
fi
