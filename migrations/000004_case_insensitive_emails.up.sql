-- Emails are unique regardless of case, like usernames. Fails if two accounts
-- already differ only in email case; resolve those first.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));
