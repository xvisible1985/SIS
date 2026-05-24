package signal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── ComputeUnit ───────────────────────────────────────────────────────────

// computeUnit represents one unique (symbol, interval, []Config) combination.
// Multiple strategy subscriptions sharing identical configs reuse the same unit.
type computeUnit struct {
	hash      string
	symbol    string
	interval  string
	configs   []Config
	signals   []Signal // built once at construction
	sigLabel  string   // human-readable, e.g. "RSI Oversold + MACD Crossover"

	mu              sync.Mutex
	lastState       State
	lastComputedAt  time.Time
	subs            map[string]func(State) // subscriptionID → callback

	emitFn func(symbol, interval, hash string, state State) // set by Engine; may be nil
}

func newComputeUnit(hash, symbol, interval string, configs []Config) (*computeUnit, error) {
	sigs := make([]Signal, 0, len(configs))
	labels := make([]string, 0, len(configs))
	for _, cfg := range configs {
		sig, err := Build(cfg)
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, sig)
		labels = append(labels, labelFor(cfg))
	}
	return &computeUnit{
		hash:      hash,
		symbol:    symbol,
		interval:  interval,
		configs:   configs,
		signals:   sigs,
		sigLabel:  strings.Join(labels, " + "),
		lastState: Neutral, // Safe default — overwritten on first kline close
		subs:      make(map[string]func(State)),
	}, nil
}

// compute evaluates all signals (AND logic) and fires callbacks on state change.
func (u *computeUnit) compute(candles []Candle, m *Metrics) {
	start := time.Now()

	if len(u.signals) == 0 {
		return
	}
	combined := u.signals[0].Compute(candles)
	for _, sig := range u.signals[1:] {
		if combined == Neutral {
			break
		}
		s := sig.Compute(candles)
		if s != combined {
			combined = Neutral
		}
	}

	elapsed := time.Since(start)
	m.recordCompute(u.hash, elapsed)

	u.mu.Lock()
	changed := combined != u.lastState
	u.lastState = combined
	u.lastComputedAt = time.Now()
	cbs := make([]func(State), 0, len(u.subs))
	for _, cb := range u.subs {
		cbs = append(cbs, cb)
	}
	emitFn := u.emitFn
	u.mu.Unlock()

	if changed {
		for _, cb := range cbs {
			if cb != nil {
				go cb(combined)
			}
		}
		if emitFn != nil {
			go emitFn(u.symbol, u.interval, u.hash, combined)
		}
	}
}

func labelFor(cfg Config) string {
	names := map[string]string{
		"rsi-os":    "RSI Oversold",
		"macd-x":    "MACD Crossover",
		"gc":        "Golden Cross",
		"bb-sq":     "BB Squeeze",
		"stoch-x":   "Stochastic Cross",
		"vol-spike": "Volume Spike",
		"breakout":  "Range Breakout",
		"ema-x":     "EMA Crossover",
		"div":       "RSI Divergence",
		"st-flip":   "SuperTrend Flip",
	}
	if n, ok := names[cfg.Name]; ok {
		return n
	}
	return cfg.Name
}

// ── Engine ────────────────────────────────────────────────────────────────

// Engine manages signal subscriptions, deduplicates compute units,
// and dispatches results to subscribers.
type Engine struct {
	hub     *KlineHub
	metrics *Metrics

	mu    sync.RWMutex
	units map[string]*computeUnit // hash → unit
	subs  map[string]string       // subscriptionID → hash

	globalCbsMu sync.RWMutex
	globalCbs   []func(symbol, interval, hash string, state State)
}

// NewEngine creates a SignalEngine. Pass a non-nil exec to persist metrics to DB.
func NewEngine(ctx context.Context, exec ExecFn) *Engine {
	hub := NewKlineHub(ctx)
	m := newMetrics(ctx, exec)
	return &Engine{
		hub:     hub,
		metrics: m,
		units:   make(map[string]*computeUnit),
		subs:    make(map[string]string),
	}
}

// OnStateChange registers a global callback that fires whenever any compute unit
// changes state. The callback is invoked in its own goroutine.
func (e *Engine) OnStateChange(cb func(symbol, interval, hash string, state State)) {
	e.globalCbsMu.Lock()
	e.globalCbs = append(e.globalCbs, cb)
	e.globalCbsMu.Unlock()
}

// emitGlobal calls all registered global callbacks in separate goroutines.
func (e *Engine) emitGlobal(symbol, interval, hash string, state State) {
	e.globalCbsMu.RLock()
	cbs := make([]func(string, string, string, State), len(e.globalCbs))
	copy(cbs, e.globalCbs)
	e.globalCbsMu.RUnlock()
	for _, cb := range cbs {
		go cb(symbol, interval, hash, state)
	}
}

// Subscribe registers a callback for (symbol, interval, configs).
// Returns a subscriptionID that can be passed to Unsubscribe.
// Configs with identical content share one compute unit (deduplication).
// cb is called asynchronously whenever the combined signal state changes.
func (e *Engine) Subscribe(
	subID, symbol, interval string,
	configs []Config,
	cb func(State),
) error {
	if len(configs) == 0 {
		return fmt.Errorf("signal engine: no configs")
	}

	hash := HashConfigs(symbol, interval, configs)

	e.mu.Lock()
	unit, exists := e.units[hash]
	isNew := false
	if !exists {
		var err error
		unit, err = newComputeUnit(hash, symbol, interval, configs)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		e.units[hash] = unit
		e.metrics.unitAdded()
		isNew = true
	}
	e.subs[subID] = hash
	e.mu.Unlock()

	unit.mu.Lock()
	unit.subs[subID] = cb
	unit.emitFn = e.emitGlobal
	unit.mu.Unlock()

	e.metrics.subAdded()

	// Register hub listener only when the unit is newly created to avoid duplicates.
	if isNew {
		e.hub.Subscribe(symbol, interval, func(candles []Candle) {
			unit.compute(candles, e.metrics)
		})
	}

	return nil
}

// Unsubscribe removes a subscription. The compute unit is kept alive as long
// as at least one other subscriber references it.
func (e *Engine) Unsubscribe(subID string) {
	e.mu.Lock()
	hash, ok := e.subs[subID]
	if !ok {
		e.mu.Unlock()
		return
	}
	delete(e.subs, subID)
	unit := e.units[hash]
	e.mu.Unlock()

	if unit == nil {
		return
	}

	unit.mu.Lock()
	delete(unit.subs, subID)
	remaining := len(unit.subs)
	unit.mu.Unlock()

	e.metrics.subRemoved()

	if remaining == 0 {
		e.mu.Lock()
		delete(e.units, hash)
		e.mu.Unlock()
		e.metrics.unitRemoved()
	}
}

// ForceRecompute immediately re-evaluates all compute units that contain a signal
// with the given name. Used to propagate test overrides without waiting for the
// next kline close event.
func (e *Engine) ForceRecompute(signalName string) {
	e.mu.RLock()
	var targets []*computeUnit
	for _, u := range e.units {
		for _, cfg := range u.configs {
			if cfg.Name == signalName {
				targets = append(targets, u)
				break
			}
		}
	}
	e.mu.RUnlock()

	for _, u := range targets {
		snap := e.hub.SnapshotOrFetch(u.symbol, u.interval)
		if len(snap) >= 2 {
			go u.compute(snap, e.metrics)
		}
	}
}

// ComputeStateForce evaluates the combined signal state without the usual
// "need at least 2 candles" guard. Override-aware signals (e.g. rsiTest) work
// with an empty snapshot; other signals return Neutral when data is absent.
func (e *Engine) ComputeStateForce(symbol, interval string, configs []Config) State {
	// Fast path: return cached state only if the unit has been computed at least once.
	// A freshly subscribed unit has lastComputedAt.IsZero() — fall through to fresh
	// computation so callers never see the uninitialised Neutral placeholder.
	hash := HashConfigs(symbol, interval, configs)
	e.mu.RLock()
	unit, exists := e.units[hash]
	e.mu.RUnlock()
	if exists {
		unit.mu.Lock()
		state := unit.lastState
		computed := !unit.lastComputedAt.IsZero()
		unit.mu.Unlock()
		if computed {
			return state
		}
	}
	// Get whatever snapshot is available (may be nil)
	snap := e.hub.SnapshotOrFetch(symbol, interval)
	if len(configs) == 0 {
		return Neutral
	}
	first, err := Build(configs[0])
	if err != nil {
		return Neutral
	}
	combined := first.Compute(snap)
	for _, cfg := range configs[1:] {
		if combined == Neutral {
			break
		}
		sig, err := Build(cfg)
		if err != nil {
			break
		}
		if sig.Compute(snap) != combined {
			combined = Neutral
		}
	}
	return combined
}

// QueryTTLRemaining returns the minimum TTL remaining in seconds across all TTLAware
// signals in the compute unit for the given (symbol, interval, configs) key.
// Returns -1 if the unit doesn't exist, no TTL is configured, or the signal hasn't fired yet.
func (e *Engine) QueryTTLRemaining(symbol, interval string, configs []Config) float64 {
	hash := HashConfigs(symbol, interval, configs)
	e.mu.RLock()
	unit, exists := e.units[hash]
	e.mu.RUnlock()
	if !exists {
		return -1
	}
	min := -1.0
	for _, sig := range unit.signals {
		ta, ok := sig.(TTLAware)
		if !ok {
			continue
		}
		rem := ta.TTLRemainingSec()
		if rem >= 0 && (min < 0 || rem < min) {
			min = rem
		}
	}
	return min
}

// Hub returns the underlying KlineHub (for conn count metrics).
func (e *Engine) Hub() *KlineHub { return e.hub }

// Metrics returns the live metrics snapshot.
func (e *Engine) Metrics() MetricsSnapshot {
	e.mu.RLock()
	units := make([]*computeUnit, 0, len(e.units))
	for _, u := range e.units {
		units = append(units, u)
	}
	e.mu.RUnlock()
	return e.metrics.snapshot(units, e.hub.ConnCount())
}

// QueryValues returns the current numeric value for each signal that implements
// SignalValuer (e.g. RSI = 49.5). Computes directly from the candle snapshot
// without holding any compute-unit locks to avoid lock-order inversions.
func (e *Engine) QueryValues(symbol, interval string, configs []Config) map[string]float64 {
	snap := e.hub.SnapshotOrFetch(symbol, interval)
	if len(snap) < 2 {
		return nil
	}
	result := make(map[string]float64)
	for _, cfg := range configs {
		sig, err := Build(cfg)
		if err != nil {
			continue
		}
		if sv, ok := sig.(SignalValuer); ok {
			result[cfg.Name] = sv.Value(snap)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// QueryState returns the current signal state without creating a subscription.
// If a compute unit already exists for this key, returns its cached lastState.
// Otherwise tries to compute from the hub's candle buffer.
// Returns (Neutral, false) when there is insufficient candle data.
func (e *Engine) QueryState(symbol, interval string, configs []Config) (State, bool) {
	hash := HashConfigs(symbol, interval, configs)
	e.mu.RLock()
	unit, exists := e.units[hash]
	e.mu.RUnlock()
	if exists {
		unit.mu.Lock()
		state := unit.lastState
		unit.mu.Unlock()
		return state, true
	}
	snap := e.hub.SnapshotOrFetch(symbol, interval)
	if len(snap) < 2 {
		return Neutral, false
	}
	if len(configs) == 0 {
		return Neutral, false
	}
	first, err := Build(configs[0])
	if err != nil {
		return Neutral, false
	}
	combined := first.Compute(snap)
	for _, cfg := range configs[1:] {
		if combined == Neutral {
			break
		}
		sig, err := Build(cfg)
		if err != nil {
			return Neutral, false
		}
		if sig.Compute(snap) != combined {
			combined = Neutral
		}
	}
	return combined, true
}
