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

-- Serves per-user task lists ordered by created_at.
CREATE INDEX IF NOT EXISTS idx_tasks_creator_created_at ON tasks (creator_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_creator_status ON tasks (creator_id, status);

-- Usernames are unique case-insensitively. Fails if such duplicates already
-- exist; rename one of them first.
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_key ON users (lower(username));
