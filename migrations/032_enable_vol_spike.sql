UPDATE signal_types
SET enabled = true, status = 'enabled', updated_at = NOW()
WHERE id = 'vol-spike';
