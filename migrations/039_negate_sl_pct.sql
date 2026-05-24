-- SL% is now stored as a negative value (e.g. -5.0 = 5% below entry).
-- Negate all existing positive sl_pct values.
UPDATE strategies SET sl_pct = -sl_pct WHERE sl_pct > 0;
