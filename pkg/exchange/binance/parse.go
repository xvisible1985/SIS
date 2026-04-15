// pkg/exchange/binance/parse.go
package binance

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"sis/pkg/models"
)

type wsKlineMsg struct {
	Symbol string `json:"s"`
	Kline  struct {
		OpenTime int64  `json:"t"`
		Interval string `json:"i"`
		Open     string `json:"o"`
		High     string `json:"h"`
		Low      string `json:"l"`
		Close    string `json:"c"`
		Volume   string `json:"v"`
		IsClosed bool   `json:"x"`
	} `json:"k"`
}

type restKlineRow [12]json.RawMessage

func parseWSCandle(data []byte, market models.Market) (models.Candle, error) {
	var msg wsKlineMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return models.Candle{}, fmt.Errorf("binance parse ws: %w", err)
	}
	k := msg.Kline
	open, _ := strconv.ParseFloat(k.Open, 64)
	high, _ := strconv.ParseFloat(k.High, 64)
	low, _ := strconv.ParseFloat(k.Low, 64)
	close_, _ := strconv.ParseFloat(k.Close, 64)
	vol, _ := strconv.ParseFloat(k.Volume, 64)

	return models.Candle{
		Exchange:  models.ExchangeBinance,
		Symbol:    msg.Symbol,
		Market:    market,
		Timeframe: models.Timeframe(k.Interval),
		OpenTime:  time.UnixMilli(k.OpenTime).UTC(),
		Open:      open,
		High:      high,
		Low:       low,
		Close:     close_,
		Volume:    vol,
		Closed:    k.IsClosed,
	}, nil
}

func parseRESTCandle(row restKlineRow, symbol string, market models.Market, tf models.Timeframe) (models.Candle, error) {
	var openTimeMS int64
	if err := json.Unmarshal(row[0], &openTimeMS); err != nil {
		return models.Candle{}, fmt.Errorf("binance parse rest open_time: %w", err)
	}
	parseStr := func(r json.RawMessage) float64 {
		var s string
		_ = json.Unmarshal(r, &s)
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	return models.Candle{
		Exchange:  models.ExchangeBinance,
		Symbol:    symbol,
		Market:    market,
		Timeframe: tf,
		OpenTime:  time.UnixMilli(openTimeMS).UTC(),
		Open:      parseStr(row[1]),
		High:      parseStr(row[2]),
		Low:       parseStr(row[3]),
		Close:     parseStr(row[4]),
		Volume:    parseStr(row[5]),
		Closed:    true,
	}, nil
}
