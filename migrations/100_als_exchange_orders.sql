-- When true, ALS top-up levels are placed as real conditional (stop-market) orders on the
-- exchange at their computed target_price, repriced as the main position's live avg entry
-- moves, instead of the default reactive behavior (alsPriceTick fires a plain market order
-- only once the price has already crossed the threshold). The reactive mode can fill at a
-- price far from the intended threshold during a fast move, and in non-monotonic order
-- relative to the levels themselves — see the 2026-10-23 incident (BIRBUSDT, three
-- thresholds crossed within 0.5s, filled out of sequence relative to their target prices).
ALTER TABLE strategies
    ADD COLUMN als_exchange_orders BOOLEAN NOT NULL DEFAULT false;
