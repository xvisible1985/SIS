package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sis/pkg/proxy"
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

// FormatQty rounds qty down to the nearest step, then enforces minQty.
// Floors to avoid accidentally exceeding account balance, but snaps up to minQty
// if the result would be below the exchange minimum order size.
func FormatQty(qty, qtyStep, minQty float64) string {
	if qtyStep <= 0 {
		if minQty > 0 && qty < minQty {
			qty = minQty
		}
		return strconv.FormatFloat(qty, 'f', 6, 64)
	}
	rounded := math.Floor(qty/qtyStep) * qtyStep
	if minQty > 0 && rounded < minQty {
		// Snap up to the smallest multiple of qtyStep that satisfies minQty.
		rounded = math.Ceil(minQty/qtyStep) * qtyStep
	}
	if rounded <= 0 {
		return "0"
	}
	return strconv.FormatFloat(rounded, 'f', stepDecimals(qtyStep), 64)
}

// ── Public instrument constraints ────────────────────────────────────────────

// PubInstrumentInfo holds exchange-enforced limits exposed to the frontend.
type PubInstrumentInfo struct {
	TickSize         float64 `json:"tick_size"`
	QtyStep          float64 `json:"qty_step"`
	MinQty           float64 `json:"min_qty"`
	MaxLeverage      float64 `json:"max_leverage"`
	MinNotionalValue float64 `json:"min_notional_value"`
	MinOrderUSDT     float64 `json:"min_order_usdt"`
}

var (
	pubInstrMu    sync.RWMutex
	pubInstrCache = map[string]struct {
		info PubInstrumentInfo
		at   time.Time
	}{}
)

// GetPublicInstrumentInfo fetches leverage and lot-size limits from Bybit.
// The endpoint is public — no API credentials required. Cached 5 minutes.
func GetPublicInstrumentInfo(ctx context.Context, category, symbol string) (PubInstrumentInfo, error) {
	key := category + "/" + symbol
	pubInstrMu.RLock()
	if e, ok := pubInstrCache[key]; ok && time.Since(e.at) < 5*time.Minute {
		pubInstrMu.RUnlock()
		return e.info, nil
	}
	pubInstrMu.RUnlock()

	url := bybitBase + "/v5/market/instruments-info?category=" + category + "&symbol=" + symbol
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return PubInstrumentInfo{}, err
	}
	resp, err := proxy.HTTPClient().Do(req)
	if err != nil {
		return PubInstrumentInfo{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return PubInstrumentInfo{}, err
	}

	var result struct {
		RetCode int `json:"retCode"`
		Result  struct {
			List []struct {
				LeverageFilter struct {
					MaxLeverage string `json:"maxLeverage"`
				} `json:"leverageFilter"`
				LotSizeFilter struct {
					QtyStep          string `json:"qtyStep"`
					MinOrderQty      string `json:"minOrderQty"`
					MinNotionalValue string `json:"minNotionalValue"`
				} `json:"lotSizeFilter"`
				PriceFilter struct {
					TickSize string `json:"tickSize"`
				} `json:"priceFilter"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil || len(result.Result.List) == 0 {
		return PubInstrumentInfo{}, fmt.Errorf("instruments-info: no data for %s/%s", category, symbol)
	}

	item := result.Result.List[0]
	info := PubInstrumentInfo{
		TickSize:         parseF64(item.PriceFilter.TickSize),
		QtyStep:          parseF64(item.LotSizeFilter.QtyStep),
		MinQty:           parseF64(item.LotSizeFilter.MinOrderQty),
		MaxLeverage:      parseF64(item.LeverageFilter.MaxLeverage),
		MinNotionalValue: parseF64(item.LotSizeFilter.MinNotionalValue),
	}

	// Fetch mark price to compute real minimum order value in USDT
	markPrice, _ := fetchMarkPricePublic(ctx, category, symbol)
	minFromQty := info.MinQty * markPrice
	if minFromQty > info.MinNotionalValue {
		info.MinOrderUSDT = math.Ceil(minFromQty*10) / 10
	} else {
		info.MinOrderUSDT = math.Ceil(info.MinNotionalValue*10) / 10
	}

	pubInstrMu.Lock()
	pubInstrCache[key] = struct {
		info PubInstrumentInfo
		at   time.Time
	}{info, time.Now()}
	pubInstrMu.Unlock()

	return info, nil
}

func fetchMarkPricePublic(ctx context.Context, category, symbol string) (float64, error) {
	url := bybitBase + "/v5/market/tickers?category=" + category + "&symbol=" + symbol
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := proxy.HTTPClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	var result struct {
		Result struct {
			List []struct {
				MarkPrice string `json:"markPrice"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, err
	}
	if len(result.Result.List) == 0 {
		return 0, fmt.Errorf("no ticker for %s/%s", category, symbol)
	}
	return strconv.ParseFloat(result.Result.List[0].MarkPrice, 64)
}

func parseF64(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// ── All linear symbols ────────────────────────────────────────────────────────

var (
	allLinSymMu    sync.RWMutex
	allLinSymCache []string
	allLinSymAt    time.Time
)

// FetchAllLinearSymbols returns all active linear USDT perpetual symbols from Bybit.
// Results are cached for 5 minutes.
func FetchAllLinearSymbols(ctx context.Context) ([]string, error) {
	allLinSymMu.RLock()
	if allLinSymCache != nil && time.Since(allLinSymAt) < 5*time.Minute {
		res := make([]string, len(allLinSymCache))
		copy(res, allLinSymCache)
		allLinSymMu.RUnlock()
		return res, nil
	}
	allLinSymMu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		bybitBase+"/v5/market/instruments-info?category=linear&status=Trading&limit=1000", nil)
	if err != nil {
		return nil, err
	}
	resp, err := proxy.HTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Result struct {
			List []struct {
				Symbol       string `json:"symbol"`
				SettleCoin   string `json:"settleCoin"`
				ContractType string `json:"contractType"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var symbols []string
	for _, item := range result.Result.List {
		// Only perpetuals — dated futures (LinearFutures) don't support hedge mode
		// or position mode switching and cause retCode=10001 errors.
		if item.SettleCoin == "USDT" && item.ContractType == "LinearPerpetual" {
			symbols = append(symbols, item.Symbol)
		}
	}

	allLinSymMu.Lock()
	allLinSymCache = symbols
	allLinSymAt = time.Now()
	allLinSymMu.Unlock()

	return symbols, nil
}
