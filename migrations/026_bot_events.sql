CREATE TABLE IF NOT EXISTS bot_events (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id  UUID NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    message TEXT NOT NULL,
    level   TEXT NOT NULL DEFAULT 'info',  -- info | warn | error
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bot_events_bot_id ON bot_events(bot_id, created_at DESC);
