# TimesFM experimental signal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new experimental `timesfm` catalog signal (Google TimesFM forecasts) that
bots can select for entries/exits and that appears on the Webhooks page, backed by a small
local Python inference service, with every real model prediction logged to a new table and
backfilled with its actual outcome for later accuracy review.

**Architecture:** `pkg/signal` gets a new symbol-keyed, TTL-aware in-memory cache (mirrors
the existing `whale`/`leverage` signals) that never touches a DB or makes HTTP calls itself.
`services/api-gateway` owns a `signal.TimesfmRefreshFunc` hook it sets at startup — that
closure is the only thing that calls the Python service and writes to Postgres, keeping
`pkg/signal`'s existing "never reaches out on its own" invariant intact. A separate periodic
job backfills prediction outcomes once each forecast's horizon has passed. The Python model
service is a brand-new component (no existing Python convention in this repo to mirror) —
a small FastAPI app run directly with `uvicorn`, not yet containerized.

**Tech Stack:** Go 1.25 (`pkg/signal`, `services/api-gateway`), PostgreSQL/TimescaleDB
migration, Python 3.11+ / FastAPI / uvicorn / `timesfm` package, React/TypeScript frontend.

**Design spec:** `docs/superpowers/specs/2026-09-17-timesfm-signal-design.md`. Two
refinements made during planning that go beyond the spec (both explained inline where they
appear below, not silently different):
1. The in-memory cache stores the raw `predicted_pct`, not a baked-in Buy/Sell/Neutral
   state — because two different bots can reference `timesfm` on the same symbol/timeframe
   with different `threshold_pct`, and deriving state at read time (from each caller's own
   threshold) avoids one bot's config silently overriding another's.
2. `pkg/signal` gets an exported `TimesfmRefreshFunc` hook variable instead of doing the
   HTTP/DB work inline, mirroring the existing `OnAccumulate`-hook pattern in
   `pkg/strategy/trade_recorder.go` — this preserves the documented invariant (see
   `pkg/signal/leverage_state.go`'s comment) that `pkg/signal` never reaches out to a DB or
   exchange/external API itself.

---

## Task 1: Database migration

**Files:**
- Create: `migrations/094_timesfm_predictions.sql`

- [ ] **Step 1: Write the migration**

```sql
-- migrations/094_timesfm_predictions.sql
-- Registers the new "timesfm" catalog signal (pkg/signal/timesfm.go) and creates its
-- prediction log. Every real model call (never a cache hit — see timesfm.go) writes one row
-- here; services/api-gateway's accuracy backfill job (timesfm_accuracy_job.go) fills in the
-- outcome once the forecast's horizon has passed. This is a platform-wide model-evaluation
-- log, not per-user data (no owner_id) — unlike custom_signals, there's exactly one shared
-- "timesfm" signal definition, not user-created ones.

INSERT INTO signal_types (id, name, status, panel)
VALUES ('timesfm', 'TimesFM Forecast', 'enabled', 'signal')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS timesfm_predictions (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  symbol              TEXT NOT NULL,
  timeframe           TEXT NOT NULL,
  context_bars        INT NOT NULL,
  horizon_bars        INT NOT NULL,
  predicted_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  price_at_predict    NUMERIC(18,8) NOT NULL,
  predicted_pct       NUMERIC(10,4) NOT NULL,
  predicted_direction TEXT NOT NULL,
  target_at           TIMESTAMPTZ NOT NULL,
  actual_price        NUMERIC(18,8),
  actual_direction    TEXT,
  correct             BOOLEAN,
  checked_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS timesfm_predictions_pending
  ON timesfm_predictions (target_at) WHERE actual_price IS NULL;

CREATE INDEX IF NOT EXISTS timesfm_predictions_symbol
  ON timesfm_predictions (symbol, predicted_at DESC);
```

- [ ] **Step 2: Apply the migration to the local database**

Run: `docker exec -i sis-timescaledb-1 psql -U sis -d sis < migrations/094_timesfm_predictions.sql`
Expected: `INSERT 0 1` (or `INSERT 0 0` if re-run), `CREATE TABLE`, `CREATE INDEX`, `CREATE INDEX` — no errors.

- [ ] **Step 3: Verify**

Run: `docker exec sis-timescaledb-1 psql -U sis -d sis -c "\d timesfm_predictions"`
Expected: table description listing all 13 columns above.

Run: `docker exec sis-timescaledb-1 psql -U sis -d sis -c "SELECT id, name, status, panel FROM signal_types WHERE id='timesfm'"`
Expected: one row `timesfm | TimesFM Forecast | enabled | signal`.

- [ ] **Step 4: Commit**

```bash
git add migrations/094_timesfm_predictions.sql
git commit -m "feat(signal): add timesfm_predictions table and signal_types row"
```

---

## Task 2: In-memory forecast cache (`pkg/signal`)

**Files:**
- Create: `pkg/signal/timesfm_state.go`
- Test: `pkg/signal/timesfm_state_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/signal/timesfm_state_test.go
package signal

import (
	"testing"
	"time"
)

func TestTimesfmForecast_MissingKey_ReturnsNotFresh(t *testing.T) {
	pct, fresh := GetTimesfmForecast("TFSTATE_MISSING", "5m", 100, 12, time.Minute)
	if fresh {
		t.Error("fresh = true for a key that was never set, want false")
	}
	if pct != 0 {
		t.Errorf("predictedPct = %v, want 0 for a missing key", pct)
	}
}

func TestTimesfmForecast_SetThenGet_FreshWithinMaxAge(t *testing.T) {
	SetTimesfmForecast("TFSTATE_FRESH", "5m", 100, 12, 1.23)
	pct, fresh := GetTimesfmForecast("TFSTATE_FRESH", "5m", 100, 12, time.Minute)
	if !fresh {
		t.Error("fresh = false immediately after Set, want true")
	}
	if pct != 1.23 {
		t.Errorf("predictedPct = %v, want 1.23", pct)
	}
}

func TestTimesfmForecast_StaleAfterMaxAge(t *testing.T) {
	SetTimesfmForecast("TFSTATE_STALE", "5m", 100, 12, 5.0)
	time.Sleep(5 * time.Millisecond)
	pct, fresh := GetTimesfmForecast("TFSTATE_STALE", "5m", 100, 12, time.Millisecond)
	if fresh {
		t.Error("fresh = true after maxAge elapsed, want false")
	}
	// The last known value is still returned even when stale — callers derive a
	// best-effort state from it while a fresh refresh is (maybe) in flight.
	if pct != 5.0 {
		t.Errorf("predictedPct = %v, want 5.0 (stale but still returned)", pct)
	}
}

func TestTimesfmForecast_DifferentContextOrHorizon_DoesNotCollide(t *testing.T) {
	SetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 12, 1.0)
	SetTimesfmForecast("TFSTATE_KEYS", "5m", 200, 12, 2.0)
	SetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 24, 3.0)

	if pct, _ := GetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 12, time.Minute); pct != 1.0 {
		t.Errorf("context=100/horizon=12 = %v, want 1.0", pct)
	}
	if pct, _ := GetTimesfmForecast("TFSTATE_KEYS", "5m", 200, 12, time.Minute); pct != 2.0 {
		t.Errorf("context=200/horizon=12 = %v, want 2.0", pct)
	}
	if pct, _ := GetTimesfmForecast("TFSTATE_KEYS", "5m", 100, 24, time.Minute); pct != 3.0 {
		t.Errorf("context=100/horizon=24 = %v, want 3.0", pct)
	}
}

func TestTimesfmRefresh_InflightDedup(t *testing.T) {
	if !tryStartTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12) {
		t.Fatal("first tryStartTimesfmRefresh = false, want true")
	}
	if tryStartTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12) {
		t.Error("second tryStartTimesfmRefresh while first still in flight = true, want false")
	}
	finishTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12)
	if !tryStartTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12) {
		t.Error("tryStartTimesfmRefresh after finish = false, want true")
	}
	finishTimesfmRefresh("TFSTATE_INFLIGHT", "5m", 100, 12) // cleanup
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/signal/... -run TestTimesfmForecast -v`
Expected: FAIL with `undefined: GetTimesfmForecast` (or similar — the file doesn't exist yet).

- [ ] **Step 3: Write the implementation**

```go
// pkg/signal/timesfm_state.go
package signal

import (
	"fmt"
	"sync"
	"time"
)

// timesfmEntry is one cached forecast result for a symbol/timeframe/context/horizon key.
type timesfmEntry struct {
	predictedPct float64
	updatedAt    time.Time
}

var timesfmCache struct {
	mu       sync.RWMutex
	entries  map[string]timesfmEntry
	inflight map[string]bool
}

func init() {
	timesfmCache.entries = make(map[string]timesfmEntry)
	timesfmCache.inflight = make(map[string]bool)
}

func timesfmCacheKey(symbol, timeframe string, contextBars, horizonBars int) string {
	return fmt.Sprintf("%s:%s:%d:%d", symbol, timeframe, contextBars, horizonBars)
}

// SetTimesfmForecast updates the cached forecast for a symbol/timeframe/context/horizon
// combination. Called only from services/api-gateway's TimesfmRefreshFunc once a real model
// call finishes — pkg/signal never calls this on its own. Only the raw predicted percentage
// is cached, NOT a Buy/Sell/Neutral state: two different bots can reference this signal on
// the same symbol/timeframe with different threshold_pct, so state must be derived at read
// time from each caller's own threshold (see timesfm.go's deriveTimesfmState), not baked in
// here from whichever config happened to trigger the refresh.
func SetTimesfmForecast(symbol, timeframe string, contextBars, horizonBars int, predictedPct float64) {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.Lock()
	timesfmCache.entries[key] = timesfmEntry{predictedPct: predictedPct, updatedAt: time.Now()}
	timesfmCache.mu.Unlock()
}

// GetTimesfmForecast returns the cached predicted percentage and whether the entry is still
// within maxAge. A missing entry returns (0, false). A stale entry still returns its last
// known value (fresh=false) — callers use the value as a best-effort answer while a refresh
// may or may not be in flight.
func GetTimesfmForecast(symbol, timeframe string, contextBars, horizonBars int, maxAge time.Duration) (predictedPct float64, fresh bool) {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.RLock()
	defer timesfmCache.mu.RUnlock()
	e, ok := timesfmCache.entries[key]
	if !ok {
		return 0, false
	}
	return e.predictedPct, time.Since(e.updatedAt) < maxAge
}

// tryStartTimesfmRefresh marks a key as having a refresh in flight and returns true if this
// call is the one that should actually run it — false means another goroutine already has
// one in flight for the same key, and the caller must not start a second.
func tryStartTimesfmRefresh(symbol, timeframe string, contextBars, horizonBars int) bool {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.Lock()
	defer timesfmCache.mu.Unlock()
	if timesfmCache.inflight[key] {
		return false
	}
	timesfmCache.inflight[key] = true
	return true
}

// finishTimesfmRefresh clears the in-flight flag for a key. Must be called (typically via
// defer) once a refresh started by tryStartTimesfmRefresh completes, success or failure.
func finishTimesfmRefresh(symbol, timeframe string, contextBars, horizonBars int) {
	key := timesfmCacheKey(symbol, timeframe, contextBars, horizonBars)
	timesfmCache.mu.Lock()
	delete(timesfmCache.inflight, key)
	timesfmCache.mu.Unlock()
}

// TimesfmRefreshFunc, when set, is invoked from a background goroutine (never inline on the
// signal engine's hot compute path — see timesfm.go's ComputeWithSymbol) whenever the cache
// is stale for a symbol/timeframe/context/horizon combination. Left nil by default —
// services/api-gateway sets it once at startup (main.go), since it owns the DB pool and the
// HTTP client the real refresh needs, neither of which pkg/signal itself ever holds. Mirrors
// why whale/leverage state is only ever pushed in from outside, never fetched by pkg/signal
// itself (see whale_state.go, leverage_state.go).
//
// contextBars is passed explicitly — NOT inferred from len(candles) by the receiver — and
// the caller MUST pass the exact same contextBars value it used for the
// GetTimesfmForecast/tryStartTimesfmRefresh/finishTimesfmRefresh calls around this refresh.
// candles can legitimately be SHORTER than contextBars (e.g. a symbol/timeframe that hasn't
// accumulated contextBars worth of history yet) — if the receiver were to key its
// SetTimesfmForecast call on len(candles) instead of the passed-in contextBars, a
// short-history call would cache its result under a DIFFERENT key than every future read
// ever queries, permanently missing the cache and re-triggering a real model call (an
// external HTTP round-trip plus a DB insert) on every single tick for that symbol until
// history happens to reach exactly contextBars candles — silently defeating the entire
// point of this cache. Always thread the caller's contextBars through untouched.
var TimesfmRefreshFunc func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/signal/... -run TestTimesfm -v`
Expected: `PASS` for all 5 tests (`TestTimesfmForecast_MissingKey_ReturnsNotFresh`,
`TestTimesfmForecast_SetThenGet_FreshWithinMaxAge`, `TestTimesfmForecast_StaleAfterMaxAge`,
`TestTimesfmForecast_DifferentContextOrHorizon_DoesNotCollide`, `TestTimesfmRefresh_InflightDedup`).

- [ ] **Step 5: Commit**

```bash
git add pkg/signal/timesfm_state.go pkg/signal/timesfm_state_test.go
git commit -m "feat(signal): add timesfm forecast cache with TTL and inflight dedup"
```

---

## Task 3: `timesfm` signal implementation and registration

**Files:**
- Create: `pkg/signal/timesfm.go`
- Modify: `pkg/signal/registry.go` (add `Register("timesfm", ...)` inside the existing `init()`)
- Test: `pkg/signal/timesfm_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/signal/timesfm_test.go
package signal

import (
	"testing"
	"time"
)

func TestInferTimeframe_KnownSpacings(t *testing.T) {
	cases := []struct {
		deltaMs int64
		want    string
	}{
		{60_000, "1m"},
		{300_000, "5m"},
		{900_000, "15m"},
		{3_600_000, "1h"},
		{14_400_000, "4h"},
		{86_400_000, "1d"},
	}
	for _, c := range cases {
		candles := []Candle{{Time: 1000}, {Time: 1000 + c.deltaMs}}
		if got := inferTimeframe(candles); got != c.want {
			t.Errorf("inferTimeframe(delta=%dms) = %q, want %q", c.deltaMs, got, c.want)
		}
	}
}

func TestInferTimeframe_TooFewCandles(t *testing.T) {
	if got := inferTimeframe(nil); got != "" {
		t.Errorf("inferTimeframe(nil) = %q, want empty", got)
	}
	if got := inferTimeframe([]Candle{{Time: 1000}}); got != "" {
		t.Errorf("inferTimeframe(1 candle) = %q, want empty", got)
	}
}

func TestInferTimeframe_UnknownSpacing(t *testing.T) {
	candles := []Candle{{Time: 0}, {Time: 12345}}
	if got := inferTimeframe(candles); got != "" {
		t.Errorf("inferTimeframe(unrecognized delta) = %q, want empty", got)
	}
}

func TestDeriveTimesfmState(t *testing.T) {
	cases := []struct {
		predictedPct, thresholdPct float64
		want                       State
	}{
		{2.0, 0.5, Buy},
		{0.5, 0.5, Buy},   // exactly at threshold counts as Buy
		{-2.0, 0.5, Sell},
		{-0.5, 0.5, Sell}, // exactly at -threshold counts as Sell
		{0.2, 0.5, Neutral},
		{-0.2, 0.5, Neutral},
		{0, 0.5, Neutral},
	}
	for _, c := range cases {
		if got := deriveTimesfmState(c.predictedPct, c.thresholdPct); got != c.want {
			t.Errorf("deriveTimesfmState(%.2f, %.2f) = %v, want %v", c.predictedPct, c.thresholdPct, got, c.want)
		}
	}
}

func TestTimesfmSignal_ComputeWithSymbol_ColdCache_TriggersRefreshOnce(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	calls := make(chan struct{}, 10)
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) {
		// A real refresh is an HTTP round-trip plus a DB write — comfortably longer than the
		// time it takes 5 goroutines to get scheduled, which is why the dedup guard
		// (tryStartTimesfmRefresh/finishTimesfmRefresh) is safe in production. A truly
		// instant mock doesn't reproduce that: on a many-core machine, the FIRST winning
		// goroutine's whole refresh-and-clear-inflight cycle can complete before the other 4
		// outer goroutines below even get scheduled onto a thread, so they'd legitimately
		// see the flag already cleared and correctly start a second refresh — not a bug in
		// the dedup guard (mutual exclusion during an ACTUAL in-flight window is exactly what
		// it promises), just a test racing against goroutine-dispatch jitter instead of
		// against realistic refresh latency. This sleep restores that realism.
		time.Sleep(50 * time.Millisecond)
		calls <- struct{}{}
	}

	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 150)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0} // 5m spacing
	}

	// Fire 5 concurrent calls for the same symbol — only one refresh must start.
	done := make(chan State, 5)
	for i := 0; i < 5; i++ {
		go func() { done <- s.ComputeWithSymbol("TFSIG_COLD", candles) }()
	}
	for i := 0; i < 5; i++ {
		<-done
	}

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("TimesfmRefreshFunc was never called")
	}
	select {
	case <-calls:
		t.Fatal("TimesfmRefreshFunc was called more than once for concurrent requests on the same key")
	case <-time.After(200 * time.Millisecond):
		// Comfortably longer than the mock's own 50ms sleep, so a genuine second call (were
		// the dedup guard actually broken) would have arrived on `calls` well within this
		// window — this isn't racing against the mock's latency the way a tighter window
		// would.
	}
}

func TestTimesfmSignal_ComputeWithSymbol_FreshCache_DoesNotRefresh(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	SetTimesfmForecast("TFSIG_FRESH", "5m", 100, 12, 1.0)
	called := false
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) { called = true }

	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 150)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0}
	}

	got := s.ComputeWithSymbol("TFSIG_FRESH", candles)
	if called {
		t.Error("TimesfmRefreshFunc was called even though the cache entry is fresh")
	}
	if got != Buy {
		t.Errorf("state = %v, want Buy (predictedPct=1.0 >= threshold=0.5)", got)
	}
}

func TestTimesfmSignal_ComputeWithSymbol_UnrecognizedSpacing_ReturnsNeutral(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()
	called := false
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) { called = true }

	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := []Candle{{Time: 0, Close: 1.0}, {Time: 12345, Close: 1.0}}

	if got := s.ComputeWithSymbol("TFSIG_BADTF", candles); got != Neutral {
		t.Errorf("state = %v, want Neutral for unrecognized candle spacing", got)
	}
	if called {
		t.Error("TimesfmRefreshFunc was called despite an unrecognized timeframe")
	}
}

// TestTimesfmSignal_ComputeWithSymbol_ShortHistory_PassesConfiguredContextBars is the
// regression for a cache-key mismatch bug caught in code review before this task was ever
// implemented: if ComputeWithSymbol has fewer candles available than s.contextBars (e.g. a
// symbol/timeframe that hasn't accumulated enough history yet), it must still pass the
// CONFIGURED contextBars (not len(candles)) to TimesfmRefreshFunc — otherwise the eventual
// SetTimesfmForecast call (made by the refresh function, in a later task) would cache its
// result under a different key than every future GetTimesfmForecast call ever queries,
// permanently missing the cache for that symbol/timeframe.
func TestTimesfmSignal_ComputeWithSymbol_ShortHistory_PassesConfiguredContextBars(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	var gotContextBars int
	var gotCandleLen int
	calls := make(chan struct{}, 1)
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) {
		gotContextBars = contextBars
		gotCandleLen = len(candles)
		calls <- struct{}{}
	}

	// contextBars=100 configured, but only 40 candles actually available — shorter history.
	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 40)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0} // 5m spacing
	}

	s.ComputeWithSymbol("TFSIG_SHORTHIST", candles)

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("TimesfmRefreshFunc was never called")
	}
	if gotContextBars != 100 {
		t.Errorf("contextBars passed to TimesfmRefreshFunc = %d, want 100 (the configured value, not len(candles))", gotContextBars)
	}
	if gotCandleLen != 40 {
		t.Errorf("len(candles) passed to TimesfmRefreshFunc = %d, want 40 (all available candles, unpadded)", gotCandleLen)
	}
}

// TestTimesfmSignal_ComputeWithSymbol_NegativeContextBars_DoesNotPanic is the regression for
// a crash found in code review: a misconfigured (e.g. negative) context_bars bot param would
// have hit `context[len(context)-contextBars:]` with an out-of-range index — and this slice
// arithmetic runs synchronously in ComputeWithSymbol, BEFORE the goroutine (and its
// recover()) is ever entered, so it would have crashed the whole process, not just this
// signal. contextBars <= 0 must fall back to "use all available history" instead.
func TestTimesfmSignal_ComputeWithSymbol_NegativeContextBars_DoesNotPanic(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	var gotCandleLen int
	calls := make(chan struct{}, 1)
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) {
		gotCandleLen = len(candles)
		calls <- struct{}{}
	}

	s := &timesfmSignal{contextBars: -5, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 150)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0} // 5m spacing
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ComputeWithSymbol panicked with negative contextBars: %v", r)
		}
	}()
	s.ComputeWithSymbol("TFSIG_NEGCTX", candles)

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("TimesfmRefreshFunc was never called")
	}
	if gotCandleLen != 150 {
		t.Errorf("len(candles) passed to TimesfmRefreshFunc = %d, want 150 (all available candles, since contextBars<=0 must not truncate)", gotCandleLen)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/signal/... -run TestInferTimeframe -v`
Expected: FAIL with `undefined: inferTimeframe`.

- [ ] **Step 3: Write the implementation**

```go
// pkg/signal/timesfm.go
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
	if !fresh && TimesfmRefreshFunc != nil && tryStartTimesfmRefresh(symbol, tf, contextBars, horizonBars) {
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
```

- [ ] **Step 4: Register the signal**

In `pkg/signal/registry.go`, inside the existing `init()` function, add this registration
right after the `Register("leverage", ...)` block:

```go
	Register("timesfm", func(cfg Config) Signal {
		return &timesfmSignal{
			contextBars:        cfg.Float("context_bars", 512),
			horizonBars:        cfg.Float("horizon_bars", 12),
			thresholdPct:       cfg.Float("threshold_pct", 0.5),
			refreshIntervalSec: cfg.Float("refresh_interval_sec", 300),
		}
	})
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/signal/... -run "TestInferTimeframe|TestDeriveTimesfmState|TestTimesfmSignal" -v`
Expected: `PASS` for all 8 tests (the 6 originally listed, plus
`TestTimesfmSignal_ComputeWithSymbol_ShortHistory_PassesConfiguredContextBars` and
`TestTimesfmSignal_ComputeWithSymbol_NegativeContextBars_DoesNotPanic`).

- [ ] **Step 6: Run the full `pkg/signal` suite for regressions**

Run: `go test ./pkg/signal/...`
Expected: `ok` — no existing test (whale, leverage, price-change, etc.) is affected by this
change; this step confirms it.

- [ ] **Step 7: Commit**

```bash
git add pkg/signal/timesfm.go pkg/signal/timesfm_test.go pkg/signal/registry.go
git commit -m "feat(signal): add timesfm signal, registered in the catalog"
```

---

## Task 4: Refresh function (`services/api-gateway`) — calls the Python service, writes the cache and the prediction log

**Files:**
- Create: `services/api-gateway/timesfm_refresh.go`
- Test: `services/api-gateway/timesfm_refresh_test.go` (integration-tagged — writes to the
  real local Postgres)

- [ ] **Step 1: Write the failing test**

```go
// services/api-gateway/timesfm_refresh_test.go
//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sis/pkg/signal"
)

func TestNewTimesfmRefreshFunc_SuccessfulCall_UpdatesCacheAndInsertsRow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFREFRESH1USDT") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"point_forecast":[100.5,101.0,102.0]}`))
	}))
	defer srv.Close()

	refresh := newTimesfmRefreshFunc(s.pool, srv.URL)

	candles := make([]signal.Candle, 5)
	for i := range candles {
		candles[i] = signal.Candle{Time: int64(i) * 300_000, Close: 100.0}
	}

	refresh("TFREFRESH1USDT", "5m", candles, 5, 3)

	// Cache must be updated: predicted = 102.0, lastClose = 100.0 → pct = 2.0%
	pct, fresh := signal.GetTimesfmForecast("TFREFRESH1USDT", "5m", 5, 3, time.Minute)
	if !fresh {
		t.Fatal("cache entry not fresh after a successful refresh")
	}
	if pct < 1.99 || pct > 2.01 {
		t.Errorf("cached predictedPct = %v, want ~2.0", pct)
	}

	var count int
	var predictedDirection string
	var predictedPct float64
	var contextBars int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH1USDT",
	).Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Fatalf("timesfm_predictions rows = %d, want 1", count)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT predicted_direction, predicted_pct, context_bars FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH1USDT",
	).Scan(&predictedDirection, &predictedPct, &contextBars); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if predictedDirection != "buy" {
		t.Errorf("predicted_direction = %q, want %q (2%% >= the fixed 0.5%% log threshold)", predictedDirection, "buy")
	}
	if predictedPct < 1.99 || predictedPct > 2.01 {
		t.Errorf("predicted_pct = %v, want ~2.0", predictedPct)
	}
	if contextBars != 5 {
		t.Errorf("context_bars = %d, want 5", contextBars)
	}
}

func TestNewTimesfmRefreshFunc_ServiceError_DoesNotUpdateCacheOrInsertRow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFREFRESH2USDT") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	refresh := newTimesfmRefreshFunc(s.pool, srv.URL)
	candles := []signal.Candle{{Time: 0, Close: 100.0}, {Time: 300_000, Close: 100.0}}

	refresh("TFREFRESH2USDT", "5m", candles, 2, 3)

	if _, fresh := signal.GetTimesfmForecast("TFREFRESH2USDT", "5m", 2, 3, time.Minute); fresh {
		t.Error("cache marked fresh after a failed model-service call")
	}
	var count int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH2USDT").Scan(&count)
	if count != 0 {
		t.Errorf("timesfm_predictions rows = %d, want 0 after a failed call", count)
	}
}

// TestNewTimesfmRefreshFunc_ContextBarsIndependentOfCandleLength is the regression for a
// cache-key mismatch bug caught in code review: the closure must cache/log under the
// EXPLICITLY PASSED contextBars, never under len(candles) — those two can legitimately
// differ (a symbol/timeframe that hasn't accumulated contextBars worth of history yet still
// gets forecast on whatever candles it has, but must be cached under the caller's configured
// contextBars so a later ComputeWithSymbol call — which always queries by configured
// contextBars, see pkg/signal/timesfm.go — actually finds it).
func TestNewTimesfmRefreshFunc_ContextBarsIndependentOfCandleLength(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFREFRESH3USDT") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"point_forecast":[100.5,101.0,102.0]}`))
	}))
	defer srv.Close()

	refresh := newTimesfmRefreshFunc(s.pool, srv.URL)

	// Only 5 candles available, but the configured contextBars is 100 (simulating a
	// symbol/timeframe still ramping up its history).
	candles := make([]signal.Candle, 5)
	for i := range candles {
		candles[i] = signal.Candle{Time: int64(i) * 300_000, Close: 100.0}
	}

	refresh("TFREFRESH3USDT", "5m", candles, 100, 3)

	// Must be cached under contextBars=100 (the configured value) — NOT contextBars=5
	// (len(candles)) — since that's the only key a real ComputeWithSymbol call will ever
	// query with.
	if _, fresh := signal.GetTimesfmForecast("TFREFRESH3USDT", "5m", 100, 3, time.Minute); !fresh {
		t.Error("cache entry not found under the configured contextBars=100 — likely cached under len(candles)=5 instead")
	}
	if _, fresh := signal.GetTimesfmForecast("TFREFRESH3USDT", "5m", 5, 3, time.Minute); fresh {
		t.Error("cache entry found under contextBars=5 (len(candles)) — must only be cached under the configured contextBars")
	}

	var contextBars int
	if err := s.pool.QueryRow(ctx,
		`SELECT context_bars FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH3USDT",
	).Scan(&contextBars); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if contextBars != 100 {
		t.Errorf("context_bars logged = %d, want 100 (the configured value)", contextBars)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./services/api-gateway/ -run TestNewTimesfmRefreshFunc -v`
Expected: FAIL to compile — `undefined: newTimesfmRefreshFunc`.

- [ ] **Step 3: Write the implementation**

```go
// services/api-gateway/timesfm_refresh.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/signal"
)

// timesfmForecastRequest/Response mirror services/timesfm-service's HTTP contract exactly
// (POST /forecast — see services/timesfm-service/main.py).
type timesfmForecastRequest struct {
	Series  []float64 `json:"series"`
	Horizon int       `json:"horizon"`
}

type timesfmForecastResponse struct {
	PointForecast []float64 `json:"point_forecast"`
}

// callTimesfmService POSTs a closing-price series to the local TimesFM Python service and
// returns its point forecast. Uses a plain http.Client — NOT proxy.HTTPClient() — the proxy
// pool is for outbound exchange traffic and would misroute a call to a local service if any
// proxies happen to be configured (proxy.HTTPClient() has no localhost-aware bypass).
func callTimesfmService(ctx context.Context, baseURL string, series []float64, horizon int) ([]float64, error) {
	body, err := json.Marshal(timesfmForecastRequest{Series: series, Horizon: horizon})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/forecast", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("timesfm service: status %d", resp.StatusCode)
	}
	var out timesfmForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.PointForecast) == 0 {
		return nil, fmt.Errorf("timesfm service: empty forecast")
	}
	return out.PointForecast, nil
}

// timesfmIntervalDuration maps this codebase's canonical timeframe strings to their
// wall-clock bar length — used to compute target_at (predicted_at + horizon_bars bars).
var timesfmIntervalDuration = map[string]time.Duration{
	"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"1h": time.Hour, "4h": 4 * time.Hour, "1d": 24 * time.Hour,
}

// timesfmExchange/timesfmMarket are the only exchange/market this platform's candles table
// currently stores (models.ExchangeBybit / models.MarketFutures — see pkg/models/candle.go).
// Written explicitly into every row (timesfm_predictions.exchange/.market, added in Task 1)
// rather than relying on a DB default, so it's always clear from the Go code — not just the
// schema — which candle series a prediction is scored against.
const (
	timesfmExchange = "bybit"
	timesfmMarket   = "futures"
)

// timesfmLogThresholdPct is the FIXED threshold used to classify predicted_direction/
// actual_direction in the timesfm_predictions log — deliberately independent of any
// individual bot's own threshold_pct (which can differ per bot config; see timesfm.go's
// deriveTimesfmState for the per-caller version). The log exists to answer "is the model
// directionally correct at all," not "would this specific bot's threshold have profited" —
// using one fixed reference keeps every logged row comparable to every other.
const timesfmLogThresholdPct = 0.5

func timesfmDirection(pct float64) string {
	switch {
	case pct >= timesfmLogThresholdPct:
		return "buy"
	case pct <= -timesfmLogThresholdPct:
		return "sell"
	default:
		return "neutral"
	}
}

func insertTimesfmPrediction(ctx context.Context, pool *pgxpool.Pool, symbol, timeframe string, contextBars, horizonBars int, priceAtPredict, predictedPct float64) error {
	barDur, ok := timesfmIntervalDuration[timeframe]
	if !ok {
		return fmt.Errorf("timesfm: unknown timeframe %q", timeframe)
	}
	predictedAt := time.Now()
	targetAt := predictedAt.Add(time.Duration(horizonBars) * barDur)
	_, err := pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		timesfmExchange, symbol, timesfmMarket, timeframe, contextBars, horizonBars, predictedAt, priceAtPredict, predictedPct, timesfmDirection(predictedPct), targetAt,
	)
	return err
}

// newTimesfmRefreshFunc builds the closure assigned to signal.TimesfmRefreshFunc at startup
// (main.go). It performs the work pkg/signal itself is never allowed to do: an HTTP call to
// the external model service, and a DB write — then pushes the result back into pkg/signal's
// cache via signal.SetTimesfmForecast. On any failure (service unreachable, bad response, DB
// error) the cache is left untouched — the next stale ComputeWithSymbol call will retry.
//
// contextBars is used verbatim for both the cache key (signal.SetTimesfmForecast) and the
// logged row (insertTimesfmPrediction) — NEVER re-derived from len(candles), which can
// legitimately be smaller (a symbol/timeframe still ramping up its history). Caching under
// len(candles) instead would write to a key no future ComputeWithSymbol call — which always
// queries by the signal's CONFIGURED contextBars — would ever read back, silently defeating
// the cache. See TimesfmRefreshFunc's doc comment in pkg/signal/timesfm_state.go.
func newTimesfmRefreshFunc(pool *pgxpool.Pool, baseURL string) func(symbol, timeframe string, candles []signal.Candle, contextBars, horizonBars int) {
	return func(symbol, timeframe string, candles []signal.Candle, contextBars, horizonBars int) {
		if len(candles) == 0 {
			return
		}
		series := make([]float64, len(candles))
		for i, c := range candles {
			series[i] = c.Close
		}
		lastClose := series[len(series)-1]

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		forecast, err := callTimesfmService(ctx, baseURL, series, horizonBars)
		if err != nil {
			log.Printf("timesfm: forecast %s/%s: %v", symbol, timeframe, err)
			return
		}

		predicted := forecast[len(forecast)-1]
		if lastClose == 0 {
			log.Printf("timesfm: %s/%s: lastClose is 0, cannot compute predicted_pct", symbol, timeframe)
			return
		}
		predictedPct := (predicted - lastClose) / lastClose * 100

		if err := insertTimesfmPrediction(ctx, pool, symbol, timeframe, contextBars, horizonBars, lastClose, predictedPct); err != nil {
			log.Printf("timesfm: insert prediction %s/%s: %v", symbol, timeframe, err)
			return
		}
		// Cache is only updated after the DB write succeeds, so a DB failure doesn't leave
		// pkg/signal confidently serving a forecast this service has no record of.
		signal.SetTimesfmForecast(symbol, timeframe, contextBars, horizonBars, predictedPct)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags=integration ./services/api-gateway/ -run TestNewTimesfmRefreshFunc -v`
Expected: `PASS` for all 3 tests (`_SuccessfulCall_UpdatesCacheAndInsertsRow`,
`_ServiceError_DoesNotUpdateCacheOrInsertRow`, `_ContextBarsIndependentOfCandleLength`).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/timesfm_refresh.go services/api-gateway/timesfm_refresh_test.go
git commit -m "feat(api-gateway): add timesfm refresh function (HTTP call + prediction log)"
```

---

## Task 5: Accuracy backfill job

**Files:**
- Create: `services/api-gateway/timesfm_accuracy_job.go`
- Test: `services/api-gateway/timesfm_accuracy_job_test.go` (integration-tagged)

- [ ] **Step 1: Write the failing test**

```go
// services/api-gateway/timesfm_accuracy_job_test.go
//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

func TestBackfillTimesfmPredictions_FillsOutcomeForPastTarget(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLUSDT") })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM candles WHERE symbol=$1", "TFBACKFILLUSDT") })

	targetAt := time.Now().Add(-10 * time.Minute)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ('bybit',$1,'futures','5m',100,3,$2,100.0,2.0,'buy',$3)`,
		"TFBACKFILLUSDT", targetAt.Add(-15*time.Minute), targetAt,
	); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}

	// A candle at-or-before target_at, close price up 3% from price_at_predict (100.0 → 103.0).
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO candles (exchange, symbol, market, timeframe, open_time, open, high, low, close, volume)
		VALUES ('bybit', $1, 'futures', '5m', $2, 102.0, 104.0, 101.0, 103.0, 1000)`,
		"TFBACKFILLUSDT", targetAt.Add(-1*time.Minute),
	); err != nil {
		t.Fatalf("seed candle: %v", err)
	}

	backfillTimesfmPredictions(ctx, s.pool)

	var actualPrice float64
	var actualDirection string
	var correct bool
	if err := s.pool.QueryRow(ctx, `
		SELECT actual_price, actual_direction, correct FROM timesfm_predictions WHERE symbol=$1`,
		"TFBACKFILLUSDT",
	).Scan(&actualPrice, &actualDirection, &correct); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if actualPrice != 103.0 {
		t.Errorf("actual_price = %v, want 103.0", actualPrice)
	}
	if actualDirection != "buy" {
		t.Errorf("actual_direction = %q, want %q (3%% >= 0.5%% threshold)", actualDirection, "buy")
	}
	if !correct {
		t.Error("correct = false, want true (predicted buy, actual buy)")
	}
}

func TestBackfillTimesfmPredictions_SkipsFutureTarget(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLFUTUREUSDT") })

	futureTarget := time.Now().Add(1 * time.Hour)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ('bybit',$1,'futures','5m',100,3,NOW(),100.0,2.0,'buy',$2)`,
		"TFBACKFILLFUTUREUSDT", futureTarget,
	); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}

	backfillTimesfmPredictions(ctx, s.pool)

	var actualPrice *float64
	if err := s.pool.QueryRow(ctx,
		`SELECT actual_price FROM timesfm_predictions WHERE symbol=$1`, "TFBACKFILLFUTUREUSDT",
	).Scan(&actualPrice); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if actualPrice != nil {
		t.Error("actual_price was filled in for a prediction whose target_at is still in the future")
	}
}

func TestBackfillTimesfmPredictions_NoCandleFound_IncrementsAttempts(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLNOCANDLEUSDT") })

	targetAt := time.Now().Add(-10 * time.Minute)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ('bybit',$1,'futures','5m',100,3,$2,100.0,2.0,'buy',$3)`,
		"TFBACKFILLNOCANDLEUSDT", targetAt.Add(-15*time.Minute), targetAt,
	); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}
	// Deliberately no candle seeded — nearestCandleClose must find nothing for this symbol.

	backfillTimesfmPredictions(ctx, s.pool)

	var attempts int
	var actualPrice *float64
	if err := s.pool.QueryRow(ctx,
		`SELECT attempts, actual_price FROM timesfm_predictions WHERE symbol=$1`, "TFBACKFILLNOCANDLEUSDT",
	).Scan(&attempts, &actualPrice); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 after one failed lookup", attempts)
	}
	if actualPrice != nil {
		t.Error("actual_price was filled in despite no matching candle existing")
	}
}

func TestBackfillTimesfmPredictions_MaxAttemptsReached_StopsBeingSelected(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLMAXATTEMPTSUSDT") })

	targetAt := time.Now().Add(-10 * time.Minute)
	var id string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at, attempts)
		VALUES ('bybit',$1,'futures','5m',100,3,$2,100.0,2.0,'buy',$3,50) RETURNING id`,
		"TFBACKFILLMAXATTEMPTSUSDT", targetAt.Add(-15*time.Minute), targetAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}
	// No candle seeded — if this row were (wrongly) selected, it would fail the lookup and
	// increment attempts past 50; the assertion below proves it was never selected at all.

	backfillTimesfmPredictions(ctx, s.pool)

	var attempts int
	if err := s.pool.QueryRow(ctx, `SELECT attempts FROM timesfm_predictions WHERE id=$1`, id).Scan(&attempts); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if attempts != 50 {
		t.Errorf("attempts = %d, want unchanged 50 — a row at the cap must not be selected/incremented again", attempts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./services/api-gateway/ -run TestBackfillTimesfmPredictions -v`
Expected: FAIL to compile — `undefined: backfillTimesfmPredictions`.

- [ ] **Step 3: Write the implementation**

```go
// services/api-gateway/timesfm_accuracy_job.go
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunTimesfmAccuracyBackfill starts a background goroutine that, every 5 minutes, fills in
// the outcome (actual_price/actual_direction/correct) for any timesfm_predictions row whose
// forecast horizon has passed. Mirrors RunLeverageRefresher's ticker/goroutine skeleton
// (services/api-gateway/leverage_cache.go): an immediate run on startup, then a fixed
// interval, exiting on ctx.Done().
func RunTimesfmAccuracyBackfill(ctx context.Context, pool *pgxpool.Pool) {
	go func() {
		for {
			backfillTimesfmPredictions(ctx, pool)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Minute):
			}
		}
	}()
}

type pendingTimesfmPrediction struct {
	id             string
	exchange       string
	symbol         string
	market         string
	timeframe      string
	priceAtPredict float64
	targetAt       time.Time
}

// timesfmMaxBackfillAttempts caps how many times the backfill job will retry looking up a
// candle for one prediction before giving up on it. Without this, a permanently
// unresolvable row (a delisted symbol, or candle history for it that simply never arrives)
// would sit in the unordered `LIMIT 200` pending scan forever and could crowd out
// genuinely-recent rows once the backlog grows past 200. A row that hits the cap stays in
// the table (still visible, still counted as "not checked") — it's just never selected
// again, distinguishable from a genuinely-pending row via its `attempts` value.
const timesfmMaxBackfillAttempts = 50

func backfillTimesfmPredictions(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `
		SELECT id, exchange, symbol, market, timeframe, price_at_predict, target_at
		FROM timesfm_predictions
		WHERE target_at <= NOW() AND actual_price IS NULL AND attempts < $1
		LIMIT 200`,
		timesfmMaxBackfillAttempts,
	)
	if err != nil {
		log.Printf("timesfm accuracy backfill: query pending: %v", err)
		return
	}
	var pending []pendingTimesfmPrediction
	for rows.Next() {
		var p pendingTimesfmPrediction
		if err := rows.Scan(&p.id, &p.exchange, &p.symbol, &p.market, &p.timeframe, &p.priceAtPredict, &p.targetAt); err != nil {
			continue
		}
		pending = append(pending, p)
	}
	rows.Close()

	for _, p := range pending {
		actualPrice, ok := nearestCandleClose(ctx, pool, p.exchange, p.symbol, p.market, p.timeframe, p.targetAt)
		if !ok {
			// No candle at/before target_at yet (or ever, for an unresolvable row) — count
			// the attempt and retry on a later sweep.
			if _, err := pool.Exec(ctx, `UPDATE timesfm_predictions SET attempts = attempts + 1 WHERE id=$1`, p.id); err != nil {
				log.Printf("timesfm accuracy backfill: increment attempts %s: %v", p.id, err)
			}
			continue
		}
		actualDirection := timesfmDirection((actualPrice - p.priceAtPredict) / p.priceAtPredict * 100)

		if _, err := pool.Exec(ctx, `
			UPDATE timesfm_predictions
			SET actual_price=$1, actual_direction=$2, correct=(predicted_direction=$2), checked_at=NOW()
			WHERE id=$3`,
			actualPrice, actualDirection, p.id,
		); err != nil {
			log.Printf("timesfm accuracy backfill: update %s: %v", p.id, err)
		}
	}
}

// nearestCandleClose returns the close price of the most recent candle at or before at, for
// the given exchange/symbol/market/timeframe — matching candles' own primary key shape
// (migrations/001_initial.sql) so a backfilled outcome is always scored against the correct
// candle series, not an assumed one.
func nearestCandleClose(ctx context.Context, pool *pgxpool.Pool, exchange, symbol, market, timeframe string, at time.Time) (float64, bool) {
	var close float64
	err := pool.QueryRow(ctx, `
		SELECT close FROM candles
		WHERE exchange=$1 AND symbol=$2 AND market=$3 AND timeframe=$4
		  AND open_time <= $5
		ORDER BY open_time DESC
		LIMIT 1`,
		exchange, symbol, market, timeframe, at,
	).Scan(&close)
	if err != nil {
		return 0, false
	}
	return close, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags=integration ./services/api-gateway/ -run TestBackfillTimesfmPredictions -v`
Expected: `PASS` for all 4 tests (`_FillsOutcomeForPastTarget`, `_SkipsFutureTarget`,
`_NoCandleFound_IncrementsAttempts`, `_MaxAttemptsReached_StopsBeingSelected`).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/timesfm_accuracy_job.go services/api-gateway/timesfm_accuracy_job_test.go
git commit -m "feat(api-gateway): add timesfm prediction accuracy backfill job"
```

---

## Task 6: API endpoint

**Files:**
- Create: `services/api-gateway/timesfm_handler.go`
- Modify: `services/api-gateway/main.go` (route registration)
- Test: `services/api-gateway/timesfm_handler_test.go` (integration-tagged)

- [ ] **Step 1: Write the failing test**

```go
// services/api-gateway/timesfm_handler_test.go
//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetTimesfmPredictions_ReturnsRowsAndAggregates(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFHANDLERUSDT") })

	insert := func(correct *bool) {
		s.pool.Exec(ctx, `
			INSERT INTO timesfm_predictions
				(symbol, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at, actual_price, actual_direction, correct, checked_at)
			VALUES ($1,'5m',100,3,NOW(),100.0,2.0,'buy',NOW(),
			        CASE WHEN $2::bool IS NULL THEN NULL ELSE 101.0 END,
			        CASE WHEN $2::bool IS NULL THEN NULL ELSE 'buy' END,
			        $2, CASE WHEN $2::bool IS NULL THEN NULL ELSE NOW() END)`,
			"TFHANDLERUSDT", correct)
	}
	tru, fls := true, false
	insert(&tru)
	insert(&fls)
	insert(nil) // still pending

	req := httptest.NewRequest(http.MethodGet, "/signals/timesfm/predictions?symbol=TFHANDLERUSDT", nil)
	w := httptest.NewRecorder()
	s.GetTimesfmPredictions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var out timesfmPredictionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Total != 3 {
		t.Errorf("total = %d, want 3", out.Total)
	}
	if out.Checked != 2 {
		t.Errorf("checked = %d, want 2", out.Checked)
	}
	if out.CorrectN != 1 {
		t.Errorf("correct = %d, want 1", out.CorrectN)
	}
	if out.WinRate < 49.9 || out.WinRate > 50.1 {
		t.Errorf("win_rate = %v, want 50.0", out.WinRate)
	}
	if len(out.Predictions) != 3 {
		t.Errorf("predictions returned = %d, want 3", len(out.Predictions))
	}
}

func TestGetTimesfmPredictions_MissingSymbol_400(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/signals/timesfm/predictions", nil)
	w := httptest.NewRecorder()
	s.GetTimesfmPredictions(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

var _ = time.Now // keep time imported if unused by a future edit; harmless no-op reference
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./services/api-gateway/ -run TestGetTimesfmPredictions -v`
Expected: FAIL to compile — `undefined: timesfmPredictionsResponse` / `s.GetTimesfmPredictions`.

- [ ] **Step 3: Write the implementation**

```go
// services/api-gateway/timesfm_handler.go
package main

import (
	"net/http"
	"time"
)

type timesfmPredictionRow struct {
	ID                 string    `json:"id"`
	Symbol             string    `json:"symbol"`
	Timeframe          string    `json:"timeframe"`
	PredictedAt        time.Time `json:"predicted_at"`
	PriceAtPredict     float64   `json:"price_at_predict"`
	PredictedPct       float64   `json:"predicted_pct"`
	PredictedDirection string    `json:"predicted_direction"`
	TargetAt           time.Time `json:"target_at"`
	ActualPrice        *float64  `json:"actual_price"`
	ActualDirection    *string   `json:"actual_direction"`
	Correct            *bool     `json:"correct"`
}

type timesfmPredictionsResponse struct {
	Predictions []timesfmPredictionRow `json:"predictions"`
	Total       int                    `json:"total"`
	Checked     int                    `json:"checked"`
	CorrectN    int                    `json:"correct"`
	WinRate     float64                `json:"win_rate"`
}

// GetTimesfmPredictions returns the most recent timesfm predictions for a symbol, plus an
// aggregate win-rate over the checked (outcome already known) subset.
// GET /signals/timesfm/predictions?symbol=BTCUSDT
func (s *Server) GetTimesfmPredictions(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}
	const limit = 50

	rows, err := s.pool.Query(r.Context(), `
		SELECT id, symbol, timeframe, predicted_at, price_at_predict, predicted_pct,
		       predicted_direction, target_at, actual_price, actual_direction, correct
		FROM timesfm_predictions
		WHERE symbol=$1
		ORDER BY predicted_at DESC
		LIMIT $2`,
		symbol, limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	var out timesfmPredictionsResponse
	out.Predictions = make([]timesfmPredictionRow, 0)
	for rows.Next() {
		var p timesfmPredictionRow
		if err := rows.Scan(&p.ID, &p.Symbol, &p.Timeframe, &p.PredictedAt, &p.PriceAtPredict,
			&p.PredictedPct, &p.PredictedDirection, &p.TargetAt, &p.ActualPrice, &p.ActualDirection, &p.Correct); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		out.Predictions = append(out.Predictions, p)
		out.Total++
		if p.Correct != nil {
			out.Checked++
			if *p.Correct {
				out.CorrectN++
			}
		}
	}
	if out.Checked > 0 {
		out.WinRate = float64(out.CorrectN) / float64(out.Checked) * 100
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Register the route**

In `services/api-gateway/main.go`, find the line `r.Get("/signals", s.ListSignals)` and add
this line immediately after it:

```go
			r.Get("/signals/timesfm/predictions", s.GetTimesfmPredictions)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -tags=integration ./services/api-gateway/ -run TestGetTimesfmPredictions -v`
Expected: `PASS` for both tests.

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/timesfm_handler.go services/api-gateway/timesfm_handler_test.go services/api-gateway/main.go
git commit -m "feat(api-gateway): add GET /signals/timesfm/predictions endpoint"
```

---

## Task 7: Wire the refresh hook and backfill job at startup

**Files:**
- Modify: `services/api-gateway/main.go`
- Modify: `.env.example` (document the new env var)

- [ ] **Step 1: Add the env var to `.env.example`**

Open `.env.example` and add this line near the other service-config entries (e.g. near
`LISTEN_ADDR`):

```
TIMESFM_SERVICE_URL=http://localhost:8500
```

- [ ] **Step 2: Wire the hook and job in `main.go`**

Find this block:

```go
	// Start max-leverage DB refresher (every 10 min, covers the full exchange symbol
	// universe — the "leverage" activation signal needs whitelist candidates too, not
	// just symbols already being traded)
	RunLeverageRefresher(ctx, pool)
```

Add immediately after it:

```go
	// Wire the experimental TimesFM signal's refresh hook — pkg/signal itself never calls
	// the model service or the DB directly (see pkg/signal/timesfm_state.go); this closure
	// is the only thing that does, invoked async whenever a subscribed symbol's cached
	// forecast goes stale. Also start the periodic job that backfills each prediction's
	// actual outcome once its forecast horizon has passed.
	timesfmURL := getEnv("TIMESFM_SERVICE_URL", "http://localhost:8500")
	signal.TimesfmRefreshFunc = newTimesfmRefreshFunc(pool, timesfmURL)
	RunTimesfmAccuracyBackfill(ctx, pool)
```

Confirm `"sis/pkg/signal"` is already imported in `main.go` (it is — `RunLeverageRefresher`'s
own file already imports it, and `main.go` itself references `signal` package symbols
elsewhere for the webhook/signal-engine wiring). If the build fails with `undefined: signal`,
add `"sis/pkg/signal"` to `main.go`'s import block.

- [ ] **Step 3: Build to verify it compiles**

Run: `go build ./...`
Expected: exits 0, no errors.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/main.go .env.example
git commit -m "feat(api-gateway): wire timesfm refresh hook and accuracy backfill job at startup"
```

---

## Task 8: Python inference service

No existing Python service convention exists in this repo (`services/parser` and every
other `services/*` directory are Go) — this is a new component built from scratch, kept
deliberately minimal. **Not containerized in this task** — run directly with `uvicorn`
during development; containerizing is future work once the CPU/memory footprint has
actually been measured (see the design spec's "Open risks").

**Files:**
- Create: `services/timesfm-service/requirements.txt`
- Create: `services/timesfm-service/forecast.py`
- Create: `services/timesfm-service/main.py`
- Test: `services/timesfm-service/test_main.py`
- Create: `services/timesfm-service/README.md`

- [ ] **Step 1: Write `requirements.txt`**

```
fastapi==0.115.0
uvicorn[standard]==0.32.0
pydantic==2.9.2
httpx==0.27.2
pytest==8.3.3
timesfm==1.2.0
```

`timesfm==1.2.0` is a best-effort pin as of this plan's writing — the package moves fast;
before installing, check `pip index versions timesfm` (or the PyPI page) for the current
latest release and use that instead if it differs.

- [ ] **Step 2: Write `forecast.py`**

```python
"""Wraps the actual TimesFM model. Loaded lazily on first real request so importing this
module (e.g. from tests, or from main.py at process start) never requires the model
checkpoint to be downloaded/present — only a real /forecast call triggers the download."""

_model = None


def _load_model():
    global _model
    if _model is None:
        import timesfm

        _model = timesfm.TimesFm(
            hparams=timesfm.TimesFmHparams(
                backend="cpu",
                horizon_len=128,
            ),
            checkpoint=timesfm.TimesFmCheckpoint(
                huggingface_repo_id="google/timesfm-1.0-200m",
            ),
        )
    return _model


def run_forecast(series: list[float], horizon: int) -> list[float]:
    """Returns a point forecast of length `horizon` for the given closing-price series.

    NOTE: this calls into the real `timesfm` package, whose exact API (TimesFmHparams /
    TimesFmCheckpoint / forecast() argument names) may have changed since this was written —
    verify against the installed package's own README/docstrings before relying on this in
    production, and adjust this function if the constructor or forecast() signature differs.
    """
    model = _load_model()
    point_forecast, _ = model.forecast([series], freq=[0])
    return point_forecast[0][:horizon].tolist()
```

- [ ] **Step 3: Write `main.py`**

```python
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

import forecast as forecast_module

app = FastAPI()

# Module-level name so tests can monkeypatch it (monkeypatch.setattr(main, "run_forecast",
# fake)) without ever triggering forecast_module's lazy real-model load.
run_forecast = forecast_module.run_forecast


class ForecastRequest(BaseModel):
    series: list[float]
    horizon: int


class ForecastResponse(BaseModel):
    point_forecast: list[float]


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/forecast", response_model=ForecastResponse)
def forecast(req: ForecastRequest):
    if not req.series:
        raise HTTPException(status_code=400, detail="series must not be empty")
    if req.horizon <= 0:
        raise HTTPException(status_code=400, detail="horizon must be positive")
    values = run_forecast(req.series, req.horizon)
    return ForecastResponse(point_forecast=values)
```

- [ ] **Step 4: Write `test_main.py`**

```python
from fastapi.testclient import TestClient

import main


def fake_forecast(series: list[float], horizon: int) -> list[float]:
    last = series[-1]
    return [last * 1.01] * horizon


def test_health():
    client = TestClient(main.app)
    resp = client.get("/health")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_forecast_returns_point_forecast_of_requested_length(monkeypatch):
    monkeypatch.setattr(main, "run_forecast", fake_forecast)
    client = TestClient(main.app)
    resp = client.post("/forecast", json={"series": [1.0, 2.0, 3.0], "horizon": 5})
    assert resp.status_code == 200
    body = resp.json()
    assert len(body["point_forecast"]) == 5
    assert body["point_forecast"][0] == 3.0 * 1.01


def test_forecast_rejects_empty_series(monkeypatch):
    monkeypatch.setattr(main, "run_forecast", fake_forecast)
    client = TestClient(main.app)
    resp = client.post("/forecast", json={"series": [], "horizon": 5})
    assert resp.status_code == 400


def test_forecast_rejects_nonpositive_horizon(monkeypatch):
    monkeypatch.setattr(main, "run_forecast", fake_forecast)
    client = TestClient(main.app)
    resp = client.post("/forecast", json={"series": [1.0, 2.0], "horizon": 0})
    assert resp.status_code == 400
```

- [ ] **Step 5: Install dependencies and run the tests**

Run:
```bash
cd services/timesfm-service
python -m venv .venv
.venv/Scripts/activate
pip install -r requirements.txt
pytest -v
```
(On Linux/Mac, activate with `source .venv/bin/activate` instead.)

Expected: `pytest` installs fine without needing the `timesfm` package's model weights (the
tests monkeypatch `run_forecast` before it's ever called), and all 4 tests `PASS`. If
`pip install timesfm==1.2.0` itself fails (package removed/renamed/version unavailable),
adjust the pin in `requirements.txt` to whatever `pip index versions timesfm` shows as
current, re-run `pip install -r requirements.txt`, and re-run the tests — the HTTP-contract
tests don't depend on which exact version is installed.

- [ ] **Step 6: Write `README.md`**

```markdown
# TimesFM forecast service

Experimental signal backend for `pkg/signal`'s `timesfm` signal — wraps Google's TimesFM
model behind a small HTTP API. Not containerized yet; run it directly:

    cd services/timesfm-service
    python -m venv .venv
    .venv/Scripts/activate   # or `source .venv/bin/activate` on Linux/Mac
    pip install -r requirements.txt
    uvicorn main:app --host 0.0.0.0 --port 8500

Set `TIMESFM_SERVICE_URL` in the project's `.env` if you run it on a different host/port
than `http://localhost:8500` (what `services/api-gateway` falls back to by default).

## API

- `GET /health` — `{"status": "ok"}` once the process is up (does NOT mean the model is
  loaded — that only happens lazily, on the first `/forecast` call).
- `POST /forecast` — `{"series": [float, ...], "horizon": int}` →
  `{"point_forecast": [float, ...]}` (length == horizon).

## First real request

The first `/forecast` call triggers a model checkpoint download from Hugging Face
(multi-GB) and loads it into memory — expect this to take a while, and expect several GB
of RAM in use afterward. Subsequent calls reuse the already-loaded model.

## Testing

`pytest -v` from this directory — the test suite monkeypatches the forecast call, so it
never needs the real model weights downloaded.
```

- [ ] **Step 7: Commit**

```bash
git add services/timesfm-service/
git commit -m "feat(timesfm-service): add Python FastAPI inference service"
```

---

## Task 9: Frontend catalog entry

**Files:**
- Modify: `frontend/src/features/indicators/signals.tsx`

- [ ] **Step 1: Add the `timesfm` entry**

Open `frontend/src/features/indicators/signals.tsx` and find the `whale` entry (search for
`id: 'whale'`). Add this new entry immediately after the whale entry's closing `},`:

```tsx
{
  id: 'timesfm', abbr: 'TFM', name: 'TimesFM Forecast', cat: 'fundamental', state: 'buy' as const,
  desc: 'Экспериментальный прогноз через модель Google TimesFM',
  about: 'Кормит модели последние N свечей и получает прогноз цены на M баров вперёд. Если прогнозное движение превышает порог — Buy/Sell, иначе Neutral. Реальный вызов модели происходит не чаще, чем раз в refresh_interval_sec — между вызовами отдаётся последний закэшированный прогноз.',
  defaults: { context_bars: 512, horizon_bars: 12, threshold_pct: 0.5, refresh_interval_sec: 300 },
  params: [
    { kind: 'number', key: 'context_bars',        label: 'Контекст (баров)', hint: 'Сколько последних свечей подаётся модели как история.', min: 32, step: 32, decimals: 0 },
    { kind: 'number', key: 'horizon_bars',         label: 'Горизонт (баров)', hint: 'На сколько баров вперёд строится прогноз.', min: 1, step: 1, decimals: 0 },
    { kind: 'number', key: 'threshold_pct',        label: 'Порог, %',        hint: 'Минимальное прогнозное движение в %, чтобы считать Buy/Sell вместо Neutral.', min: 0.1, step: 0.1, decimals: 1 },
    { kind: 'number', key: 'refresh_interval_sec', label: 'Обновление, сек', hint: 'Минимальный интервал между реальными вызовами модели на символ.', min: 30, step: 30, decimals: 0 },
  ],
  formula: (p: any) => <>{name('TimesFM')} {op('прогноз ≥')} {num(`${p.threshold_pct}%`)} {op('за')} {num(p.horizon_bars)} {op('баров')}</>,
  compute: (): SignalState => 'neutral',
},
```

- [ ] **Step 2: Verify it compiles and type-checks**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: exits 0, no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/features/indicators/signals.tsx
git commit -m "feat(frontend): add timesfm entry to the signal catalog"
```

---

## Task 10: Frontend API client

**Files:**
- Create: `frontend/src/api/timesfm.ts`

- [ ] **Step 1: Write the client function**

```ts
import { apiClient } from './client'

export type TimesfmPrediction = {
  id: string
  symbol: string
  timeframe: string
  predicted_at: string
  price_at_predict: number
  predicted_pct: number
  predicted_direction: 'buy' | 'sell' | 'neutral'
  target_at: string
  actual_price: number | null
  actual_direction: string | null
  correct: boolean | null
}

export type TimesfmPredictionsResponse = {
  predictions: TimesfmPrediction[]
  total: number
  checked: number
  correct: number
  win_rate: number
}

export async function listTimesfmPredictions(symbol: string): Promise<TimesfmPredictionsResponse> {
  const res = await apiClient.get<TimesfmPredictionsResponse>(
    `/signals/timesfm/predictions?symbol=${encodeURIComponent(symbol)}`
  )
  return res.data
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: exits 0, no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/timesfm.ts
git commit -m "feat(frontend): add timesfm predictions API client"
```

---

## Task 11: Webhooks page — forecast panel instead of the (unusable) historical chart

**Files:**
- Create: `frontend/src/features/webhooks/TimesfmForecastPanel.tsx`
- Modify: `frontend/src/pages/WebhooksPage.tsx`

- [ ] **Step 1: Write the panel component**

```tsx
// frontend/src/features/webhooks/TimesfmForecastPanel.tsx
import { useEffect, useState } from 'react'
import { listTimesfmPredictions, type TimesfmPredictionsResponse } from '../../api/timesfm'

type Props = { symbol: string; tf: string }

// Replaces SignalPreviewChart for the timesfm signal: that chart recomputes Compute() over
// every past candle, which timesfm's own signal deliberately never supports (Compute()
// always returns Neutral — only ComputeWithSymbol, backed by the live cache, does anything —
// same reason whale/leverage/bybit-news are hidden from the default catalog filter, see
// WebhooksPage.tsx's DEFAULT_CATS comment). This shows the current cached forecast plus a
// running accuracy log instead.
export function TimesfmForecastPanel({ symbol, tf }: Props) {
  const [data, setData] = useState<TimesfmPredictionsResponse | null>(null)

  useEffect(() => {
    setData(null)
    listTimesfmPredictions(symbol).then(setData).catch(() => setData(null))
  }, [symbol])

  if (data === null) {
    return <div className="px-3 py-2 text-[11px] text-slate-500">Загрузка…</div>
  }

  const latest = data.predictions[0]

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="border-b border-white/[.06] px-3 py-2">
        <div className="text-[11px] text-slate-500">Текущий прогноз ({symbol}, {tf})</div>
        {latest ? (
          <div className="mt-1 flex items-center gap-2 text-[12px]">
            <span
              className={
                latest.predicted_direction === 'buy'
                  ? 'text-emerald-400'
                  : latest.predicted_direction === 'sell'
                  ? 'text-rose-400'
                  : 'text-slate-400'
              }
            >
              {latest.predicted_direction.toUpperCase()}
            </span>
            <span className="text-slate-300">{latest.predicted_pct.toFixed(2)}%</span>
            <span className="text-slate-600">{new Date(latest.predicted_at).toLocaleString('ru-RU')}</span>
          </div>
        ) : (
          <div className="mt-1 text-[12px] text-slate-600">Прогнозов ещё не было</div>
        )}
        <div className="mt-1 text-[11px] text-slate-500">
          Точность: {data.checked > 0 ? `${data.win_rate.toFixed(0)}% (${data.correct}/${data.checked})` : '—'}
        </div>
      </div>
      <div className="max-h-40 overflow-auto px-3 py-2">
        {data.predictions.length === 0 ? (
          <div className="text-[11px] text-slate-500">История пуста</div>
        ) : (
          data.predictions.map(p => (
            <div
              key={p.id}
              className="flex items-center justify-between gap-2 border-b border-white/[.04] py-1 text-[11px] last:border-0"
            >
              <span className="text-slate-500">{new Date(p.predicted_at).toLocaleString('ru-RU')}</span>
              <span
                className={
                  p.predicted_direction === 'buy'
                    ? 'text-emerald-400'
                    : p.predicted_direction === 'sell'
                    ? 'text-rose-400'
                    : 'text-slate-400'
                }
              >
                {p.predicted_direction}
              </span>
              <span className="text-slate-600">
                {p.correct === null ? 'ожидание' : p.correct ? 'верно' : 'неверно'}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Wire it into `WebhooksPage.tsx`**

Add the import near the top of `frontend/src/pages/WebhooksPage.tsx`, next to the other
feature imports:

```tsx
import { TimesfmForecastPanel } from '../features/webhooks/TimesfmForecastPanel'
```

Find this exact line:

```tsx
<SignalPreviewChart symbol={symbol} tf={timeframe} activeSignal={chartActiveSignal} combo={comboPreviewState} />
```

Replace it with:

```tsx
{chartActiveSignal?.id === 'timesfm'
  ? <TimesfmForecastPanel symbol={symbol} tf={timeframe} />
  : <SignalPreviewChart symbol={symbol} tf={timeframe} activeSignal={chartActiveSignal} combo={comboPreviewState} />}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: exits 0, no errors.

- [ ] **Step 4: Manual browser check**

Start the dev server (`npm run dev` from `frontend/`, or however you normally run it),
open the Webhooks page, select the "TimesFM Forecast" signal from the picker, and confirm:
the forecast panel renders instead of the chart, shows "Прогнозов ещё не было" (no
`services/timesfm-service` running yet at this point, so there's nothing in the DB), and no
console errors appear.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/features/webhooks/TimesfmForecastPanel.tsx frontend/src/pages/WebhooksPage.tsx
git commit -m "feat(frontend): show live forecast + accuracy log for timesfm on Webhooks page"
```

---

## Final verification (run after all tasks)

- [ ] **Full Go build**

Run: `go build ./...`
Expected: exits 0.

- [ ] **Full Go unit test suite**

Run: `go test ./pkg/...`
Expected: `ok` for every package, including `pkg/signal` (whale/leverage/price-change tests
unaffected).

- [ ] **Full Go integration test suite for `services/api-gateway`**

Run: `go test -tags=integration ./services/api-gateway/ -run "Timesfm"`
Expected: `PASS` for every `Timesfm*` test written in Tasks 4-6.

Note: the broader `go test -tags=integration ./services/api-gateway/...` (every test in the
package, not just the new ones) is known to have ~10 pre-existing failures unrelated to this
feature (an `ENCRYPTION_KEY`/environment mismatch in that test setup, confirmed via a clean
`git stash` A/B comparison in an earlier session) — don't treat those as caused by this plan;
only the `Timesfm`-prefixed tests above are this feature's regression signal.

- [ ] **Frontend type-check**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: exits 0.

- [ ] **Frontend unit test suite**

Run: `cd frontend && npx vitest run`
Expected: all existing suites still pass (no test added in this plan touches frontend unit
tests — the two frontend tasks are UI-only, verified manually per Task 11 Step 4).

- [ ] **Report to the user**

Per this repo's `CLAUDE.md`, explicitly state: which tests were run, that they passed, and
that the following require a manual step before the feature is actually usable end-to-end:
1. `services/timesfm-service` must be started manually (`uvicorn main:app --port 8500`) —
   it is not yet process-supervised or auto-started by anything.
2. `services/api-gateway` needs a rebuild + restart to pick up the new route, the
   `TimesfmRefreshFunc` wiring, and the backfill job.
3. The first real forecast call will be slow (model checkpoint download) — this has not
   been measured on this machine yet (flagged as an open risk in the design spec).

---

## Self-review notes (completed during plan writing, not a separate pass)

- **Spec coverage**: every section of `docs/superpowers/specs/2026-09-17-timesfm-signal-design.md`
  maps to a task — Python service (Task 8), Go signal + cache (Tasks 2-3), accuracy log +
  backfill (Tasks 1, 5), API surface (Task 6), frontend catalog + forecast panel (Tasks 9-11),
  startup wiring (Task 7).
- **Two deliberate deviations from the spec's original (looser) wording**, both explained in
  the header: (1) cache stores raw `predicted_pct` instead of a baked-in state, to avoid
  cross-bot `threshold_pct` collisions the spec didn't fully work through; (2) `pkg/signal`
  gets a hook variable instead of doing HTTP/DB work inline, to preserve the package's
  existing no-external-calls invariant that the spec's Architecture section glossed over.
- **The spec's open risk "exact `correct` rule"** is resolved concretely in Task 5: `correct
  = (predicted_direction = actual_direction)`, both computed against a fixed 0.5% reference
  threshold (`timesfmLogThresholdPct`) — independent of any individual bot's own
  `threshold_pct` — documented at the constant's definition in Task 4.
- **Type/name consistency checked**: `signal.TimesfmRefreshFunc`'s signature
  (`func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int)`) is
  identical everywhere it's referenced (Task 2's declaration, Task 3's `ComputeWithSymbol`,
  Task 4's `newTimesfmRefreshFunc` return type, Task 7's assignment). `SetTimesfmForecast`/
  `GetTimesfmForecast`'s four-key signature (`symbol, timeframe string, contextBars,
  horizonBars int`) is likewise consistent across Tasks 2-4.
- **Post-Task-2-review fix (before Task 3 was ever implemented):** code review on Task 2
  found that `TimesfmRefreshFunc`'s original signature (`..., horizonBars int`, no
  `contextBars`) would have let Task 3/4's sample code cache a forecast under `len(candles)`
  instead of the signal's configured `contextBars` whenever a symbol/timeframe had fewer
  candles available than configured — permanently missing the cache for that key. Fixed by
  threading `contextBars` explicitly through the whole call chain (never re-derived from
  slice length), with a dedicated regression test in both Task 3
  (`TestTimesfmSignal_ComputeWithSymbol_ShortHistory_PassesConfiguredContextBars`) and Task 4
  (`TestNewTimesfmRefreshFunc_ContextBarsIndependentOfCandleLength`). Also added `recover()`
  around the refresh goroutine's body in Task 3 (an unrecovered panic in any goroutine
  crashes the whole process, not just this signal — the goroutine calls out to an external
  HTTP service this process doesn't control).
