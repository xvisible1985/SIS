-- migrations/045_rename_dca_to_matrix.sql
-- Rename strategy_type 'dca' → 'matrix' for consistency with frontend naming.
UPDATE strategies SET strategy_type = 'matrix' WHERE strategy_type = 'dca';
