package main

import "math"

// rescueSignalTrigger describes a signal-based condition for the RescueBot
// partial-close trigger. Name is the signal name (e.g. "st-flip"); Params are
// passed to signal.ComputeStateForce as signal.Config.Params.
type rescueSignalTrigger struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params,omitempty"`
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
