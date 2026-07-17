//go:build integration

package strategy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool — минимальный пул для этого пакета (services/api-gateway тесты используют
// свой newTestServer; здесь тестируем pkg/strategy напрямую через DATABASE_URL из окружения).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://sis:sis_secret@localhost:6432/sis")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestInsertMatrixTPProfit_Idempotent: два вызова с одинаковым OrderID пишут ровно одну
// строку (ON CONFLICT DO NOTHING), значения gross/net корректны.
func TestInsertMatrixTPProfit_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"insmtp-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'INSUSDT','long','matrix','stopped') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM matrix_tp_profits WHERE strategy_id=$1", stratID) })

	in := MatrixTPInsertInput{
		StrategyID: stratID, BotID: nil, AccountID: accID,
		CycleNum: 1, Symbol: "INSUSDT", GrossPnl: 5.0, OrderID: "order-abc-123",
	}
	InsertMatrixTPProfit(ctx, pool, in)
	InsertMatrixTPProfit(ctx, pool, in) // second call — must be a no-op

	var count int
	var netPnl float64
	if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(net_pnl),0) FROM matrix_tp_profits WHERE strategy_id=$1`, stratID).Scan(&count, &netPnl); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (idempotent insert)", count)
	}
	if netPnl != 5.0 {
		t.Errorf("net_pnl = %v, want 5.0 (no trader_executions fees seeded)", netPnl)
	}
}
