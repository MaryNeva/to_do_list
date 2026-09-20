#!/usr/bin/env bash
# Proves the migrations are reversible and safe to apply to a database that
# already holds data.
#
# Two things are checked, and neither is covered by the integration suite,
# which only ever migrates an empty schema forward:
#
#   1. up -> down -> up leaves the schema where it started.
#   2. applying the newest migration to a database that already has rows
#      keeps those rows and fills the new column.
#
# The work happens in a throwaway database that is created and dropped here,
# so nothing touches the development database.
#
# Usage:
#   scripts/migrate-verify.sh
#
# Connection: MIGRATE_VERIFY_ADMIN_URL points at a database the script may
# create and drop others from (default: the service's own connection with the
# database swapped for "postgres").

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

MIGRATE_VERSION="${MIGRATE_VERSION:-v4.19.1}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations}"
VERIFY_DB="${VERIFY_DB:-to_do_migrate_verify_$$}"

# MIGRATE_BIN lets a caller supply an already-built CLI (CI, or an offline
# machine); otherwise the pinned version is fetched and run on the spot. The
# postgres build tag is what registers the driver - without it the CLI starts
# and then reports "unknown driver postgres".
migrate() {
	if [[ -n "${MIGRATE_BIN:-}" ]]; then
		"$MIGRATE_BIN" "$@"
	else
		go run -tags postgres "github.com/golang-migrate/migrate/v4/cmd/migrate@${MIGRATE_VERSION}" "$@"
	fi
}

# The service resolves the DSN itself, so a password with @ : or / is escaped
# the same way here as in the running process.
BASE_URL="$(go run ./cmd/dsn)"

ADMIN_URL="${MIGRATE_VERIFY_ADMIN_URL:-$(python3 - "$BASE_URL" <<'PY'
import sys
from urllib.parse import urlparse, urlunparse
u = urlparse(sys.argv[1])
print(urlunparse(u._replace(path="/postgres")))
PY
)}"

VERIFY_URL="$(python3 - "$BASE_URL" "$VERIFY_DB" <<'PY'
import sys
from urllib.parse import urlparse, urlunparse
u = urlparse(sys.argv[1])
print(urlunparse(u._replace(path="/" + sys.argv[2])))
PY
)"

psql_admin() { psql "$ADMIN_URL" -v ON_ERROR_STOP=1 -tA "$@"; }
psql_verify() { psql "$VERIFY_URL" -v ON_ERROR_STOP=1 -tA "$@"; }

cleanup() {
	psql_admin -c "DROP DATABASE IF EXISTS \"${VERIFY_DB}\";" >/dev/null 2>&1 || true
}
trap cleanup EXIT

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

schema_fingerprint() {
	# Column layout of every table the migrations own, in a stable order.
	psql_verify -c "
	  SELECT table_name || '.' || column_name || ':' || data_type || ':' || is_nullable
	  FROM information_schema.columns
	  WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
	  ORDER BY table_name, column_name;"
}

step "Creating the throwaway database ${VERIFY_DB}"
psql_admin -c "CREATE DATABASE \"${VERIFY_DB}\";" >/dev/null
pass "created"

step "Applying every migration (up)"
migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" up
AFTER_UP="$(schema_fingerprint)"
if [[ -n "$AFTER_UP" ]]; then
	pass "schema created ($(echo "$AFTER_UP" | wc -l | tr -d ' ') columns)"
else
	fail "the schema is empty after migrating up"
fi

step "Rolling everything back (down) and applying it again"
migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" down -all
AFTER_DOWN="$(schema_fingerprint)"
if [[ -z "$AFTER_DOWN" ]]; then
	pass "down removed every table it created"
else
	fail "down left tables behind:"$'\n'"$AFTER_DOWN"
fi

migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" up
AFTER_SECOND_UP="$(schema_fingerprint)"
if [[ "$AFTER_SECOND_UP" == "$AFTER_UP" ]]; then
	pass "up -> down -> up reproduces the same schema"
else
	fail "the schema differs after the second up"
	diff <(echo "$AFTER_UP") <(echo "$AFTER_SECOND_UP") || true
fi

step "Applying the newest migration to a database that already holds data"
# Roll back to the state before the last migration, insert a row the way an
# older release would have, then migrate forward over it.
LATEST="$(find "$MIGRATIONS_DIR" -name '*.up.sql' | sed 's#.*/##' | cut -d_ -f1 | sort -n | tail -1)"
PREVIOUS=$((10#$LATEST - 1))

migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" goto "$PREVIOUS"

psql_verify -c "
  INSERT INTO users (username, email, password_hash)
  VALUES ('legacy_user', 'legacy@example.com', 'not-a-real-hash');" >/dev/null
psql_verify -c "
  INSERT INTO tasks (title, description, status, creator_id)
  SELECT 'legacy task', 'written before the migration', 'created', id
  FROM users WHERE username = 'legacy_user';" >/dev/null

migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" up

survived="$(psql_verify -c "SELECT count(*) FROM users WHERE username = 'legacy_user';")"
task_survived="$(psql_verify -c "SELECT count(*) FROM tasks WHERE title = 'legacy task';")"
if [[ "$survived" == "1" && "$task_survived" == "1" ]]; then
	pass "existing rows survived the upgrade"
else
	fail "rows were lost: users=${survived}, tasks=${task_survived}"
fi

version="$(psql_verify -c "SELECT credentials_version FROM users WHERE username = 'legacy_user';")"
if [[ "$version" == "1" ]]; then
	pass "the new column was backfilled on the existing row (credentials_version=${version})"
else
	fail "credentials_version on the pre-existing row is '${version}', want 1"
fi

dirty="$(psql_verify -c "SELECT dirty FROM schema_migrations;")"
if [[ "$dirty" == "f" ]]; then
	pass "schema_migrations is not dirty"
else
	fail "schema_migrations reports dirty=${dirty}"
fi

echo
echo "========================================"
echo "  passed: ${PASS}   failed: ${FAIL}"
echo "========================================"

[[ "$FAIL" -eq 0 ]]
