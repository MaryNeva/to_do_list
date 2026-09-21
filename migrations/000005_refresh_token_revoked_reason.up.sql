-- Why a refresh token was revoked. Only presenting a 'rotated' token again is
-- treated as reuse. Tokens revoked before this migration keep NULL and are
-- treated as ordinary revocations.
ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS revoked_reason TEXT
        CHECK (revoked_reason IN ('rotated', 'logout', 'password_change', 'reuse')),
    ADD CONSTRAINT refresh_tokens_reason_needs_revocation
        CHECK (revoked_reason IS NULL OR revoked_at IS NOT NULL);
