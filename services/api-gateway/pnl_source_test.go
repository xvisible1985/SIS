//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBotPnlUnionSQL_MatchesStrategyCumulativePnl: сумма по трёхкомпонентному источнику
// (botPnlUnionSQL) для стратегии должна совпадать с тем, что уже отдаёт
// GetStrategyCumulativePnl — иначе "Накоплено" и статистика бота расходятся.
func TestBotPnlUnionSQL_MatchesStrategyCumulativePnl(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "pnlunion")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "pnlunion")

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'PNLUUSDT','long','matrix','active') RETURNING id`,
		userID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// trade_history source.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, bot_id, account_id, owner_id, symbol, category, direction, cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,$4,'PNLUUSDT','linear','long',1,'tp',NOW(),NOW(),2.5)`,
		stratID, botID, accID, userID); err != nil {
		t.Fatalf("insert trade_history: %v", err)
	}

	// matrix_tp_profits source.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO matrix_tp_profits (strategy_id, bot_id, account_id, cycle_num, symbol, net_pnl, bybit_order_id)
		 VALUES ($1,$2,$3,1,'PNLUUSDT',1.5,'pnlu-order-1')`,
		stratID, botID, accID); err != nil {
		t.Fatalf("insert matrix_tp_profits: %v", err)
	}

	// strategy_levels source.
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,2,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, realized_pnl, sl_closed_at)
		 VALUES ($1,$2,1,'Sell',1.0,10,'10','sl_closed',0.3,NOW())`,
		stratID, cycleID); err != nil {
		t.Fatalf("insert strategy_levels: %v", err)
	}

	want := 2.5 + 1.5 + 0.3

	// Read via GetStrategyCumulativePnl (existing endpoint).
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+stratID+"/cumulative-pnl", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": stratID})
	rec := httptest.NewRecorder()
	s.GetStrategyCumulativePnl(rec, req)
	var cpResp struct {
		CumulativePnl float64 `json:"cumulative_pnl"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&cpResp); err != nil {
		t.Fatalf("decode cumulative-pnl: %v", err)
	}
	if diff := cpResp.CumulativePnl - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("GetStrategyCumulativePnl = %v, want %v", cpResp.CumulativePnl, want)
	}

	// Read via the new shared botPnlUnionSQL directly.
	var unionSum float64
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(bp.net_pnl),0) FROM `+botPnlUnionSQL+` bp WHERE bp.bot_id = $1`,
		botID).Scan(&unionSum); err != nil {
		t.Fatalf("query botPnlUnionSQL: %v", err)
	}
	if diff := unionSum - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("botPnlUnionSQL sum = %v, want %v", unionSum, want)
	}
}
