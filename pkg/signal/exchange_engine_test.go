package signal

import (
	"context"
	"testing"
	"time"
)

func TestSignalEngineSeparatesCandlesByExchange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bybit := NewEngine(ctx, nil)
	binance := bybit.ForExchange("binance")
	bybitKey := bybit.Hub().key("BTCUSDT", "1m")
	binanceKey := binance.Hub().key("BTCUSDT", "1m")

	now := time.Now().Truncate(time.Minute)
	for i := 61; i > 0; i-- {
		ts := now.Add(-time.Duration(i) * time.Minute).UnixMilli()
		bybit.Hub().buffers[bybitKey] = append(bybit.Hub().buffers[bybitKey], Candle{Time: ts, Close: 100})
		binance.Hub().buffers[binanceKey] = append(binance.Hub().buffers[binanceKey], Candle{Time: ts, Close: 200})
	}
	bybit.Hub().buffers[bybitKey][60].Close = 120
	binance.Hub().buffers[binanceKey][60].Close = 160

	if got := bybit.Hub().SnapshotOrFetch("BTCUSDT", "1m")[60].Close; got != 120 {
		t.Fatalf("Bybit close = %v, want 120", got)
	}
	if got := binance.Hub().SnapshotOrFetch("BTCUSDT", "1m")[60].Close; got != 160 {
		t.Fatalf("Binance close = %v, want 160", got)
	}
	cfg := []Config{{Name: "price-change", Params: map[string]any{"periodHours": 1, "thresholdPct": 10}}}
	if got := bybit.ComputeStateForce("BTCUSDT", "1m", cfg); got != Buy {
		t.Fatalf("Bybit state = %v, want Buy", got)
	}
	if got := binance.ComputeStateForce("BTCUSDT", "1m", cfg); got != Sell {
		t.Fatalf("Binance state = %v, want Sell", got)
	}
	if bybit.ForExchange("bybit") != bybit || binance.ForExchange("binance") != binance {
		t.Fatal("exchange engines must be stable")
	}
}

func TestTimesfmForecastUsesCandleExchange(t *testing.T) {
	const symbol = "EXCHANGE-SCOPED-TEST"
	SetTimesfmForecast(symbol, "1m", 2, 1, 20)
	SetTimesfmForecastForExchange("binance", symbol, "1m", 2, 1, -20)
	forecast := &timesfmSignal{contextBars: 2, horizonBars: 1, thresholdPct: 10, refreshIntervalSec: 60}
	candles := []Candle{{Time: 60_000}, {Time: 120_000}}
	if got := forecast.ComputeWithSymbol(symbol, candles); got != Buy {
		t.Fatalf("Bybit forecast = %v, want Buy", got)
	}
	candles[1].Exchange = "binance"
	if got := forecast.ComputeWithSymbol(symbol, candles); got != Sell {
		t.Fatalf("Binance forecast = %v, want Sell", got)
	}
}

func TestBinanceClosedDailyCandleRoutesToCanonicalSubscription(t *testing.T) {
	hub := NewKlineHub(context.Background())
	hub.binance = true
	hub.buffers[hub.key("BTCUSDT", "1d")] = []Candle{{Time: 1699913600000}}
	var received []Candle
	hub.listeners[hub.key("BTCUSDT", "1D")] = []func([]Candle){func(c []Candle) { received = c }}
	message := []byte(`{"e":"kline","s":"BTCUSDT","k":{"t":1700000000000,"T":1700086399999,"i":"1d","o":"100","h":"110","L":420,"l":"99","c":"105","v":"20","V":"15","x":true}}`)
	hub.handleBinanceMessage(message)
	if len(received) != 2 || received[1].Close != 105 || received[1].Exchange != "binance" {
		t.Fatalf("listener received %+v, want latest Binance candle at close 105", received)
	}
	if got := hub.Snapshot("BTCUSDT", "D"); len(got) != 2 {
		t.Fatalf("D alias has %d candles, want 2", len(got))
	}
}

func TestBinanceGapClearsBufferWithoutPublishingIncompleteSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hub := NewKlineHub(ctx)
	hub.binance = true
	key := hub.key("BTCUSDT", "1m")
	hub.buffers[key] = []Candle{{Time: 60_000}, {Time: 120_000}}
	published := make(chan struct{}, 1)
	hub.listeners[key] = []func([]Candle){func([]Candle) { published <- struct{}{} }}

	hub.acceptBinanceClose("BTCUSDT", "1m", Candle{Time: 240_000})
	if snapshot := hub.Snapshot("BTCUSDT", "1m"); len(snapshot) != 0 {
		t.Fatalf("gap left %d candles visible, want an empty buffer", len(snapshot))
	}
	select {
	case <-published:
		t.Fatal("gap triggered a signal before history was refilled")
	default:
	}
}
