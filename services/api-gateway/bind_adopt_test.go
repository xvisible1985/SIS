package main

import (
	"strings"
	"testing"
)

// TestBuildAdoptData: bind must adopt a live position (build adopt_position_data) instead
// of letting the engine place a fresh L0 entry on top of it. Pure helper, no DB/network.
func TestBuildAdoptData(t *testing.T) {
	pm := map[string]map[string]hedgePosInfo{
		"XUSDT": {
			"Buy":  {Symbol: "XUSDT", Side: "Buy", Size: 100, EntryPrice: 1.5},
			"Sell": {Symbol: "XUSDT", Side: "Sell", Size: 0, EntryPrice: 2.0},
		},
	}

	// Long leg → adopt the Buy position.
	long := buildAdoptData(pm, "XUSDT", "long")
	if long == nil {
		t.Fatal("expected adopt data for open long position, got nil")
	}
	if !strings.Contains(*long, "100") || !strings.Contains(*long, "1.5") {
		t.Errorf("long adopt data = %s, want size 100 + entry 1.5", *long)
	}

	// Short leg → Sell position has size 0 → no adopt.
	if short := buildAdoptData(pm, "XUSDT", "short"); short != nil {
		t.Errorf("expected nil for zero-size short position, got %s", *short)
	}

	// No position for the symbol → nil.
	if none := buildAdoptData(pm, "YUSDT", "long"); none != nil {
		t.Errorf("expected nil for absent symbol, got %s", *none)
	}
}
