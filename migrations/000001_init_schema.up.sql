-- Initial schema: users and tasks.
--
-- Differences from the original hand-run init.sql:
--   * CREATE USER / CREATE DATABASE removed - that is infrastructure setup,
--     handled by Postgres's own POSTGRES_USER/POSTGRES_PASSWORD/POSTGRES_DB
--     environment variables in deployments/docker-compose.yml, not by a
--     migration (a migration runs *inside* a database that must already
--     exist, connected as a user that must already exist).
--   * users.username is now UNIQUE - login looks users up by username, so a
--     duplicate username would previously have made login for either
--     account resolve to whichever row Postgres happened to return first.
--   * password column renamed password_hash to make it unambiguous at the
--     schema level that this is never a plaintext password.
--   * created_at/updated_at are TIMESTAMPTZ, not TIMESTAMP, so stored
--     instants are unambiguous regardless of the server's or a client's
--     local timezone.
--   * status is a plain TEXT column with a CHECK constraint instead of a
--     Postgres ENUM type. Adding a new status to an ENUM requires
--     ALTER TYPE ... ADD VALUE, which cannot run inside a transaction in
--     older Postgres versions and complicates migrations; CHECK is just as
--     safe and trivially adjusted by a later migration.

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tasks (
    id          BIGSERIAL PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'created'
                    CHECK (status IN ('created', 'in_progress', 'completed')),
    creator_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tasks_creator_id ON tasks (creator_id);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_tasks_updated_at
    BEFORE UPDATE ON tasks
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
