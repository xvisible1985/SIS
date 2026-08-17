package strategy

import (
	"context"
	"encoding/json"
	"fmt"
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

	// OnMainTpClosed is called when a strategy cycle closes at TP.
	// Injected by the hedge engine to trigger flip logic.
	OnMainTpClosed func(ctx context.Context, strategyID string)
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
// After loading, reconcileStoppedCycles runs once in the background to close any
// strategy_cycles that were left open when their strategy was stopped while the
// position had already disappeared on Bybit.
func (e *Engine) Start(ctx context.Context) {
	rows, err := e.pool.Query(ctx,
		`SELECT id, owner_id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
		        leverage, COALESCE(margin_type,'isolated'),
		        entry_order_type, COALESCE(steps::text,'[]'),
		        COALESCE(signal_configs::text,'[]'),
		        cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,''),
		        COALESCE(protected_build,false),
		        COALESCE(matrix_rebuild_on_sl,false),
		        COALESCE(matrix_rebuild_from_entry,false),
		        COALESCE(relative_slots,false),
		        COALESCE(strategy_type,'grid'),
		        COALESCE(size_as_main,false),
		        COALESCE(hedge_tp_suppressed,false),
		        COALESCE(hedge_sl_suppressed,false),
		        hedge_stopped_by::text,
		        COALESCE(adopt_position_data::text,''),
		        tp_signal_dir,
		        sl_signal_dir,
		        tp_signal_configs::text,
		        sl_signal_configs::text
		 FROM strategies
		 WHERE status IN ('active','finishing')
		    OR (status = 'stopped' AND EXISTS (
		            SELECT 1 FROM strategy_cycles
		            WHERE strategy_id = strategies.id AND ended_at IS NULL
		        ))
		`)
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

	// Close orphaned cycles for stopped strategies whose Bybit position is gone.
	go e.reconcileStoppedCycles(ctx)
	// Load stopped strategies (no active cycle) that have a real open position
	// and TP/SL configured — one FetchPositions call per account, done in background.
	go e.reconcileStoppedNoCycle(ctx)
	// Ongoing backstop (not just at boot): re-registers any active strategy that ended up
	// with no runner at all — e.g. a bot-created strategy whose one-shot, unretried
	// Notify() call failed. See reconcileMissingRunners for the live incident this fixes.
	go e.runReconcileMissingRunnersLoop(ctx)
	// Safety net for ApplyUnappliedFunding's primary path (AccountRunner.OnExecutionEvent,
	// which reacts within moments of Bybit's real-time WS "execution" push) — catches
	// funding settlements the WS path missed during a disconnect/reconnect gap.
	go e.runFundingReconcileLoop(ctx)
}

// fundingReconcileInterval bounds how stale accumulated_pnl's funding component can get
// if the WS "execution" feed misses a settlement (disconnect, reconnect gap) — the
// primary path (AccountRunner.OnExecutionEvent) applies funding within moments of the
// real Bybit settlement, so this is a backstop, not the main channel.
const fundingReconcileInterval = 4 * time.Hour

// runFundingReconcileLoop periodically calls ApplyUnappliedFunding for every
// account+symbol that currently has an active (paired) hedge_sessions row. Safe to run
// concurrently with the WS-driven path — ApplyUnappliedFunding's atomic claim guarantees
// each funding execution is applied exactly once regardless of which path gets there first.
func (e *Engine) runFundingReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(fundingReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reconcileFundingOnce(ctx)
		}
	}
}

func (e *Engine) reconcileFundingOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("strategy: reconcileFundingOnce: panic: %v\n%s", r, debug.Stack())
		}
	}()
	rows, err := e.pool.Query(ctx,
		`SELECT DISTINCT s.account_id, s.symbol
		 FROM hedge_sessions hs
		 JOIN strategies s ON s.id = hs.main_strategy_id OR s.id = hs.hedge_strategy_id
		 WHERE hs.ended_at IS NULL`)
	if err != nil {
		log.Printf("strategy: reconcileFundingOnce: query active pairs: %v", err)
		return
	}
	type pair struct{ accountID, symbol string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if rows.Scan(&p.accountID, &p.symbol) == nil {
			pairs = append(pairs, p)
		}
	}
	rows.Close()
	for _, p := range pairs {
		ApplyUnappliedFunding(ctx, e.pool, p.accountID, p.symbol)
	}
}

// Notify reloads a strategy from DB after a REST update (status change or param edit).
func (e *Engine) Notify(ctx context.Context, strategyID string) {
	var s Strategy
	row := e.pool.QueryRow(ctx,
		`SELECT id, owner_id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
		        leverage, COALESCE(margin_type,'isolated'),
		        entry_order_type, COALESCE(steps::text,'[]'),
		        COALESCE(signal_configs::text,'[]'),
		        cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,''),
		        COALESCE(protected_build,false),
		        COALESCE(matrix_rebuild_on_sl,false),
		        COALESCE(matrix_rebuild_from_entry,false),
		        COALESCE(relative_slots,false),
		        COALESCE(strategy_type,'grid'),
		        COALESCE(size_as_main,false),
		        COALESCE(hedge_tp_suppressed,false),
		        COALESCE(hedge_sl_suppressed,false),
		        hedge_stopped_by::text,
		        COALESCE(adopt_position_data::text,''),
		        tp_signal_dir,
		        sl_signal_dir,
		        tp_signal_configs::text,
		        sl_signal_configs::text
		 FROM strategies WHERE id=$1`, strategyID)
	if err := scanStrategyRow(row, &s); err != nil {
		log.Printf("strategy engine: notify %s: %v", strategyID, err)
		return
	}
	e.loadStrategy(ctx, s)
}

// NotifyExpectedClose tells the strategy runner (if currently loaded in memory) that an
// external close is about to happen for a known reason — e.g. matrix paired-close market
// orders placed by the bot engine (stopMatrixPair) — so the runner labels the resulting
// trade_history row accurately instead of assuming a genuine manual close. Best-effort:
// a no-op if the strategy/account isn't currently loaded (rare — the close then falls
// back to the default "manual_close" label, same as before this feature existed).
// Must NOT be called with any StrategyRunner lock held.
func (e *Engine) NotifyExpectedClose(strategyID, accountID, reason string) {
	e.mu.RLock()
	runner, ok := e.runners[accountID]
	e.mu.RUnlock()
	if !ok {
		return
	}
	runner.mu.RLock()
	sr, ok := runner.strategies[strategyID]
	runner.mu.RUnlock()
	if !ok {
		return
	}
	sr.mu.Lock()
	sr.expectedCloseReason = reason
	sr.expectedCloseSetAt = time.Now()
	sr.mu.Unlock()
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
		runner = newAccountRunner(s.AccountID, info.accountLabel, info.ownerUsername, info.creds, e.pool, e.signalEngine, e, cancel)
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
	logEvent(ctx, e.pool, strategyID, "info", "api", msg)
}

// ForceRemoveStrategy cancels all exchange orders for a strategy and removes its
// in-memory runner. Called when a strategy is deleted via the API so that TP/SL
// orders are not left dangling on the exchange after a server restart.
func (e *Engine) ForceRemoveStrategy(ctx context.Context, strategyID, accountID string) {
	e.mu.RLock()
	runner, ok := e.runners[accountID]
	e.mu.RUnlock()
	if !ok {
		return
	}
	runner.mu.RLock()
	sr, ok := runner.strategies[strategyID]
	runner.mu.RUnlock()
	if !ok {
		return
	}
	go func() {
		// cancelAllStrategyOrders cancels every open order with the strategy prefix
		// (L-orders, TP, SL). Must be called before removeStrategy so the order
		// index entries are still valid during the cancellation loop.
		sr.cancelAllStrategyOrders(ctx)
		runner.removeStrategy(strategyID)
	}()
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
// Runs for any strategy with an active cycle, including stopped ones —
// a stopped strategy may still hold an open position that needs TP/SL protection.
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
				if sr.cycle == nil {
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

// GetTradingHaltReason returns why order placement is currently suppressed for a strategy:
//   - "" — trading is normal
//   - "Closed", "PreLaunch", etc. — instrument status returned by Bybit (market session closed)
//   - "circuit_breaker" — TP was cancelled >= 5 times in a row; engine stopped retrying
func (e *Engine) GetTradingHaltReason(strategyID string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if ok {
			sr.mu.RLock()
			reason := sr.tradingHaltReason
			streak := sr.tpCancelStreak
			sr.mu.RUnlock()
			if reason != "" {
				return reason
			}
			if streak >= 5 {
				return "circuit_breaker"
			}
			return ""
		}
	}
	return ""
}

// GetTPHalted returns true when trading is halted for any reason (market closed or circuit breaker).
// Kept for backward compatibility — prefer GetTradingHaltReason for detailed reason.
func (e *Engine) GetTPHalted(strategyID string) bool {
	return e.GetTradingHaltReason(strategyID) != ""
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
			sr.mu.RLock()
			state := sr.currentSignalState
			sr.mu.RUnlock()
			return state
		}
	}
	return ""
}

// GetMatrixSafeZone returns the current SafeZone for a matrix strategy.
// By design at most one slot can be in the waiting-for-reentry state at a time,
// so we take the first slot with a valid SL trigger price. Returns nil if no slot
// is waiting or safe_zone_pct is zero.
func (e *Engine) GetMatrixSafeZone(strategyID string) *MatrixSafeZone {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if !ok {
			continue
		}
		sr.mu.RLock()
		szPct := sr.strategy.SafeZonePct
		dir := sr.strategy.Direction
		slots := sr.matrixWaitingSlots
		lastSlot := sr.matrixLastSLSlot
		sr.mu.RUnlock()

		if szPct <= 0 || len(slots) == 0 {
			return nil
		}
		// When multiple slots are waiting, show the SZ for the most recently stopped one.
		// matrixLastSLSlot is updated every time a new SL fires, so it's always current.
		// Fall back to the minimum slot number when lastSlot isn't in the map — the minimum
		// slot is the most recently stopped level (it is nearest to entry and fires last).
		// Deterministic fallback avoids the flickering caused by random Go map iteration.
		slTrigger, ok := slots[lastSlot]
		if !ok || slTrigger <= 0 {
			// deterministic fallback: pick the minimum slot with a valid trigger
			minSlot := -1
			for s, t := range slots {
				if t > 0 && (minSlot == -1 || s < minSlot) {
					minSlot = s
					slTrigger = t
					ok = true
				}
			}
		}
		if !ok {
			return nil
		}
		var low, high float64
		if dir == DirectionLong {
			low = slTrigger
			high = slTrigger * (1 + szPct/100)
		} else {
			low = slTrigger * (1 - szPct/100)
			high = slTrigger
		}
		return &MatrixSafeZone{Low: low, High: high}
	}
	return nil
}

// MatrixRelativePreview describes the upcoming (not-yet-triggered) relative slot for one
// side of a relative_slots matrix strategy — used by the chart to show the next target
// before price actually reaches it, mirroring how absolute-mode virtual levels are
// visible in advance of triggering.
type MatrixRelativePreview struct {
	Slot    int     `json:"slot"`
	Price   float64 `json:"price"`
	Virtual bool    `json:"virtual"`
}

// GetMatrixRelativePreview returns the next accumulation-side and counter-side relative
// slot targets for a relative_slots matrix strategy. Either return value is nil when not
// applicable (not a relative-slots strategy, no active cycle, expansion currently
// blocked by a pending/placed slot on that side, or the concurrency cap is reached).
// Read-only — computes exactly what matrixRelativeExpand would place, without any side
// effects, via the same matrixNextRelativeSlot the live engine uses.
func (e *Engine) GetMatrixRelativePreview(strategyID string) (accum, counter *MatrixRelativePreview) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, runner := range e.runners {
		runner.mu.RLock()
		sr, ok := runner.strategies[strategyID]
		runner.mu.RUnlock()
		if !ok {
			continue
		}
		sr.mu.RLock()
		defer sr.mu.RUnlock()
		if !sr.strategy.RelativeSlots {
			return nil, nil
		}
		accumSide := matrixAccumSide(sr.strategy.Direction)
		counterSide := "below"
		if accumSide == "below" {
			counterSide = "above"
		}
		if s, p, v, ok := sr.matrixNextRelativeSlotPreview(accumSide); ok {
			accum = &MatrixRelativePreview{Slot: s, Price: p, Virtual: v}
		}
		if s, p, v, ok := sr.matrixNextRelativeSlotPreview(counterSide); ok {
			counter = &MatrixRelativePreview{Slot: s, Price: p, Virtual: v}
		}
		return accum, counter
	}
	return nil, nil
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
		sr.mu.RLock()
		configs := sr.strategy.SignalConfigs
		symbol := sr.strategy.Symbol
		sr.mu.RUnlock()
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
	var whitelistedIPs []string
	if err := e.pool.QueryRow(ctx,
		`SELECT ea.api_key_enc, ea.secret_enc, ea.label, ea.whitelisted_ips,
		        NULLIF(COALESCE(u.username, ''), '')
		 FROM exchange_accounts ea
		 JOIN users u ON u.id = ea.owner_id
		 WHERE ea.id = $1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &label, &whitelistedIPs, &username); err != nil {
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
		creds:         trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs},
		accountLabel:  label,
		ownerUsername: un,
	}, nil
}

// scanStrategy scans a pgx row into a Strategy.
func scanStrategy(rows interface{ Scan(...any) error }, s *Strategy) error {
	var dir, stat, tpm, slt, stepsJSON, signalConfigsJSON, matrixJSON, entryLevelJSON, adoptJSON string
	var sf, hm bool
	var stoppedByStr *string
	var tpSignalDir, slSignalDir *string
	var tpSigCfgJSON, slSigCfgJSON *string
	err := rows.Scan(
		&s.ID, &s.OwnerID, &s.AccountID, &s.Symbol, &s.Category,
		&dir, &stat,
		&s.GridLevels, &s.GridActive, &s.MaxStopActive, &s.GridStepPct, &s.GridSizeUSDT,
		&tpm, &s.TPPct, &slt, &s.SLPct, &sf, &hm,
		&s.Leverage, &s.MarginType,
		&s.EntryOrderType, &stepsJSON, &signalConfigsJSON,
		&s.CycleCount, &s.MaxCycles, &s.BotID, &matrixJSON, &s.SafeZonePct, &entryLevelJSON,
		&s.ProtectedBuild,
		&s.RebuildOnSL,
		&s.RebuildFromEntry,
		&s.RelativeSlots,
		&s.StrategyType,
		&s.SizeAsMain,
		&s.HedgeTpSuppressed,
		&s.HedgeSlSuppressed,
		&stoppedByStr,
		&adoptJSON,
		&tpSignalDir,
		&slSignalDir,
		&tpSigCfgJSON,
		&slSigCfgJSON,
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
	s.HedgeStoppedBy = stoppedByStr
	if tpSignalDir != nil {
		s.TPSignalDir = *tpSignalDir
	}
	if slSignalDir != nil {
		s.SLSignalDir = *slSignalDir
	}
	if adoptJSON != "" && adoptJSON != "null" {
		var apd AdoptPositionData
		if err := json.Unmarshal([]byte(adoptJSON), &apd); err == nil && apd.EntryPrice != "" {
			s.AdoptPositionData = &apd
		}
	}
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
	if tpSigCfgJSON != nil && *tpSigCfgJSON != "" && *tpSigCfgJSON != "[]" && *tpSigCfgJSON != "null" {
		_ = json.Unmarshal([]byte(*tpSigCfgJSON), &s.TPSignalConfigs)
	}
	if slSigCfgJSON != nil && *slSigCfgJSON != "" && *slSigCfgJSON != "[]" && *slSigCfgJSON != "null" {
		_ = json.Unmarshal([]byte(*slSigCfgJSON), &s.SLSignalConfigs)
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
	engine        *Engine
	mu            sync.RWMutex
	strategies    map[string]*StrategyRunner
	orderIndex    map[string]orderRef // exchangeOrderID → ref
	tradeStream   *trader.TradeStream
	cancel        context.CancelFunc
	reconcileMu   sync.Mutex

	// positions caches the latest position size (in coins) from private WS events.
	// posAvgEntry caches the exchange avg entry price for the same position.
	// Key for both: "SYMBOL:positionIdx" (e.g. "BTCUSDT:1").
	// discrepancyLoggedAt tracks when a position discrepancy was last logged per symbol
	// to prevent log spam when Bybit sends high-frequency position snapshots.
	posMu               sync.RWMutex
	positions           map[string]float64
	posAvgEntry         map[string]float64
	discrepancyLoggedAt map[string]time.Time

	// authDead guards stopAllOnPermanentAuthFailure against re-entry: the private WS
	// keeps retrying (and re-reporting the same auth failure) every few seconds, but
	// the account only needs to be stopped once. Cleared on a successful reconnect so
	// a later genuine failure (e.g. after the user rotates the key again) is handled.
	authDead bool

	// riskMu guards risk (see risk.go): equity/mmRate/thresholds refreshed periodically
	// by runRiskMonitorLoop, read synchronously by placeMatrixLevel on every new entry.
	riskMu sync.RWMutex
	risk   accountRiskState
}

func newAccountRunner(accountID, accountLabel, ownerUsername string, creds trader.Credentials, pool *pgxpool.Pool, signalEngine *signal.Engine, eng *Engine, cancel context.CancelFunc) *AccountRunner {
	return &AccountRunner{
		accountID:           accountID,
		accountLabel:        accountLabel,
		ownerUsername:       ownerUsername,
		creds:               creds,
		pool:                pool,
		signalEngine:        signalEngine,
		engine:              eng,
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		tradeStream:         trader.NewTradeStream(creds),
		cancel:              cancel,
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
}

// GetPositionSizeCoins returns the cached position size (in coins) for a given symbol
// and positionIdx (0=one-way, 1=long hedge, 2=short hedge). Returns 0 if unknown.
func (ar *AccountRunner) GetPositionSizeCoins(symbol string, positionIdx int) float64 {
	key := symbol + ":" + strconv.Itoa(positionIdx)
	ar.posMu.RLock()
	v := ar.positions[key]
	ar.posMu.RUnlock()
	return v
}

// GetPositionAvgEntry returns the cached exchange avg entry price for a given symbol
// and positionIdx. Returns 0 if not yet received via WS (falls back to avgEntry()).
func (ar *AccountRunner) GetPositionAvgEntry(symbol string, positionIdx int) float64 {
	key := symbol + ":" + strconv.Itoa(positionIdx)
	ar.posMu.RLock()
	v := ar.posAvgEntry[key]
	ar.posMu.RUnlock()
	return v
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
	go safeLoop(ctx, "riskMonitor/"+ar.accountID, ar.runRiskMonitorLoop)
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
		prevStrategyType := existing.strategy.StrategyType
		prevSignalFilter := existing.strategy.SignalFilter
		prevConfigsLen := len(existing.strategy.SignalConfigs)
		prevTpSuppressed := existing.strategy.HedgeTpSuppressed
		prevSlSuppressed := existing.strategy.HedgeSlSuppressed
		hadNoCycle := existing.cycle == nil
		if existing.strategy.HedgeMode != s.HedgeMode {
			existing.positionModeVerified = false
		}
		existing.strategy = s
		existing.mu.Unlock()
		// Re-activate: runner was stopped/idle, now active again — start a fresh cycle.
		if prevStatus != StatusActive && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.loadOrStart(ctx) })
		}
		// Self-heal: an already-registered, still-active runner with no in-memory cycle
		// must reload it — e.g. after reviveMatrixSplitBrain (matrix_engine.go) clears
		// strategy_cycles.ended_at without ever notifying THIS runner to reload it: only
		// a status transition (above) retriggers loadOrStart, but a split-brain revival
		// only touches strategy_cycles, leaving sr.cycle permanently nil even though the
		// strategy is otherwise healthy and active. Without this, the runner can never
		// place/manage TP/SL/levels again, and reconcile()'s orphan sweep keeps cancelling
		// its real resting orders every ~20s as "no active cycle" — found live
		// (2026-07-20): a matrix pair flapped "цикл оживлён" ~46 times over 2+ hours while
		// its TP/SL orders were repeatedly cancelled and never re-placed.
		if strategyNeedsCycleReload(prevStatus, s.Status, hadNoCycle) {
			existing.submit(func(ctx context.Context) { existing.loadOrStart(ctx) })
		}
		// Stop: cancel placed L-orders but keep TP/SL so the cycle ends naturally.
		if prevStatus != StatusStopped && s.Status == StatusStopped {
			existing.submit(func(ctx context.Context) { existing.handleStopRequest(ctx) })
		}
		// Strategy type changed matrix→grid: cancel per-level SL orders and apply
		// grid-style TP/SL for the current position. Works even for stopped strategies.
		if prevStrategyType == "matrix" && s.StrategyType == "grid" {
			existing.submit(func(ctx context.Context) {
				existing.mu.Lock()
				defer existing.mu.Unlock()
				existing.matrixCancelPerLevelSLs(ctx)
				if existing.cycle == nil {
					return
				}
				if err := existing.updateTP(ctx); err != nil {
					log.Printf("strategy: matrix→grid UpdateTP %s: %v", s.ID[:8], err)
				}
				if err := existing.updateSL(ctx); err != nil {
					log.Printf("strategy: matrix→grid UpdateSL %s: %v", s.ID[:8], err)
				}
			})
		}
		// Signal filter or configs changed while the strategy is running.
		if prevStatus == StatusActive && s.Status == StatusActive &&
			(prevSignalFilter != s.SignalFilter || len(s.SignalConfigs) != prevConfigsLen) {
			existing.submit(func(ctx context.Context) { existing.handleSignalConfigUpdate(ctx) })
		}
		// Hedge TP suppression activated — cancel existing TP order immediately.
		if !prevTpSuppressed && s.HedgeTpSuppressed {
			existing.submit(func(ctx context.Context) { existing.cancelTPForHedge(ctx) })
		}
		// Hedge TP suppression cleared — re-place TP if strategy is active.
		if prevTpSuppressed && !s.HedgeTpSuppressed && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.restoreTPAfterHedge(ctx) })
		}
		// Hedge SL suppression activated — cancel all existing SL orders immediately.
		if !prevSlSuppressed && s.HedgeSlSuppressed {
			existing.submit(func(ctx context.Context) { existing.cancelSLForHedge(ctx) })
		}
		// Hedge SL suppression cleared — re-place SL orders if strategy is active.
		if prevSlSuppressed && !s.HedgeSlSuppressed && s.Status == StatusActive {
			existing.submit(func(ctx context.Context) { existing.restoreSLAfterHedge(ctx) })
		}
		return
	}
	// tpPlaceSeq and slPlaceSeq start from a time-based offset so linkIds generated
	// after a restart never collide with ones from previous runs (Bybit 110072).
	seqBase := int(time.Now().Unix()) - 1_700_000_000 // ~47M today, 8 digits, safe for 36-char linkId limit
	sr := &StrategyRunner{strategy: s, runner: ar, tpPlaceSeq: seqBase, slPlaceSeq: seqBase}
	// Buffer sized for bursts (storm markets, mass fills + position events +
	// reconcile tasks arriving together). submit() is intentionally non-blocking
	// and drops on overflow — see submit() for the deadlock-avoidance rationale —
	// so a comfortable buffer minimises drops; the reconcile loop is the backstop.
	sr.taskCh = make(chan func(context.Context), 256)
	ar.strategies[s.ID] = sr
	ar.mu.Unlock()
	sr.startWorker()
	sr.submit(func(ctx context.Context) { sr.loadOrStart(ctx) })
}

// strategyNeedsCycleReload reports whether an already-registered runner must reload its
// in-memory cycle: the strategy was and remains active, yet the runner currently has no
// cycle object. This is always an inconsistent state — a healthy active strategy always
// has an in-memory cycle once loadOrStart has run. When true, without a fresh reload
// the runner can never place/manage TP/SL/levels for that strategy again. Genuine
// reactivation (prevStatus != Active) is intentionally excluded — that case is already
// handled by the reactivation branch right above this check's call site.
func strategyNeedsCycleReload(prevStatus, status Status, hadNoCycle bool) bool {
	return prevStatus == StatusActive && status == StatusActive && hadNoCycle
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
	avgPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)

	// Update position size and avg entry caches.
	// Also detect when avgPrice changes — that means a fill changed the position,
	// so we need to re-place TP with the correct exchange entry price.
	key := ev.Symbol + ":" + strconv.Itoa(ev.PositionIdx)
	ar.posMu.Lock()
	ar.positions[key] = size
	prevAvg := ar.posAvgEntry[key]
	if avgPrice > 0 {
		ar.posAvgEntry[key] = avgPrice
	} else if size == 0 {
		ar.posAvgEntry[key] = 0
	}
	ar.posMu.Unlock()

	// When the exchange avg entry changes (position opened or averaged), re-place TP
	// on all matching strategies so it reflects the accurate entry price.
	// Bybit sends position snapshots on every order event (place/cancel) — the avgPrice
	// guard ensures we only act on real position changes, not noise.
	avgChanged := size > 0 && avgPrice > 0 && avgPrice != prevAvg

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
		// When the exchange avg entry changed (new fill or averaging), re-place TP
		// with the accurate WS price. This runs after handleLevelFill (which uses the
		// internal avgEntry fallback) and corrects the TP to the real exchange value.
		// Also re-place SL: if handleLevelFill ran before this position event arrived,
		// the SL may have been placed with stale (smaller) WS qty. Re-running updateSL
		// now — with the freshly-updated position cache — corrects the SL qty.
		if avgChanged {
			sr.submit(func(ctx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				if sr.cycle == nil || sr.strategy.Status != StatusActive {
					return
				}
				if err := sr.updateTPByType(ctx); err != nil {
					sr.warn(ctx, fmt.Sprintf("position avg update: TP: %v", err))
				}
				if err := sr.updateSL(ctx); err != nil {
					sr.warn(ctx, fmt.Sprintf("position avg update: SL: %v", err))
				}
			})
		}
	}
	// Discrepancy check: if position is larger than our recorded fills, trigger reconcile.
	// Rate-limited to once per 60 s per symbol to prevent log spam when Bybit sends
	// high-frequency position snapshots during an unresolved discrepancy.
	if size > 0 && len(matched) > 0 {
		symbol := ev.Symbol
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
				const cooldown = 60 * time.Second
				ar.posMu.Lock()
				lastLogged := ar.discrepancyLoggedAt[symbol]
				shouldLog := time.Since(lastLogged) >= cooldown
				if shouldLog {
					ar.discrepancyLoggedAt[symbol] = time.Now()
				}
				ar.posMu.Unlock()
				if shouldLog {
					log.Printf("strategy: position discrepancy symbol=%s pos=%.6f fills=%.6f — triggering reconcile", symbol, size, filledQty)
					ar.tryReconcile(context.Background())
				}
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
				cancelType := ev.CancelType
				cancelledOrderID := ev.OrderID
				sr.submit(func(ctx context.Context) { sr.handleTPSLCancelled(ctx, refType, cancelType, cancelledOrderID) })
			case "level":
				levelID, orderID, cancelType := ref.levelID, ev.OrderID, ev.CancelType
				sr.submit(func(ctx context.Context) { sr.handleLevelCancelled(ctx, levelID, orderID, cancelType) })
			case "matrix_sl":
				levelID := ref.levelID
				sr.submit(func(ctx context.Context) { sr.handleMatrixSLCancelled(ctx, levelID) })
			}
		}
		return
	}
	// "Deactivated" is sent by Bybit for conditional (stop) orders when the position closes.
	// For entry level orders, treat it the same as "Cancelled" so they get re-placed if the
	// strategy is still active. TP/SL deactivation is intentional (position closed) — skip.
	if ev.OrderStatus == "Deactivated" {
		ar.mu.Lock()
		ref, hasRef := ar.orderIndex[ev.OrderID]
		if !hasRef {
			ref, hasRef = ar.orderIndex[ev.OrderLinkID]
		}
		delete(ar.orderIndex, ev.OrderID)
		delete(ar.orderIndex, ev.OrderLinkID)
		sr := ar.strategies[ref.strategyID]
		ar.mu.Unlock()
		if hasRef && sr != nil && ref.refType == "level" {
			levelID, orderID, cancelType := ref.levelID, ev.OrderID, ev.CancelType
			sr.submit(func(ctx context.Context) { sr.handleLevelCancelled(ctx, levelID, orderID, cancelType) })
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
		tpOrderID := ev.OrderID
		sr.submit(func(ctx context.Context) { sr.handleTPFill(ctx, tpOrderID, fillPrice, fillQty) })
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

// OnExecutionEvent implements trader.PrivateStreamHandler. Only Funding executions are
// acted on here — Trade executions are still driven entirely by OnOrderEvent/
// OnPositionEvent's fill handling, which already has richer per-strategy context
// (cycle, level) this generic execution feed doesn't carry.
//
// Funding is the one thing Bybit only tells us about at the account+symbol level, tied
// to no order — this is the sole real-time signal for it. Persists the execution (same
// upsert the REST syncer uses, so whichever of the two sees a given exec_id first wins —
// harmless either way) and immediately tries to apply any unclaimed funding for this
// symbol into the active pair's accumulated_pnl, if one exists right now. See
// ApplyUnappliedFunding's doc comment for the full picture (why per-cycle net_pnl no
// longer includes funding at all, and why the periodic runFundingReconcileLoop exists
// as a backstop for whatever a WS disconnect causes this path to miss).
func (ar *AccountRunner) OnExecutionEvent(ev trader.Execution) {
	if ev.ExecType != "Funding" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("strategy: OnExecutionEvent funding: panic: %v\n%s", r, debug.Stack())
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		var ownerID, exchange string
		if err := ar.pool.QueryRow(ctx,
			`SELECT owner_id, exchange FROM exchange_accounts WHERE id = $1`, ar.accountID,
		).Scan(&ownerID, &exchange); err != nil {
			log.Printf("strategy: OnExecutionEvent funding: owner lookup account=%s: %v", ar.accountID, err)
			return
		}
		if err := trader.UpsertExecution(ctx, ar.pool, ownerID, ar.accountID, exchange, ev.Category, ev); err != nil {
			log.Printf("strategy: OnExecutionEvent funding: upsert exec=%s: %v", ev.ExecId, err)
			return
		}
		ApplyUnappliedFunding(ctx, ar.pool, ar.accountID, ev.Symbol)
	}()
}

// OnConnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnConnected() {
	log.Printf("strategy: bybit WS connected account=%s", ar.accountID)
	ar.mu.Lock()
	ar.authDead = false
	ar.mu.Unlock()
	go ar.tryReconcile(context.Background())
}

// OnDisconnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnDisconnected(err error) {
	log.Printf("strategy: bybit WS disconnected account=%s err=%v", ar.accountID, err)
	if !trader.IsPermanentAuthError(err) {
		return
	}
	ar.mu.Lock()
	already := ar.authDead
	ar.authDead = true
	ar.mu.Unlock()
	if !already {
		go ar.stopAllOnPermanentAuthFailure(context.Background(), err)
	}
}

// stopAllOnPermanentAuthFailure stops every active/finishing bot and strategy on this
// account after the exchange reports the API key as expired (trader.IsPermanentAuthError).
// Left running, every strategy's retry loop (order placement, matrix reconcile, TP/SL
// health-check) hammers the same dead key every few seconds forever — this was observed
// live on 2026-08-12: an expired key spammed WS reconnects and order-placement retries
// across 4 strategies indefinitely, and deactivating the exchange_accounts row alone did
// NOT stop it, since that flag isn't read by any of the retry loops.
// Runs once per account (ar.authDead guards re-entry); OnConnected clears the guard so a
// later genuine failure (e.g. after the user rotates the key again) is handled too.
func (ar *AccountRunner) stopAllOnPermanentAuthFailure(ctx context.Context, cause error) {
	log.Printf("strategy: account=%s API key expired (%v) — stopping all bots/strategies", ar.accountID, cause)

	if _, err := ar.pool.Exec(ctx,
		`UPDATE bots SET status='stopped', updated_at=NOW() WHERE account_id=$1 AND status='active'`,
		ar.accountID,
	); err != nil {
		log.Printf("strategy: account=%s stop bots after auth failure: %v", ar.accountID, err)
	}

	ar.mu.RLock()
	ids := make([]string, 0, len(ar.strategies))
	for id, sr := range ar.strategies {
		sr.mu.Lock()
		active := sr.strategy.Status == StatusActive || sr.strategy.Status == StatusFinishing
		sr.mu.Unlock()
		if active {
			ids = append(ids, id)
		}
	}
	ar.mu.RUnlock()

	const msg = "API-ключ биржи истёк — стратегия автоматически остановлена. Обновите ключ в настройках аккаунта и запустите заново."
	for _, id := range ids {
		if _, err := ar.pool.Exec(ctx,
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1 AND status IN ('active','finishing')`,
			id,
		); err != nil {
			log.Printf("strategy: account=%s stop strategy %s after auth failure: %v", ar.accountID, id, err)
			continue
		}
		ar.engine.LogUserAction(ctx, id, msg)
		ar.engine.Notify(ctx, id)
	}
}
