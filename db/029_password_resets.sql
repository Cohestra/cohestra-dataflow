-- Password reset links. Only the SHA-256 of the token is stored; a link is
-- single-use (consumed with DELETE ... RETURNING) and expires after an hour.
-- Like email_verifications, this table is looked up before any tenant is
-- known, so it carries no tenant_id and no row-level security.
CREATE TABLE IF NOT EXISTS password_resets (
  token_hash  TEXT PRIMARY KEY,
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets(user_id);
GRANT SELECT, INSERT, UPDATE, DELETE ON password_resets TO dataflow_app;
