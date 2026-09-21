package strategy

import (
	"testing"

	"sis/pkg/signal"
)

// TestSignalGateAllows_NoSignalEngine_AllowsByDefault mirrors awaitSignal's own fallback
// (cycle.go: "if signalEngine == nil || len(configs) == 0 { ...start unconditionally... }")
// — a UseSignal=true level must never block a bot's trading indefinitely just because the
// signal engine isn't wired up in this test/runtime context.
func TestSignalGateAllows_NoSignalEngine_AllowsByDefault(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: []SignalConfig{{Name: "rsi-os"}},
		},
		runner: &AccountRunner{}, // signalEngine left nil
	}
	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() = false, want true — no signal engine must fall back to allow")
	}
}

// TestSignalGateAllows_NoConfigs_AllowsByDefault is the other half of the same fallback —
// SignalConfigs empty even though a signal engine exists.
func TestSignalGateAllows_NoConfigs_AllowsByDefault(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Direction: DirectionLong,
			Symbol:    "TESTUSDT",
			// SignalConfigs deliberately empty
		},
		runner: &AccountRunner{signalEngine: &signal.Engine{}},
	}
	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() = false, want true — empty SignalConfigs must fall back to allow")
	}
}
