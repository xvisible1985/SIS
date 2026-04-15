// pkg/models/candle_test.go
package models_test

import (
	"testing"
	"sis/pkg/models"
)

func TestCandleRedisChannel(t *testing.T) {
	c := models.Candle{
		Exchange:  models.ExchangeBinance,
		Symbol:    "BTCUSDT",
		Market:    models.MarketFutures,
		Timeframe: models.TF1h,
	}
	want := "candles:binance:BTCUSDT:futures:1h"
	if got := c.RedisChannel(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
