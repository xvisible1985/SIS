-- migrations/048_protected_build.sql

ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS protected_build BOOLEAN NOT NULL DEFAULT FALSE;
