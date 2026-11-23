// pkg/exchange/binance/rest.go
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"sis/pkg/models"
	"sis/pkg/proxy"
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

	return fetchKlines(ctx, url, symbol, market, tf, time.Time{})
}

// FetchRecentClosedFuturesCandles returns the latest fully closed USD-M candles.
func FetchRecentClosedFuturesCandles(ctx context.Context, symbol string, tf models.Timeframe, limit int) ([]models.Candle, error) {
	query := url.Values{"symbol": {symbol}, "interval": {string(tf)}, "limit": {strconv.Itoa(limit + 1)}}
	candles, err := fetchKlines(ctx, futuresBaseURL+"/fapi/v1/klines?"+query.Encode(), symbol, models.MarketFutures, tf, time.Now())
	if len(candles) > limit {
		candles = candles[len(candles)-limit:]
	}
	return candles, err
}

func fetchKlines(ctx context.Context, url, symbol string, market models.Market, tf models.Timeframe, closedBefore time.Time) ([]models.Candle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("binance rest: new request: %w", err)
	}
	resp, err := proxy.HTTPClient().Do(req)
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
		if !closedBefore.IsZero() {
			var closeTime int64
			if err := json.Unmarshal(row[6], &closeTime); err != nil {
				return nil, fmt.Errorf("binance rest: decode close time: %w", err)
			}
			if closeTime >= closedBefore.UnixMilli() {
				continue
			}
		}
		c, err := parseRESTCandle(row, symbol, market, tf)
		if err != nil {
			return nil, err
		}
		candles = append(candles, c)
	}
	return candles, nil
}
