//go:build integration

package strategy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mlslFixture holds the account/bot/strategy/session rows shared by every test in this
// file — extracted to avoid re-typing the same 6-table setup per test case.
type mlslFixture struct {
	ownerID, accID, mainID, hedgeID, sessionID string
}

func newMLSLFixture(t *testing.T, pool *pgxpool.Pool, ctx context.Context, symbol string) mlslFixture {
	t.Helper()
	var f mlslFixture
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"mlslacc-"+t.Name()+"@example.com").Scan(&f.ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", f.ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		f.ownerID).Scan(&f.accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", f.accID) })
	var botID string
	if err := pool.QueryRow(ctx, `INSERT INTO bots (owner_id, account_id, name, status, strategy_config) VALUES ($1,$2,'x','active','{}'::jsonb) RETURNING id`,
		f.ownerID, f.accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id) VALUES ($1,$2,$3,'long','matrix','active',$4) RETURNING id`,
		f.ownerID, f.accID, symbol, botID).Scan(&f.mainID); err != nil {
		t.Fatalf("create main strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", f.mainID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id) VALUES ($1,$2,$3,'short','matrix','active',$4) RETURNING id`,
		f.ownerID, f.accID, symbol, botID).Scan(&f.hedgeID); err != nil {
		t.Fatalf("create hedge strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", f.hedgeID) })
	if err := pool.QueryRow(ctx, `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3) RETURNING id`,
		botID, f.mainID, f.hedgeID).Scan(&f.sessionID); err != nil {
		t.Fatalf("create hedge_sessions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", f.sessionID) })
	return f
}

func mlslReadAccumulated(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sessionID string) float64 {
	t.Helper()
	var got float64
	if err := pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&got); err != nil {
		t.Fatalf("read accumulated_pnl: %v", err)
	}
	return got
}

// TestAccumulateMatrixLevelSLPnlNow_NetsFeesFromTraderExecutions: накопление from a
// per-level matrix SL close must be fee-adjusted the same way RecordStrategyTrade's
// cycle-close path already is (Task 2) — not the raw gross PnL. Mirrors
// InsertMatrixTPProfit's fee lookup (trader_executions by account_id+order_id) exactly.
// Seeds TWO trader_executions rows for the target order (0.75 and -0.30) plus one for an
// UNRELATED order (999.0) to make the assertion load-bearing: a wrong aggregation (MAX
// instead of SUM, or a missing account_id/order_id filter) would produce a value other
// than 9.55, not coincidentally the same answer as a single-row seed would.
func TestAccumulateMatrixLevelSLPnlNow_NetsFeesFromTraderExecutions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	f := newMLSLFixture(t, pool, ctx, "MLSUSDT")

	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, order_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES
		   ($1,$2,'exec-1','sl-order-1','bybit','MLSUSDT','linear','Trade',0.75,NOW()),
		   ($1,$2,'exec-2','sl-order-1','bybit','MLSUSDT','linear','Trade',-0.30,NOW()),
		   ($1,$2,'exec-3','sl-order-OTHER','bybit','MLSUSDT','linear','Trade',999.0,NOW())`,
		f.ownerID, f.accID); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", f.accID) })

	accumulateMatrixLevelSLPnlNow(ctx, pool, MatrixLevelSLAccumulateInput{
		StrategyID: f.mainID, AccountID: f.accID, OrderID: "sl-order-1", GrossPnl: 10.0,
	})

	// fees = SUM(ABS(0.75), ABS(-0.30)) = 1.05, excluding the unrelated order's 999.0.
	if got := mlslReadAccumulated(t, pool, ctx, f.sessionID); got != 8.95 {
		t.Errorf("accumulated_pnl = %v, want 8.95 (10.0 gross - (0.75+0.30) fees, unrelated order excluded)", got)
	}
}

// TestAccumulateMatrixLevelSLPnlNow_NoMatchingFeeRows_TreatsAsZeroFees: when OrderID
// doesn't match any trader_executions row (e.g. fee data hasn't landed yet, or the order
// id is otherwise unresolvable), the function must treat fees as zero and still accumulate
// the full gross PnL — not skip the accumulation or error out.
func TestAccumulateMatrixLevelSLPnlNow_NoMatchingFeeRows_TreatsAsZeroFees(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	f := newMLSLFixture(t, pool, ctx, "MLSNOMATCHUSDT")

	accumulateMatrixLevelSLPnlNow(ctx, pool, MatrixLevelSLAccumulateInput{
		StrategyID: f.mainID, AccountID: f.accID, OrderID: "nonexistent-order", GrossPnl: 10.0,
	})

	if got := mlslReadAccumulated(t, pool, ctx, f.sessionID); got != 10.0 {
		t.Errorf("accumulated_pnl = %v, want 10.0 (no matching fee rows -> fees=0, full gross accumulated)", got)
	}
}
