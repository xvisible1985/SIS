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

		inJob := job
		inJob.PeriodFrom = w.InFrom
		inJob.PeriodTo = w.InTo
		inResult, err := RunGridSearch(ctx, pool, inJob, nil)
		if err != nil || len(inResult.TopCombinations) == 0 {
			continue
		}

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
				cp := make(Combination, len(combo))
				for k, v := range combo {
					cp[k] = v
				}
				result = append(result, comboEntry{combo: cp, tp: tp, sl: sl})
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
		// computeMetrics sets MaxFloat64 when totalLoss==0; cap to avoid comparison issues
		if r.ProfitFactor >= math.MaxFloat64 {
			return math.MaxFloat64
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
		outTo := from.Add(step * time.Duration(i+2))
		if i == folds-2 {
			outTo = to
		}
		windows = append(windows, WFWindow{
			InFrom:  from,
			InTo:    from.Add(step * time.Duration(i+1)),
			OutFrom: from.Add(step * time.Duration(i+1)),
			OutTo:   outTo,
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
	top := make([]RankedResult, topN)
	copy(top, results[:topN])
	best := Combination{}
	if len(top) > 0 {
		best = top[0].Params
	}
	return OptimizeResult{TopCombinations: top, BestParams: best}
}
