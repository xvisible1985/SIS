// pkg/exchange/bybit/parse_test.go
package bybit

import (
	"testing"

	"sis/pkg/models"
)

func TestParseWSCandles(t *testing.T) {
	raw := []byte(`{"topic":"kline.1.BTCUSDT","data":[{"start":1700000000000,"end":1700000059999,"interval":"1","open":"67000","high":"67100","low":"66900","close":"67050","volume":"10.5","confirm":true}],"ts":1700000001000}`)
	candles, err := parseWSCandles(raw, "BTCUSDT", models.MarketSpot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 1 {
		t.Fatalf("expected 1 candle, got %d", len(candles))
	}
	c := candles[0]
	if c.Symbol != "BTCUSDT" {
		t.Errorf("symbol: got %q, want BTCUSDT", c.Symbol)
	}
	if c.Close != 67050 {
		t.Errorf("close: got %v, want 67050", c.Close)
	}
	if !c.Closed {
		t.Error("expected closed=true")
	}
}

func TestParseRESTCandlesReversal(t *testing.T) {
	raw := []byte(`{"result":{"symbol":"ETHUSDT","list":[["1700000120000","3010","3020","3000","3015","8.0"],["1700000060000","3000","3050","2990","3010","5.0"],["1700000000000","2990","3005","2985","3000","6.0"]]}}`)
	candles, err := parseRESTCandles(raw, models.MarketFutures, models.TF1m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 3 {
		t.Fatalf("expected 3 candles, got %d", len(candles))
	}
	if candles[0].OpenTime.UnixMilli() >= candles[1].OpenTime.UnixMilli() {
		t.Error("candles should be in ascending order")
	}
}
