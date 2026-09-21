#!/usr/bin/env bash
# Checks that migrations are reversible (up -> down -> up gives the same
# columns, indexes and constraints) and that migrations 4-6 upgrade and roll
# back a database that already holds users, tasks and refresh tokens.
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

# Columns, indexes and constraints of the tables the migrations own.
schema_fingerprint() {
	psql_verify -c "
	  SELECT 'column ' || table_name || '.' || column_name || ':' || data_type || ':' || is_nullable
	    || ':' || coalesce(column_default, '')
	  FROM information_schema.columns
	  WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
	  UNION ALL
	  SELECT 'index ' || indexname || ': ' || indexdef
	  FROM pg_indexes
	  WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
	  UNION ALL
	  SELECT 'constraint ' || c.conrelid::regclass || '.' || c.conname || ': ' || pg_get_constraintdef(c.oid)
	  FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
	  WHERE n.nspname = 'public' AND c.conrelid::regclass::text <> 'schema_migrations'
	  ORDER BY 1;"
}

has_index() { [[ "$(psql_verify -c "SELECT count(*) FROM pg_indexes WHERE indexname = '$1';")" == "1" ]]; }
has_constraint() { [[ "$(psql_verify -c "SELECT count(*) FROM pg_constraint WHERE conname = '$1';")" == "1" ]]; }
has_column() {
	[[ "$(psql_verify -c "SELECT count(*) FROM information_schema.columns
	  WHERE table_schema = 'public' AND table_name = '$1' AND column_name = '$2';")" == "1" ]]
}
check() { # check "description" command...
	local what="$1"
	shift
	if "$@"; then pass "$what"; else fail "$what"; fi
}
rejects() { ! psql_verify -c "$1" >/dev/null 2>&1; }
goto() { migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" goto "$1"; }

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
	pass "schema created ($(echo "$AFTER_UP" | wc -l | tr -d ' ') columns, indexes and constraints)"
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

step "Migration 4 refuses emails that differ only in case"
goto 3
psql_verify -c "
  INSERT INTO users (username, email, password_hash) VALUES
    ('legacy_user', 'Legacy@Example.com', 'not-a-real-hash'),
    ('legacy_twin', 'legacy@example.com', 'not-a-real-hash');" >/dev/null
if goto 4 >/dev/null 2>&1; then
	fail "migration 4 accepted two emails that differ only in case"
else
	pass "migration 4 fails while case-duplicate emails exist"
fi
migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" force 3
psql_verify -c "DELETE FROM users WHERE username = 'legacy_twin';" >/dev/null

step "Upgrading 3 -> 4 over existing rows"
psql_verify -c "
  INSERT INTO tasks (title, description, status, creator_id)
  SELECT 'legacy task', 'written before the migration', 'created', id
  FROM users WHERE username = 'legacy_user';" >/dev/null
check "no case-insensitive email index at version 3" eval '! has_index users_email_lower_key'
goto 4
check "migration 4 adds users_email_lower_key" has_index users_email_lower_key
check "emails are unique regardless of case after migration 4" \
	rejects "INSERT INTO users (username, email, password_hash) VALUES ('another', 'LEGACY@example.com', 'h');"

step "Upgrading 4 -> 6 over existing refresh tokens"
psql_verify -c "
  INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at)
  SELECT id, 'legacy-active', now() + interval '1 day', NULL FROM users WHERE username = 'legacy_user'
  UNION ALL
  SELECT id, 'legacy-revoked', now() + interval '1 day', now() FROM users WHERE username = 'legacy_user';" >/dev/null
goto 5
check "migration 5 adds revoked_reason" has_column refresh_tokens revoked_reason
check "migration 5 adds refresh_tokens_reason_needs_revocation" has_constraint refresh_tokens_reason_needs_revocation
check "existing tokens keep a NULL revoked_reason (never treated as reuse)" \
	test "$(psql_verify -c "SELECT count(*) FROM refresh_tokens WHERE revoked_reason IS NULL;")" == "2"
check "a reason on an unrevoked token is rejected" \
	rejects "UPDATE refresh_tokens SET revoked_reason = 'logout' WHERE token_hash = 'legacy-active';"
check "an unknown reason is rejected" \
	rejects "UPDATE refresh_tokens SET revoked_reason = 'bogus' WHERE token_hash = 'legacy-revoked';"

migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" up
check "migration 6 adds idx_refresh_tokens_family_id" has_index idx_refresh_tokens_family_id
check "every existing token got its own family" \
	test "$(psql_verify -c "SELECT count(DISTINCT family_id) FROM refresh_tokens WHERE family_id IS NOT NULL;")" == "2"
check "family_id is required" \
	test "$(psql_verify -c "SELECT is_nullable FROM information_schema.columns
	  WHERE table_name = 'refresh_tokens' AND column_name = 'family_id';")" == "NO"
check "the active token is still active" \
	test "$(psql_verify -c "SELECT count(*) FROM refresh_tokens WHERE token_hash = 'legacy-active' AND revoked_at IS NULL;")" == "1"

survived="$(psql_verify -c "SELECT count(*) FROM users WHERE username = 'legacy_user';")"
task_survived="$(psql_verify -c "SELECT count(*) FROM tasks WHERE title = 'legacy task';")"
if [[ "$survived" == "1" && "$task_survived" == "1" ]]; then
	pass "existing users and tasks survived the upgrades"
else
	fail "rows were lost: users=${survived}, tasks=${task_survived}"
fi

version="$(psql_verify -c "SELECT credentials_version FROM users WHERE username = 'legacy_user';")"
check "the pre-existing user has credentials_version=1" test "$version" == "1"

step "Rolling 6 -> 3 back over the same rows removes what 4-6 added"
goto 3
check "users_email_lower_key is gone" eval '! has_index users_email_lower_key'
check "revoked_reason is gone" eval '! has_column refresh_tokens revoked_reason'
check "refresh_tokens_reason_needs_revocation is gone" eval '! has_constraint refresh_tokens_reason_needs_revocation'
check "family_id and its index are gone" eval '! has_column refresh_tokens family_id && ! has_index idx_refresh_tokens_family_id'
migrate -path "$MIGRATIONS_DIR" -database "$VERIFY_URL" up
check "the rows survive going back up" \
	test "$(psql_verify -c "SELECT count(*) FROM refresh_tokens WHERE token_hash IN ('legacy-active', 'legacy-revoked');")" == "2"

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
