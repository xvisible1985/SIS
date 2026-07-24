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

// TestPairedCloseTargetPrice_RoiPercentMode: mode 1's threshold is a % of total notional
// (Em*Sm + Eh*Sh), not a flat $ value.
func TestPairedCloseTargetPrice_RoiPercentMode(t *testing.T) {
	price, ok := pairedCloseTargetPrice(
		"long", "short",
		100.0, 100.0,
		2.0, 1.0,
		1, 5.0, // closeType=1 (roi%), closeValue=5.0
		0,
	)
	if !ok {
		t.Fatal("expected a finite target price for mode 1")
	}
	// notional = 100*2 + 100*1 = 300; threshold = 300*5/100 = 15.
	// combined(price) = price - 100 (same as above). Solve price-100=15 -> price=115.
	if got, want := price, 115.0; got < want-0.0001 || got > want+0.0001 {
		t.Errorf("target price = %v, want %v", got, want)
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
		0, 5.0,
		0,
	)
	if ok {
		t.Error("expected ok=false for perfectly-hedged equal sizes (no finite crossing price)")
	}
}
