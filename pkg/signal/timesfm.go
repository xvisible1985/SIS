package signal

import (
	"log"
	"time"
)

// timesfmSignal reads a symbol+timeframe forecast from the in-memory cache
// (timesfm_state.go), kicking off an async refresh via TimesfmRefreshFunc whenever the
// cached entry is missing or older than refresh_interval_sec. Mirrors whaleSignal's shape
// (signals.go) but adds TTL and inflight dedup: a real refresh means an HTTP call to an
// external model service plus a DB write, so — unlike whale's plain cache read — this
// signal is also the thing that decides WHEN a refresh is worth paying for.
type timesfmSignal struct {
	contextBars        float64
	horizonBars        float64
	thresholdPct       float64
	refreshIntervalSec float64
}

func (s *timesfmSignal) Compute(_ []Candle) State { return Neutral }

func (s *timesfmSignal) ComputeWithSymbol(symbol string, candles []Candle) State {
	tf := inferTimeframe(candles)
	if tf == "" {
		return Neutral
	}
	contextBars := int(s.contextBars)
	horizonBars := int(s.horizonBars)
	maxAge := time.Duration(s.refreshIntervalSec) * time.Second

	predictedPct, fresh := GetTimesfmForecast(symbol, tf, contextBars, horizonBars, maxAge)
	if !fresh && TimesfmRefreshFunc != nil && tryStartTimesfmRefresh(symbol, tf, contextBars, horizonBars, maxAge) {
		context := candles
		// contextBars > 0 guards against a misconfigured (e.g. negative) context_bars bot
		// param slicing out of bounds here — this runs synchronously, BEFORE the goroutine
		// below (and its recover()) is even entered, so an unguarded negative index here
		// would crash the whole process, not just this signal. A non-positive contextBars
		// falls back to "use all available history" rather than panicking.
		if contextBars > 0 && len(context) > contextBars {
			context = context[len(context)-contextBars:]
		}
		refresh := TimesfmRefreshFunc
		go func() {
			defer finishTimesfmRefresh(symbol, tf, contextBars, horizonBars)
			// recover() here matters beyond just this one forecast: TimesfmRefreshFunc's
			// real implementation (a later task) makes an HTTP call to an external service
			// this process doesn't control — an unrecovered panic in ANY goroutine, including
			// this one, crashes the entire api-gateway process (all live trading, not just
			// this signal). finishTimesfmRefresh still runs via defer either way (Go runs
			// deferred calls during panic unwinding), so without this recover the failure
			// mode wouldn't even be "this forecast stays stale" — it would take down every
			// bot this process is running.
			defer func() {
				if r := recover(); r != nil {
					log.Printf("timesfm: refresh panic for %s/%s: %v", symbol, tf, r)
				}
			}()
			// contextBars is passed explicitly (not re-derived from len(context)) — see
			// TimesfmRefreshFunc's doc comment in timesfm_state.go for why: context can be
			// shorter than contextBars when a symbol/timeframe hasn't accumulated enough
			// history yet, and the eventual SetTimesfmForecast call must cache under the
			// SAME key this ComputeWithSymbol call (and every future one) reads from.
			refresh(symbol, tf, context, contextBars, horizonBars)
		}()
	}
	return deriveTimesfmState(predictedPct, s.thresholdPct)
}

// deriveTimesfmState turns a raw predicted percentage into Buy/Sell/Neutral against a
// caller-supplied threshold — kept separate from the cache (timesfm_state.go) so two bots
// referencing this signal on the same symbol/timeframe with different threshold_pct each
// see the state their own configured threshold implies, from the same underlying forecast.
func deriveTimesfmState(predictedPct, thresholdPct float64) State {
	switch {
	case predictedPct >= thresholdPct:
		return Buy
	case predictedPct <= -thresholdPct:
		return Sell
	default:
		return Neutral
	}
}

// timesfmIntervalsMs maps a candle's bar duration (ms) to this codebase's canonical
// timeframe strings — the same values pkg/models' TF1m/TF5m/etc. and the candles table's
// timeframe column already use everywhere else. ComputeWithSymbol has no timeframe
// parameter (the SymbolComputer interface only carries symbol+candles), so it's inferred
// from candle spacing instead of threading a new parameter through pkg/signal's public
// interface.
var timesfmIntervalsMs = map[int64]string{
	60_000:     "1m",
	300_000:    "5m",
	900_000:    "15m",
	1_800_000:  "30m",
	3_600_000:  "1h",
	14_400_000: "4h",
	86_400_000: "1d",
}

func inferTimeframe(candles []Candle) string {
	if len(candles) < 2 {
		return ""
	}
	delta := candles[len(candles)-1].Time - candles[len(candles)-2].Time
	return timesfmIntervalsMs[delta]
}
