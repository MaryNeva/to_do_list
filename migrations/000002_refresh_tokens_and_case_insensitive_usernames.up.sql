CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);

-- Covers "tasks of one user, newest first" and its paginated variants, which
-- the single-column index on creator_id could not serve for the ORDER BY.
CREATE INDEX IF NOT EXISTS idx_tasks_creator_created_at ON tasks (creator_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_creator_status ON tasks (creator_id, status);

-- Logins are matched case-insensitively, so "Alice" and "alice" must not be
-- two accounts. This fails if such a pair already exists; resolve it by
-- renaming one of them before migrating.
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_key ON users (lower(username));
