// pkg/exchange/binance/rest.go
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sis/pkg/models"
)

const (
	spotBaseURL    = "https://api.binance.com"
	futuresBaseURL = "https://fapi.binance.com"
)

func baseURL(market models.Market) string {
	if market == models.MarketFutures {
		return futuresBaseURL
	}
	return spotBaseURL
}

// FetchCandles fetches up to 1000 historical candles via REST.
func FetchCandles(ctx context.Context, symbol string, market models.Market, tf models.Timeframe, from, to time.Time) ([]models.Candle, error) {
	var url string
	if market == models.MarketFutures {
		url = fmt.Sprintf("%s/fapi/v1/klines?symbol=%s&interval=%s&startTime=%d&endTime=%d&limit=1000",
			futuresBaseURL, symbol, string(tf), from.UnixMilli(), to.UnixMilli())
	} else {
		url = fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=%s&startTime=%d&endTime=%d&limit=1000",
			spotBaseURL, symbol, string(tf), from.UnixMilli(), to.UnixMilli())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("binance rest: new request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance rest: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance rest: status %d", resp.StatusCode)
	}

	var rows []restKlineRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("binance rest: decode: %w", err)
	}

	candles := make([]models.Candle, 0, len(rows))
	for _, row := range rows {
		c, err := parseRESTCandle(row, symbol, market, tf)
		if err != nil {
			return nil, err
		}
		candles = append(candles, c)
	}
	return candles, nil
}
