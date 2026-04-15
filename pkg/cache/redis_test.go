// pkg/cache/redis_test.go
package cache_test

import (
	"context"
	"testing"
	"time"

	"sis/pkg/cache"
	"sis/pkg/models"
)

// TestPublishCandle verifies that PublishCandle sends to the correct channel.
// Requires a running Redis on localhost:6379 (use `docker compose up -d redis`).
func TestPublishCandle(t *testing.T) {
	ctx := context.Background()
	c, err := cache.Connect(ctx, "redis://localhost:6379/0")
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer c.Close()

	candle := models.Candle{
		Exchange:  models.ExchangeBinance,
		Symbol:    "BTCUSDT",
		Market:    models.MarketSpot,
		Timeframe: models.TF1m,
		OpenTime:  time.Now().Truncate(time.Minute),
		Close:     67000,
		Closed:    true,
	}

	sub := c.Subscribe(ctx, candle.RedisChannel())
	defer sub.Close()

	if err := cache.PublishCandle(ctx, c, candle); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msg, err := sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Channel != candle.RedisChannel() {
		t.Errorf("channel: got %q, want %q", msg.Channel, candle.RedisChannel())
	}
}
