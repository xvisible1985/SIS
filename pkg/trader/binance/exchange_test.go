package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBinanceExchange_GetMarkPrice_UsesPublicEndpoint(t *testing.T) {
	var gotAPIKeyHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","markPrice":"67890.50000000"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.GetMarkPrice(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("GetMarkPrice: %v", err)
	}
	if got != 67890.5 {
		t.Errorf("GetMarkPrice = %v, want 67890.5", got)
	}
	if gotAPIKeyHeader != "" {
		t.Error("GetMarkPrice must use the public (unauthenticated) endpoint")
	}
}

func TestBinanceExchange_FetchPositions_TranslatesPositionRisk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"symbol":"BTCUSDT","positionSide":"LONG","positionAmt":"0.50000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"500.0","liquidationPrice":"40000.0","leverage":"10"},
			{"symbol":"ETHUSDT","positionSide":"BOTH","positionAmt":"0.00000000","entryPrice":"0.0","markPrice":"3000.0","unRealizedProfit":"0.0","liquidationPrice":"0.0","leverage":"20"}
		]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	// Only positions with a nonzero positionAmt are real open positions — Binance
	// returns every symbol's row (mostly zeros) unlike Bybit, which only lists real
	// positions. Filtering zero-size rows keeps behavior compatible with Bybit's
	// FetchPositions (used by pkg/strategy to find actually-open positions).
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1 (the zero-size ETHUSDT row must be filtered)", len(got))
	}
	p := got[0]
	if p.Symbol != "BTCUSDT" || p.Side != "Buy" || p.Size != "0.50000000" || p.EntryPrice != "60000.0" {
		t.Errorf("FetchPositions[0] = %+v, want BTCUSDT Buy 0.5 @ 60000.0", p)
	}
	if p.PositionIdx != 1 {
		t.Errorf("PositionIdx = %d, want 1 (LONG hedge-mode slot, matching Bybit's convention)", p.PositionIdx)
	}
	if p.Category != "linear" {
		t.Errorf("Category = %q, want linear", p.Category)
	}
}

func TestBinanceExchange_FetchPositions_ShortSideFromNegativeAmt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"SHORT","positionAmt":"-0.25000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"-250.0","liquidationPrice":"80000.0","leverage":"10"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1", len(got))
	}
	p := got[0]
	// Bybit's Position.Size is always a positive magnitude with Side telling direction —
	// Binance's positionAmt carries the sign instead. Must translate to Bybit's convention
	// since pkg/strategy reads Size as an unsigned magnitude string.
	if p.Side != "Sell" || p.Size != "0.25000000" {
		t.Errorf("FetchPositions[0] = %+v, want Sell with positive size 0.25 (sign stripped)", p)
	}
	if p.PositionIdx != 2 {
		t.Errorf("PositionIdx = %d, want 2 (SHORT hedge-mode slot)", p.PositionIdx)
	}
}

func TestBinanceExchange_FetchPositions_OneWayModeShortGetsPositionIdxZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"-0.30000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"-300.0","liquidationPrice":"90000.0","leverage":"10"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1", len(got))
	}
	p := got[0]
	// A one-way-mode account still reports positionSide:"BOTH" even for a short
	// (negative positionAmt) — PositionIdx must stay 0 (one-way slot), not fall
	// through to the hedge-mode short slot 2 just because the sign is negative.
	if p.Side != "Sell" || p.PositionIdx != 0 {
		t.Errorf("FetchPositions[0] = %+v, want Sell with PositionIdx=0 (one-way mode uses slot 0 regardless of side)", p)
	}
	if p.Size != "0.30000000" {
		t.Errorf("Size = %q, want 0.30000000", p.Size)
	}
}

func TestBinanceExchange_GetWalletBalance_TranslatesAccountEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalMarginBalance":"1000.5","assets":[{"asset":"USDT","availableBalance":"800.25"},{"asset":"BUSD","availableBalance":"5.0"}]}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	equity, available, err := ex.GetWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("GetWalletBalance: %v", err)
	}
	if equity != 1000.5 {
		t.Errorf("equity = %v, want 1000.5 (totalMarginBalance)", equity)
	}
	if available != 800.25 {
		t.Errorf("available = %v, want 800.25 (USDT asset's availableBalance, not BUSD)", available)
	}
}
