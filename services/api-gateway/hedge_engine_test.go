package main

import "testing"

// TestMeetsPairedCloseCriteria_Breakeven_UsesAccumulatedPlusLive: mode 2 (breakeven) must
// compare accumulated_pnl (this session's realized history) PLUS the live combined
// unrealized PnL against the threshold — not live-only, which ignored all historical
// realized PnL and fees. Modes 0/1 are unaffected (live-only, unchanged).
func TestMeetsPairedCloseCriteria_Breakeven_UsesAccumulatedPlusLive(t *testing.T) {
	cfg := botCfgJSON{HedgeDeactCloseType: 2, HedgeBreakevenProfit: 10.0}
	main := hedgePosInfo{UnrealisedPnl: 3.0}
	hedge := hedgePosInfo{UnrealisedPnl: 2.0}
	// live combined = 5.0; accumulated = 4.0 → total = 9.0, below threshold 10.0
	if meetsPairedCloseCriteria(main, hedge, cfg, 4.0) {
		t.Error("9.0 total < 10.0 threshold: expected false")
	}
	// accumulated = 5.5 → total = 10.5, above threshold
	if !meetsPairedCloseCriteria(main, hedge, cfg, 5.5) {
		t.Error("10.5 total >= 10.0 threshold: expected true")
	}
}

// TestMeetsPairedCloseCriteria_PnlDollar_IgnoresAccumulated: mode 0 stays live-only —
// accumulated_pnl must not affect it even when large.
func TestMeetsPairedCloseCriteria_PnlDollar_IgnoresAccumulated(t *testing.T) {
	cfg := botCfgJSON{HedgeDeactCloseType: 0, HedgeDeactCloseValue: 10.0}
	main := hedgePosInfo{UnrealisedPnl: 3.0}
	hedge := hedgePosInfo{UnrealisedPnl: 2.0}
	// live combined = 5.0, below threshold — a huge accumulated value must not flip this.
	if meetsPairedCloseCriteria(main, hedge, cfg, 1000.0) {
		t.Error("mode 0 must ignore accumulated_pnl entirely")
	}
}

// TestMeetsPairedCloseCriteria_ROI_UsesMarginNotNotional: mode 1 divides by MARGIN
// (entryPrice * size / leverage per leg), not notional (entryPrice * size). Confusing
// these two was a live bug — this test pins the correct formula.
func TestMeetsPairedCloseCriteria_ROI_UsesMarginNotNotional(t *testing.T) {
	// main long 2 units @ 100 with leverage 10 → margin = 200/10 = 20
	// hedge short 1 unit @ 100 with leverage 10 → margin = 100/10 = 10
	// total margin = 30; threshold 5% → 1.5 USDT combined live PnL needed.
	cfg := botCfgJSON{HedgeDeactCloseType: 1, HedgeDeactCloseValue: 5.0}
	main := hedgePosInfo{EntryPrice: 100, Size: 2, Leverage: 10, UnrealisedPnl: 1.0}
	hedge := hedgePosInfo{EntryPrice: 100, Size: 1, Leverage: 10, UnrealisedPnl: 0.4}
	// combined = 1.4, below 1.5 threshold
	if meetsPairedCloseCriteria(main, hedge, cfg, 0) {
		t.Error("combined 1.4 < roi threshold 1.5 (5%% of margin 30): expected false")
	}
	main2 := hedgePosInfo{EntryPrice: 100, Size: 2, Leverage: 10, UnrealisedPnl: 1.2}
	hedge2 := hedgePosInfo{EntryPrice: 100, Size: 1, Leverage: 10, UnrealisedPnl: 0.4}
	// combined = 1.6, above 1.5 threshold
	if !meetsPairedCloseCriteria(main2, hedge2, cfg, 0) {
		t.Error("combined 1.6 >= roi threshold 1.5 (5%% of margin 30): expected true")
	}
}

// TestMeetsPairedCloseCriteria_ROI_ZeroMarginReturnsFalse: when total margin is 0
// (both legs have zero size/entry — can't happen with real positions but must not
// divide by zero), must return false rather than panic or return true.
func TestMeetsPairedCloseCriteria_ROI_ZeroMarginReturnsFalse(t *testing.T) {
	cfg := botCfgJSON{HedgeDeactCloseType: 1, HedgeDeactCloseValue: 1.0}
	if meetsPairedCloseCriteria(hedgePosInfo{}, hedgePosInfo{}, cfg, 0) {
		t.Error("zero-margin mode 1: expected false (not division by zero)")
	}
}
