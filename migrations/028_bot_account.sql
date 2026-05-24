ALTER TABLE bots
  ADD COLUMN account_id UUID REFERENCES exchange_accounts(id) ON DELETE SET NULL;

CREATE INDEX idx_bots_account ON bots(account_id) WHERE account_id IS NOT NULL;
