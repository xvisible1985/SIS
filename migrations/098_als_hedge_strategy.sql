-- ALS (Anti-Liquidation System) hedge strategy — foundational schema.
-- See docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md for the full design.

ALTER TABLE strategies
    ADD COLUMN als_thresholds JSONB,
    ADD COLUMN unsafe_hedge_min_pct NUMERIC(10,4),
    ADD COLUMN unsafe_hedge_max_pct NUMERIC(10,4),
    ADD COLUMN stop_market_size_k NUMERIC(10,4),
    ADD COLUMN tp_control_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN initial_stop_be_pct NUMERIC(10,4);

-- No new strategy_cycles columns: TP pause/resume (see
-- 2026-10-18-als-hedge-polish.md) reuses the EXISTING hedge_tp_suppressed column on
-- strategies and Engine.Notify — the exact mechanism services/api-gateway/hedge_engine.go
-- already uses for the Matrix hedge bot, which always recomputes the TP fresh from the
-- main's current avg entry/tp_pct once un-suppressed rather than restoring a saved
-- snapshot. A design-spec revision from the original "save/restore paused_tp_price" plan,
-- found during implementation planning — reuses more of the existing, already-proven
-- code path instead of adding new columns that would duplicate it.

CREATE TABLE als_stop_memory (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id   UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    level_id      UUID NOT NULL REFERENCES strategy_levels(id) ON DELETE CASCADE,
    symbol        TEXT NOT NULL,
    side          TEXT NOT NULL,
    trigger_price NUMERIC(18,8) NOT NULL,
    qty           TEXT NOT NULL,
    evicted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_als_stop_memory_strategy ON als_stop_memory (strategy_id);
