package strategy

import "testing"

// Regression for a live incident (2026-08-10, ALLOUSDT short): a matrix cycle created by
// the repair path filled its entry level but matrixUpdateTP silently no-op'd (instrument
// info not loaded yet), leaving tp_order_id NULL. Neither resumeMatrixCycle nor the
// reconcile.go self-heal pass ever rechecked TP for matrix cycles (grid always did via
// updateTP), so the position ran unprotected for 26+ hours across two restarts. This pins
// the shared decision both call sites now use.
func TestMatrixNeedsTPRestore(t *testing.T) {
	cases := []struct {
		name              string
		tpOrderID         string
		hasPosition       bool
		hedgeTpSuppressed bool
		want              bool
	}{
		{"no TP, position open — restore", "", true, false, true},
		{"TP already tracked — no-op", "order-123", true, false, false},
		{"no TP, no position yet — nothing to protect", "", false, false, false},
		{"no TP, position open, but hedge suppresses TP — leave suppressed", "", true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matrixNeedsTPRestore(c.tpOrderID, c.hasPosition, c.hedgeTpSuppressed)
			if got != c.want {
				t.Errorf("matrixNeedsTPRestore(%q, %v, %v) = %v, want %v",
					c.tpOrderID, c.hasPosition, c.hedgeTpSuppressed, got, c.want)
			}
		})
	}
}
