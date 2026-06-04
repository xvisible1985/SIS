CREATE TABLE IF NOT EXISTS strategy_defaults (
  strategy_type TEXT PRIMARY KEY,
  config        JSONB NOT NULL DEFAULT '{}',
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO strategy_defaults (strategy_type, config) VALUES
  ('grid', '{
    "leverage": 5,
    "grid_size_usdt": 100,
    "tp_pct": 2.0,
    "sl_pct": 5.0,
    "trailing_activation_pct": 1.5,
    "trailing_callback_pct": 0.5,
    "steps": [
      {"price_move_pct": 0, "size_pct": 50},
      {"price_move_pct": -1.5, "size_pct": 100},
      {"price_move_pct": -2.0, "size_pct": 150}
    ]
  }'),
  ('matrix', '{
    "leverage": 5,
    "grid_size_usdt": 100,
    "safe_zone_pct": 1.5
  }')
ON CONFLICT DO NOTHING;
