//go:build integration

package main

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// createTestAccountExchange is createTestAccount's counterpart for a specific exchange —
// used to exercise computeBotStrategyCols's Plan #4b/#4c auto-raise dispatch against a
// Binance account, which createTestAccount (hardcoded to "bybit") can't do.
func createTestAccountExchange(t *testing.T, s *Server, userID, exchange string) string {
	t.Helper()
	s.encKey = testEncKey
	var id string
	err := s.pool.QueryRow(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,$2,'test','enc_key','enc_secret') RETURNING id`, userID, exchange,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestAccountExchange: %v", err)
	}
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", id)
	})
	return id
}

// TestComputeBotStrategyCols_RaisesGridSizeByAccountExchange locks in Plan #4b/#4c's
// grid_size_usdt auto-raise dispatch: a bot bound to a Binance account must have its
// minimum-order check performed against Binance's own MinOrderUSDT, not Bybit's — using
// the wrong exchange's minimum here could under-raise (leaving grid_size_usdt below what
// Binance actually requires, reproducing the live "-1111 Precision" rejection one step
// earlier, at order placement) or over-raise (an unnecessarily large position for a
// symbol Bybit prices very differently).
func TestComputeBotStrategyCols_RaisesGridSizeByAccountExchange(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cbsccols")
	binanceAcctID := createTestAccountExchange(t, s, userID, "binance")
	bybitAcctID := createTestAccountExchange(t, s, userID, "bybit")

	origBybit, origBinance := bybitGetPublicInstrumentInfo, binanceGetPublicInstrumentInfo
	t.Cleanup(func() { bybitGetPublicInstrumentInfo, binanceGetPublicInstrumentInfo = origBybit, origBinance })

	bybitGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{MinOrderUSDT: 5, MinQty: 0.001}, nil
	}
	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{MinOrderUSDT: 100, MinQty: 0.001}, nil
	}

	cfg := botCfgJSON{GridSizeUSDT: 10} // below both minimums' 10% buffer, so both must raise it

	binanceCols := s.computeBotStrategyCols(ctx, cfg, "BTCUSDT", binanceAcctID, 0)
	// 100 * 1.1 = 110, rounded up to one decimal by math.Ceil(x*10)/10 → 110.0
	if binanceCols.gridSize < 109 || binanceCols.gridSize > 111 {
		t.Errorf("binance account: gridSize = %.4f, want ~110 (raised to binance's MinOrderUSDT*1.1)", binanceCols.gridSize)
	}

	bybitCols := s.computeBotStrategyCols(ctx, cfg, "BTCUSDT", bybitAcctID, 0)
	// cfg.GridSizeUSDT=10 is already above bybit's MinOrderUSDT*1.1 (5.5), so it's left as-is.
	if bybitCols.gridSize != 10 {
		t.Errorf("bybit account: gridSize = %.4f, want 10 (unchanged, already above bybit's minimum)", bybitCols.gridSize)
	}
}
