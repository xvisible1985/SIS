INSERT INTO signal_types (id, name, status, panel)
VALUES ('rsi-test', 'RSI Test', 'enabled', 'signal')
ON CONFLICT (id) DO NOTHING;
