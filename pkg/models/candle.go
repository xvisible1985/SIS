// pkg/models/candle.go
package models

import "time"

type Exchange string
type Market string
type Timeframe string

const (
	ExchangeBinance Exchange = "binance"
	ExchangeBybit   Exchange = "bybit"

	MarketSpot    Market = "spot"
	MarketFutures Market = "futures"

	TF1m  Timeframe = "1m"
	TF5m  Timeframe = "5m"
	TF15m Timeframe = "15m"
	TF1h  Timeframe = "1h"
	TF4h  Timeframe = "4h"
	TF1d  Timeframe = "1d"
)

// TimeframeMinutes converts a Timeframe to its duration in minutes.
var TimeframeMinutes = map[Timeframe]int{
	TF1m: 1, TF5m: 5, TF15m: 15, TF1h: 60, TF4h: 240, TF1d: 1440,
}

type Candle struct {
	Exchange  Exchange
	Symbol    string
	Market    Market
	Timeframe Timeframe
	OpenTime  time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Closed    bool // true = candle is complete
}

// RedisChannel returns the pub/sub channel name for this candle's stream.
func (c Candle) RedisChannel() string {
	return "candles:" + string(c.Exchange) + ":" + c.Symbol + ":" + string(c.Market) + ":" + string(c.Timeframe)
}
