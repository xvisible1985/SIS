// pkg/signals/evaluator_test.go
package signals_test

import (
	"math"
	"testing"
	"time"

	"sis/pkg/models"
	"sis/pkg/signals"
)

func makeCandles(n int, startClose float64) []models.Candle {
	candles := make([]models.Candle, n)
	for i := range candles {
		c := startClose + float64(i)
		candles[i] = models.Candle{
			OpenTime: time.Now().Add(time.Duration(i) * time.Minute),
			Open:     c - 0.5,
			High:     c + 1,
			Low:      c - 1,
			Close:    c,
			Volume:   1000,
		}
	}
	return candles
}

func ptr(v float64) *float64 { return &v }

func TestParseConditions_AND(t *testing.T) {
	json := []byte(`{
		"type": "AND",
		"children": [
			{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": "<", "value": 70},
			{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": ">", "value": 30}
		]
	}`)
	node, err := signals.ParseConditions(json)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
}

func TestEvaluate_SimpleCondition_RSI(t *testing.T) {
	candles := makeCandles(50, 100)
	// RSI for linearly rising prices should be above 50
	json := []byte(`{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": ">", "value": 50}`)
	node, err := signals.ParseConditions(json)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cache := signals.IndicatorCache{}
	// Test at a valid index (past warmup period)
	result, err := signals.Evaluate(node, candles, 49, cache, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result {
		t.Error("expected RSI > 50 for rising prices")
	}
}

func TestEvaluate_NaNReturns_False(t *testing.T) {
	candles := makeCandles(10, 100)
	// RSI with period 14 needs at least 15 candles — all values NaN
	json := []byte(`{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": "<", "value": 80}`)
	node, _ := signals.ParseConditions(json)

	cache := signals.IndicatorCache{}
	result, err := signals.Evaluate(node, candles, 5, cache, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result {
		t.Error("expected false for NaN indicator value")
	}
}

func TestEvaluate_AND_ShortCircuit(t *testing.T) {
	candles := makeCandles(50, 100)
	// Second condition always false → AND must return false
	json := []byte(`{
		"type": "AND",
		"children": [
			{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": ">", "value": 0},
			{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": ">", "value": 999}
		]
	}`)
	node, _ := signals.ParseConditions(json)

	cache := signals.IndicatorCache{}
	result, err := signals.Evaluate(node, candles, 49, cache, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result {
		t.Error("AND should return false when one child is false")
	}
}

func TestEvaluate_OR(t *testing.T) {
	candles := makeCandles(50, 100)
	// First condition false, second true → OR returns true
	json := []byte(`{
		"type": "OR",
		"children": [
			{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": ">", "value": 999},
			{"type": "condition", "indicator": "RSI", "params": {"period": 14}, "operator": ">", "value": 0}
		]
	}`)
	node, _ := signals.ParseConditions(json)

	cache := signals.IndicatorCache{}
	result, err := signals.Evaluate(node, candles, 49, cache, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result {
		t.Error("OR should return true when one child is true")
	}
}

func TestEvaluate_CachingCorrectness(t *testing.T) {
	candles := makeCandles(50, 100)
	json := []byte(`{"type": "condition", "indicator": "EMA", "params": {"period": 9}, "operator": ">", "value": 0}`)
	node, _ := signals.ParseConditions(json)
	cache := signals.IndicatorCache{}

	// Evaluate twice — second should use cache
	r1, err1 := signals.Evaluate(node, candles, 40, cache, nil)
	r2, err2 := signals.Evaluate(node, candles, 40, cache, nil)
	if err1 != nil || err2 != nil {
		t.Fatal("evaluate error")
	}
	if r1 != r2 {
		t.Error("cached and fresh evaluation differ")
	}
}

func TestEvaluate_CrossesAbove(t *testing.T) {
	// Craft candles where EMA(9) crosses above EMA(21)
	// Use flat prices then a sharp spike
	candles := make([]models.Candle, 50)
	for i := 0; i < 40; i++ {
		candles[i] = models.Candle{Close: 100, High: 101, Low: 99, Volume: 100}
	}
	for i := 40; i < 50; i++ {
		candles[i] = models.Candle{Close: 200, High: 201, Low: 199, Volume: 100}
	}
	json := []byte(`{
		"type": "condition",
		"indicator": "EMA",
		"params": {"period": 9},
		"operator": "crosses_above",
		"compare_to": {"indicator": "EMA", "params": {"period": 21}}
	}`)
	node, err := signals.ParseConditions(json)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cache := signals.IndicatorCache{}
	// Cross should happen somewhere in range 40-49
	found := false
	for i := 21; i < 50; i++ {
		result, err := signals.Evaluate(node, candles, i, cache, nil)
		if err != nil {
			t.Fatalf("evaluate at %d: %v", i, err)
		}
		if result {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected crosses_above event after price spike")
	}
}

func TestIndicatorCache_ReusedAcrossConditions(t *testing.T) {
	candles := makeCandles(30, 50)
	cache := signals.IndicatorCache{}
	node, _ := signals.ParseConditions([]byte(`{"type":"condition","indicator":"SMA","params":{"period":10},"operator":">","value":0}`))

	_, _ = signals.Evaluate(node, candles, 20, cache, nil)
	if len(cache) == 0 {
		t.Error("cache should have at least one entry after evaluation")
	}

	// _ = math.NaN() avoids unused import
	_ = math.NaN()
}
