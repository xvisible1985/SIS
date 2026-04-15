// pkg/exchange/binance/ws.go
package binance

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"sis/pkg/exchange"
	"sis/pkg/models"
)

const (
	spotWSURL    = "wss://stream.binance.com:9443/stream?streams="
	futuresWSURL = "wss://fstream.binance.com/stream?streams="
)

// Client implements exchange.Client for Binance.
type Client struct{}

func New() *Client { return &Client{} }

func (c *Client) Name() models.Exchange { return models.ExchangeBinance }

func (c *Client) FetchCandles(ctx context.Context, symbol string, market models.Market, tf models.Timeframe, from, to time.Time) ([]models.Candle, error) {
	return FetchCandles(ctx, symbol, market, tf, from, to)
}

// Subscribe connects to Binance combined stream and calls handler for each candle update.
// Reconnects automatically on disconnect. Blocks until ctx is cancelled.
func (c *Client) Subscribe(ctx context.Context, symbols []string, market models.Market, tf models.Timeframe, handler exchange.CandleHandler) error {
	streams := make([]string, len(symbols))
	for i, s := range symbols {
		streams[i] = strings.ToLower(s) + "@kline_" + string(tf)
	}

	wsBase := spotWSURL
	if market == models.MarketFutures {
		wsBase = futuresWSURL
	}
	url := wsBase + strings.Join(streams, "/")

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := c.runWSSession(ctx, url, market, handler); err != nil {
			log.Printf("binance ws: session ended (%v), reconnecting in 3s", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Client) runWSSession(ctx context.Context, url string, market models.Market, handler exchange.CandleHandler) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		// Combined stream wraps: {"stream":"btcusdt@kline_1m","data":{...}}
		start := strings.Index(string(msg), `"data":`)
		if start == -1 {
			continue
		}
		inner := msg[start+7 : len(msg)-1]

		candle, err := parseWSCandle(inner, market)
		if err != nil {
			log.Printf("binance ws: parse error: %v", err)
			continue
		}
		handler(candle)
	}
}
