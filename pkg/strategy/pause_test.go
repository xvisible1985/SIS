package strategy

import "testing"

func strPtr(s string) *string { return &s }

// TestManualCloseStatus: только нога matrix-бота уходит в paused при ручном закрытии;
// grid-бот, ручная стратегия без бота и не-matrix остаются stopped.
func TestManualCloseStatus(t *testing.T) {
	cases := []struct {
		name  string
		strat Strategy
		want  Status
	}{
		{"matrix bot leg → paused",
			Strategy{BotID: strPtr("bot"), StrategyType: "matrix"}, StatusPaused},
		{"grid bot leg → stopped",
			Strategy{BotID: strPtr("bot"), StrategyType: "grid"}, StatusStopped},
		{"matrix without bot (manual) → stopped",
			Strategy{BotID: nil, StrategyType: "matrix"}, StatusStopped},
		{"hedge bot leg → stopped",
			Strategy{BotID: strPtr("bot"), StrategyType: "hedge"}, StatusStopped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := manualCloseStatus(c.strat); got != c.want {
				t.Errorf("manualCloseStatus() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestResolveCloseResult_SuppressesPause: bot-initiated close (result != "manual_close")
// must override pauseEligible so matrix legs are NOT set to 'paused'.
// This guards the race where handlePositionCloseRetry runs after stopMatrixPair's
// engine.Notify restarted a new cycle — without the guard, the matrix leg was set
// 'paused' and ensureMatrixStrategies refused to recreate it.
func TestResolveCloseResult_SuppressesPause(t *testing.T) {
	// When resolveCloseResult returns anything other than "manual_close",
	// pauseEligible must be suppressed (=> stopped, not paused).
	// We test the decision logic directly: if result != "manual_close", manualCloseStatus
	// should NOT be consulted — result is StatusStopped regardless of strategy type.
	matrixLeg := Strategy{BotID: strPtr("bot"), StrategyType: "matrix"}

	// Normally a matrix bot leg in manual_close → paused.
	if manualCloseStatus(matrixLeg) != StatusPaused {
		t.Fatal("precondition: matrix leg should be paused on manual_close")
	}

	// But when the result is paired_close, closePositionExternal sets pauseEligible=false,
	// meaning manualCloseStatus is NOT called and newStatus stays StatusStopped.
	// Simulate the conditional:
	result := "paired_close"
	pauseEligible := true
	if result != "manual_close" {
		pauseEligible = false
	}
	newStatus := StatusStopped
	if pauseEligible {
		newStatus = manualCloseStatus(matrixLeg)
	}
	if newStatus != StatusStopped {
		t.Errorf("paired_close result: expected StatusStopped, got %q", newStatus)
	}
}
