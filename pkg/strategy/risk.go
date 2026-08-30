package strategy

import (
	"context"
	"log"
	"strconv"
	"time"

	"sis/pkg/trader"
)

const riskMonitorInterval = 20 * time.Second

// OnRiskEvent, if set, is called once per pause-state TRANSITION (entering or lifting —
// never once per tick, matching OnAccumulate's pattern in trade_recorder.go), so
// services/api-gateway can push a single Telegram alert per episode instead of one every
// riskMonitorInterval while the account stays paused. Wired at startup (see
// services/api-gateway/server.go); nil in tests and in any build that doesn't set it.
//
// Called from checkRiskOnce OUTSIDE ar.riskMu (see OnAccumulate's precedent for why: this
// may do DB/network I/O in the consumer, e.g. looking up a Telegram chat_id — holding a
// lock across that would block every other goroutine reading ar.risk for no reason).
// checkRiskOnce runs inside runRiskMonitorLoop → safeLoop, which already recovers panics
// (restarting just this account's risk loop) — lower stakes than OnAccumulate's bare
// unrecovered goroutines, but the consumer should still recover defensively rather than
// rely on that outer safety net.
var OnRiskEvent func(accountID string, entering bool, mmRatePct, pausePct float64)

// accountRiskState is AccountRunner's cached view of account-wide risk, refreshed by
// runRiskMonitorLoop and read synchronously by placeMatrixLevel on every new entry order.
// mmRatePct mirrors Bybit's own accountMMRate (0-100) — the same signal the exchange uses
// to decide liquidation, so no separate risk math needs to be reconstructed here.
type accountRiskState struct {
	equity      float64
	mmRatePct   float64
	updatedAt   time.Time
	warnPct     float64
	pausePct    float64
	notionalPct float64
	paused      bool
	// guardDisabled mirrors exchange_accounts.risk_guard_enabled inverted (zero-value
	// false = guard active, matching every existing accountRiskState literal that doesn't
	// set this field — no accidental bypass from an unset field). The account-level master
	// switch for the whole risk guard: when true, riskGate skips BOTH the margin-ratio
	// pause check and the per-symbol notional cap; the threshold values above stay loaded
	// (so the UI keeps showing them, and re-enabling takes effect immediately) but are not
	// enforced. Thresholds are still polled/computed normally while disabled — only
	// enforcement in riskGate is skipped — so mmRate/pause bookkeeping stays accurate for
	// display and resumes cleanly the moment the guard is re-enabled.
	guardDisabled bool
}

// runRiskMonitorLoop polls account equity + margin rate and the account's configurable
// thresholds (exchange_accounts.margin_warn_pct / margin_pause_pct / max_symbol_notional_pct)
// every riskMonitorInterval, so a threshold edited in the UI takes effect within one tick
// without restarting the engine. See placeMatrixLevel (matrix.go) for enforcement.
func (ar *AccountRunner) runRiskMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(riskMonitorInterval)
	defer ticker.Stop()
	ar.checkRiskOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ar.checkRiskOnce(ctx)
		}
	}
}

func (ar *AccountRunner) checkRiskOnce(ctx context.Context) {
	var warnPct, pausePct, notionalPct float64
	var enabled bool
	if err := ar.pool.QueryRow(ctx,
		`SELECT margin_warn_pct, margin_pause_pct, max_symbol_notional_pct, risk_guard_enabled FROM exchange_accounts WHERE id=$1`,
		ar.accountID,
	).Scan(&warnPct, &pausePct, &notionalPct, &enabled); err != nil {
		log.Printf("strategy: riskMonitor account=%s: load thresholds: %v", ar.accountID, err)
		return
	}

	equity, _, mmRatePct, err := trader.GetAccountMarginInfo(ctx, ar.creds)
	if err != nil {
		log.Printf("strategy: riskMonitor account=%s: %v", ar.accountID, err)
		return
	}

	ar.riskMu.Lock()
	wasPaused := ar.risk.paused
	nowPaused := nextPausedState(wasPaused, mmRatePct, warnPct, pausePct)
	ar.risk = accountRiskState{
		equity:        equity,
		mmRatePct:     mmRatePct,
		updatedAt:     time.Now(),
		warnPct:       warnPct,
		pausePct:      pausePct,
		notionalPct:   notionalPct,
		paused:        nowPaused,
		guardDisabled: !enabled,
	}
	ar.riskMu.Unlock()

	if nowPaused && !wasPaused {
		log.Printf("strategy: account=%s ENTERING risk pause: mmRate=%.2f%% >= pausePct=%.2f%% (equity=%.2f) — new matrix/grid entries blocked until mmRate < warnPct=%.2f%%", ar.accountID, mmRatePct, pausePct, equity, warnPct)
		if OnRiskEvent != nil {
			OnRiskEvent(ar.accountID, true, mmRatePct, pausePct)
		}
	} else if !nowPaused && wasPaused {
		log.Printf("strategy: account=%s risk pause LIFTED: mmRate=%.2f%% < warnPct=%.2f%% (equity=%.2f)", ar.accountID, mmRatePct, warnPct, equity)
		if OnRiskEvent != nil {
			OnRiskEvent(ar.accountID, false, mmRatePct, pausePct)
		}
	} else if mmRatePct >= warnPct && mmRatePct < pausePct {
		log.Printf("strategy: account=%s risk WARNING: mmRate=%.2f%% >= warnPct=%.2f%% (equity=%.2f, pausePct=%.2f%%)", ar.accountID, mmRatePct, warnPct, equity, pausePct)
	}
}

// nextPausedState applies hysteresis to the pause decision: entering pause fires the
// instant mmRatePct reaches pausePct, but resuming only fires once mmRate drops back under
// the LOWER warnPct threshold (not merely below pausePct) — otherwise margin hovering right
// at the pause line would flap trading on/off every risk-monitor tick.
func nextPausedState(wasPaused bool, mmRatePct, warnPct, pausePct float64) bool {
	if mmRatePct >= pausePct {
		return true
	}
	if mmRatePct < warnPct {
		return false
	}
	return wasPaused
}

// RiskSnapshot returns the last-known risk state for API/UI consumption.
func (ar *AccountRunner) RiskSnapshot() (equity, mmRatePct float64, paused bool, updatedAt time.Time) {
	ar.riskMu.RLock()
	defer ar.riskMu.RUnlock()
	return ar.risk.equity, ar.risk.mmRatePct, ar.risk.paused, ar.risk.updatedAt
}

// riskGate decides whether a new entry order (qty*price notional, opening/adding to
// positionIdx on symbol) is allowed. Returns (true, "") if allowed, else (false, reason)
// for logging. Never blocks closing/reduce-only orders — those go through
// matrixUpdateTP/matrixPlacePerLevelSL, not placeMatrixLevel, so this gate naturally only
// ever touches entry-adding orders.
//
// The notional cap is enforced on NET exposure (long leg minus short leg), not gross
// (long+short) — critical for hedge-mode accounts running a long AND a short strategy on
// the same symbol simultaneously (e.g. ALLOUSDT during the 2026-08-16 liquidation). A gross cap
// would count a hedge's opposing leg as MORE risk instead of less, and could block the
// hedge from opening at exactly the moment it's needed to offset the main position — the
// opposite of what a risk control should do. Netting means: an order that reduces |net
// exposure| (a hedge, or trimming an existing directional bet) is ALWAYS allowed regardless
// of the cap; only an order that GROWS |net exposure| past the cap gets blocked.
func (ar *AccountRunner) riskGate(symbol string, positionIdx int, addNotional float64) (bool, string) {
	ar.riskMu.RLock()
	st := ar.risk
	ar.riskMu.RUnlock()

	if st.updatedAt.IsZero() {
		// Risk monitor hasn't completed its first tick yet (e.g. just after startup) —
		// fail open rather than blocking every entry for the first ~20s of runtime.
		return true, ""
	}
	if st.guardDisabled {
		// Account-level master switch (risk_guard_enabled) — bypasses BOTH the margin-pause
		// check and the notional cap below. Thresholds stay loaded/displayed either way.
		return true, ""
	}
	if st.paused {
		return false, "риск-пауза по счёту (margin ratio)"
	}
	if st.notionalPct > 0 && st.equity > 0 {
		netBefore := ar.symbolNetExposureUSD(symbol)
		netAfter := netBefore
		switch positionIdx {
		case 1: // long leg
			netAfter += addNotional
		case 2: // short leg
			netAfter -= addNotional
		default: // one-way (idx=0) — side isn't cached, treat conservatively as additive
			netAfter += addNotional
		}
		cap := st.equity * st.notionalPct / 100
		growing := absF(netAfter) > absF(netBefore)
		if growing && absF(netAfter) > cap {
			return false, "лимит notional на символ"
		}
	}
	return true, ""
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// SymbolNotionalUSD sums the GROSS notional value of both hedge-mode legs (positionIdx 1
// and 2) plus the one-way leg (0) for symbol — informational (e.g. for future UI display),
// NOT used by riskGate (which nets long against short; see symbolNetExposureUSD).
func (ar *AccountRunner) SymbolNotionalUSD(symbol string) float64 {
	var total float64
	for _, idx := range []int{0, 1, 2} {
		key := symbol + ":" + strconv.Itoa(idx)
		ar.posMu.RLock()
		size := ar.positions[key]
		price := ar.posAvgEntry[key]
		ar.posMu.RUnlock()
		total += size * price
	}
	return total
}

// symbolNetExposureUSD returns the signed net notional for symbol: long leg (idx 1) minus
// short leg (idx 2), using each leg's cached avg entry price as a stand-in for mark price
// (close enough for a risk cap, avoids an extra FetchMarkPrice call on every order). A
// one-way (idx=0) position has no opposing leg to net against, so its notional is added
// unsigned — one-way mode isn't the hedge-pair scenario this netting exists for.
func (ar *AccountRunner) symbolNetExposureUSD(symbol string) float64 {
	ar.posMu.RLock()
	longNotional := ar.positions[symbol+":1"] * ar.posAvgEntry[symbol+":1"]
	shortNotional := ar.positions[symbol+":2"] * ar.posAvgEntry[symbol+":2"]
	onewayNotional := ar.positions[symbol+":0"] * ar.posAvgEntry[symbol+":0"]
	ar.posMu.RUnlock()
	return longNotional - shortNotional + onewayNotional
}
