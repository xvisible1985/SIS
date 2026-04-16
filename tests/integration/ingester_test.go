//go:build integration

// tests/integration/ingester_test.go
package integration_test

import (
	"context"
	"testing"
	"time"

	"sis/pkg/cache"
	"sis/pkg/exchange/binance"
	"sis/pkg/models"
)

// TestBinanceRESTFetchCandles verifies historical candle fetch end-to-end.
// Requires internet access.
func TestBinanceRESTFetchCandles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	to := time.Now().UTC().Truncate(time.Hour)
	from := to.Add(-2 * time.Hour)

	candles, err := binance.FetchCandles(ctx, "BTCUSDT", models.MarketSpot, models.TF1m, from, to)
	if err != nil {
		t.Fatalf("fetch candles: %v", err)
	}
	if len(candles) == 0 {
		t.Fatal("expected candles, got 0")
	}
	for i := 1; i < len(candles); i++ {
		if !candles[i].OpenTime.After(candles[i-1].OpenTime) {
			t.Errorf("candles not in ascending order at index %d", i)
		}
	}
	t.Logf("fetched %d candles from %v to %v", len(candles), candles[0].OpenTime, candles[len(candles)-1].OpenTime)
}

// TestRedisPublishSubscribe verifies the pub/sub pipeline.
func TestRedisPublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb, err := cache.Connect(ctx, "redis://localhost:6379/0")
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer rdb.Close()

	candle := models.Candle{
		Exchange:  models.ExchangeBinance,
		Symbol:    "BTCUSDT",
		Market:    models.MarketSpot,
		Timeframe: models.TF1m,
		OpenTime:  time.Now().Truncate(time.Minute),
		Close:     67000,
		Closed:    true,
	}

	sub := rdb.Subscribe(ctx, candle.RedisChannel())
	defer sub.Close()

	if err := cache.PublishCandle(ctx, rdb, candle); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msg, err := sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Channel != candle.RedisChannel() {
		t.Errorf("wrong channel: got %q", msg.Channel)
	}
	t.Logf("received on channel %s", msg.Channel)
}
