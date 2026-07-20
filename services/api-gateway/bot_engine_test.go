package main

import (
	"testing"

	"sis/pkg/signal"
)

// TestEffectivePrioritySignal: an explicit priority_signal always wins (base name, with
// any ":tf" suffix stripped). Without one, a bot whose activation signals include
// "price-change" auto-ranks by it — so a price-change-driven bot prioritizes the
// strongest move without needing separate priority_signal configuration. Otherwise no
// ranking applies (every candidate scores 1.0, first-come order).
func TestEffectivePrioritySignal(t *testing.T) {
	priceChangeCfg := []signal.Config{{Name: "price-change"}}
	stFlipCfg := []signal.Config{{Name: "st-flip"}}
	mixedCfg := []signal.Config{{Name: "rsi"}, {Name: "price-change"}}

	cases := []struct {
		name     string
		priority string
		cfgs     []signal.Config
		want     string
	}{
		{"explicit priority wins over price-change signals present", "st-flip", priceChangeCfg, "st-flip"},
		{"explicit priority with :tf suffix stripped", "st-flip:1h", stFlipCfg, "st-flip"},
		{"no explicit priority, price-change signal present — auto-selected", "", priceChangeCfg, "price-change"},
		{"no explicit priority, price-change among mixed signals — auto-selected", "", mixedCfg, "price-change"},
		{"no explicit priority, no price-change signal — no ranking", "", stFlipCfg, ""},
		{"no explicit priority, no signals at all — no ranking", "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := effectivePrioritySignal(c.priority, c.cfgs)
			if got != c.want {
				t.Errorf("effectivePrioritySignal(%q, %v) = %q, want %q", c.priority, c.cfgs, got, c.want)
			}
		})
	}
}
