// services/signal-engine/optimizer_test.go
package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestGenerateCombinations_CartesianProduct(t *testing.T) {
	space := ParamSpace{"period": {10, 14, 20}}
	combos := generateCombinations(space, []float64{1.5, 2.0}, []float64{0.5, 1.0})
	// 3 period × 2 tp × 2 sl = 12
	if len(combos) != 12 {
		t.Errorf("expected 12 combinations, got %d", len(combos))
	}
}

func TestGenerateCombinations_TwoParams(t *testing.T) {
	space := ParamSpace{"rsi": {10, 14}, "ema": {20, 50}}
	combos := generateCombinations(space, []float64{2.0}, []float64{1.0})
	// 2 rsi × 2 ema × 1 tp × 1 sl = 4
	if len(combos) != 4 {
		t.Errorf("expected 4 combinations, got %d", len(combos))
	}
}

func TestGenerateCombinations_EmptyParamSpace(t *testing.T) {
	combos := generateCombinations(ParamSpace{}, []float64{2.0}, []float64{1.0})
	if len(combos) != 1 {
		t.Errorf("empty param space: expected 1 (tp/sl only), got %d", len(combos))
	}
	if combos[0].tp != 2.0 || combos[0].sl != 1.0 {
		t.Errorf("expected tp=2.0 sl=1.0, got tp=%v sl=%v", combos[0].tp, combos[0].sl)
	}
}

func TestGenerateCombinations_NoDuplicatesEmptySpace(t *testing.T) {
	combos := generateCombinations(ParamSpace{}, []float64{1.5, 2.0}, []float64{0.5, 1.0})
	// Should be exactly 4, not 8 (old bug doubled them)
	if len(combos) != 4 {
		t.Errorf("expected 4 (no duplicates), got %d", len(combos))
	}
}

func TestGenerateCombinations_MapNotShared(t *testing.T) {
	space := ParamSpace{"period": {10, 14}}
	combos := generateCombinations(space, []float64{2.0}, []float64{1.0})
	// Mutating one combo's map must not affect another
	combos[0].combo["period"] = 999
	if combos[1].combo["period"] == 999 {
		t.Error("combo maps are shared — mutation affected another entry")
	}
}

func TestCartesianProduct_TwoSets(t *testing.T) {
	result := cartesianProduct([][]float64{{1, 2}, {3, 4}})
	if len(result) != 4 {
		t.Fatalf("expected 4, got %d", len(result))
	}
	found := make(map[[2]float64]bool)
	for _, r := range result {
		found[[2]float64{r[0], r[1]}] = true
	}
	for _, pair := range [][2]float64{{1, 3}, {1, 4}, {2, 3}, {2, 4}} {
		if !found[pair] {
			t.Errorf("missing combination %v", pair)
		}
	}
}

func TestCartesianProduct_Empty(t *testing.T) {
	result := cartesianProduct([][]float64{})
	if len(result) != 1 || len(result[0]) != 0 {
		t.Errorf("expected [[]], got %v", result)
	}
}

func TestCartesianProduct_SingleSet(t *testing.T) {
	result := cartesianProduct([][]float64{{5, 10, 15}})
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
}

func TestSubstituteTemplate_SingleParam(t *testing.T) {
	tmpl := json.RawMessage(`{"type":"condition","indicator":"RSI","params":{"period":"{{rsi_period}}"},"operator":"<","value":30}`)
	combo := Combination{"rsi_period": 14}

	result, err := substituteTemplate(tmpl, combo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var node map[string]interface{}
	if err := json.Unmarshal(result, &node); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	params := node["params"].(map[string]interface{})
	if params["period"].(float64) != 14 {
		t.Errorf("expected period=14, got %v", params["period"])
	}
}

func TestSubstituteTemplate_MultipleParams(t *testing.T) {
	tmpl := json.RawMessage(`{"type":"AND","children":[` +
		`{"type":"condition","indicator":"RSI","params":{"period":"{{rsi_period}}"},"operator":"<","value":30},` +
		`{"type":"condition","indicator":"EMA","params":{"period":"{{ema_period}}"},"operator":">","value":0}]}`)
	combo := Combination{"rsi_period": 12, "ema_period": 20}

	result, err := substituteTemplate(tmpl, combo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var check interface{}
	if err := json.Unmarshal(result, &check); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
}

func TestSubstituteTemplate_NoPlaceholders(t *testing.T) {
	tmpl := json.RawMessage(`{"type":"condition","indicator":"RSI","params":{"period":14},"operator":"<","value":30}`)
	result, err := substituteTemplate(tmpl, Combination{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != string(tmpl) {
		t.Errorf("template with no placeholders should be unchanged")
	}
}

func TestSubstituteTemplate_IntegerValue(t *testing.T) {
	tmpl := json.RawMessage(`{"type":"condition","indicator":"RSI","params":{"period":"{{p}}"},"operator":"<","value":50}`)
	result, err := substituteTemplate(tmpl, Combination{"p": 14})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(result), `"{{p}}"`) {
		t.Error("placeholder was not substituted")
	}
}

func TestScoreBacktest_ProfitFactor(t *testing.T) {
	r := BacktestResult{TotalSignals: 10, ProfitFactor: 2.5}
	if scoreBacktest(r, "profit_factor") != 2.5 {
		t.Errorf("expected 2.5, got %v", scoreBacktest(r, "profit_factor"))
	}
}

func TestScoreBacktest_WinRate(t *testing.T) {
	r := BacktestResult{TotalSignals: 10, WinRate: 0.6}
	if scoreBacktest(r, "win_rate") != 0.6 {
		t.Errorf("expected 0.6, got %v", scoreBacktest(r, "win_rate"))
	}
}

func TestScoreBacktest_AvgGain(t *testing.T) {
	r := BacktestResult{TotalSignals: 10, AvgGain: 1.23}
	if scoreBacktest(r, "avg_gain") != 1.23 {
		t.Errorf("expected 1.23, got %v", scoreBacktest(r, "avg_gain"))
	}
}

func TestScoreBacktest_ZeroSignals(t *testing.T) {
	r := BacktestResult{TotalSignals: 0, ProfitFactor: 99}
	if scoreBacktest(r, "profit_factor") != 0 {
		t.Errorf("expected 0 for empty result")
	}
}

func TestScoreBacktest_MaxFloat64ProfitFactor(t *testing.T) {
	r := BacktestResult{TotalSignals: 5, ProfitFactor: math.MaxFloat64}
	score := scoreBacktest(r, "profit_factor")
	if score != math.MaxFloat64 {
		t.Errorf("expected math.MaxFloat64, got %v", score)
	}
}

func TestSplitWalkForwardWindows_4Folds(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)

	windows := splitWalkForwardWindows(from, to, 4)
	if len(windows) != 3 {
		t.Fatalf("expected 3 windows for 4 folds, got %d", len(windows))
	}
	for i, w := range windows {
		if !w.InFrom.Equal(from) {
			t.Errorf("window %d: InFrom should be %v, got %v", i, from, w.InFrom)
		}
		if !w.InTo.Equal(w.OutFrom) {
			t.Errorf("window %d: InTo != OutFrom", i)
		}
	}
	// Last window's OutTo must equal to (clamped)
	last := windows[len(windows)-1]
	if !last.OutTo.Equal(to) {
		t.Errorf("last window OutTo should be %v, got %v", to, last.OutTo)
	}
}

func TestSplitWalkForwardWindows_TooFewFolds(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	if windows := splitWalkForwardWindows(from, to, 1); windows != nil {
		t.Error("expected nil for folds=1")
	}
	if windows := splitWalkForwardWindows(from, to, 0); windows != nil {
		t.Error("expected nil for folds=0")
	}
}

func TestBuildOptimizeResult_SortedDescending(t *testing.T) {
	results := []RankedResult{
		{Score: 1.5},
		{Score: 3.0},
		{Score: 2.0},
	}
	out := buildOptimizeResult(results, 2)
	if len(out.TopCombinations) != 2 {
		t.Fatalf("expected 2, got %d", len(out.TopCombinations))
	}
	if out.TopCombinations[0].Score != 3.0 {
		t.Errorf("expected top score 3.0, got %v", out.TopCombinations[0].Score)
	}
	if out.TopCombinations[1].Score != 2.0 {
		t.Errorf("expected second score 2.0, got %v", out.TopCombinations[1].Score)
	}
}

func TestBuildOptimizeResult_TopNLargerThanResults(t *testing.T) {
	results := []RankedResult{{Score: 1.0}, {Score: 2.0}}
	out := buildOptimizeResult(results, 100)
	if len(out.TopCombinations) != 2 {
		t.Errorf("expected 2 (all results), got %d", len(out.TopCombinations))
	}
}

func TestBuildOptimizeResult_BestParamsFromTop(t *testing.T) {
	results := []RankedResult{
		{Score: 1.0, Params: Combination{"period": 20}},
		{Score: 3.0, Params: Combination{"period": 14}},
	}
	out := buildOptimizeResult(results, 10)
	if out.BestParams["period"] != 14 {
		t.Errorf("BestParams should be from top-scored result, expected 14 got %v", out.BestParams["period"])
	}
}

func TestBuildOptimizeResult_NoAliasing(t *testing.T) {
	results := []RankedResult{{Score: 2.0}, {Score: 1.0}}
	out := buildOptimizeResult(results, 2)
	// Mutating the returned slice must not affect re-running the sort
	out.TopCombinations[0].Score = -1
	if results[0].Score == -1 {
		t.Error("TopCombinations aliases the input results slice")
	}
}
