//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTimesfmPredictions_ReturnsRowsAndAggregates(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFHANDLERUSDT") })

	insert := func(correct *bool) {
		s.pool.Exec(ctx, `
			INSERT INTO timesfm_predictions
				(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at, actual_price, actual_direction, correct, checked_at)
			VALUES ('bybit',$1,'futures','5m',100,3,NOW(),100.0,2.0,'buy',NOW(),
			        CASE WHEN $2::bool IS NULL THEN NULL ELSE 101.0 END,
			        CASE WHEN $2::bool IS NULL THEN NULL ELSE 'buy' END,
			        $2, CASE WHEN $2::bool IS NULL THEN NULL ELSE NOW() END)`,
			"TFHANDLERUSDT", correct)
	}
	tru, fls := true, false
	insert(&tru)
	insert(&fls)
	insert(nil) // still pending

	req := httptest.NewRequest(http.MethodGet, "/signals/timesfm/predictions?symbol=TFHANDLERUSDT", nil)
	w := httptest.NewRecorder()
	s.GetTimesfmPredictions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var out timesfmPredictionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Total != 3 {
		t.Errorf("total = %d, want 3", out.Total)
	}
	if out.Checked != 2 {
		t.Errorf("checked = %d, want 2", out.Checked)
	}
	if out.CorrectN != 1 {
		t.Errorf("correct = %d, want 1", out.CorrectN)
	}
	if out.WinRate < 49.9 || out.WinRate > 50.1 {
		t.Errorf("win_rate = %v, want 50.0", out.WinRate)
	}
	if len(out.Predictions) != 3 {
		t.Errorf("predictions returned = %d, want 3", len(out.Predictions))
	}
}

func TestGetTimesfmPredictions_FiltersByTimeframeAndLimit(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFHANDLERTFUSDT") })

	insert := func(timeframe string) {
		s.pool.Exec(ctx, `
			INSERT INTO timesfm_predictions
				(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at, actual_price, actual_direction, correct, checked_at)
			VALUES ('bybit',$1,'futures',$2,100,3,NOW(),100.0,2.0,'buy',NOW(),NULL,NULL,NULL,NULL)`,
			"TFHANDLERTFUSDT", timeframe)
	}
	insert("5m")
	insert("5m")
	insert("15m")

	// timeframe filter: only the two 5m rows should come back.
	req := httptest.NewRequest(http.MethodGet, "/signals/timesfm/predictions?symbol=TFHANDLERTFUSDT&timeframe=5m", nil)
	w := httptest.NewRecorder()
	s.GetTimesfmPredictions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var out timesfmPredictionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Total != 2 {
		t.Errorf("timeframe=5m: total = %d, want 2", out.Total)
	}
	for _, p := range out.Predictions {
		if p.Timeframe != "5m" {
			t.Errorf("timeframe=5m filter leaked row with timeframe=%q", p.Timeframe)
		}
	}

	// limit: capped to the requested count across all timeframes for the symbol.
	req = httptest.NewRequest(http.MethodGet, "/signals/timesfm/predictions?symbol=TFHANDLERTFUSDT&limit=1", nil)
	w = httptest.NewRecorder()
	s.GetTimesfmPredictions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	out = timesfmPredictionsResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Total != 1 {
		t.Errorf("limit=1: total = %d, want 1", out.Total)
	}
}

func TestGetTimesfmPredictions_MissingSymbol_400(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/signals/timesfm/predictions", nil)
	w := httptest.NewRecorder()
	s.GetTimesfmPredictions(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
