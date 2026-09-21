#!/usr/bin/env bash
# Checks that migrations are reversible (up -> down -> up gives the same
# schema) and that the newest migration keeps existing rows.
# Runs in a throwaway database created and dropped by this script.
#
# Usage: scripts/migrate-verify.sh
#
# MIGRATE_VERIFY_ADMIN_URL: connection used to create/drop the database
# (default: the service DSN with the database set to "postgres").

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

MIGRATE_VERSION="${MIGRATE_VERSION:-v4.19.1}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations}"
# Unique per run: PID alone can repeat across reboots or containers.
VERIFY_DB="${VERIFY_DB:-to_do_migrate_verify_$$_$(date +%s)}"

# Interpolated into SQL, so only plain identifiers are allowed.
if ! [[ "$VERIFY_DB" =~ ^[A-Za-z_][A-Za-z0-9_]{0,62}$ ]]; then
	echo "VERIFY_DB=${VERIFY_DB} is not a plain SQL identifier" >&2
	exit 2
fi

# MIGRATE_BIN: prebuilt CLI; otherwise run the pinned version with the
# postgres build tag (required to register the driver).
migrate() {
	if [[ -n "${MIGRATE_BIN:-}" ]]; then
		"$MIGRATE_BIN" "$@"
	else
		go run -tags postgres "github.com/golang-migrate/migrate/v4/cmd/migrate@${MIGRATE_VERSION}" "$@"
	fi
}

# Use the service's own DSN builder so passwords are escaped identically.
BASE_URL="$(go run ./cmd/dsn)"

ADMIN_URL="${MIGRATE_VERIFY_ADMIN_URL:-$(python3 - "$BASE_URL" <<'PY'
import sys
from urllib.parse import urlparse, urlunparse
u = urlparse(sys.argv[1])
print(urlunparse(u._replace(path="/postgres")))
PY
)}"

# Derived from ADMIN_URL so the database is migrated on the server where it
# was created.
VERIFY_URL="$(python3 - "$ADMIN_URL" "$VERIFY_DB" <<'PY'
import sys
from urllib.parse import urlparse, urlunparse
u = urlparse(sys.argv[1])
print(urlunparse(u._replace(path="/" + sys.argv[2])))
PY
)"

psql_admin() { psql "$ADMIN_URL" -v ON_ERROR_STOP=1 -tA "$@"; }
psql_verify() { psql "$VERIFY_URL" -v ON_ERROR_STOP=1 -tA "$@"; }

# The trap drops the database only if this run created it (CREATED=1), so an
# existing database named VERIFY_DB is never dropped.
CREATED=0
cleanup() {
	[[ "$CREATED" == "1" ]] || return 0
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

# Compares columns only; indexes and constraints are not checked.
schema_fingerprint() {
	psql_verify -c "
	  SELECT table_name || '.' || column_name || ':' || data_type || ':' || is_nullable
	  FROM information_schema.columns
	  WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
	  ORDER BY table_name, column_name;"
}

step "Creating the throwaway database ${VERIFY_DB}"
if [[ "$(psql_admin -c "SELECT count(*) FROM pg_database WHERE datname = '${VERIFY_DB}';")" != "0" ]]; then
	echo "  FAIL - database ${VERIFY_DB} already exists; this script drops the database it creates, and will not touch one it did not" >&2
	exit 2
fi
psql_admin -c "CREATE DATABASE \"${VERIFY_DB}\";" >/dev/null
CREATED=1
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
# Go back one migration, insert rows as an older release would, then migrate up.
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
