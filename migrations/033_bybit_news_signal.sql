-- Add Bybit News as a signal type for bot activation
INSERT INTO signal_types (id, name, status, panel)
VALUES ('bybit-news', 'Bybit News Listing', 'enabled', 'signal')
ON CONFLICT (id) DO NOTHING;
