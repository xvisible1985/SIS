package main

import "math"

// rescueSignalTrigger describes a signal-based condition for the RescueBot
// partial-close trigger. Name is the signal name (e.g. "st-flip"); Params are
// passed to signal.ComputeStateForce as signal.Config.Params.
type rescueSignalTrigger struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// rescuePriceMoveTowardMainPct returns how many percent the price has moved
// against the main position (toward the loss side) since its entry. Returns 0
// when the move is in the profitable direction (not a rescue-trigger condition).
//
// mainDir: "buy" = long position, "sell" = short position.
func rescuePriceMoveTowardMainPct(entryAt, currentAt float64, mainDir string) float64 {
	if entryAt <= 0 {
		return 0
	}
	var movePct float64
	switch mainDir {
	case "buy": // long — adverse move is price falling
		movePct = (entryAt - currentAt) / entryAt * 100
	case "sell": // short — adverse move is price rising
		movePct = (currentAt - entryAt) / entryAt * 100
	}
	if movePct < 0 {
		return 0
	}
	return movePct
}

// rescuePriceLevelReached returns true when the current price has reached or
// passed the configured rescue level in the direction adverse to the main position.
//
// For a long (buy) main: the rescue level is a downside price, reached when
// currentAt <= level. For a short (sell) main: it's an upside price, reached
// when currentAt >= level.
func rescuePriceLevelReached(level, currentAt float64, mainDir string) bool {
	switch mainDir {
	case "buy":
		return currentAt <= level
	case "sell":
		return currentAt >= level
	}
	return false
}

// rescueCalcPartialCloseQty returns the coin quantity to partially close on the
// main position, funded by the hedge's accumulated PnL minus what was already
// reduced. Returns 0 when the available USDT is insufficient (below minQty value
// at current price) or when price is zero.
//
// The result is floored to qtyStep precision (same rounding used by FormatQty in
// pkg/trader/instruments.go) and checked against minQty before returning.
func rescueCalcPartialCloseQty(accumulatedPnl, mainReducedUsdt, price, qtyStep, minQty float64) float64 {
	if price <= 0 || qtyStep <= 0 {
		return 0
	}
	availableUsdt := accumulatedPnl - mainReducedUsdt
	if availableUsdt <= 0 {
		return 0
	}
	rawQty := availableUsdt / price
	// Floor to qtyStep (same as trader.FormatQty semantics).
	qty := math.Floor(rawQty/qtyStep) * qtyStep
	if qty < minQty {
		return 0
	}
	return qty
}
