package binance

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"sis/pkg/trader"
)

// BinanceExchange implements trader.Exchange against Binance USDⓈ-M Futures.
// category parameters accepted by interface methods are always effectively "linear"
// here — Binance separates USDⓈ-M/COIN-M by base URL/endpoint family rather than a
// query param the way Bybit does, and this client only ever targets USDⓈ-M (see the
// design spec's non-goals) — the param exists purely for interface compatibility.
type BinanceExchange struct {
	creds trader.Credentials
}

// NewBinanceExchange builds a BinanceExchange for one account's credentials.
func NewBinanceExchange(creds trader.Credentials) *BinanceExchange {
	return &BinanceExchange{creds: creds}
}

func (e *BinanceExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	data, err := doPublicGET(ctx, e.creds, "/fapi/v1/premiumIndex", url.Values{"symbol": {symbol}})
	if err != nil {
		return 0, err
	}
	var r struct {
		MarkPrice string `json:"markPrice"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(r.MarkPrice, 64)
}

// binancePositionRow is one row of GET /fapi/v3/positionRisk.
type binancePositionRow struct {
	Symbol           string `json:"symbol"`
	PositionSide     string `json:"positionSide"` // "BOTH", "LONG", "SHORT"
	PositionAmt      string `json:"positionAmt"`  // signed: negative = short
	EntryPrice       string `json:"entryPrice"`
	MarkPrice        string `json:"markPrice"`
	UnRealizedProfit string `json:"unRealizedProfit"`
	LiquidationPrice string `json:"liquidationPrice"`
	Leverage         string `json:"leverage"`
}

func (e *BinanceExchange) FetchPositions(ctx context.Context) ([]trader.Position, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v3/positionRisk", nil)
	if err != nil {
		return nil, err
	}
	var rows []binancePositionRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	var out []trader.Position
	for _, r := range rows {
		amt, err := strconv.ParseFloat(r.PositionAmt, 64)
		if err != nil || amt == 0 {
			continue // Binance lists every symbol; only nonzero positionAmt is a real open position
		}
		side := "Buy"
		positionIdx := 1
		// Strip the sign from the original string (rather than reformatting the
		// parsed float) so precision/trailing zeros from the exchange are preserved
		// exactly — e.g. "-0.25000000" becomes "0.25000000", not "0.25".
		size := strings.TrimPrefix(r.PositionAmt, "-")
		if amt < 0 {
			side = "Sell"
		}
		// PositionSide=="BOTH" (one-way mode) always maps to slot 0, regardless of
		// sign — a one-way-mode short still reports positionSide:"BOTH" with a
		// negative positionAmt, so this must not be an `else if` chained after the
		// sign check above (that would wrongly give it the hedge-mode short slot 2).
		if r.PositionSide == "BOTH" {
			positionIdx = 0
		} else if amt < 0 {
			positionIdx = 2
		}
		out = append(out, trader.Position{
			Symbol:        r.Symbol,
			Side:          side,
			Size:          size,
			EntryPrice:    r.EntryPrice,
			MarkPrice:     r.MarkPrice,
			LiqPrice:      r.LiquidationPrice,
			UnrealisedPnl: r.UnRealizedProfit,
			Leverage:      r.Leverage,
			PositionIdx:   positionIdx,
			Category:      "linear",
		})
	}
	return out, nil
}

func (e *BinanceExchange) GetWalletBalance(ctx context.Context) (equity, available float64, err error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v2/account", nil)
	if err != nil {
		return 0, 0, err
	}
	var r struct {
		TotalMarginBalance string `json:"totalMarginBalance"`
		Assets             []struct {
			Asset            string `json:"asset"`
			AvailableBalance string `json:"availableBalance"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, 0, err
	}
	equity, err = strconv.ParseFloat(r.TotalMarginBalance, 64)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range r.Assets {
		if a.Asset == "USDT" {
			available, _ = strconv.ParseFloat(a.AvailableBalance, 64)
			break
		}
	}
	return equity, available, nil
}
