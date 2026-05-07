package strategy

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
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
	pool    *pgxpool.Pool
	encKey  string
	mu      sync.RWMutex
	runners map[string]*AccountRunner // accountID → runner
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
		        COALESCE(steps::text,'[]')
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
		        COALESCE(steps::text,'[]')
		 FROM strategies WHERE id=$1`, strategyID)
	if err := scanStrategyRow(row, &s); err != nil {
		log.Printf("strategy engine: notify %s: %v", strategyID, err)
		return
	}
	if s.Status == StatusStopped {
		e.mu.RLock()
		runner := e.runners[s.AccountID]
		e.mu.RUnlock()
		if runner != nil {
			runner.removeStrategy(strategyID)
		}
		return
	}
	e.loadStrategy(ctx, s)
}

func (e *Engine) loadStrategy(ctx context.Context, s Strategy) {
	e.mu.Lock()
	runner, ok := e.runners[s.AccountID]
	if !ok {
		creds, err := e.loadCreds(ctx, s.AccountID)
		if err != nil {
			e.mu.Unlock()
			log.Printf("strategy engine: creds for account %s: %v", s.AccountID, err)
			return
		}
		runCtx, cancel := context.WithCancel(context.Background())
		runner = newAccountRunner(s.AccountID, creds, e.pool, cancel)
		e.runners[s.AccountID] = runner
		e.mu.Unlock()
		go runner.run(runCtx)
	} else {
		e.mu.Unlock()
	}
	runner.addStrategy(s)
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
			go sr.restartCycle(context.Background())
			return
		}
	}
}

func (e *Engine) loadCreds(ctx context.Context, accountID string) (trader.Credentials, error) {
	var apiKeyEnc, secretEnc string
	if err := e.pool.QueryRow(ctx,
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc); err != nil {
		return trader.Credentials{}, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, e.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	secret, err := crypto.Decrypt(secretEnc, e.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	return trader.Credentials{APIKey: apiKey, SecretKey: secret}, nil
}

// scanStrategy scans a pgx row into a Strategy.
func scanStrategy(rows interface{ Scan(...any) error }, s *Strategy) error {
	var dir, stat, tpm, slt, stepsJSON string
	var sf, hm bool
	err := rows.Scan(
		&s.ID, &s.OwnerID, &s.AccountID, &s.Symbol, &s.Category,
		&dir, &stat,
		&s.GridLevels, &s.GridActive, &s.GridStepPct, &s.GridSizeUSDT,
		&tpm, &s.TPPct, &slt, &s.SLPct, &sf, &hm,
		&stepsJSON,
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
	if stepsJSON != "" && stepsJSON != "[]" {
		_ = json.Unmarshal([]byte(stepsJSON), &s.Steps)
	}
	return nil
}

// scanStrategyRow scans a pgx.Row into a Strategy.
func scanStrategyRow(row interface{ Scan(...any) error }, s *Strategy) error {
	return scanStrategy(row, s)
}

// ─── AccountRunner ──────────────────────────────────────────────────────────

// AccountRunner owns one Bybit private WS connection and all strategies for one account.
type AccountRunner struct {
	accountID  string
	creds      trader.Credentials
	pool       *pgxpool.Pool
	mu         sync.RWMutex
	strategies map[string]*StrategyRunner
	orderIndex map[string]orderRef // exchangeOrderID → ref
	cancel     context.CancelFunc
}

func newAccountRunner(accountID string, creds trader.Credentials, pool *pgxpool.Pool, cancel context.CancelFunc) *AccountRunner {
	return &AccountRunner{
		accountID:  accountID,
		creds:      creds,
		pool:       pool,
		strategies: make(map[string]*StrategyRunner),
		orderIndex: make(map[string]orderRef),
		cancel:     cancel,
	}
}

func (ar *AccountRunner) run(ctx context.Context) {
	go ar.startReconcileLoop(ctx)
	trader.RunPrivateStream(ctx, ar.creds, ar)
}

func (ar *AccountRunner) addStrategy(s Strategy) {
	ar.mu.Lock()
	existing, ok := ar.strategies[s.ID]
	if ok {
		existing.mu.Lock()
		prevStatus := existing.strategy.Status
		existing.strategy = s
		existing.mu.Unlock()
		ar.mu.Unlock()
		// Re-activate: runner was stopped/idle (e.g. after handlePositionClose),
		// now status is active again — start a fresh cycle.
		if prevStatus != StatusActive && s.Status == StatusActive {
			go existing.loadOrStart(context.Background())
		}
		return
	}
	sr := &StrategyRunner{strategy: s, runner: ar}
	ar.strategies[s.ID] = sr
	ar.mu.Unlock()
	go sr.loadOrStart(context.Background())
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
	go sr.cancelAllPlaced(context.Background())
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
// Handles manual position close: stops strategy and logs.
func (ar *AccountRunner) OnPositionEvent(ev trader.PositionEvent) {
	size, _ := strconv.ParseFloat(ev.Size, 64)
	if size != 0 {
		return
	}
	ar.mu.RLock()
	var runners []*StrategyRunner
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
		runners = append(runners, sr)
	}
	ar.mu.RUnlock()
	for _, sr := range runners {
		go sr.handlePositionClose(context.Background())
	}
}

// OnOrderEvent implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnOrderEvent(ev trader.OrderEvent) {
	if ev.OrderStatus == "Cancelled" {
		// Unregister cancelled orders so the slot is freed.
		ar.mu.Lock()
		delete(ar.orderIndex, ev.OrderID)
		delete(ar.orderIndex, ev.OrderLinkID)
		ar.mu.Unlock()
		return
	}
	if ev.OrderStatus != "Filled" {
		return
	}
	ar.mu.RLock()
	ref, ok := ar.orderIndex[ev.OrderID]
	if !ok {
		// Fallback: market orders may fill before RegisterOrder(orderId) runs;
		// linkID was pre-registered so we can still route the event.
		ref, ok = ar.orderIndex[ev.OrderLinkID]
	}
	if !ok {
		ar.mu.RUnlock()
		return
	}
	sr := ar.strategies[ref.strategyID]
	ar.mu.RUnlock()
	if sr == nil {
		return
	}
	ctx := context.Background()
	switch ref.refType {
	case "level":
		price, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		qty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		sr.handleLevelFill(ctx, ref.levelID, price, qty)
	case "tp":
		sr.handleTPFill(ctx)
	case "sl":
		sr.handleSLFill(ctx)
	}
}

// OnConnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnConnected() {
	log.Printf("strategy: bybit WS connected account=%s", ar.accountID)
}

// OnDisconnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnDisconnected(err error) {
	log.Printf("strategy: bybit WS disconnected account=%s err=%v", ar.accountID, err)
}
