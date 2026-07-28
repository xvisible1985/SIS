package strategy

import "testing"

// TestStrategyNeedsCycleReload: an already-registered runner must reload its in-memory
// cycle only when the strategy was and remains active, yet has no cycle object — any
// other combination (just activated, just stopped, still active WITH a cycle, or
// active-with-no-cycle right after a genuine reactivation which loadOrStart already
// handles via the prevStatus!=Active branch) must not trigger a redundant reload.
func TestStrategyNeedsCycleReload(t *testing.T) {
	cases := []struct {
		name               string
		prevStatus, status Status
		hadNoCycle         bool
		want               bool
	}{
		{"stayed active, cycle missing — needs reload", StatusActive, StatusActive, true, true},
		{"stayed active, cycle present — no-op", StatusActive, StatusActive, false, false},
		{"just reactivated, cycle missing — handled by the reactivation branch instead", StatusStopped, StatusActive, true, false},
		{"stayed stopped, cycle missing — irrelevant while stopped", StatusStopped, StatusStopped, true, false},
		{"just stopped, cycle present — no-op", StatusActive, StatusStopped, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strategyNeedsCycleReload(c.prevStatus, c.status, c.hadNoCycle)
			if got != c.want {
				t.Errorf("strategyNeedsCycleReload(%v, %v, %v) = %v, want %v",
					c.prevStatus, c.status, c.hadNoCycle, got, c.want)
			}
		})
	}
}
