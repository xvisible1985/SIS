package strategy

import (
	"context"
	"encoding/json"
	"log"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

// orderRef identifies what a placed exchange order belongs to.
type orderRef struct {
	strategyID string
	levelID    string // UUID of strategy_levels row; empty for tp/sl
	refType    string // "level" | "tp" | "sl"
}

// Engine coordinates all strategy runners, grouped by account.
type Engine struct {
	pool         *pgxpool.Pool
	encKey       string
	mu           sync.RWMutex
	runners      map[string]*AccountRunner // accountID → runner
	signalEngine *signal.Engine
}

// SetSignalEngine wires the signal engine into the strategy engine so runners
// can subscribe to signals before starting a new cycle.
func (e *Engine) SetSignalEngine(se *signal.Engine) {
	e.mu.Lock()
	e.signalEngine = se
	e.mu.Unlock()
}

// New creates a new Engine. Call Start(ctx) to begin.
func New(pool *pgxpool.Pool, encKey string) *Engine {
	return &Engine{pool: pool, encKey: encKey, runners: make(map[string]*AccountRunner)}
}

// Start loads all active/finishing strategies from DB and launches account runners.
func (e *Engine) Start(ctx context.Context) {
	rows, err := e.pool.Query(ctx,
		`SELECT id, owner_id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
		        entry_order_type, COALESCE(steps::text,'[]'),
		        COALESCE(signal_configs::text,'[]'),
		        after_stop_mode, cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,'')
		 FROM strategies WHERE status IN ('active','finishing')`)
	if err != nil {
		log.Printf("strategy engine: load: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var s Strategy
		if err := scanStrategy(rows, &s); err == nil {
			e.loadStrategy(ctx, s)
		}
	}
}

// Notify reloads a strategy from DB after a REST update (status change or param edit).
func (e *Engine) Notify(ctx context.Context, strategyID string) {
	var s Strategy
	row := e.pool.QueryRow(ctx,
		`SELECT id, owner_id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
		        entry_order_type, COALESCE(steps::text,'[]'),
		        COALESCE(signal_configs::text,'[]'),
		        after_stop_mode, cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,'')
		 FROM strategies WHERE id=$1`, strategyID)
	if err := scanStrategyRow(row, &s); err != nil {
		log.Printf("strategy engine: notify %s: %v", strategyID, err)
		return
	}
	e.loadStrategy(ctx, s)
}

func (e *Engine) loadStrategy(ctx context.Context, s Strategy) {
	e.mu.Lock()
	runner, ok := e.runners[s.AccountID]
	if !ok {
		info, err := e.loadAccountInfo(ctx, s.AccountID)
		if err != nil {
			e.mu.Unlock()
			log.Printf("strategy engine: account info for %s: %v", s.AccountID, err)
			return
		}
		runCtx, cancel := context.WithCancel(context.Background())
		runner = newAccountRunner(s.AccountID, info.accountLabel, info.ownerUsername, info.creds, e.pool, e.signalEngine, cancel)
		e.runners[s.AccountID] = runner
		e.mu.Unlock()
		go runner.run(runCtx)
	} else {
		e.mu.Unlock()
	}
	runner.addStrategy(s)
}

// LogUserAction writes a user-initiated action to the strategy event log.
func (e *Engine) LogUserAction(ctx context.Context, strategyID, msg string) {
	logEvent(ctx, e.pool, strategyID, "info", msg)
}

// ActiveStats returns counts of active strategies and active cycles across all accounts.
func (e *Engine) ActiveStats() (strategies, cycles int) {
	// Snapshot strategy runners without holding runner.mu while acquiring sr.mu.
	// Holding runner.mu (= ar.mu RLock) while locking sr.mu would deadlock with worker
	// tasks that hold sr.mu and call RegisterOrder (which needs ar.mu write-lock).
	e.mu.RLock()
	var allSR []*StrategyRunner
	for _, runner := range e.runners {
		runner.mu.RLock()
		for _, sr := range runner.strategies {
			allSR = append(allSR, sr)
		}
		runner.mu.RUnlock()
	}
	e.mu.RUnlock()
	for _, sr := range allSR {
		sr.mu.Lock()
		if sr.strategy.Status == StatusActive {
			strategies++
			if sr.cycle != nil {
				cycles++
			}
		}
		sr.mu.Unlock()
	}
	return
}

// StrategyWorkerStat holds health metrics for one strategy's worker goroutine.
type StrategyWorkerStat struct {
	StrategyID    string `json:"strategy_id"`
	AccountID     string `json:"account_id"`
	AccountLabel  string `json:"account_label"`
	OwnerID       string `json:"owner_id"`
	OwnerUsername string `json:"owner_username"`
	Symbol        string `json:"symbol"`
	Category      string `json:"category"`
	Status        string `json:"status"`
	QueueDepth    int    `json:"queue_depth"`
	IsProcessing  bool   `json:"is_processing"`
	PanicCount    int32  `json:"panic_count"`
	TasksDropped  int32  `json:"tasks_dropped"`
	LastTaskAt    string `json:"last_task_at"` // RFC3339; empty if never run
}

// WorkerStats returns health metrics for every registered strategy worker.
func (e *Engine) WorkerStats() []StrategyWorkerStat {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []StrategyWorkerStat
	for _, runner := range e.runners {
		runner.mu.RLock()
		for _, sr := range runner.strategies {
			nano := atomic.LoadInt64(&sr.lastTaskNano)
			var lastAt string
			if nano != 0 {
				lastAt = time.Unix(0, nano).UTC().Format(time.RFC3339)
			}
			out = append(out, StrategyWorkerStat{
				StrategyID:    sr.strategy.ID,
				AccountID:     sr.strategy.AccountID,
				AccountLabel:  runner.accountLabel,
				OwnerID:       sr.strategy.OwnerID,
				OwnerUsername: runner.ownerUsername,
				Symbol:        sr.strategy.Symbol,
				Category:      sr.strategy.Category,
				Status:        string(sr.strategy.Status),
				QueueDepth:    len(sr.taskCh),
				IsProcessing:  atomic.LoadInt32(&sr.workerRunning) == 1,
				PanicCount:    atomic.LoadInt32(&sr.panicCount),
				TasksDropped:  atomic.LoadInt32(&sr.tasksDropped),
				LastTaskAt:    lastAt,
			})
		}
		runner.mu.RUnlock()
	}
	return out
}

// GetTradeStream returns the trade WS stream for an account, or nil if no runner exists.
func (e *Engine) GetTradeStream(accountID string) *trader.TradeStream {
	e.mu.RLock()
	runner := e.runners[accountID]
	e.mu.RUnlock()
	if runner == nil {
		return nil
	}
	return runner.tradeStream
}

// RestartCycle cancels the current cycle for a strategy and starts a new one.
// Called after strategy settings are updated so new grid parameters take effect.
func (e *Engine) RestartCycle(ctx context.Context, strategyID string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if ok {
			sr.submit(func(ctx context.Context) { sr.restartCycle(ctx) })
			return
		}
	}
}

// UpdateTPSL recalculates and re-places only TP and SL for a strategy without
// touching grid level orders. Called when only TP/SL settings changed.
func (e *Engine) UpdateTPSL(ctx context.Context, strategyID string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if ok {
			sr.submit(func(ctx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				if sr.cycle == nil || sr.strategy.Status != StatusActive {
					return
				}
				if err := sr.updateTP(ctx); err != nil {
					sr.errlog(ctx, "Ошибка обновления TP: "+err.Error())
				}
				if err := sr.updateSL(ctx); err != nil {
					sr.errlog(ctx, "Ошибка обновления SL: "+err.Error())
				}
			})
			return
		}
	}
}

// GetSignalState returns the current signal state string for a running strategy:
// "buy", "sell", "neutral", or "" (no signal filter / unknown).
func (e *Engine) GetSignalState(strategyID string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if ok {
			sr.mu.Lock()
			state := sr.currentSignalState
			sr.mu.Unlock()
			return state
		}
	}
	return ""
}

// PushSignalOverride recomputes and directly sets currentSignalState for every
// strategy that uses the named signal. Works even for stopped strategies and
// strategies without an active signal-monitor subscription.
func (e *Engine) PushSignalOverride(signalName string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		if runner.signalEngine == nil {
			continue
		}
		// Snapshot runners under RLock, then check signal configs without holding runner.mu —
		// same lock-ordering fix as ActiveStats: runner.mu.RLock + sr.mu.Lock deadlocks
		// with worker tasks that hold sr.mu and call RegisterOrder (ar.mu.Lock).
		runner.mu.RLock()
		allSR := make([]*StrategyRunner, 0, len(runner.strategies))
		for _, sr := range runner.strategies {
			allSR = append(allSR, sr)
		}
		runner.mu.RUnlock()

		var targets []*StrategyRunner
		for _, sr := range allSR {
			sr.mu.Lock()
			for _, cfg := range sr.strategy.SignalConfigs {
				if cfg.Name == signalName {
					targets = append(targets, sr)
					break
				}
			}
			sr.mu.Unlock()
		}

		for _, sr := range targets {
			sr.mu.Lock()
			configs := sr.strategy.SignalConfigs
			symbol := sr.strategy.Symbol
			sr.mu.Unlock()

			tf := "60"
			for _, cfg := range configs {
				if v, ok := cfg.Params["tf"]; ok {
					if s, ok2 := v.(string); ok2 && s != "" {
						tf = s
						break
					}
				}
			}
			sigCfgs := make([]signal.Config, len(configs))
			for i, c := range configs {
				sigCfgs[i] = signal.Config{Name: c.Name, Params: c.Params}
			}
			// ComputeStateForce works even with empty candle snapshot,
			// so override-aware signals (rsi-test) always return the right state.
			state := runner.signalEngine.ComputeStateForce(symbol, tf, sigCfgs)
			sr.mu.Lock()
			sr.currentSignalState = string(state)
			sr.mu.Unlock()
		}
	}
}

// GetSignalValues returns per-signal numeric values for a running strategy
// (e.g. RSI = 49.5). Returns nil when no signal filter is active or no values available.
func (e *Engine) GetSignalValues(strategyID string) map[string]float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if !ok {
			continue
		}
		sr.mu.Lock()
		configs := sr.strategy.SignalConfigs
		symbol := sr.strategy.Symbol
		sr.mu.Unlock()
		if len(configs) == 0 || runner.signalEngine == nil {
			return nil
		}
		tf := "1h"
		for _, cfg := range configs {
			if v, ok2 := cfg.Params["tf"]; ok2 {
				if s, ok3 := v.(string); ok3 && s != "" {
					tf = s
					break
				}
			}
		}
		sigCfgs := make([]signal.Config, len(configs))
		for i, c := range configs {
			sigCfgs[i] = signal.Config{Name: c.Name, Params: c.Params}
		}
		return runner.signalEngine.QueryValues(symbol, tf, sigCfgs)
	}
	return nil
}

// GetAccountRunner returns the AccountRunner for the given account, or nil if not found.
func (e *Engine) GetAccountRunner(accountID string) *AccountRunner {
	e.mu.RLock()
	r := e.runners[accountID]
	e.mu.RUnlock()
	return r
}

type accountInfo struct {
	creds         trader.Credentials
	accountLabel  string
	ownerUsername string
}

func (e *Engine) loadAccountInfo(ctx context.Context, accountID string) (accountInfo, error) {
	var apiKeyEnc, secretEnc, label string
	var username *string
	if err := e.pool.QueryRow(ctx,
		`SELECT ea.api_key_enc, ea.secret_enc, ea.label,
		        NULLIF(COALESCE(u.username, ''), '')
		 FROM exchange_accounts ea
		 JOIN users u ON u.id = ea.owner_id
		 WHERE ea.id = $1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &label, &username); err != nil {
		return accountInfo{}, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, e.encKey)
	if err != nil {
		return accountInfo{}, err
	}
	secret, err := crypto.Decrypt(secretEnc, e.encKey)
	if err != nil {
		return accountInfo{}, err
	}
	un := ""
	if username != nil {
		un = *username
	}
	return accountInfo{
		creds:         trader.Credentials{APIKey: apiKey, SecretKey: secret},
		accountLabel:  label,
		ownerUsername: un,
	}, nil
}

// scanStrategy scans a pgx row into a Strategy.
func scanStrategy(rows interface{ Scan(...any) error }, s *Strategy) error {
	var dir, stat, tpm, slt, stepsJSON, signalConfigsJSON, afterStopMode, matrixJSON, entryLevelJSON string
	var sf, hm bool
	err := rows.Scan(
		&s.ID, &s.OwnerID, &s.AccountID, &s.Symbol, &s.Category,
		&dir, &stat,
		&s.GridLevels, &s.GridActive, &s.GridStepPct, &s.GridSizeUSDT,
		&tpm, &s.TPPct, &slt, &s.SLPct, &sf, &hm,
		&s.EntryOrderType, &stepsJSON, &signalConfigsJSON,
		&afterStopMode, &s.CycleCount, &s.MaxCycles, &s.BotID, &matrixJSON, &s.SafeZonePct, &entryLevelJSON,
	)
	if err != nil {
		return err
	}
	s.Direction = Direction(dir)
	s.Status = Status(stat)
	s.TPMode = TPMode(tpm)
	s.SLType = SLType(slt)
	s.SignalFilter = sf
	s.HedgeMode = hm
	s.AfterStopMode = AfterStopMode(afterStopMode)
	if stepsJSON != "" && stepsJSON != "[]" {
		_ = json.Unmarshal([]byte(stepsJSON), &s.Steps)
	}
	if signalConfigsJSON != "" && signalConfigsJSON != "[]" {
		_ = json.Unmarshal([]byte(signalConfigsJSON), &s.SignalConfigs)
	}
	if matrixJSON != "" && matrixJSON != "[]" && matrixJSON != "null" {
		_ = json.Unmarshal([]byte(matrixJSON), &s.MatrixLevels)
	}
	if entryLevelJSON != "" && entryLevelJSON != "null" {
		var el MatrixEntryLevel
		if json.Unmarshal([]byte(entryLevelJSON), &el) == nil {
			s.MatrixEntryLevel = &el
		}
	}
	return nil
}

// scanStrategyRow scans a pgx.Row into a Strategy.
func scanStrategyRow(row interface{ Scan(...any) error }, s *Strategy) error {
	return scanStrategy(row, s)
}

// ─── AccountRunner ──────────────────────────────────────────────────────────

// AccountRunner owns one Bybit private WS and one trade WS connection for one account.
type AccountRunner struct {
	accountID     string
	accountLabel  string
	ownerUsername string
	creds         trader.Credentials
	pool          *pgxpool.Pool
	signalEngine  *signal.Engine
	mu            sync.RWMutex
	strategies    map[string]*StrategyRunner
	orderIndex    map[string]orderRef // exchangeOrderID → ref
	tradeStream   *trader.TradeStream
	cancel        context.CancelFunc
	reconcileMu   sync.Mutex
}

func newAccountRunner(accountID, accountLabel, ownerUsername string, creds trader.Credentials, pool *pgxpool.Pool, signalEngine *signal.Engine, cancel context.CancelFunc) *AccountRunner {
	return &AccountRunner{
		accountID:     accountID,
		accountLabel:  accountLabel,
		ownerUsername: ownerUsername,
		creds:         creds,
		pool:          pool,
		signalEngine:  signalEngine,
		strategies:    make(map[string]*StrategyRunner),
		orderIndex:    make(map[string]orderRef),
		tradeStream:   trader.NewTradeStream(creds),
		cancel:        cancel,
	}
}

// safeLoop runs fn with full panic recovery. If fn panics and ctx is still active,
// it waits 5 s then restarts — so a bug in one goroutine cannot permanently kill it.
// Returns when fn exits without panicking (normal shutdown) or ctx is cancelled.
func safeLoop(ctx context.Context, name string, fn func(context.Context)) {
	for {
		if ctx.Err() != nil {
			return
		}
		panicked := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("safeLoop %s: panic: %v\n%s", name, r, debug.Stack())
				} else {
					panicked = false
				}
			}()
			fn(ctx)
		}()
		if !panicked || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			log.Printf("safeLoop %s: restarting after panic", name)
		}
	}
}

// tryReconcile runs reconcile if one isn't already running, otherwise skips.
func (ar *AccountRunner) tryReconcile(ctx context.Context) {
	if !ar.reconcileMu.TryLock() {
		return
	}
	defer ar.reconcileMu.Unlock()
	ar.reconcile(ctx)
}

// SnapshotOrderIndex returns a copy of the in-memory order index as a set of orderIDs.
func (ar *AccountRunner) SnapshotOrderIndex() map[string]bool {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	snap := make(map[string]bool, len(ar.orderIndex))
	for id := range ar.orderIndex {
		snap[id] = true
	}
	return snap
}

func (ar *AccountRunner) run(ctx context.Context) {
	go safeLoop(ctx, "tradeStream/"+ar.accountID, ar.tradeStream.Run)
	go safeLoop(ctx, "reconcile/"+ar.accountID, ar.startReconcileLoop)
	safeLoop(ctx, "privateStream/"+ar.accountID, func(ctx context.Context) {
		trader.RunPrivateStream(ctx, ar.creds, ar)
	})
}

func (ar *AccountRunner) addStrategy(s Strategy) {
	ar.mu.Lock()
	existing, ok := ar.strategies[s.ID]
	if ok {
		// Release ar.mu BEFORE acquiring existing.mu (sr.mu) to prevent deadlock.
		// Worker tasks hold sr.mu and call RegisterOrder → ar.mu.Lock(), so holding
		// ar.mu.Lock() while waiting for sr.mu creates a circular wait.
		ar.mu.Unlock()
		existing.mu.Lock()
		if existing.pendingDelete {
			existing.mu.Unlock()
			return
		}
		prevStatus := existing.strategy.Status
		prevSignalFilter := existing.strategy.SignalFilter
		prevConfigsLen := len(existing.strategy.SignalConfigs)
		if existing.strategy.HedgeMode != s.HedgeMode {
			existing.positionModeVerified = false
		}
		existing.strategy = s
		existing.mu.Unlock()
		// Re-activate: runner was stopped/idle, now active again — start a fresh cycle.
		if prevStatus != StatusActive && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.loadOrStart(ctx) })
		}
		// Stop: cancel placed L-orders but keep TP/SL so the cycle ends naturally.
		if prevStatus != StatusStopped && s.Status == StatusStopped {
			existing.submit(func(ctx context.Context) { existing.handleStopRequest(ctx) })
		}
		// Signal filter or configs changed while the strategy is running.
		if prevStatus == StatusActive && s.Status == StatusActive &&
			(prevSignalFilter != s.SignalFilter || len(s.SignalConfigs) != prevConfigsLen) {
			existing.submit(func(ctx context.Context) { existing.handleSignalConfigUpdate(ctx) })
		}
		return
	}
	// tpPlaceSeq and slPlaceSeq start from a time-based offset so linkIds generated
	// after a restart never collide with ones from previous runs (Bybit 110072).
	seqBase := int(time.Now().Unix()) - 1_700_000_000 // ~47M today, 8 digits, safe for 36-char linkId limit
	sr := &StrategyRunner{strategy: s, runner: ar, tpPlaceSeq: seqBase, slPlaceSeq: seqBase}
	sr.taskCh = make(chan func(context.Context), 64)
	ar.strategies[s.ID] = sr
	ar.mu.Unlock()
	sr.startWorker()
	sr.submit(func(ctx context.Context) { sr.loadOrStart(ctx) })
}

func (ar *AccountRunner) removeStrategy(strategyID string) {
	ar.mu.Lock()
	sr, ok := ar.strategies[strategyID]
	if !ok {
		ar.mu.Unlock()
		return
	}
	delete(ar.strategies, strategyID)
	for id, ref := range ar.orderIndex {
		if ref.strategyID == strategyID {
			delete(ar.orderIndex, id)
		}
	}
	ar.mu.Unlock()
	sr.stopWorker()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("strategy: cancelAllPlaced %s: panic: %v\n%s", strategyID, r, debug.Stack())
			}
		}()
		sr.cancelAllPlaced(context.Background())
	}()
}

// RegisterOrder adds an exchange order → strategy mapping so WS events can be routed.
func (ar *AccountRunner) RegisterOrder(exchangeOrderID string, ref orderRef) {
	ar.mu.Lock()
	ar.orderIndex[exchangeOrderID] = ref
	ar.mu.Unlock()
}

// UnregisterOrder removes a mapping (called on fill or cancel).
func (ar *AccountRunner) UnregisterOrder(exchangeOrderID string) {
	ar.mu.Lock()
	delete(ar.orderIndex, exchangeOrderID)
	ar.mu.Unlock()
}

// OnPositionEvent implements trader.PrivateStreamHandler.
// Handles full and partial position changes.
func (ar *AccountRunner) OnPositionEvent(ev trader.PositionEvent) {
	size, _ := strconv.ParseFloat(ev.Size, 64)
	ar.mu.RLock()
	var matched []*StrategyRunner
	for _, sr := range ar.strategies {
		if sr.strategy.Symbol != ev.Symbol {
			continue
		}
		// In hedge mode each position slot belongs to one direction; route only to
		// the matching strategy so a short-position close doesn't kill the long one.
		if ev.PositionIdx != 0 {
			if positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction) != ev.PositionIdx {
				continue
			}
		}
		matched = append(matched, sr)
	}
	ar.mu.RUnlock()
	for _, sr := range matched {
		if size == 0 {
			sr.submit(func(ctx context.Context) { sr.handlePositionClose(ctx) })
		} else {
			sr.submit(func(ctx context.Context) { sr.handlePartialPositionChange(ctx, size) })
		}
	}
	// Discrepancy check: if position is larger than our recorded fills, trigger reconcile.
	if size > 0 && len(matched) > 0 {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("strategy: position discrepancy check: panic: %v", r)
				}
			}()
			var filledQty float64
			for _, sr := range matched {
				sr.mu.Lock()
				_, q := sr.avgEntry()
				sr.mu.Unlock()
				filledQty += q
			}
			const eps = 0.000001
			if size-filledQty > eps {
				log.Printf("strategy: position discrepancy symbol=%s pos=%.6f fills=%.6f — triggering reconcile", ev.Symbol, size, filledQty)
				ar.tryReconcile(context.Background())
			}
		}()
	}
}

// OnOrderEvent implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnOrderEvent(ev trader.OrderEvent) {
	if ev.OrderStatus == "Cancelled" {
		ar.mu.Lock()
		ref, hasRef := ar.orderIndex[ev.OrderID]
		if !hasRef {
			ref, hasRef = ar.orderIndex[ev.OrderLinkID]
		}
		delete(ar.orderIndex, ev.OrderID)
		delete(ar.orderIndex, ev.OrderLinkID)
		sr := ar.strategies[ref.strategyID]
		ar.mu.Unlock()
		// If a TP or SL belonging to us was cancelled externally, re-place it.
		if hasRef && sr != nil {
			switch ref.refType {
			case "tp", "sl":
				refType := ref.refType
				sr.submit(func(ctx context.Context) { sr.handleTPSLCancelled(ctx, refType) })
			case "level":
				levelID, orderID := ref.levelID, ev.OrderID
				sr.submit(func(ctx context.Context) { sr.handleLevelCancelled(ctx, levelID, orderID) })
			case "matrix_sl":
				levelID := ref.levelID
				sr.submit(func(ctx context.Context) { sr.handleMatrixSLCancelled(ctx, levelID) })
			}
		}
		return
	}
	if ev.OrderStatus != "Filled" {
		return
	}
	// Use a write-lock so we can delete the entry atomically. This prevents
	// duplicate fill events (e.g. after a WS reconnect) from submitting the
	// same handler twice, which would cause a second maybeRestart and a
	// duplicate cycle with two simultaneous TP orders.
	ar.mu.Lock()
	ref, ok := ar.orderIndex[ev.OrderID]
	if !ok {
		// Fallback: market orders may fill before RegisterOrder(orderId) runs;
		// linkID was pre-registered so we can still route the event.
		ref, ok = ar.orderIndex[ev.OrderLinkID]
	}
	if ok {
		delete(ar.orderIndex, ev.OrderID)
		delete(ar.orderIndex, ev.OrderLinkID)
	}
	sr := ar.strategies[ref.strategyID]
	ar.mu.Unlock()
	if !ok || sr == nil {
		return
	}
	switch ref.refType {
	case "level":
		price, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		qty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		levelID := ref.levelID
		sr.submit(func(ctx context.Context) { sr.handleLevelFill(ctx, levelID, price, qty) })
	case "tp":
		fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		fillQty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		sr.submit(func(ctx context.Context) { sr.handleTPFill(ctx, fillPrice, fillQty) })
	case "sl":
		fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		fillQty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		sr.submit(func(ctx context.Context) { sr.handleSLFill(ctx, fillPrice, fillQty) })
	case "matrix_sl":
		fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		levelID := ref.levelID
		sr.submit(func(ctx context.Context) { sr.handleMatrixSLFill(ctx, levelID, fillPrice) })
	}
}

// OnConnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnConnected() {
	log.Printf("strategy: bybit WS connected account=%s", ar.accountID)
	go ar.tryReconcile(context.Background())
}

// OnDisconnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnDisconnected(err error) {
	log.Printf("strategy: bybit WS disconnected account=%s err=%v", ar.accountID, err)
}
