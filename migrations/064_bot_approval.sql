-- migrations/064_bot_approval.sql

-- Timer fields on bots
ALTER TABLE bots
  ADD COLUMN IF NOT EXISTS active_seconds_acc BIGINT      NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS active_since       TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS approval_status    TEXT
    CONSTRAINT bots_approval_status_check
      CHECK (approval_status IN ('pending', 'approved', 'rejected'));

-- Configurable threshold (days) for submitting for approval
ALTER TABLE coin_filter_settings
  ADD COLUMN IF NOT EXISTS min_publish_days INTEGER NOT NULL DEFAULT 15;

-- Index for admin "pending approval" queries
CREATE INDEX IF NOT EXISTS bots_approval_pending
  ON bots (owner_id)
  WHERE approval_status = 'pending';

-- Ensure min_publish_days is always at least 1
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'coin_filter_min_publish_days_check'
  ) THEN
    ALTER TABLE coin_filter_settings
      ADD CONSTRAINT coin_filter_min_publish_days_check
        CHECK (min_publish_days >= 1)
        NOT VALID;
  END IF;
END $$;
