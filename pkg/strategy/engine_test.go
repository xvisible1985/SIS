package strategy

import (
	"math"
	"testing"
)

func TestCalculateGridLevels_Long(t *testing.T) {
	prices := calculateGridLevels(100.0, 1.0, 5, "Buy")
	expected := []float64{99.0, 98.01, 97.0299, 96.0596, 95.0990}
	if len(prices) != 5 {
		t.Fatalf("want 5 levels, got %d", len(prices))
	}
	for i, p := range prices {
		if math.Abs(p-expected[i]) > 0.001 {
			t.Errorf("level[%d]: want %.4f, got %.4f", i, expected[i], p)
		}
	}
}

func TestCalculateGridLevels_Short(t *testing.T) {
	prices := calculateGridLevels(100.0, 1.0, 3, "Sell")
	expected := []float64{101.0, 102.01, 103.0301}
	if len(prices) != 3 {
		t.Fatalf("want 3 levels, got %d", len(prices))
	}
	for i, p := range prices {
		if math.Abs(p-expected[i]) > 0.001 {
			t.Errorf("level[%d]: want %.4f, got %.4f", i, expected[i], p)
		}
	}
}

func TestPlacedCount(t *testing.T) {
	sr := &StrategyRunner{
		levels: []GridLevel{
			{Status: LevelPlaced},
			{Status: LevelPending},
			{Status: LevelFilled},
			{Status: LevelPlaced},
		},
	}
	if got := sr.placedCount(); got != 2 {
		t.Errorf("want 2 placed, got %d", got)
	}
}

// TestPlacedCountExcludesTPSL verifies that TP and SL orders do not count toward
// GridActive. They live in sr.tpOrderID / sr.slOrderID, not in sr.levels.
func TestPlacedCountExcludesTPSL(t *testing.T) {
	sr := &StrategyRunner{
		tpOrderID: "tp-order-abc",
		slOrderID: "sl-order-xyz",
		levels: []GridLevel{
			{Status: LevelPlaced},
			{Status: LevelPending},
		},
	}
	if got := sr.placedCount(); got != 1 {
		t.Errorf("want 1 (TP/SL must not count toward GridActive), got %d", got)
	}
}

func TestAvgEntry(t *testing.T) {
	p1, p2 := 99.0, 98.0
	sr := &StrategyRunner{
		levels: []GridLevel{
			{Status: LevelFilled, FilledPrice: p1, Qty: "1.0"},
			{Status: LevelFilled, FilledPrice: p2, Qty: "1.0"},
			{Status: LevelPending},
		},
	}
	avg, total := sr.avgEntry()
	if math.Abs(avg-98.5) > 0.001 {
		t.Errorf("want avg 98.5, got %.4f", avg)
	}
	if math.Abs(total-2.0) > 0.001 {
		t.Errorf("want total 2.0, got %.4f", total)
	}
}
