-- Links the two underlying bot rows of a "Мультибот" (signal + hedge, created together
-- via POST /bots/multi). Set bidirectionally on both rows so either side can be looked up
-- directly. NULL for every standalone bot (the overwhelming majority).
ALTER TABLE bots
  ADD COLUMN paired_bot_id UUID REFERENCES bots(id) ON DELETE SET NULL;

CREATE INDEX idx_bots_paired_bot_id ON bots(paired_bot_id) WHERE paired_bot_id IS NOT NULL;
