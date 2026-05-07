package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// InstrumentInfo holds the precision rules for a trading pair.
type InstrumentInfo struct {
	TickSize float64 // price tick size
	QtyStep  float64 // lot size step
	MinQty   float64 // minimum order qty
}

type instrEntry struct {
	info InstrumentInfo
	at   time.Time
}

var (
	instrMu    sync.RWMutex
	instrCache = map[string]instrEntry{}
)

// GetInstrumentInfo returns the tick/step sizes for a symbol, with a 1-hour cache.
func GetInstrumentInfo(ctx context.Context, creds Credentials, category, symbol string) (InstrumentInfo, error) {
	key := category + "/" + symbol
	instrMu.RLock()
	if e, ok := instrCache[key]; ok && time.Since(e.at) < time.Hour {
		instrMu.RUnlock()
		return e.info, nil
	}
	instrMu.RUnlock()

	q := "category=" + category + "&symbol=" + symbol
	data, err := doSignedGET(ctx, creds, "/v5/market/instruments-info", q)
	if err != nil {
		return InstrumentInfo{}, err
	}
	var resp struct {
		Result struct {
			List []struct {
				LotSizeFilter struct {
					QtyStep     string `json:"qtyStep"`
					MinOrderQty string `json:"minOrderQty"`
				} `json:"lotSizeFilter"`
				PriceFilter struct {
					TickSize string `json:"tickSize"`
				} `json:"priceFilter"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Result.List) == 0 {
		return InstrumentInfo{}, fmt.Errorf("instruments-info: no data for %s/%s", category, symbol)
	}
	item := resp.Result.List[0]
	tick, _ := strconv.ParseFloat(item.PriceFilter.TickSize, 64)
	step, _ := strconv.ParseFloat(item.LotSizeFilter.QtyStep, 64)
	minQty, _ := strconv.ParseFloat(item.LotSizeFilter.MinOrderQty, 64)
	info := InstrumentInfo{TickSize: tick, QtyStep: step, MinQty: minQty}

	instrMu.Lock()
	instrCache[key] = instrEntry{info: info, at: time.Now()}
	instrMu.Unlock()
	return info, nil
}

// stepDecimals returns the number of decimal places implied by a step value.
func stepDecimals(step float64) int {
	s := strconv.FormatFloat(step, 'f', -1, 64)
	dot := strings.Index(s, ".")
	if dot < 0 {
		return 0
	}
	return len(strings.TrimRight(s[dot+1:], "0"))
}

// FormatPrice rounds price to the nearest tick and returns it as a string.
func FormatPrice(price, tickSize float64) string {
	if tickSize <= 0 {
		return strconv.FormatFloat(price, 'f', 4, 64)
	}
	rounded := math.Round(price/tickSize) * tickSize
	return strconv.FormatFloat(rounded, 'f', stepDecimals(tickSize), 64)
}

// FormatQty rounds qty down to the nearest step and returns it as a string.
// Floors (not rounds) to avoid accidentally exceeding account balance.
func FormatQty(qty, qtyStep float64) string {
	if qtyStep <= 0 {
		return strconv.FormatFloat(qty, 'f', 6, 64)
	}
	rounded := math.Floor(qty/qtyStep) * qtyStep
	return strconv.FormatFloat(rounded, 'f', stepDecimals(qtyStep), 64)
}
