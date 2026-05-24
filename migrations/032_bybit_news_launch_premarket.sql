-- Add launch date and pre-market flag to Bybit announcements
ALTER TABLE bybit_announcements
    ADD COLUMN launch_at TIMESTAMPTZ,
    ADD COLUMN is_pre_market BOOLEAN DEFAULT false;
