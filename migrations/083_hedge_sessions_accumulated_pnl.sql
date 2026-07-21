-- migrations/083_hedge_sessions_accumulated_pnl.sql
-- Bot-level accumulated realized PnL for a hedge/matrix pairing session, incremented
-- directly by the code that realizes each PnL event (closeCycle's async trade recorder,
-- handleMatrixSLFill's per-level SL close) rather than reconstructed later from
-- trade_history/strategy_levels — see docs/superpowers/specs/2026-07-21-matrix-cycle-
-- lifecycle-redesign-design.md Section 2 for why: trade_history attribution is not
-- reliable enough to drive a real-money paired-close trigger.
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS accumulated_pnl NUMERIC(18, 8) NOT NULL DEFAULT 0;
