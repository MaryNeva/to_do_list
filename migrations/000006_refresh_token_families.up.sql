-- A family is one login's chain of rotated tokens. Replaying a rotated token
-- counts as reuse only while its family still has a live token; once the
-- chain was ended (logout, password change, reuse) a replay is just rejected.
-- Existing rows cannot be linked to their chains, so each becomes its own family.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS family_id UUID;
UPDATE refresh_tokens SET family_id = gen_random_uuid() WHERE family_id IS NULL;
ALTER TABLE refresh_tokens
    ALTER COLUMN family_id SET DEFAULT gen_random_uuid(),
    ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family_id ON refresh_tokens (family_id);
