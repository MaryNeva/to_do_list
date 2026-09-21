ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_reason_needs_revocation,
    DROP COLUMN IF EXISTS revoked_reason;
