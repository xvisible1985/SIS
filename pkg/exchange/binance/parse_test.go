// pkg/exchange/binance/parse_test.go
package binance

import (
	"testing"
	"time"

	"sis/pkg/models"
)

func TestParseWSCandle(t *testing.T) {
	raw := []byte(`{"e":"kline","s":"BTCUSDT","k":{"t":1700000000000,"T":1700000059999,"i":"1m","o":"67000","h":"67100","L":12345,"l":"66900","c":"67050","v":"10.5","V":"4","x":true}}`)
	candle, err := ParseWSCandle(raw, models.MarketSpot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candle.Symbol != "BTCUSDT" {
		t.Errorf("symbol: got %q, want BTCUSDT", candle.Symbol)
	}
	if candle.Low != 66900 || candle.Volume != 10.5 {
		t.Fatalf("low=%v, volume=%v; want 66900, 10.5", candle.Low, candle.Volume)
	}
	if candle.Close != 67050 {
		t.Errorf("close: got %v, want 67050", candle.Close)
	}
	if !candle.Closed {
		t.Error("expected closed=true")
	}
	if candle.OpenTime != time.UnixMilli(1700000000000).UTC() {
		t.Errorf("open_time mismatch")
	}
}

func TestParseWSCandleNotClosed(t *testing.T) {
	raw := []byte(`{"e":"kline","s":"ETHUSDT","k":{"t":1700000060000,"i":"1m","o":"3000","h":"3050","L": 12345, "l":"2990","c":"3020","v":"5.0","x":false}}`)
	candle, err := ParseWSCandle(raw, models.MarketFutures)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candle.Closed {
		t.Error("expected closed=false")
	}
}
