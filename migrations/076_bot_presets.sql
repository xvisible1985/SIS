CREATE TABLE bot_presets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    aggressiveness  TEXT NOT NULL DEFAULT 'moderate'
                        CHECK (aggressiveness IN ('conservative', 'moderate', 'aggressive')),
    coin_type       TEXT NOT NULL DEFAULT 'both'
                        CHECK (coin_type IN ('stable', 'meme', 'both')),
    trading_style   TEXT NOT NULL DEFAULT 'both'
                        CHECK (trading_style IN ('diversified', 'concentrated', 'both')),
    tags            TEXT[] NOT NULL DEFAULT '{}',
    is_active       BOOLEAN NOT NULL DEFAULT true,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE bot_preset_bots (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preset_id   UUID NOT NULL REFERENCES bot_presets(id) ON DELETE CASCADE,
    bot_id      UUID NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'trading'
                    CHECK (role IN ('trading', 'hedging')),
    sort_order  INT NOT NULL DEFAULT 0,
    UNIQUE (preset_id, bot_id)
);

CREATE INDEX bot_preset_bots_preset ON bot_preset_bots(preset_id);
CREATE INDEX bot_presets_active ON bot_presets(sort_order) WHERE is_active = true;
