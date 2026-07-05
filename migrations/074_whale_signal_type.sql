INSERT INTO signal_types (id, name, status, panel)
VALUES ('whale', 'Whale Tracker', 'enabled', 'signal')
ON CONFLICT (id) DO NOTHING;
