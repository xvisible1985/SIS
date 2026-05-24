-- Add parsed listing fields to bybit_announcements
ALTER TABLE bybit_announcements
    ADD COLUMN symbols TEXT[],
    ADD COLUMN markets TEXT[],
    ADD COLUMN max_leverage TEXT,
    ADD COLUMN parsed_at TIMESTAMPTZ;
