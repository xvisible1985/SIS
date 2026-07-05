-- Detach published bots from their creator so deleting the creator's personal bot
-- does not remove the public library entry.
--
-- Publishing now creates an independent catalog copy owned by a dedicated system
-- "Catalog" account. The copy links back to the original via published_from_id
-- (ON DELETE SET NULL — the copy survives when the original is deleted) and keeps
-- the real author via original_author_id for attribution.

-- System account that owns detached public library copies (role must be 'user'
-- per users_role_check). Fixed UUID so the backend can reference it directly.
INSERT INTO users (id, email, username, password_hash, role, email_verified)
VALUES ('00000000-0000-0000-0000-0000000000ca', 'catalog@novabot.system', 'NovaBot Catalog', '', 'user', true)
ON CONFLICT (id) DO NOTHING;

ALTER TABLE bots ADD COLUMN IF NOT EXISTS published_from_id UUID REFERENCES bots(id) ON DELETE SET NULL;
ALTER TABLE bots ADD COLUMN IF NOT EXISTS original_author_id UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS bots_published_from ON bots(published_from_id) WHERE published_from_id IS NOT NULL;
