// pkg/exchange/bybit/rest.go
package bybit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"sis/pkg/models"
	"sis/pkg/proxy"
)

const bybitBaseURL = "https://api.bybit.com"

func FetchCandles(ctx context.Context, symbol string, market models.Market, tf models.Timeframe, from, to time.Time) ([]models.Candle, error) {
	category := "spot"
	if market == models.MarketFutures {
		category = "linear"
	}
	interval := tfToBybitInterval[tf]
	if interval == "" {
		return nil, fmt.Errorf("bybit: unsupported timeframe %s", tf)
	}

	url := fmt.Sprintf("%s/v5/market/kline?category=%s&symbol=%s&interval=%s&start=%d&end=%d&limit=1000",
		bybitBaseURL, category, symbol, interval, from.UnixMilli(), to.UnixMilli())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bybit rest: new request: %w", err)
	}
	resp, err := proxy.HTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit rest: do: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bybit rest: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bybit rest: status %d: %s", resp.StatusCode, body)
	}
	return parseRESTCandles(body, market, tf)
}
