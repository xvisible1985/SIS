//go:build integration

package main

import (
	"context"
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
