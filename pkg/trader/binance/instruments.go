package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"sis/pkg/trader"
)

// exchangeInfoCache holds every symbol's precision rules from a single
// /fapi/v1/exchangeInfo fetch — Binance returns every symbol in one response (unlike
// Bybit's instruments-info, which takes a symbol param and is naturally cached
// per-symbol), so there's nothing to gain from a per-symbol cache key here; one fetch
// serves every symbol any account asks about.
var (
	exchangeInfoMu    sync.RWMutex
	exchangeInfoCache map[string]trader.InstrumentInfo
	exchangeInfoAt    time.Time
)

const exchangeInfoTTL = 5 * time.Minute

type binanceExchangeInfoResponse struct {
	Symbols []struct {
		Symbol  string `json:"symbol"`
		Filters []struct {
			FilterType  string `json:"filterType"`
			TickSize    string `json:"tickSize"`
			StepSize    string `json:"stepSize"`
			MinQty      string `json:"minQty"`
			Notional    string `json:"notional"`    // USDⓈ-M futures' MIN_NOTIONAL field name
			MinNotional string `json:"minNotional"` // Spot API's equivalent field name, handled defensively
		} `json:"filters"`
	} `json:"symbols"`
}

// loadExchangeInfo fetches and parses the full symbol list, or returns the cached copy
// if it's under 5 minutes old. Public endpoint — creds is only used for its
// WhitelistedIPs-driven proxy selection (doPublicGET's own contract), same as every
// other public call in this package.
func loadExchangeInfo(ctx context.Context, creds trader.Credentials) (map[string]trader.InstrumentInfo, error) {
	exchangeInfoMu.RLock()
	if exchangeInfoCache != nil && time.Since(exchangeInfoAt) < exchangeInfoTTL {
		m := exchangeInfoCache
		exchangeInfoMu.RUnlock()
		return m, nil
	}
	exchangeInfoMu.RUnlock()

	data, err := doPublicGET(ctx, creds, "/fapi/v1/exchangeInfo", nil)
	if err != nil {
		return nil, err
	}
	var resp binanceExchangeInfoResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if len(resp.Symbols) == 0 {
		return nil, fmt.Errorf("binance: exchangeInfo returned no symbols")
	}

	m := make(map[string]trader.InstrumentInfo, len(resp.Symbols))
	for _, s := range resp.Symbols {
		var info trader.InstrumentInfo
		for _, f := range s.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				info.TickSize = parseFloatOrZero(f.TickSize)
			case "LOT_SIZE":
				info.QtyStep = parseFloatOrZero(f.StepSize)
				info.MinQty = parseFloatOrZero(f.MinQty)
			case "MIN_NOTIONAL", "NOTIONAL":
				n := f.Notional
				if n == "" {
					n = f.MinNotional
				}
				info.MinNotionalValue = parseFloatOrZero(n)
			}
		}
		m[s.Symbol] = info
	}

	exchangeInfoMu.Lock()
	exchangeInfoCache = m
	exchangeInfoAt = time.Now()
	exchangeInfoMu.Unlock()
	return m, nil
}

func parseFloatOrZero(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f) //nolint:errcheck
	return f
}

// GetInstrumentInfo returns the exchange-enforced tick/step/minimum-order rules for
// symbol. Every qty/price pkg/strategy sends to PlaceOrder is rounded against these —
// Binance strictly rejects (code -1111 "Precision is over the maximum defined for this
// asset") any value with more decimal places than the symbol's filters allow, and this
// was previously never fetched at all for Binance accounts (the whole call chain was
// Bybit-only — see trader.Exchange's GetInstrumentInfo doc comment).
func (e *BinanceExchange) GetInstrumentInfo(ctx context.Context, category, symbol string) (trader.InstrumentInfo, error) {
	all, err := loadExchangeInfo(ctx, e.creds)
	if err != nil {
		return trader.InstrumentInfo{}, err
	}
	info, ok := all[symbol]
	if !ok {
		return trader.InstrumentInfo{}, fmt.Errorf("binance: no exchangeInfo entry for symbol %s", symbol)
	}
	return info, nil
}

// GetPublicInstrumentInfo is the Binance counterpart of trader.GetPublicInstrumentInfo
// (Bybit) — same PubInstrumentInfo shape, so callers (services/api-gateway's
// /instrument-info endpoint, bot-creation sizing, the rescue engine) can pick either by
// exchange name without caring which one they got. No account credentials needed — both
// exchangeInfo and the mark-price lookup are public endpoints.
func GetPublicInstrumentInfo(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
	all, err := loadExchangeInfo(ctx, trader.Credentials{})
	if err != nil {
		return trader.PubInstrumentInfo{}, err
	}
	item, ok := all[symbol]
	if !ok {
		return trader.PubInstrumentInfo{}, fmt.Errorf("binance: no exchangeInfo entry for symbol %s", symbol)
	}
	info := trader.PubInstrumentInfo{
		TickSize:         item.TickSize,
		QtyStep:          item.QtyStep,
		MinQty:           item.MinQty,
		MinNotionalValue: item.MinNotionalValue,
		// Binance USDⓈ-M leverage is per-notional-bracket, not a single scalar the way
		// Bybit's leverageFilter.maxLeverage is — left at 0 (frontend already treats
		// max_leverage==0 as "no additional cap beyond what the account itself allows").
	}

	pub := (&BinanceExchange{}).GetMarkPrice
	markPrice, _ := pub(ctx, category, symbol)
	minFromQty := info.MinQty * markPrice
	if minFromQty > info.MinNotionalValue {
		info.MinOrderUSDT = math.Ceil(minFromQty*10) / 10
	} else {
		info.MinOrderUSDT = math.Ceil(info.MinNotionalValue*10) / 10
	}
	return info, nil
}
