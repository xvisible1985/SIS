-- Track which main strategy a hedge strategy is hedging.
-- Unique partial index ensures at most one active/finishing hedge per main strategy —
-- the first hedge bot to claim it wins; when stopped the slot is freed automatically.
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS hedged_strategy_id UUID REFERENCES strategies(id);

CREATE UNIQUE INDEX IF NOT EXISTS strategies_hedged_active_uniq
    ON strategies (hedged_strategy_id)
    WHERE hedged_strategy_id IS NOT NULL
      AND status IN ('active', 'finishing');
