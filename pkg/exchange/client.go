// pkg/exchange/client.go
package exchange

import (
	"context"
	"time"

	"sis/pkg/models"
)

// CandleHandler is called for each received candle (open and closed).
type CandleHandler func(candle models.Candle)

// Client is the common interface for exchange data sources.
type Client interface {
	// Name returns the exchange identifier.
	Name() models.Exchange

	// FetchCandles fetches historical closed candles for the given symbol.
	// Returns candles in ascending order by OpenTime.
	FetchCandles(
		ctx context.Context,
		symbol string,
		market models.Market,
		tf models.Timeframe,
		from, to time.Time,
	) ([]models.Candle, error)

	// Subscribe streams candles for the given symbols.
	// handler is called for every received candle update.
	// Blocks until ctx is cancelled.
	Subscribe(
		ctx context.Context,
		symbols []string,
		market models.Market,
		tf models.Timeframe,
		handler CandleHandler,
	) error
}
