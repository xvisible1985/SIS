// services/signal-engine/backtest_test.go
package main

import (
	"testing"
	"time"

	"sis/pkg/models"
)

func makeSyntheticCandles(n int, basePrice float64) []models.Candle {
	candles := make([]models.Candle, n)
	price := basePrice
	for i := range candles {
		candles[i] = models.Candle{
			OpenTime:  time.Now().Add(time.Duration(i) * time.Minute),
			Open:      price,
			High:      price * 1.005,
			Low:       price * 0.995,
			Close:     price,
			Volume:    1000,
			Closed:    true,
			Exchange:  models.ExchangeBinance,
			Symbol:    "BTCUSDT",
			Market:    models.MarketSpot,
			Timeframe: models.TF1m,
		}
		price += 0.5 // slight uptrend
	}
	return candles
}

func TestComputeMetrics_AllWins(t *testing.T) {
	trades := []Trade{
		{Result: "win", GainPct: 2.0, EntryTime: time.Now()},
		{Result: "win", GainPct: 2.0, EntryTime: time.Now()},
		{Result: "win", GainPct: 2.0, EntryTime: time.Now()},
	}
	r := computeMetrics(trades)
	if r.WinRate != 1.0 {
		t.Errorf("win_rate: got %v, want 1.0", r.WinRate)
	}
	if r.WinCount != 3 {
		t.Errorf("win_count: got %d, want 3", r.WinCount)
	}
	if r.LossCount != 0 {
		t.Errorf("loss_count: got %d, want 0", r.LossCount)
	}
}

func TestComputeMetrics_AllLosses(t *testing.T) {
	trades := []Trade{
		{Result: "loss", GainPct: -1.0, EntryTime: time.Now()},
		{Result: "loss", GainPct: -1.0, EntryTime: time.Now()},
	}
	r := computeMetrics(trades)
	if r.WinRate != 0.0 {
		t.Errorf("win_rate: got %v, want 0.0", r.WinRate)
	}
	if r.ProfitFactor != 0.0 {
		t.Errorf("profit_factor: got %v, want 0.0 (no gains)", r.ProfitFactor)
	}
}

func TestComputeMetrics_Mixed(t *testing.T) {
	trades := []Trade{
		{Result: "win", GainPct: 4.0},
		{Result: "loss", GainPct: -2.0},
		{Result: "win", GainPct: 4.0},
		{Result: "loss", GainPct: -2.0},
	}
	r := computeMetrics(trades)
	if r.WinRate != 0.5 {
		t.Errorf("win_rate: got %v, want 0.5", r.WinRate)
	}
	// ProfitFactor = totalGain / totalLoss = 8 / 4 = 2.0
	if r.ProfitFactor != 2.0 {
		t.Errorf("profit_factor: got %v, want 2.0", r.ProfitFactor)
	}
}

func TestComputeMetrics_MaxDrawdown(t *testing.T) {
	// Peak at +10, then drops to -5 → drawdown = 15
	trades := []Trade{
		{Result: "win", GainPct: 5.0},
		{Result: "win", GainPct: 5.0},
		{Result: "loss", GainPct: -10.0},
		{Result: "loss", GainPct: -5.0},
	}
	r := computeMetrics(trades)
	if r.MaxDrawdown != 15.0 {
		t.Errorf("max_drawdown: got %v, want 15.0", r.MaxDrawdown)
	}
}

func TestSimulateTrade_LongWin(t *testing.T) {
	// Entry at price 100, TP at 102 (2%), SL at 99 (1%)
	// Next candle high=103 → should hit TP
	candles := []models.Candle{
		{OpenTime: time.Now(), Close: 100, High: 100.5, Low: 99.5},
		{OpenTime: time.Now().Add(time.Minute), Close: 103, High: 103, Low: 101},
	}
	p := BacktestParams{Direction: "LONG", TakeProfit: 2.0, StopLoss: 1.0}
	trade, skip := simulateTrade(candles, 0, p, candles[0])
	if trade.Result != "win" {
		t.Errorf("expected win, got %s", trade.Result)
	}
	if skip != 1 {
		t.Errorf("expected skip=1, got %d", skip)
	}
}

func TestSimulateTrade_LongLoss(t *testing.T) {
	// Entry at price 100, SL at 99 (1%), next candle low=98 → SL hit
	candles := []models.Candle{
		{OpenTime: time.Now(), Close: 100, High: 100.5, Low: 99.5},
		{OpenTime: time.Now().Add(time.Minute), Close: 98, High: 100, Low: 98},
	}
	p := BacktestParams{Direction: "LONG", TakeProfit: 2.0, StopLoss: 1.0}
	trade, _ := simulateTrade(candles, 0, p, candles[0])
	if trade.Result != "loss" {
		t.Errorf("expected loss, got %s", trade.Result)
	}
}

func TestSimulateTrade_OpenAtEnd(t *testing.T) {
	// Only 1 future candle, neither TP nor SL hit
	candles := []models.Candle{
		{OpenTime: time.Now(), Close: 100, High: 100.5, Low: 99.5},
		{OpenTime: time.Now().Add(time.Minute), Close: 100.5, High: 100.8, Low: 99.8},
	}
	p := BacktestParams{Direction: "LONG", TakeProfit: 5.0, StopLoss: 5.0}
	trade, _ := simulateTrade(candles, 0, p, candles[0])
	if trade.Result != "open" {
		t.Errorf("expected open, got %s", trade.Result)
	}
}
