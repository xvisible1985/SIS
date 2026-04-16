//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createTestSignal(t *testing.T, s *Server, userID string) string {
	t.Helper()
	body := `{"name":"job_test","description":"","exchange":"binance","symbol":"BTCUSDT","market":"spot","timeframe":"1h","direction":"LONG","conditions":` + testConditions + `}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateSignal(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create signal for test: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	sigID, _ := created["id"].(string)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM signals WHERE id=$1", sigID)
	})
	return sigID
}

func TestSubmitBacktest_Success(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	btBody := `{"period_from":"2024-01-01T00:00:00Z","period_to":"2024-12-31T00:00:00Z","take_profit":2.0,"stop_loss":1.0}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals/"+sigID+"/backtest", bytes.NewBufferString(btBody))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": sigID})
	s.SubmitBacktest(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["job_id"] == "" {
		t.Error("expected job_id in response")
	}
}

func TestSubmitOptimize_Success(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	optBody := `{"period_from":"2024-01-01T00:00:00Z","period_to":"2024-12-31T00:00:00Z","mode":"fast","score_by":"profit_factor","top_n":5,"take_profits":[2.0],"stop_losses":[1.0],"param_space":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals/"+sigID+"/optimize", bytes.NewBufferString(optBody))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": sigID})
	s.SubmitOptimize(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["job_id"] == "" {
		t.Error("expected job_id in response")
	}
}

func TestGetBacktestResults_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signals/"+sigID+"/backtest-results", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": sigID})
	s.GetBacktestResults(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var results []any
	json.NewDecoder(rec.Body).Decode(&results)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestGetOptimizationResults_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signals/"+sigID+"/optimization-results", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": sigID})
	s.GetOptimizationResults(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var results []any
	json.NewDecoder(rec.Body).Decode(&results)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}
