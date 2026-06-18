-- Track which hedge bot last promoted a strategy via flip.
-- Allows the hedge bot to re-hedge its own flipped strategies even after bot_id=NULL.
ALTER TABLE strategies
    ADD COLUMN IF NOT EXISTS flip_origin_bot_id UUID REFERENCES bots(id) ON DELETE SET NULL;
