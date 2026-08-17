package strategy

import (
	"testing"
	"time"
)

func TestNextPausedState(t *testing.T) {
	if !nextPausedState(false, 80, 50, 75) {
		t.Error("mmRate above pausePct must enter pause")
	}
	if !nextPausedState(true, 60, 50, 75) {
		t.Error("mmRate between warnPct and pausePct must STAY paused (hysteresis) once already paused")
	}
	if nextPausedState(false, 60, 50, 75) {
		t.Error("mmRate between warnPct and pausePct must NOT enter pause if not already paused")
	}
	if nextPausedState(true, 40, 50, 75) {
		t.Error("mmRate below warnPct must lift pause")
	}
	if !nextPausedState(true, 55, 50, 75) {
		t.Error("mmRate right at warnPct (not below it) must remain paused — resume needs mmRate < warnPct")
	}
}

func TestSymbolNotionalUSD_SumsAllLegsGross(t *testing.T) {
	ar := &AccountRunner{
		positions:   map[string]float64{"BTCUSDT:0": 1, "BTCUSDT:1": 2, "BTCUSDT:2": 3},
		posAvgEntry: map[string]float64{"BTCUSDT:0": 10, "BTCUSDT:1": 20, "BTCUSDT:2": 5},
	}
	// 1*10 + 2*20 + 3*5 = 10 + 40 + 15 = 65 (gross — long+short, informational only)
	if got := ar.SymbolNotionalUSD("BTCUSDT"); got != 65 {
		t.Errorf("SymbolNotionalUSD = %v, want 65", got)
	}
}

func TestSymbolNetExposureUSD_NetsLongAgainstShort(t *testing.T) {
	ar := &AccountRunner{
		positions:   map[string]float64{"BTCUSDT:1": 10, "BTCUSDT:2": 7},
		posAvgEntry: map[string]float64{"BTCUSDT:1": 5, "BTCUSDT:2": 5},
	}
	// long 10*5=50, short 7*5=35 → net = 50-35 = 15, NOT 85 (gross)
	if got := ar.symbolNetExposureUSD("BTCUSDT"); got != 15 {
		t.Errorf("symbolNetExposureUSD = %v, want 15 (netted, not gross 85)", got)
	}
}

func TestSymbolNotionalUSD_IgnoresOtherSymbol(t *testing.T) {
	ar := &AccountRunner{
		positions:   map[string]float64{"BTCUSDT:1": 100},
		posAvgEntry: map[string]float64{"BTCUSDT:1": 100},
	}
	if got := ar.SymbolNotionalUSD("ETHUSDT"); got != 0 {
		t.Errorf("SymbolNotionalUSD for unrelated symbol = %v, want 0", got)
	}
}

func TestRiskGate_FailsOpenBeforeFirstTick(t *testing.T) {
	ar := &AccountRunner{}
	allowed, reason := ar.riskGate("BTCUSDT", 1, 1_000_000)
	if !allowed {
		t.Errorf("riskGate before first monitor tick must fail open, got blocked: %s", reason)
	}
}

func TestRiskGate_BlocksWhenPaused(t *testing.T) {
	ar := &AccountRunner{}
	ar.risk = accountRiskState{equity: 1000, notionalPct: 50, paused: true, updatedAt: time.Now()}
	allowed, reason := ar.riskGate("BTCUSDT", 1, 10)
	if allowed {
		t.Error("riskGate must block new entries while account is risk-paused")
	}
	if reason == "" {
		t.Error("riskGate should give a reason when blocking")
	}
}

func TestRiskGate_NotionalCap_GrowingDirectionalBetBlocked(t *testing.T) {
	ar := &AccountRunner{
		positions:   map[string]float64{"BTCUSDT:1": 20},
		posAvgEntry: map[string]float64{"BTCUSDT:1": 1}, // existing net = 20
	}
	ar.risk = accountRiskState{equity: 100, notionalPct: 25, paused: false, updatedAt: time.Now()} // cap = 25

	// adding MORE to the same (long) leg: 20 + 10 = 30 > cap 25, and growing → blocked
	if allowed, _ := ar.riskGate("BTCUSDT", 1, 10); allowed {
		t.Error("riskGate must block an entry that grows net exposure past the cap")
	}
	// 20 + 4 = 24 <= cap 25, growing but within cap → allowed
	if allowed, reason := ar.riskGate("BTCUSDT", 1, 4); !allowed {
		t.Errorf("riskGate must allow a growing entry that stays within the cap, got blocked: %s", reason)
	}
}

// TestRiskGate_HedgeNeverBlockedByGrossCap is the direct regression for the concern raised
// after shipping this feature: with a GROSS cap, a hedge leg opening opposite an oversized
// main position would itself get blocked by the very position it exists to offset —
// blocking protection exactly when it's needed most. Netting must prevent this: opening the
// opposite leg always REDUCES |net exposure|, so it must never be blocked by the cap,
// however large the main leg already is relative to equity.
func TestRiskGate_HedgeNeverBlockedByGrossCap(t *testing.T) {
	ar := &AccountRunner{
		// Main strategy's long leg is already 90 — far past what a 25%-of-100-equity cap
		// would allow for a NEW position, by design (simulates an already-oversized main
		// leg, e.g. from before thresholds were tightened).
		positions:   map[string]float64{"BTCUSDT:1": 90},
		posAvgEntry: map[string]float64{"BTCUSDT:1": 1},
	}
	ar.risk = accountRiskState{equity: 100, notionalPct: 25, paused: false, updatedAt: time.Now()} // cap = 25

	// Hedge opens the SHORT leg (idx=2) sized to fully offset the long: net goes 90 → 0.
	// |netAfter|=0 < |netBefore|=90 → not growing → must be allowed regardless of the cap.
	allowed, reason := ar.riskGate("BTCUSDT", 2, 90)
	if !allowed {
		t.Fatalf("riskGate must NEVER block a hedge leg that reduces net exposure, even far over the cap — got blocked: %s", reason)
	}
}

// TestRiskGate_HedgeOvershootIntoNewDirectionalBetStillCapped: a "hedge" order big enough
// to flip past zero into a large NEW net position in the opposite direction is no longer
// risk-reducing — it must still respect the cap once it starts growing |net| again.
func TestRiskGate_HedgeOvershootIntoNewDirectionalBetStillCapped(t *testing.T) {
	ar := &AccountRunner{
		positions:   map[string]float64{"BTCUSDT:1": 10},
		posAvgEntry: map[string]float64{"BTCUSDT:1": 1}, // net = 10
	}
	ar.risk = accountRiskState{equity: 100, notionalPct: 25, paused: false, updatedAt: time.Now()} // cap = 25

	// Short leg of 50: net goes 10 → 10-50 = -40. |netAfter|=40 > |netBefore|=10 (growing,
	// now in the opposite direction) AND 40 > cap 25 → must be blocked.
	allowed, _ := ar.riskGate("BTCUSDT", 2, 50)
	if allowed {
		t.Error("riskGate must still cap an order that overshoots into a large new net position in the opposite direction")
	}
}

func TestRiskGate_UnrestrictedWhenNotionalCapDisabled(t *testing.T) {
	ar := &AccountRunner{
		positions:   map[string]float64{"BTCUSDT:1": 1_000_000},
		posAvgEntry: map[string]float64{"BTCUSDT:1": 1},
	}
	ar.risk = accountRiskState{equity: 100, notionalPct: 0, paused: false, updatedAt: time.Now()}
	if allowed, reason := ar.riskGate("BTCUSDT", 1, 1_000_000); !allowed {
		t.Errorf("notionalPct=0 must mean the cap is disabled (not zero-allowance), got blocked: %s", reason)
	}
}
