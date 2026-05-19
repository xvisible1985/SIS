CREATE TABLE bots (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_bot_id    UUID REFERENCES bots(id) ON DELETE SET NULL,
    is_fork          BOOLEAN NOT NULL DEFAULT FALSE,

    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    is_public        BOOLEAN NOT NULL DEFAULT FALSE,
    status           TEXT NOT NULL DEFAULT 'stopped'
                         CHECK (status IN ('active', 'stopped', 'draft')),

    symbol_whitelist TEXT[]  NOT NULL DEFAULT '{}',
    symbol_blacklist TEXT[]  NOT NULL DEFAULT '{}',

    triggers         JSONB   NOT NULL DEFAULT '[]',
    strategy_config  JSONB   NOT NULL DEFAULT '{}',

    deploy_count     INT     NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX bots_owner   ON bots (owner_id);
CREATE INDEX bots_source  ON bots (source_bot_id) WHERE source_bot_id IS NOT NULL;
CREATE INDEX bots_catalog ON bots (created_at DESC) WHERE is_public = TRUE;
