-- Sync any indicator_types already in panel='signal' into signal_types
-- so they appear in the strategy signal picker.
INSERT INTO signal_types (id, name, status, panel)
SELECT id, name, status, 'signal'
FROM indicator_types
WHERE panel = 'signal'
ON CONFLICT (id) DO UPDATE SET
    name       = EXCLUDED.name,
    status     = EXCLUDED.status,
    panel      = 'signal',
    updated_at = NOW();
