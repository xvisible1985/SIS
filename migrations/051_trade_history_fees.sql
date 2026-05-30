-- migrations/051_trade_history_fees.sql
-- Add fee/funding tracking to trade_history for real net PnL.
-- fees   = sum of exchange trading commissions during the cycle
-- funding = sum of funding payments (positive = paid, negative = received)
-- net_pnl = pnl - fees - funding

ALTER TABLE trade_history
  ADD COLUMN fees    NUMERIC(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN funding NUMERIC(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN net_pnl NUMERIC(18,4) NOT NULL DEFAULT 0;

-- Backfill: historical rows have no fee data, net_pnl = gross pnl
UPDATE trade_history SET net_pnl = COALESCE(pnl, 0);
