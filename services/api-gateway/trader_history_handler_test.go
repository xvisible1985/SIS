//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListTraderOrders_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th1")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/orders?status=all", nil)
	req = withUserID(req, userID)
	s.ListTraderOrders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTraderExecutions_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th2")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/executions?type=all", nil)
	req = withUserID(req, userID)
	s.ListTraderExecutions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetTraderStats_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th3")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/stats", nil)
	req = withUserID(req, userID)
	s.GetTraderStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func createTestAccount(t *testing.T, s *Server, userID string) string {
	t.Helper()
	s.encKey = testEncKey
	var id string
	err := s.pool.QueryRow(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,'bybit','test','enc_key','enc_secret') RETURNING id`, userID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestAccount: %v", err)
	}
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", id)
	})
	return id
}

func TestListTraderOrders_WithData(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th4")
	accID := createTestAccount(t, s, userID)

	s.pool.Exec(context.Background(),
		`INSERT INTO trader_orders (owner_id, account_id, order_link_id, exchange, symbol, category, side, order_type, qty)
		 VALUES ($1,$2,'sis_test_001','bybit','BTCUSDT','linear','Buy','Market',0.001)`,
		userID, accID,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/orders?status=all&limit=10", nil)
	req = withUserID(req, userID)
	s.ListTraderOrders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInsertAndQueryExecution(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th5")
	accID := createTestAccount(t, s, userID)

	s.pool.Exec(context.Background(),
		`INSERT INTO trader_executions
		 (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_time)
		 VALUES ($1,$2,'exec_001','bybit','BTCUSDT','linear','Trade',$3)`,
		userID, accID, time.Now(),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/executions?type=Trade", nil)
	req = withUserID(req, userID)
	s.ListTraderExecutions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestListTraderExecutions_BackfillsOrderLinkIdFromTraderOrders is the regression test
// for the live bug (2026-10-18, strategy NEARUSDT, acc N2 Binance): Binance executions
// are persisted with order_link_id deliberately NULL (see binanceUserTradeToExecution's
// doc comment in pkg/trader/binance/exchange.go — avoids an extra REST round-trip per
// order at sync time), but the terminal chart's fill-line rendering depends on it
// (frontend TerminalPage.tsx's historicalExecs filters on order_link_id starting with
// "SIS_STR") — so every Binance strategy's filled grid/matrix level was invisible on the
// chart even though the fill itself was recorded correctly. ListTraderExecutions must
// backfill it via a LEFT JOIN against trader_orders (same account_id+order_id), which
// already has it from the original PlaceOrder call.
func TestListTraderExecutions_BackfillsOrderLinkIdFromTraderOrders(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "execlink")
	accID := createTestAccount(t, s, userID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trader_orders WHERE account_id=$1", accID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trader_orders (owner_id, account_id, order_link_id, order_id, exchange, symbol, category, side, order_type, qty, status)
		 VALUES ($1,$2,'SIS_STR-abc12345-1-2-0','999111222','binance','NEARUSDT','linear','Buy','Market','2','Filled')`,
		userID, accID,
	); err != nil {
		t.Fatalf("seed trader_orders: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, order_id, order_link_id, exchange, symbol, category, side, exec_type, qty, price, exec_value, exec_fee, exec_time)
		 VALUES ($1,$2,'exec-1','999111222',NULL,'binance','NEARUSDT','linear','Buy','Trade',2,4.717,9.434,0.004717,NOW())`,
		userID, accID,
	); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/executions?account_id="+accID+"&type=all", nil)
	req = withUserID(req, userID)
	s.ListTraderExecutions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Executions []struct {
			ExecID      string  `json:"exec_id"`
			OrderLinkID *string `json:"order_link_id"`
		} `json:"executions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Executions) != 1 {
		t.Fatalf("executions = %d, want 1", len(body.Executions))
	}
	got := body.Executions[0]
	if got.OrderLinkID == nil || *got.OrderLinkID != "SIS_STR-abc12345-1-2-0" {
		t.Errorf("order_link_id = %v, want backfilled %q from trader_orders", got.OrderLinkID, "SIS_STR-abc12345-1-2-0")
	}
}

// TestListTraderExecutions_BackfillsOrderLinkIdFromStrategyLevels covers the SECOND,
// more impactful source for the same bug as
// TestListTraderExecutions_BackfillsOrderLinkIdFromTraderOrders — pkg/strategy never
// writes to trader_orders at all (only the manual-terminal TraderPlaceOrder handler
// does), so a strategy-engine-placed order's link_id can ONLY be recovered from
// strategy_levels (exchange_order_id/exchange_link_id) — this is the join that actually
// fixed the live NEARUSDT bug.
func TestListTraderExecutions_BackfillsOrderLinkIdFromStrategyLevels(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "execlevellink")
	accID := createTestAccount(t, s, userID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE account_id=$1", accID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	var stratID, cycleID, levelID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'ELLUSDT','long','grid','active') RETURNING id`,
		userID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID,
	).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, exchange_order_id, exchange_link_id)
		 VALUES ($1,$2,2,'Buy',4.913,10,'2.08','filled','888777666','SIS_STR-deadbeef-1-2-2') RETURNING id`,
		stratID, cycleID,
	).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, order_id, order_link_id, exchange, symbol, category, side, exec_type, qty, price, exec_value, exec_fee, exec_time)
		 VALUES ($1,$2,'exec-2','888777666',NULL,'binance','ELLUSDT','linear','Buy','Trade',2,4.717,9.434,0.004717,NOW())`,
		userID, accID,
	); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/executions?account_id="+accID+"&type=all", nil)
	req = withUserID(req, userID)
	s.ListTraderExecutions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Executions []struct {
			ExecID      string  `json:"exec_id"`
			OrderLinkID *string `json:"order_link_id"`
		} `json:"executions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Executions) != 1 {
		t.Fatalf("executions = %d, want 1", len(body.Executions))
	}
	got := body.Executions[0]
	if got.OrderLinkID == nil || *got.OrderLinkID != "SIS_STR-deadbeef-1-2-2" {
		t.Errorf("order_link_id = %v, want backfilled %q from strategy_levels", got.OrderLinkID, "SIS_STR-deadbeef-1-2-2")
	}
}
