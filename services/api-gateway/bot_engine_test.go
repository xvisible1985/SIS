package main

import (
	"testing"
	"time"

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

// TestBotEngineTickStuck: the watchdog must never flag a stall before the tick loop has
// completed its first run (lastTickAt zero — RunBotEngine's synchronous first tick just
// hasn't returned yet), must not flag a normal 30s-interval loop, and must flag once the
// gap clearly exceeds botEngineTick's own 90s per-tick ctx timeout.
// Found live (2026-07-21): the tick loop silently stalled for 3.5+ hours with no log trace
// at all — a bot the user had disabled kept trading because the reactive signal processor
// ran off a bot snapshot that stopped being refreshed, and nobody noticed until live
// symptoms were reported hours later.
func TestBotEngineTickStuck(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		lastTickAt time.Time
		want       bool
	}{
		{"never ticked yet (startup grace)", time.Time{}, false},
		{"just ticked", now, false},
		{"one interval ago (30s) — normal", now.Add(-30 * time.Second), false},
		{"just under threshold (2m59s)", now.Add(-(3*time.Minute - time.Second)), false},
		{"past threshold (4m)", now.Add(-4 * time.Minute), true},
		{"way past threshold (3.5h, the live incident)", now.Add(-3*time.Hour - 30*time.Minute), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := botEngineTickStuck(c.lastTickAt, now); got != c.want {
				t.Errorf("botEngineTickStuck(%v, now) = %v, want %v", c.lastTickAt, got, c.want)
			}
		})
	}
}
