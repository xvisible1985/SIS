package main

import "testing"

// TestPairedCloseTargetPrice_PnlDollarMode: mode 0 solves for the price where combined
// unrealized PnL alone (no накопление involved — mode 0 is live-only) equals the
// configured $ threshold. Both legs long is impossible in practice (matrix/hedge pairs are
// opposite directions) but the formula itself doesn't assume that — this test uses a
// realistic opposite-direction pair.
func TestPairedCloseTargetPrice_PnlDollarMode(t *testing.T) {
	// main long 1 unit @ 100, hedge short 1 unit @ 100 — perfectly offsetting sizes at
	// entry, so as price moves, main gains what hedge loses at the same rate UNLESS sizes
	// differ. Use different sizes so price genuinely affects combined PnL.
	price, ok := pairedCloseTargetPrice(
		"long", "short",
		100.0, 100.0, // mainEntry, hedgeEntry
		2.0, 1.0, // mainSize, hedgeSize (units, not USDT)
		10.0, 10.0, // mainLeverage, hedgeLeverage (unused for mode 0)
		0, 5.0, // closeType=0 (pnl$), closeValue=5.0
		0, // накопление unused for mode 0
	)
	if !ok {
		t.Fatal("expected a finite target price for mode 0 with unequal leg sizes")
	}
	// combined(price) = 2*(price-100) + 1*(100-price) = price - 100. Solve price-100=5 -> price=105.
	if got, want := price, 105.0; got < want-0.0001 || got > want+0.0001 {
		t.Errorf("target price = %v, want %v", got, want)
	}
}

// TestPairedCloseTargetPrice_RoiPercentMode: mode 1's threshold is a % of total MARGIN
// (Em*Sm/Lm + Eh*Sh/Lh), not notional and not a flat $ value — must match
// meetsPairedCloseCriteria's mainMargin/hMargin computation exactly (leverage divides in).
func TestPairedCloseTargetPrice_RoiPercentMode(t *testing.T) {
	price, ok := pairedCloseTargetPrice(
		"long", "short",
		100.0, 100.0,
		2.0, 1.0,
		10.0, 10.0, // mainLeverage, hedgeLeverage
		1, 5.0, // closeType=1 (roi%), closeValue=5.0
		0,
	)
	if !ok {
		t.Fatal("expected a finite target price for mode 1")
	}
	// margin = 100*2/10 + 100*1/10 = 20+10 = 30; threshold = 30*5/100 = 1.5.
	// combined(price) = price - 100 (same as above). Solve price-100=1.5 -> price=101.5.
	if got, want := price, 101.5; got < want-0.0001 || got > want+0.0001 {
		t.Errorf("target price = %v, want %v", got, want)
	}
}

// TestPairedCloseTargetPrice_MatchesMeetsPairedCloseCriteriaBoundary_RoiMode: the price
// pairedCloseTargetPrice returns must be the genuine boundary where meetsPairedCloseCriteria
// flips from false to true — cross-checked directly against the real criteria function
// instead of trusting a hand-derived expected value, since a hand-derived value can encode
// the same bug the implementation has (exactly what happened with the original notional-
// vs-margin mistake in this mode).
func TestPairedCloseTargetPrice_MatchesMeetsPairedCloseCriteriaBoundary_RoiMode(t *testing.T) {
	mainEntry, hedgeEntry := 100.0, 100.0
	mainSize, hedgeSize := 2.0, 1.0
	mainLev, hedgeLev := 10.0, 10.0
	closeValue := 5.0

	price, ok := pairedCloseTargetPrice("long", "short", mainEntry, hedgeEntry, mainSize, hedgeSize, mainLev, hedgeLev, 1, closeValue, 0)
	if !ok {
		t.Fatal("expected a finite target price")
	}

	cfg := botCfgJSON{HedgeDeactCloseType: 1, HedgeDeactCloseValue: closeValue}
	mainAtPrice := func(p float64) hedgePosInfo {
		return hedgePosInfo{EntryPrice: mainEntry, Size: mainSize, Leverage: mainLev, UnrealisedPnl: (p - mainEntry) * mainSize}
	}
	hedgeAtPrice := func(p float64) hedgePosInfo {
		return hedgePosInfo{EntryPrice: hedgeEntry, Size: hedgeSize, Leverage: hedgeLev, UnrealisedPnl: (hedgeEntry - p) * hedgeSize}
	}

	justBelow := meetsPairedCloseCriteria(mainAtPrice(price-0.01), hedgeAtPrice(price-0.01), cfg, 0)
	justAbove := meetsPairedCloseCriteria(mainAtPrice(price+0.01), hedgeAtPrice(price+0.01), cfg, 0)
	if justBelow {
		t.Errorf("meetsPairedCloseCriteria already true just BELOW the computed target price %v — target price is wrong (likely too low)", price)
	}
	if !justAbove {
		t.Errorf("meetsPairedCloseCriteria still false just ABOVE the computed target price %v — target price is wrong (likely too high)", price)
	}
}

// TestPairedCloseTargetPrice_BreakevenMode_AccountsForAccumulated: mode 2's threshold is
// hedge_breakeven_profit, and накопление offsets how much live combined PnL is still
// needed — this is the bug found and fixed during this plan's own research (the old JS
// formula ignored накопление and the real threshold entirely). With накопление already at
// 8 and a threshold of 10, only 2 more of live combined PnL is needed to cross.
func TestPairedCloseTargetPrice_BreakevenMode_AccountsForAccumulated(t *testing.T) {
	price, ok := pairedCloseTargetPrice(
		"long", "short",
		100.0, 100.0,
		2.0, 1.0,
		10.0, 10.0, // mainLeverage, hedgeLeverage (unused for mode 2)
		2, 10.0, // closeType=2 (breakeven), closeValue=hedge_breakeven_profit=10.0
		8.0, // накопление already at 8
	)
	if !ok {
		t.Fatal("expected a finite target price for mode 2")
	}
	// effective threshold = 10 - 8 = 2. combined(price) = price - 100. Solve price-100=2 -> price=102.
	if got, want := price, 102.0; got < want-0.0001 || got > want+0.0001 {
		t.Errorf("target price = %v, want %v (накопление=8 must reduce how much live PnL is still needed)", got, want)
	}
}

// TestPairedCloseTargetPrice_PerfectlyHedgedEqualSizes_NoFinitePrice: when both legs have
// identical size, combined PnL doesn't move with price at all (gains on one leg exactly
// offset losses on the other) — there is no finite price where the threshold is crossed
// (unless already crossed at every price, or never). Must return ok=false, not divide by
// zero or return a nonsense value.
func TestPairedCloseTargetPrice_PerfectlyHedgedEqualSizes_NoFinitePrice(t *testing.T) {
	_, ok := pairedCloseTargetPrice(
		"long", "short",
		100.0, 100.0,
		1.0, 1.0, // equal sizes
		10.0, 10.0, // mainLeverage, hedgeLeverage (unused for mode 0)
		0, 5.0,
		0,
	)
	if ok {
		t.Error("expected ok=false for perfectly-hedged equal sizes (no finite crossing price)")
	}
}
