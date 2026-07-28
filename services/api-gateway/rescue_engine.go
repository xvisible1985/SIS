package main

import (
	"math"
	"time"
)

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

// rescueTriggersMet returns true when ALL active (non-nil) triggers are
// satisfied simultaneously (AND logic). signalMet is pre-computed by the caller
// (requires signal.Engine, so it's injected rather than evaluated here to keep
// this function pure/testable).
//
// When no triggers are configured, returns true — the outer gate
// (RescuePartialCloseEnabled check) is the caller's responsibility.
func rescueTriggersMet(cfg botCfgJSON, currentPrice, mainEntryPrice float64, mainDir string, accumulatedPnl float64, signalMet bool) bool {
	if cfg.RescueTriggerPriceMovePct != nil {
		if rescuePriceMoveTowardMainPct(mainEntryPrice, currentPrice, mainDir) < *cfg.RescueTriggerPriceMovePct {
			return false
		}
	}
	if cfg.RescueTriggerPriceLevel != nil {
		if !rescuePriceLevelReached(*cfg.RescueTriggerPriceLevel, currentPrice, mainDir) {
			return false
		}
	}
	if cfg.RescueTriggerAccumulatedMinUsdt != nil {
		if accumulatedPnl < *cfg.RescueTriggerAccumulatedMinUsdt {
			return false
		}
	}
	if cfg.RescueTriggerSignal != nil && !signalMet {
		return false
	}
	return true
}

// rescueCooldownElapsed returns true when enough time has passed since the last
// partial close to allow another one. lastPartialCloseAt nil means "never closed"
// → always allowed. minIntervalSec=0 means no cooldown.
func rescueCooldownElapsed(lastPartialCloseAt *time.Time, minIntervalSec int) bool {
	if lastPartialCloseAt == nil {
		return true
	}
	if minIntervalSec <= 0 {
		return true
	}
	return time.Since(*lastPartialCloseAt) >= time.Duration(minIntervalSec)*time.Second
}

// rescueCloseReq is returned by rescuePartialCloseRequest when all conditions
// are met. It carries everything needed to place the reduce-only market order.
type rescueCloseReq struct {
	Symbol string
	Side   string  // "buy" (closing short) or "sell" (closing long)
	Qty    float64 // coin quantity, already rounded to qtyStep
}

// rescuePartialCloseRequest evaluates all rescue conditions and returns a close
// request when every gate is open, or nil when any gate blocks. The caller is
// responsible for checking cfg.RescuePartialCloseEnabled before calling.
func rescuePartialCloseRequest(
	cfg botCfgJSON,
	symbol, mainDir string,
	currentPrice, mainEntryPrice float64,
	accumulatedPnl, mainReducedUsdt float64,
	qtyStep, minQty float64,
	lastPartialCloseAt *time.Time,
	signalMet bool,
) *rescueCloseReq {
	if !rescueCooldownElapsed(lastPartialCloseAt, cfg.RescueMinIntervalSec) {
		return nil
	}
	if !rescueTriggersMet(cfg, currentPrice, mainEntryPrice, mainDir, accumulatedPnl, signalMet) {
		return nil
	}
	qty := rescueCalcPartialCloseQty(accumulatedPnl, mainReducedUsdt, currentPrice, qtyStep, minQty)
	if qty <= 0 {
		return nil
	}
	// closing side is opposite to main position direction
	closeSide := "sell"
	if mainDir == "sell" {
		closeSide = "buy"
	}
	return &rescueCloseReq{Symbol: symbol, Side: closeSide, Qty: qty}
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
