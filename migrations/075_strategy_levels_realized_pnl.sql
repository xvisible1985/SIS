-- Track per-level realized PnL for matrix per-level SL closes.
-- Full-cycle closes remain in trade_history; this captures partial closes
-- so that GetStrategyCumulativePnl reflects all realized P&L.
ALTER TABLE strategy_levels ADD COLUMN IF NOT EXISTS realized_pnl FLOAT8;
