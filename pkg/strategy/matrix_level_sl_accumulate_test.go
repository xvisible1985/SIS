//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestAccumulateMatrixLevelSLPnlNow_NetsFeesFromTraderExecutions: накопление from a
// per-level matrix SL close must be fee-adjusted the same way RecordStrategyTrade's
// cycle-close path already is (Task 2) — not the raw gross PnL. Mirrors
// InsertMatrixTPProfit's fee lookup (trader_executions by account_id+order_id) exactly.
func TestAccumulateMatrixLevelSLPnlNow_NetsFeesFromTraderExecutions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, botID, mainID, hedgeID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"mlslacc-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO bots (owner_id, account_id, name, status, strategy_config) VALUES ($1,$2,'x','active','{}'::jsonb) RETURNING id`,
		ownerID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id) VALUES ($1,$2,'MLSUSDT','long','matrix','active',$3) RETURNING id`,
		ownerID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("create main strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id) VALUES ($1,$2,'MLSUSDT','short','matrix','active',$3) RETURNING id`,
		ownerID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("create hedge strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeID) })
	var sessionID string
	if err := pool.QueryRow(ctx, `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3) RETURNING id`,
		botID, mainID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("create hedge_sessions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, order_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES ($1,$2,'exec-1','sl-order-1','bybit','MLSUSDT','linear','Trade',0.75,NOW())`,
		ownerID, accID); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	accumulateMatrixLevelSLPnlNow(ctx, pool, MatrixLevelSLAccumulateInput{
		StrategyID: mainID, AccountID: accID, OrderID: "sl-order-1", GrossPnl: 10.0,
	})

	var got float64
	if err := pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&got); err != nil {
		t.Fatalf("read accumulated_pnl: %v", err)
	}
	if got != 9.25 {
		t.Errorf("accumulated_pnl = %v, want 9.25 (10.0 gross - 0.75 fee)", got)
	}
}
