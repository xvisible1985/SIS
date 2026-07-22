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
