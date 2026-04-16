// pkg/exchange/bybit/parse.go
package bybit

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"sis/pkg/models"
)

type wsKlineMsg struct {
	Topic string `json:"topic"`
	Data  []struct {
		Start    int64  `json:"start"`
		Interval string `json:"interval"`
		Open     string `json:"open"`
		High     string `json:"high"`
		Low      string `json:"low"`
		Close    string `json:"close"`
		Volume   string `json:"volume"`
		Confirm  bool   `json:"confirm"`
	} `json:"data"`
}

type restKlineResult struct {
	Result struct {
		Symbol string     `json:"symbol"`
		List   [][]string `json:"list"`
	} `json:"result"`
}

var bybitIntervalToTF = map[string]models.Timeframe{
	"1": models.TF1m, "5": models.TF5m, "15": models.TF15m,
	"60": models.TF1h, "240": models.TF4h, "D": models.TF1d,
}

var tfToBybitInterval = map[models.Timeframe]string{
	models.TF1m: "1", models.TF5m: "5", models.TF15m: "15",
	models.TF1h: "60", models.TF4h: "240", models.TF1d: "D",
}

func parseWSCandles(data []byte, symbol string, market models.Market) ([]models.Candle, error) {
	var msg wsKlineMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("bybit parse ws: %w", err)
	}

	candles := make([]models.Candle, 0, len(msg.Data))
	for _, k := range msg.Data {
		tf, ok := bybitIntervalToTF[k.Interval]
		if !ok {
			tf = models.Timeframe(k.Interval)
		}
		p := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		candles = append(candles, models.Candle{
			Exchange:  models.ExchangeBybit,
			Symbol:    symbol,
			Market:    market,
			Timeframe: tf,
			OpenTime:  time.UnixMilli(k.Start).UTC(),
			Open:      p(k.Open),
			High:      p(k.High),
			Low:       p(k.Low),
			Close:     p(k.Close),
			Volume:    p(k.Volume),
			Closed:    k.Confirm,
		})
	}
	return candles, nil
}

func parseRESTCandles(data []byte, market models.Market, tf models.Timeframe) ([]models.Candle, error) {
	var result restKlineResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("bybit parse rest: %w", err)
	}
	rows := result.Result.List
	candles := make([]models.Candle, 0, len(rows))
	p := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		ts, _ := strconv.ParseInt(row[0], 10, 64)
		candles = append(candles, models.Candle{
			Exchange:  models.ExchangeBybit,
			Symbol:    result.Result.Symbol,
			Market:    market,
			Timeframe: tf,
			OpenTime:  time.UnixMilli(ts).UTC(),
			Open:      p(row[1]),
			High:      p(row[2]),
			Low:       p(row[3]),
			Close:     p(row[4]),
			Volume:    p(row[5]),
			Closed:    true,
		})
	}
	// Reverse to ascending order (Bybit returns newest-first)
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}
	return candles, nil
}
