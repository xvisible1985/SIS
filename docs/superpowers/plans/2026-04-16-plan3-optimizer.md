# Crypto Signal Analyzer — Plan 3: Optimizer (Grid Search + Walk-Forward)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Реализовать движок оптимизации параметров сигналов: Grid Search перебирает все комбинации параметров (индикаторные периоды, TP/SL) и ранжирует по profit_factor/win_rate; Walk-Forward Validation проверяет устойчивость лучших параметров на out-of-sample данных.

**Architecture:** Два новых файла в `services/signal-engine/`. `optimizer.go` содержит чистую бизнес-логику (Pure functions + RunGridSearch + RunWalkForward). `optimizer_consumer.go` добавляет второй Redis Streams consumer (`jobs:optimize`) к существующему `Worker`. `main.go` запускает оба consumer-а параллельно. Условия сигнала задаются как JSON-шаблон с плейсхолдерами `"{{param_name}}"` вместо числовых значений; optimizer подставляет конкретные значения перед каждым бэктестом.

**Зависимости:** Plan 2 завершён — `RunBacktest`, `signals.ParseConditions`, `BacktestParams`, `Trade`, `BacktestResult` уже существуют в `services/signal-engine/`.

**Tech Stack:** Go 1.22, `pgx/v5`, `go-redis/v9`, Redis Streams, TimescaleDB

---

## File Structure

```
services/signal-engine/
├── backtest.go              # существует — RunBacktest, computeMetrics, etc.
├── backtest_test.go         # существует
├── patterns.go              # существует
├── worker.go                # существует — добавим RunOptimizer метод
├── main.go                  # модифицировать — запустить оба consumer-а
├── optimizer.go             # создать — Grid Search + Walk-Forward + pure helpers
├── optimizer_consumer.go    # создать — Redis Streams consumer для jobs:optimize
└── optimizer_test.go        # создать — unit-тесты для чистых функций
```

---

## Task 1: Core optimizer engine

**Files:**
- Create: `services/signal-engine/optimizer.go`

Чистые функции + RunGridSearch + RunWalkForward. Зависит только от `RunBacktest` и `signals.ParseConditions`.

- [ ] **Шаг 1: Создать services/signal-engine/optimizer.go**

```go
// services/signal-engine/optimizer.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/models"
	"sis/pkg/signals"
)

// ParamSpace maps placeholder names to lists of candidate values.
// Example: {"rsi_period": [10, 12, 14, 16], "take_profit": [1.5, 2.0, 2.5]}
type ParamSpace map[string][]float64

// Combination is a concrete assignment of all param placeholders.
type Combination map[string]float64

// WFWindow holds in-sample and out-of-sample time bounds for one walk-forward fold.
type WFWindow struct {
	InFrom  time.Time
	InTo    time.Time
	OutFrom time.Time
	OutTo   time.Time
}

// OptimizeJob holds all parameters for an optimization run.
type OptimizeJob struct {
	SignalID           string
	Symbol             string
	Market             models.Market
	Timeframe          models.Timeframe
	Exchange           models.Exchange
	Direction          string
	PeriodFrom         time.Time
	PeriodTo           time.Time
	Mode               string          // "fast" | "walk_forward"
	ScoreBy            string          // "profit_factor" | "win_rate" | "avg_gain"
	TopN               int
	TakeProfits        []float64
	StopLosses         []float64
	ConditionsTemplate json.RawMessage // JSON with "{{param}}" string placeholders
	ParamSpace         ParamSpace
	WFFolds            int // walk-forward folds (default 4)
}

// RankedResult pairs a combination with its backtest metrics and score.
type RankedResult struct {
	Params       Combination `json:"params"`
	TakeProfit   float64     `json:"take_profit"`
	StopLoss     float64     `json:"stop_loss"`
	Score        float64     `json:"score"`
	WinRate      float64     `json:"win_rate"`
	AvgGain      float64     `json:"avg_gain"`
	ProfitFactor float64     `json:"profit_factor"`
	TotalSignals int         `json:"total_signals"`
}

// OptimizeResult holds the output of an optimization run.
type OptimizeResult struct {
	TopCombinations []RankedResult `json:"top_combinations"`
	BestParams      Combination    `json:"best_params"`
}

// comboEntry bundles param values with their tp/sl for a single backtest run.
type comboEntry struct {
	combo Combination
	tp    float64
	sl    float64
}

// RunGridSearch runs a grid search over the param space on a single period.
// Calls RunBacktest for each combination; skips combinations that error (e.g. no candles).
func RunGridSearch(ctx context.Context, pool *pgxpool.Pool, job OptimizeJob, progress func(int)) (OptimizeResult, error) {
	combos := generateCombinations(job.ParamSpace, job.TakeProfits, job.StopLosses)
	if len(combos) == 0 {
		return OptimizeResult{}, fmt.Errorf("optimizer: empty param space — provide take_profits and stop_losses")
	}

	results := make([]RankedResult, 0, len(combos))
	total := len(combos)

	for idx, c := range combos {
		if ctx.Err() != nil {
			return OptimizeResult{}, ctx.Err()
		}

		condBytes, err := substituteTemplate(job.ConditionsTemplate, c.combo)
		if err != nil {
			return OptimizeResult{}, fmt.Errorf("optimizer: substitute combo %d: %w", idx, err)
		}
		node, err := signals.ParseConditions(condBytes)
		if err != nil {
			return OptimizeResult{}, fmt.Errorf("optimizer: parse conditions combo %d: %w", idx, err)
		}

		params := BacktestParams{
			SignalID:   job.SignalID,
			Symbol:     job.Symbol,
			Market:     job.Market,
			Timeframe:  job.Timeframe,
			Exchange:   job.Exchange,
			Direction:  job.Direction,
			PeriodFrom: job.PeriodFrom,
			PeriodTo:   job.PeriodTo,
			TakeProfit: c.tp,
			StopLoss:   c.sl,
			Conditions: node,
		}

		r, err := RunBacktest(ctx, pool, params, nil)
		if err != nil {
			continue // skip — no candles or other transient error
		}

		results = append(results, RankedResult{
			Params:       c.combo,
			TakeProfit:   c.tp,
			StopLoss:     c.sl,
			Score:        scoreBacktest(r, job.ScoreBy),
			WinRate:      r.WinRate,
			AvgGain:      r.AvgGain,
			ProfitFactor: r.ProfitFactor,
			TotalSignals: r.TotalSignals,
		})

		if progress != nil && total > 0 {
			progress(idx * 100 / total)
		}
	}

	if progress != nil {
		progress(100)
	}
	return buildOptimizeResult(results, job.TopN), nil
}

// RunWalkForward runs walk-forward validation.
// For each fold: grid search on in-sample, test best params on out-of-sample.
// Returns out-of-sample results ranked by score.
func RunWalkForward(ctx context.Context, pool *pgxpool.Pool, job OptimizeJob, progress func(int)) (OptimizeResult, error) {
	folds := job.WFFolds
	if folds < 2 {
		folds = 4
	}
	windows := splitWalkForwardWindows(job.PeriodFrom, job.PeriodTo, folds)
	if len(windows) == 0 {
		return OptimizeResult{}, fmt.Errorf("optimizer: no walk-forward windows generated")
	}

	var outResults []RankedResult
	total := len(windows)

	for wi, w := range windows {
		if ctx.Err() != nil {
			return OptimizeResult{}, ctx.Err()
		}
		if progress != nil {
			progress(wi * 100 / total)
		}

		// Grid search on in-sample window
		inJob := job
		inJob.PeriodFrom = w.InFrom
		inJob.PeriodTo = w.InTo
		inResult, err := RunGridSearch(ctx, pool, inJob, nil)
		if err != nil || len(inResult.TopCombinations) == 0 {
			continue
		}

		// Test best in-sample params on out-of-sample window
		best := inResult.TopCombinations[0]
		condBytes, err := substituteTemplate(job.ConditionsTemplate, best.Params)
		if err != nil {
			continue
		}
		node, err := signals.ParseConditions(condBytes)
		if err != nil {
			continue
		}

		outParams := BacktestParams{
			SignalID:   job.SignalID,
			Symbol:     job.Symbol,
			Market:     job.Market,
			Timeframe:  job.Timeframe,
			Exchange:   job.Exchange,
			Direction:  job.Direction,
			PeriodFrom: w.OutFrom,
			PeriodTo:   w.OutTo,
			TakeProfit: best.TakeProfit,
			StopLoss:   best.StopLoss,
			Conditions: node,
		}
		outBT, err := RunBacktest(ctx, pool, outParams, nil)
		if err != nil {
			continue
		}

		outResults = append(outResults, RankedResult{
			Params:       best.Params,
			TakeProfit:   best.TakeProfit,
			StopLoss:     best.StopLoss,
			Score:        scoreBacktest(outBT, job.ScoreBy),
			WinRate:      outBT.WinRate,
			AvgGain:      outBT.AvgGain,
			ProfitFactor: outBT.ProfitFactor,
			TotalSignals: outBT.TotalSignals,
		})
	}

	if progress != nil {
		progress(100)
	}
	return buildOptimizeResult(outResults, job.TopN), nil
}

// generateCombinations returns all Cartesian product combinations of ParamSpace values
// crossed with every (tp, sl) pair.
func generateCombinations(space ParamSpace, tps, sls []float64) []comboEntry {
	// Sort param names for deterministic ordering
	names := make([]string, 0, len(space))
	for k := range space {
		names = append(names, k)
	}
	sort.Strings(names)

	values := make([][]float64, len(names))
	for i, name := range names {
		values[i] = space[name]
	}

	paramCombos := cartesianProduct(values)

	var result []comboEntry
	for _, pc := range paramCombos {
		combo := make(Combination, len(names))
		for i, name := range names {
			combo[name] = pc[i]
		}
		for _, tp := range tps {
			for _, sl := range sls {
				result = append(result, comboEntry{combo: combo, tp: tp, sl: sl})
			}
		}
	}

	// If no param space defined, still try all tp/sl combinations
	if len(space) == 0 {
		for _, tp := range tps {
			for _, sl := range sls {
				result = append(result, comboEntry{combo: Combination{}, tp: tp, sl: sl})
			}
		}
	}

	return result
}

// cartesianProduct computes the Cartesian product of float64 slices.
func cartesianProduct(sets [][]float64) [][]float64 {
	if len(sets) == 0 {
		return [][]float64{{}}
	}
	rest := cartesianProduct(sets[1:])
	var result [][]float64
	for _, v := range sets[0] {
		for _, r := range rest {
			row := make([]float64, 0, 1+len(r))
			row = append(row, v)
			row = append(row, r...)
			result = append(result, row)
		}
	}
	return result
}

// substituteTemplate replaces JSON string placeholders "{{param_name}}" with float64 literals.
// Example: {"params":{"period":"{{rsi_period}}"}} + {"rsi_period":14} → {"params":{"period":14}}
func substituteTemplate(template json.RawMessage, combo Combination) (json.RawMessage, error) {
	s := string(template)
	for name, val := range combo {
		placeholder := `"{{` + name + `}}"`
		replacement := strconv.FormatFloat(val, 'f', -1, 64)
		s = strings.ReplaceAll(s, placeholder, replacement)
	}
	var check interface{}
	if err := json.Unmarshal([]byte(s), &check); err != nil {
		return nil, fmt.Errorf("substituteTemplate: invalid JSON after substitution: %w", err)
	}
	return json.RawMessage(s), nil
}

// scoreBacktest returns a scalar score for ranking backtest results.
func scoreBacktest(r BacktestResult, scoreBy string) float64 {
	if r.TotalSignals == 0 {
		return 0
	}
	switch scoreBy {
	case "win_rate":
		return r.WinRate
	case "avg_gain":
		return r.AvgGain
	default: // "profit_factor"
		if r.ProfitFactor == math.MaxFloat64 || math.IsInf(r.ProfitFactor, 1) {
			return 999
		}
		return r.ProfitFactor
	}
}

// splitWalkForwardWindows divides [from, to] into folds equal slices.
// Returns folds-1 windows with expanding in-sample and fixed-size out-of-sample.
func splitWalkForwardWindows(from, to time.Time, folds int) []WFWindow {
	if folds < 2 {
		return nil
	}
	step := to.Sub(from) / time.Duration(folds)
	windows := make([]WFWindow, 0, folds-1)
	for i := 0; i < folds-1; i++ {
		windows = append(windows, WFWindow{
			InFrom:  from,
			InTo:    from.Add(step * time.Duration(i+1)),
			OutFrom: from.Add(step * time.Duration(i+1)),
			OutTo:   from.Add(step * time.Duration(i+2)),
		})
	}
	return windows
}

// buildOptimizeResult sorts results by score descending and picks top N.
func buildOptimizeResult(results []RankedResult, topN int) OptimizeResult {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if topN <= 0 || topN > len(results) {
		topN = len(results)
	}
	top := results[:topN]
	best := Combination{}
	if len(top) > 0 {
		best = top[0].Params
	}
	return OptimizeResult{TopCombinations: top, BestParams: best}
}
```

- [ ] **Шаг 2: Commit**

```bash
git add services/signal-engine/optimizer.go
git commit -m "feat: optimizer core — grid search, walk-forward, param substitution"
```

---

## Task 2: Optimizer unit tests

**Files:**
- Create: `services/signal-engine/optimizer_test.go`

Тестируем только чистые функции (без DB). Каждый тест — изолированный.

- [ ] **Шаг 1: Создать services/signal-engine/optimizer_test.go**

```go
// services/signal-engine/optimizer_test.go
package main

import (
	"encoding/json"
	"math"
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

func TestCartesianProduct_TwoSets(t *testing.T) {
	result := cartesianProduct([][]float64{{1, 2}, {3, 4}})
	if len(result) != 4 {
		t.Fatalf("expected 4, got %d", len(result))
	}
	// verify each combination exists
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
	// FormatFloat with 'f' and -1 prec: 14.0 → "14", not "14.000000"
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

func TestScoreBacktest_ZeroSignals(t *testing.T) {
	r := BacktestResult{TotalSignals: 0, ProfitFactor: 99}
	if scoreBacktest(r, "profit_factor") != 0 {
		t.Errorf("expected 0 for empty result")
	}
}

func TestScoreBacktest_InfiniteProfitFactor(t *testing.T) {
	r := BacktestResult{TotalSignals: 5, ProfitFactor: math.MaxFloat64}
	if scoreBacktest(r, "profit_factor") != 999 {
		t.Errorf("expected 999 for MaxFloat64 profit factor, got %v", scoreBacktest(r, "profit_factor"))
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
		if w.OutTo.IsZero() {
			t.Errorf("window %d: OutTo is zero", i)
		}
	}
	last := windows[len(windows)-1]
	if !last.OutTo.Equal(to) {
		t.Errorf("last window OutTo should be %v, got %v", to, last.OutTo)
	}
}

func TestSplitWalkForwardWindows_TooFewFolds(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	if windows := splitWalkForwardWindows(from, to, 1); windows != nil {
		t.Error("expected nil for folds < 2")
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
	best := Combination{"period": 14}
	results := []RankedResult{
		{Score: 1.0, Params: Combination{"period": 20}},
		{Score: 3.0, Params: best},
	}
	out := buildOptimizeResult(results, 10)
	if out.BestParams["period"] != 14 {
		t.Errorf("BestParams should be from top-scored result")
	}
}
```

Обрати внимание: в тест-файле используется `strings.Contains` — нужен импорт `"strings"`. Добавь в import-блок.

- [ ] **Шаг 2: Исправить импорт в optimizer_test.go** — добавить `"strings"` в import-блок

Замени import-блок на:
```go
import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)
```

- [ ] **Шаг 3: Запустить тесты**

```bash
/c/Program\ Files/Go/bin/go test ./services/signal-engine/... -run "TestGenerate|TestCartesian|TestSubstitute|TestScore|TestSplit|TestBuild" -v
```

Ожидаемый вывод: все перечисленные тесты `PASS`.

- [ ] **Шаг 4: Commit**

```bash
git add services/signal-engine/optimizer_test.go
git commit -m "test: optimizer unit tests (pure functions)"
```

---

## Task 3: Optimizer Redis Streams consumer

**Files:**
- Create: `services/signal-engine/optimizer_consumer.go`

Consumer читает задания из `jobs:optimize`, вызывает RunGridSearch или RunWalkForward, сохраняет в `optimization_results`.

- [ ] **Шаг 1: Создать services/signal-engine/optimizer_consumer.go**

```go
// services/signal-engine/optimizer_consumer.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"sis/pkg/models"
)

const (
	streamOptimize        = "jobs:optimize"
	optimizeConsumerGroup = "signal-engine-optimize"
	optimizeConsumerName  = "optimizer-1"
	optimizeProgressFmt   = "jobs:%s:optimize:progress"
)

// OptimizeJobPayload is the Redis Streams message structure for optimization jobs.
type OptimizeJobPayload struct {
	JobID              string          `json:"job_id"`
	SignalID           string          `json:"signal_id"`
	Symbol             string          `json:"symbol"`
	Market             string          `json:"market"`
	Timeframe          string          `json:"timeframe"`
	Exchange           string          `json:"exchange"`
	Direction          string          `json:"direction"`
	PeriodFrom         string          `json:"period_from"` // RFC3339
	PeriodTo           string          `json:"period_to"`   // RFC3339
	Mode               string          `json:"mode"`        // "fast" | "walk_forward"
	ScoreBy            string          `json:"score_by"`    // "profit_factor" | "win_rate" | "avg_gain"
	TopN               int             `json:"top_n"`
	TakeProfits        []float64       `json:"take_profits"`
	StopLosses         []float64       `json:"stop_losses"`
	ConditionsTemplate json.RawMessage `json:"conditions_template"`
	ParamSpace         ParamSpace      `json:"param_space"`
	WFFolds            int             `json:"wf_folds"` // walk-forward folds, default 4
}

// RunOptimizer starts the optimizer job consumer. Blocks until ctx is cancelled.
func (w *Worker) RunOptimizer(ctx context.Context) {
	w.rdb.XGroupCreateMkStream(ctx, streamOptimize, optimizeConsumerGroup, "0")

	log.Printf("optimizer: listening on stream %s", streamOptimize)
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    optimizeConsumerGroup,
			Consumer: optimizeConsumerName,
			Streams:  []string{streamOptimize, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("optimizer: xreadgroup error: %v", err)
			continue
		}

		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				w.handleOptimizeMessage(ctx, msg)
			}
		}
	}
}

func (w *Worker) handleOptimizeMessage(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["payload"]
	if !ok {
		log.Printf("optimizer: message %s missing payload", msg.ID)
		w.ackOptimize(ctx, msg.ID)
		return
	}

	var payload OptimizeJobPayload
	if err := json.Unmarshal([]byte(raw.(string)), &payload); err != nil {
		log.Printf("optimizer: unmarshal job %s: %v", msg.ID, err)
		w.ackOptimize(ctx, msg.ID)
		return
	}

	log.Printf("optimizer: processing job %s signal=%s mode=%s", payload.JobID, payload.SignalID, payload.Mode)

	if err := w.runOptimizeJob(ctx, payload); err != nil {
		log.Printf("optimizer: job %s failed: %v", payload.JobID, err)
	}
	w.ackOptimize(ctx, msg.ID)
}

func (w *Worker) runOptimizeJob(ctx context.Context, payload OptimizeJobPayload) error {
	from, err := time.Parse(time.RFC3339, payload.PeriodFrom)
	if err != nil {
		return fmt.Errorf("parse period_from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, payload.PeriodTo)
	if err != nil {
		return fmt.Errorf("parse period_to: %w", err)
	}

	topN := payload.TopN
	if topN <= 0 {
		topN = 10
	}
	folds := payload.WFFolds
	if folds <= 0 {
		folds = 4
	}
	scoreBy := payload.ScoreBy
	if scoreBy == "" {
		scoreBy = "profit_factor"
	}
	tps := payload.TakeProfits
	if len(tps) == 0 {
		tps = []float64{2.0}
	}
	sls := payload.StopLosses
	if len(sls) == 0 {
		sls = []float64{1.0}
	}

	job := OptimizeJob{
		SignalID:           payload.SignalID,
		Symbol:             payload.Symbol,
		Market:             models.Market(payload.Market),
		Timeframe:          models.Timeframe(payload.Timeframe),
		Exchange:           models.Exchange(payload.Exchange),
		Direction:          payload.Direction,
		PeriodFrom:         from,
		PeriodTo:           to,
		Mode:               payload.Mode,
		ScoreBy:            scoreBy,
		TopN:               topN,
		TakeProfits:        tps,
		StopLosses:         sls,
		ConditionsTemplate: payload.ConditionsTemplate,
		ParamSpace:         payload.ParamSpace,
		WFFolds:            folds,
	}

	progressKey := fmt.Sprintf(optimizeProgressFmt, payload.JobID)
	progress := func(pct int) {
		w.rdb.HSet(ctx, progressKey, "pct", pct, "updated_at", time.Now().Unix())
	}
	progress(0)

	var result OptimizeResult
	switch payload.Mode {
	case "walk_forward":
		result, err = RunWalkForward(ctx, w.pool, job, progress)
	default:
		result, err = RunGridSearch(ctx, w.pool, job, progress)
	}
	if err != nil {
		return fmt.Errorf("run optimize: %w", err)
	}

	if err := w.saveOptimizeResult(ctx, payload, result); err != nil {
		return fmt.Errorf("save optimize result: %w", err)
	}

	w.rdb.HSet(ctx, progressKey, "pct", 100, "status", "done", "updated_at", time.Now().Unix())
	log.Printf("optimizer: job %s done — %d top combinations", payload.JobID, len(result.TopCombinations))
	return nil
}

func (w *Worker) saveOptimizeResult(ctx context.Context, payload OptimizeJobPayload, r OptimizeResult) error {
	topJSON, _ := json.Marshal(r.TopCombinations)
	bestJSON, _ := json.Marshal(r.BestParams)
	jobParamsJSON, _ := json.Marshal(payload)

	mode := payload.Mode
	if mode == "" {
		mode = "fast"
	}

	_, err := w.pool.Exec(ctx, `
		INSERT INTO optimization_results
			(signal_id, job_params, mode, top_combinations, best_params)
		VALUES ($1, $2, $3, $4, $5)`,
		payload.SignalID,
		jobParamsJSON,
		mode,
		topJSON,
		bestJSON,
	)
	return err
}

func (w *Worker) ackOptimize(ctx context.Context, msgID string) {
	if err := w.rdb.XAck(ctx, streamOptimize, optimizeConsumerGroup, msgID).Err(); err != nil {
		log.Printf("optimizer: ack error %s: %v", msgID, err)
	}
}
```

- [ ] **Шаг 2: Commit**

```bash
git add services/signal-engine/optimizer_consumer.go
git commit -m "feat: optimizer Redis Streams consumer (jobs:optimize)"
```

---

## Task 4: Wire up in main.go + build + full test run

**Files:**
- Modify: `services/signal-engine/main.go`

Запускаем оба consumer-а параллельно через goroutine.

- [ ] **Шаг 1: Обновить services/signal-engine/main.go**

Текущий код вызывает `worker.Start(ctx)` синхронно. Добавь горутину для optimizer перед ним.

Замени последние строки `main()` (начиная с `worker := NewWorker(...)`) на:

```go
	worker := NewWorker(pool, rdb)
	log.Println("signal-engine: starting")
	go worker.RunOptimizer(ctx)
	worker.Start(ctx)
	log.Println("signal-engine: stopped")
```

- [ ] **Шаг 2: Собрать бинарник**

```bash
/c/Program\ Files/Go/bin/go build -o bin/signal-engine ./services/signal-engine/
```

Ожидаемый вывод: нет ошибок, файл `bin/signal-engine` обновлён.

- [ ] **Шаг 3: Запустить все тесты**

```bash
/c/Program\ Files/Go/bin/go test ./...
```

Ожидаемый вывод: все пакеты `PASS`, нет ошибок компиляции.

- [ ] **Шаг 4: Commit**

```bash
git add services/signal-engine/main.go
git commit -m "feat: wire optimizer consumer into signal-engine main"
```

---

## Self-Review

**Spec coverage:**
- ✅ Grid Search — перебор комбинаций параметров (индикаторные периоды + TP/SL) — Task 1 (RunGridSearch)
- ✅ Walk-Forward Validation — in-sample grid search → out-of-sample test — Task 1 (RunWalkForward)
- ✅ Параметризованный шаблон условий через JSON-плейсхолдеры `"{{param}}"` — Task 1 (substituteTemplate)
- ✅ Ранжирование по profit_factor / win_rate / avg_gain — Task 1 (scoreBacktest, buildOptimizeResult)
- ✅ Прогресс задания в Redis — Task 3 (progress callback)
- ✅ Сохранение в optimization_results — Task 3 (saveOptimizeResult)
- ✅ Redis Streams consumer (jobs:optimize) — Task 3
- ✅ Unit-тесты для всех чистых функций — Task 2

**Pending для Plan 4:**
- ⚠️ REST API для создания заданий оптимизации (POST /signals/:id/optimize)
- ⚠️ WebSocket прогресс-события для UI

---

## Следующие планы

- **Plan 4:** API Gateway + Auth + WebSocket
- **Plan 5:** Webhook Dispatcher
- **Plan 6:** React Frontend
