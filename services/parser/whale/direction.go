// services/parser/whale/direction.go
package whale

import (
	"context"
	"strings"
	"time"
)

// stablecoin symbols to exclude from accumulation scoring
var stablecoins = map[string]bool{
	"USDT": true, "USDC": true, "DAI": true, "BUSD": true,
	"TUSD": true, "FDUSD": true, "PYUSD": true,
}

// knownBybitETHWallets lists known Bybit ETH deposit hot wallets.
var knownBybitETHWallets = map[string]bool{
	strings.ToLower("0xf89d7b9c864f589bbF53a82105107622B35EaA40"): true,
	strings.ToLower("0x2b5c7025998f88550Ef2fece8bf87935f542C190"): true,
	strings.ToLower("0x15F5e2B0cE6d41AC2C99B4A2Da22a6EB6a21b84"):  true,
}

// knownBybitTronWallets lists known Bybit Tron deposit hot wallets.
var knownBybitTronWallets = map[string]bool{
	"TJDENsfBJs4RFETt1X1W8wMDc8M5XnJhd":  true,
	"TWd4WrZ9wn84f5x1hZhL4DHvk738ns5jwH": true,
}

// ScoreResult holds the direction analysis output.
type ScoreResult struct {
	Score     float64 // -1..+1: positive = accumulation, negative = distribution
	Direction string  // "buy" | "sell" | "neutral"
	TopToken  string  // highest-activity non-stable token symbol
}

// Analyzer holds API clients for ETH and Tron chains.
type Analyzer struct {
	eth  *EthClient
	tron *TronClient
}

func newAnalyzer(eth *EthClient, tron *TronClient) *Analyzer {
	return &Analyzer{eth: eth, tron: tron}
}

// Analyze computes the 30-day accumulation/distribution score for a given address.
func (a *Analyzer) Analyze(ctx context.Context, address, chain string) ScoreResult {
	since30d := time.Now().AddDate(0, 0, -30)
	tokenFlow := make(map[string]float64) // symbol → net flow (positive = received)

	switch chain {
	case "eth":
		sinceTS := since30d.Unix()
		txs, err := a.eth.TokenTxSince(ctx, address, sinceTS)
		if err != nil {
			return ScoreResult{Direction: "neutral"}
		}
		addrLower := strings.ToLower(address)
		for _, tx := range txs {
			sym := strings.ToUpper(tx.TokenSymbol)
			if stablecoins[sym] {
				continue
			}
			amount := rawToAmount(tx.Value, tx.TokenDecimals)
			if strings.ToLower(tx.To) == addrLower {
				tokenFlow[sym] += amount
			} else {
				tokenFlow[sym] -= amount
			}
		}

	case "tron":
		sinceMS := since30d.UnixMilli()
		txs, err := a.tron.TokenTxSince(ctx, address, sinceMS)
		if err != nil {
			return ScoreResult{Direction: "neutral"}
		}
		for _, tx := range txs {
			sym := strings.ToUpper(tx.TokenSymbol)
			if stablecoins[sym] {
				continue
			}
			amount := rawToAmount(tx.Value, tx.Decimals)
			if tx.To == address {
				tokenFlow[sym] += amount
			} else {
				tokenFlow[sym] -= amount
			}
		}
	}

	var topToken string
	var maxAbs float64
	var totalReceived, totalSent float64
	for sym, net := range tokenFlow {
		abs := net
		if abs < 0 {
			abs = -abs
		}
		if abs > maxAbs {
			maxAbs = abs
			topToken = sym
		}
		if net > 0 {
			totalReceived += net
		} else {
			totalSent += -net
		}
	}

	total := totalReceived + totalSent
	if total == 0 {
		return ScoreResult{Direction: "neutral", TopToken: topToken}
	}

	score := (totalReceived - totalSent) / total
	direction := "neutral"
	switch {
	case score > 0.3:
		direction = "buy"
	case score < -0.3:
		direction = "sell"
	}

	return ScoreResult{Score: score, Direction: direction, TopToken: topToken}
}

// IsExchangeDeposit returns true if `to` is a known Bybit hot wallet for the chain.
func IsExchangeDeposit(to, chain string) bool {
	switch chain {
	case "eth":
		return knownBybitETHWallets[strings.ToLower(to)]
	case "tron":
		return knownBybitTronWallets[to]
	}
	return false
}

// tokenToSymbol maps a token symbol to a Bybit futures symbol (e.g. "BTC" → "BTCUSDT").
func tokenToSymbol(token string) string {
	token = strings.ToUpper(token)
	if token == "" || stablecoins[token] {
		return ""
	}
	return token + "USDT"
}
