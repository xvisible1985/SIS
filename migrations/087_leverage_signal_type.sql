INSERT INTO signal_types (id, name, status, panel)
VALUES ('leverage', 'Leverage Filter', 'enabled', 'signal')
ON CONFLICT (id) DO NOTHING;
