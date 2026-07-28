INSERT INTO indicator_types (id, name, status, panel)
VALUES ('price-change', 'Изменение цены', 'disabled', 'indicator')
ON CONFLICT (id) DO NOTHING;
