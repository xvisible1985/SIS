-- Floating PnL of open positions captured by the 30-min collector, so the dashboard "Депозит"
-- widget can show the drawdown next to the (equity-based) deposit.
--   unrealized_close — floating PnL at the day's last snapshot (negative = drawdown)
--   dd_worst_usd/pct — the worst (most negative) floating PnL seen in any snapshot of the day,
--                      and that snapshot's share of its equity, in percent (both ≤ 0)
-- NULL on days recorded before this migration: the figure cannot be reconstructed.
ALTER TABLE account_daily_equity
    ADD COLUMN IF NOT EXISTS unrealized_close NUMERIC(18,8),
    ADD COLUMN IF NOT EXISTS dd_worst_usd     NUMERIC(18,8),
    ADD COLUMN IF NOT EXISTS dd_worst_pct     NUMERIC(10,4);
