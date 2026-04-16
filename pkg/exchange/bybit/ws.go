// pkg/exchange/bybit/ws.go
package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"sis/pkg/exchange"
	"sis/pkg/models"
)

const (
	bybitSpotWSURL    = "wss://stream.bybit.com/v5/public/spot"
	bybitFuturesWSURL = "wss://stream.bybit.com/v5/public/linear"
)

// Client implements exchange.Client for Bybit.
type Client struct{}

func New() *Client { return &Client{} }

func (c *Client) Name() models.Exchange { return models.ExchangeBybit }

func (c *Client) FetchCandles(ctx context.Context, symbol string, market models.Market, tf models.Timeframe, from, to time.Time) ([]models.Candle, error) {
	return FetchCandles(ctx, symbol, market, tf, from, to)
}

func (c *Client) Subscribe(ctx context.Context, symbols []string, market models.Market, tf models.Timeframe, handler exchange.CandleHandler) error {
	wsURL := bybitSpotWSURL
	if market == models.MarketFutures {
		wsURL = bybitFuturesWSURL
	}
	interval := tfToBybitInterval[tf]

	topics := make([]string, len(symbols))
	for i, s := range symbols {
		topics[i] = "kline." + interval + "." + s
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := c.runWSSession(ctx, wsURL, topics, market, handler); err != nil {
			log.Printf("bybit ws: session ended (%v), reconnecting in 3s", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Client) runWSSession(ctx context.Context, wsURL string, topics []string, market models.Market, handler exchange.CandleHandler) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("bybit ws: dial: %w", err)
	}
	defer conn.Close()

	subMsg, _ := json.Marshal(map[string]any{
		"op":   "subscribe",
		"args": topics,
	})
	if err := conn.WriteMessage(websocket.TextMessage, subMsg); err != nil {
		return fmt.Errorf("bybit ws: subscribe: %w", err)
	}

	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pingTicker.C:
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"op":"ping"}`))
			}
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("bybit ws: read: %w", err)
		}

		if strings.Contains(string(msg), `"op"`) {
			continue
		}

		var raw struct {
			Topic string `json:"topic"`
		}
		if err := json.Unmarshal(msg, &raw); err != nil || raw.Topic == "" {
			continue
		}
		parts := strings.Split(raw.Topic, ".")
		if len(parts) < 3 {
			continue
		}
		symbol := parts[2]

		candles, err := parseWSCandles(msg, symbol, market)
		if err != nil {
			log.Printf("bybit ws: parse error: %v", err)
			continue
		}
		for _, candle := range candles {
			handler(candle)
		}
	}
}
