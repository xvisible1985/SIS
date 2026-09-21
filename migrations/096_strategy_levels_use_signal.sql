-- migrations/096_strategy_levels_use_signal.sql
-- Persists, per placed/pending level row, whether this level's order is signal-gated —
-- mirrors force_virtual (added earlier for the same "should this level be software-monitored
-- instead of a blind resting order" question). A level created before this feature existed
-- defaults to false (unchanged, unconditional placement), matching every level's actual
-- historical behavior.
ALTER TABLE strategy_levels ADD COLUMN IF NOT EXISTS use_signal BOOLEAN NOT NULL DEFAULT false;
