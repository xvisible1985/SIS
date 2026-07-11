package main

import "testing"

// TestMatrixLegCloseRequest verifies the reduce-only market close request built for a
// matrix pair leg: side must be the OPPOSITE of the position (so it flattens, not doubles),
// positionIdx is the hedge slot (long=1, short=2), qty is the raw exchange size string.
func TestMatrixLegCloseRequest(t *testing.T) {
	// Long (Buy) position → close with a reduce-only Sell on positionIdx 1.
	req, ok := matrixLegCloseRequest(hedgePosInfo{Side: "Buy", Size: 2310, SizeStr: "2310"}, "TACUSDT", "linear", 1)
	if !ok {
		t.Fatal("expected ok for open long position")
	}
	if req.Side != "Sell" || req.PositionIdx != 1 || !req.ReduceOnly ||
		req.OrderType != "Market" || req.Qty != "2310" || req.Symbol != "TACUSDT" || req.Category != "linear" {
		t.Errorf("long close request wrong: %+v", req)
	}

	// Short (Sell) position → close with a reduce-only Buy on positionIdx 2.
	req2, ok2 := matrixLegCloseRequest(hedgePosInfo{Side: "Sell", Size: 7050, SizeStr: "7050"}, "TACUSDT", "linear", 2)
	if !ok2 {
		t.Fatal("expected ok for open short position")
	}
	if req2.Side != "Buy" || req2.PositionIdx != 2 || !req2.ReduceOnly || req2.Qty != "7050" {
		t.Errorf("short close request wrong: %+v", req2)
	}

	// Nothing to close (zero size) → ok=false, no order.
	if _, ok3 := matrixLegCloseRequest(hedgePosInfo{Side: "Buy", Size: 0, SizeStr: ""}, "TACUSDT", "linear", 1); ok3 {
		t.Error("expected ok=false for zero-size position")
	}
}
