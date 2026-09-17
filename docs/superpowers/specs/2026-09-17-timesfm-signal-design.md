# TimesFM experimental signal — design

## Context

The user asked (conversationally, not from a bug report) whether Google's TimesFM —
a decoder-only foundation model for zero-shot time-series forecasting — could be fed
tick/candle data for a coin and forecast its near-term behavior. After discussing that
raw tick data is too noisy for this to be reliable and that OHLCV bars plus rigorous
backtesting would be the sane way to try it, the user asked to wire it in as an
**experimental signal**, immediately live: selectable in bot `signal_configs`/
`activation_signals` (can open/close real positions) and visible on the Webhooks page
signal picker, with configurable parameters and a way to evaluate the model's own
prediction accuracy over time.

TimesFM has no Go port — it's a JAX/Python model — so this is the platform's first
signal backed by an external non-exchange service call, and its first ML-style signal
whose predictions are worth logging for later evaluation (no existing signal does this).

Research into the existing signal system (`pkg/signal`) found a directly reusable
precedent: the `whale` signal (`pkg/signal/signals.go`, `whale_state.go`) is symbol-keyed,
computed out-of-band, and read from a cheap in-memory cache in the hot path
(`computeSignalState`, `pkg/signal/engine.go`) rather than computed synchronously per
candle close. TimesFM follows the same shape.

## Goals

- A new catalog signal (`timesfm`) that any bot can select like any other signal
  (`SignalPickerField`), and that appears on the Webhooks page's signal picker for
  alerting, from day one — no separate "observation-only" rollout phase (explicit user
  decision, despite the recommendation to start observation-only).
- Configurable per use: how much history the model sees, how far ahead it forecasts, and
  the move-size threshold that turns a forecast into Buy/Sell vs Neutral.
- A persisted log of every real model prediction (not cache hits) with its outcome filled
  in once the forecast horizon has passed, so prediction accuracy can be reviewed —
  this is genuinely new: no existing signal has prediction-vs-outcome history.
- The signal engine's hot path (`computeUnit.compute`, runs on every subscribed
  symbol/timeframe kline close) must never block on the Python model call.

## Non-goals

- Backtester support (`pkg/signals`, `services/signal-engine`). That evaluator is a pure,
  deterministic function of `(candles, index)` with no notion of an async external call or
  a warm cache; wiring TimesFM into it is a separate, larger effort (would need either a
  synchronous-but-slow backtest mode or a pre-computed forecast cache keyed by candle
  index) and isn't needed for an experimental live signal. Same limitation the `whale`
  signal already has.
- Historical chart preview on the Webhooks page (the per-past-candle recompute every other
  signal gets). Explicit user decision: live current prediction only, shown as a small
  "current forecast" card instead of a flip-history chart.
- Quantile/confidence-interval-based filtering of predictions. TimesFM can emit quantile
  forecasts; v1 uses only the point forecast + a % threshold. A confidence-based param can
  be added later without changing the architecture.
- GPU inference / high-frequency polling of many symbols. Scoped to CPU inference on a
  multi-minute refresh cadence for whatever symbols are actually subscribed.
- A frontend accuracy dashboard beyond a simple table. The API returns recent predictions
  + aggregate win-rate; a polished visualization is future work.

## Approaches considered

1. **(Chosen) Self-refreshing in-memory cache inside `pkg/signal`, demand-driven.**
   `ComputeWithSymbol` reads a `map[symbol+tf]cacheEntry` cache; a stale/missing entry
   triggers a non-blocking goroutine that calls the Python service and updates the cache.
   Only symbol/timeframe pairs some bot or webhook is actually subscribed to ever get
   computed. Cost: cache is lost on api-gateway restart (cold start until next tick per
   symbol) — acceptable for an experimental signal, matches how e.g. `TickerHub`'s other
   in-memory state already behaves across restarts.
2. **Separate standalone poller service + Redis cache**, mirroring `whale`'s exact
   architecture (`services/parser` writes, `services/signal-engine` reads/polls Redis).
   Survives restarts and has no per-subscriber cold start, but proactively computes for
   every symbol on a schedule regardless of whether anything is watching it — wasted
   inference cost, and more new infrastructure (new service, Redis key scheme) than an
   experimental signal justifies. Rejected for now; revisit if usage grows past a
   handful of symbols.
3. **Synchronous call inside `Compute()`, no cache.** Simplest to write, but every
   subscribed symbol's kline-close tick would block on an HTTP round-trip to the Python
   process — directly violates the hot-path constraint in Goals, and a slow/down model
   service would degrade the entire signal engine, not just this one signal. Rejected.

## Architecture

### 1. Python inference service (new component: `services/timesfm-service/`)

Stateless with respect to symbols — takes a plain numeric series in, returns a forecast
out. The Go side is responsible for turning candles into a series and interpreting the
result.

```
POST /forecast
  { "series": [float, ...], "horizon": int }
  -> { "point_forecast": [float, ...] }   // length == horizon

GET /health
  -> 200 once the model checkpoint is loaded
```

- FastAPI (or equivalent minimal Python HTTP framework already acceptable in this repo's
  Python services, e.g. `services/parser`'s stack) wrapping the TimesFM JAX model.
- Model checkpoint loaded once at process startup and held in memory; each `/forecast`
  call is a single inference pass (~1–3s on CPU for typical context lengths per the
  hardware discussion below).
- Runs as its own long-lived process alongside api-gateway (not per-request spawned),
  addressed over plain HTTP on localhost (no auth needed at this stage — internal only,
  same trust boundary as e.g. the squid proxy container).

### 2. Go signal (new file: `pkg/signal/timesfm.go`)

Registered via the existing `Register("timesfm", factory)` mechanism
(`pkg/signal/registry.go`), implementing `Signal` + the `SymbolComputer` extension the
same way `whale` does.

```go
type timesfmCacheEntry struct {
    state          State
    predictedPct   float64
    predictedPrice float64
    computedAt     time.Time
}

// keyed by "<symbol>:<timeframe>", guarded by sync.RWMutex — same shape as whale_state.go
var timesfmCache map[string]timesfmCacheEntry
var timesfmInflight map[string]bool // dedup: only one fetch per key at a time
```

`ComputeWithSymbol(symbol, tf, candles)`:
1. Look up `timesfmCache[key]`. If present and `now - computedAt < refresh_interval_sec`
   → return its `state` immediately.
2. Otherwise, if `timesfmInflight[key]` is not already true, mark it true and spawn a
   goroutine that: takes the last `context_bars` closes from `candles`, POSTs to the
   Python service with `horizon_bars`, computes
   `predicted_pct = (point_forecast[last] - lastClose) / lastClose * 100`, derives
   `state` by comparing `predicted_pct` against `threshold_pct` (Buy if
   `predicted_pct >= threshold_pct`, Sell if `<= -threshold_pct`, else Neutral), updates
   the cache, clears the inflight flag, and inserts a row into `timesfm_predictions`
   (§3) — but only for this real inference call, never for a cache hit.
3. Returns whatever's currently cached (possibly stale, or `Neutral` on a true cold
   start) without waiting for the goroutine — this is what keeps the hot path
   non-blocking.

**Signal parameters** (read via `cfg.Float(name, default)`, same untyped-map convention
every other signal already uses — see `signal.Config` in `pkg/strategy/types.go`):

| Param | Meaning | Default |
|---|---|---|
| `context_bars` | how many trailing candles feed the model as context | 512 |
| `horizon_bars` | how many bars ahead to forecast | 12 |
| `threshold_pct` | min forecast move % to call Buy/Sell instead of Neutral | 0.5 |
| `refresh_interval_sec` | cache TTL — real minimum time between model calls per symbol/tf | 300 |

### 3. Accuracy log (new table + backfill job)

New migration, table `timesfm_predictions`:

```sql
CREATE TABLE timesfm_predictions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol            TEXT NOT NULL,
    timeframe         TEXT NOT NULL,
    context_bars      INT NOT NULL,
    horizon_bars      INT NOT NULL,
    predicted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    price_at_predict  NUMERIC(18,8) NOT NULL,
    predicted_pct     NUMERIC(10,4) NOT NULL,
    predicted_direction TEXT NOT NULL, -- 'buy' | 'sell' | 'neutral'
    target_at         TIMESTAMPTZ NOT NULL, -- predicted_at + horizon_bars * timeframe interval
    actual_price      NUMERIC(18,8),
    actual_direction  TEXT,
    correct           BOOLEAN,
    checked_at        TIMESTAMPTZ
);
CREATE INDEX idx_timesfm_predictions_pending ON timesfm_predictions (target_at) WHERE actual_price IS NULL;
CREATE INDEX idx_timesfm_predictions_symbol ON timesfm_predictions (symbol, predicted_at DESC);
```

A periodic job (new goroutine started alongside the other periodic refreshers in
`services/api-gateway/main.go`, e.g. `RunLeverageRefresher`'s pattern), every few
minutes: selects rows where `target_at <= NOW() AND actual_price IS NULL`, looks up the
actual close price for that symbol at/near `target_at` (from existing candle storage),
fills in `actual_price`, `actual_direction` (same buy/sell/neutral derivation, using
`predicted_direction`'s original threshold), `correct` (`predicted_direction ==
actual_direction`, or a looser "did price move the predicted way at all" rule — exact
rule finalized during planning), and `checked_at`.

### 4. API + frontend surface

- `GET /signals/timesfm/predictions?symbol=&limit=` — recent predictions (checked and
  pending) plus an aggregate `{ total, checked, correct, win_rate }`. New handler in
  `services/api-gateway`, read-only.
- `frontend/src/features/indicators/signals.tsx` — add the `timesfm` catalog entry (id,
  label, the four params above as configurable fields), same shape as every other
  signal definition there.
- Webhooks page (`WebhooksPage.tsx` / `SignalPreviewChart`): the per-signal historical
  preview currently recomputes `Compute()` over every past candle. `timesfm`'s catalog
  entry carries a `noHistoricalPreview: true` flag; when set, the preview area renders a
  small "current forecast" card (direction, predicted %, last-updated time) instead of
  the recompute-driven chart. A small new table/section (reusing the accuracy endpoint
  above) shows recent predictions and running win-rate near the signal's picker —
  exact placement (own tab vs. inline section) finalized during planning.

## Testing

- Python service: a unit test hitting `/forecast` with a synthetic series, asserting the
  response shape and length — not asserting anything about forecast quality (that's not
  testable in CI and isn't the point of this service's tests).
- Go, unit-level (no live Postgres/HTTP):
  - TTL/cache-hit-vs-refresh logic, and the `predicted_pct` → Buy/Sell/Neutral threshold
    derivation, as pure functions.
  - `ComputeWithSymbol` against an `httptest.Server` standing in for the Python service,
    asserting: a cache hit doesn't call the server; a stale/missing entry does, exactly
    once even under concurrent calls for the same key (inflight dedup).
- Go, integration-tagged: the backfill job correctly fills `actual_price`/`correct` for a
  seeded prediction row whose `target_at` is in the past, and leaves a future-`target_at`
  row untouched.
- No changes to any existing signal's behavior — `whale` and the rest are untouched;
  regression coverage there is "existing tests still pass," nothing new to add.

## Rollout

- No feature flag / gating — the user explicitly wants it live and selectable
  immediately, same as any other catalog signal. Operationally, the user should validate
  with a bot on small size/margin before trusting it broadly, but that's a usage
  decision, not something the system enforces (matches how newly-added catalog signals
  have always shipped here — no precedent for a soft-launch gate).
- Python service is a new deployable unit: needs its own process supervision (systemd
  unit, or however other sidecar processes are currently run — matches whatever pattern
  `services/parser` or the tg-bot container already use) and a health check the Go side
  can use to decide "service is down, keep serving cached/Neutral state" rather than
  spamming failed requests.

## Scope / affected files

- New: `services/timesfm-service/` (Python, new directory)
- New: `pkg/signal/timesfm.go`, plus its test file(s)
- New: migration for `timesfm_predictions`
- New: backfill job (likely `services/api-gateway/timesfm_accuracy_job.go` or similar)
- New: `GET /signals/timesfm/predictions` handler + route registration
- Modified: `frontend/src/features/indicators/signals.tsx` (new catalog entry)
- Modified: `frontend/src/pages/WebhooksPage.tsx` / `SignalPreviewChart` (branch for
  `noHistoricalPreview`, render the forecast card + accuracy table)
- Not modified: `pkg/signal/engine.go`, `pkg/strategy/*`, the `whale` signal, the
  backtester — the whole point of following the `whale` precedent is that no shared
  plumbing needs to change for a new symbol-keyed, cache-backed signal to slot in.

## Open risks carried into planning

- **Hardware/ops**: TimesFM 1.0 is ~200M params; CPU inference at a multi-minute refresh
  cadence for a handful of symbols is expected to be fine (~1–3s/call, a few GB RAM for
  the JAX runtime + weights). Not yet measured on this machine — first implementation
  task should include an actual latency/memory measurement before assuming the "CPU is
  enough" plan holds.
- **Timeframe → wall-clock interval mapping** for computing `target_at` and for the
  backfill job's "actual price at target_at" lookup needs a concrete source of truth
  (existing candle/kline storage keyed by symbol+timeframe+time) — confirm during
  planning which existing table/query serves this without new candle-storage work.
- **`predicted_direction`-vs-`actual_direction` "correct" rule** is stated loosely above
  (exact price match vs. directional match) — needs to be nailed down as one specific,
  testable rule during planning, not left ambiguous in the implementation.
- **No historical validation of TimesFM's actual forecasting quality on crypto data has
  been done.** This spec covers wiring the signal in correctly; whether its predictions
  are any good is exactly what the accuracy log exists to find out, not something this
  design can assert in advance.
