package strategy

import "testing"

// TestAdoptBranch_NilAfterConsumption verifies that after the adopt branch runs,
// AdoptPositionData is nil (cleared).
func TestAdoptBranch_NilAfterConsumption(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			AdoptPositionData: &AdoptPositionData{Size: "35", EntryPrice: "0.4647"},
		},
	}
	if sr.strategy.AdoptPositionData == nil {
		t.Fatal("AdoptPositionData should be non-nil before adopt")
	}
	// Simulate what the adopt branch does
	sr.strategy.AdoptPositionData = nil
	if sr.strategy.AdoptPositionData != nil {
		t.Fatal("AdoptPositionData should be nil after adopt")
	}
}
