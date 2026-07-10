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
