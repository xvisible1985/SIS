package strategy

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

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
		        entry_order_type, COALESCE(steps::text,'[]')
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
		        entry_order_type, COALESCE(steps::text,'[]')
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

// LogUserAction writes a user-initiated action to the strategy event log.
func (e *Engine) LogUserAction(ctx context.Context, strategyID, msg string) {
	logEvent(ctx, e.pool, strategyID, "info", msg)
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
			go sr.restartCycle(context.Background())
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
			go func() {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				if sr.cycle == nil || sr.strategy.Status != StatusActive {
					return
				}
				if err := sr.updateTP(context.Background()); err != nil {
					sr.errlog(context.Background(), "Ошибка обновления TP: "+err.Error())
				}
				if err := sr.updateSL(context.Background()); err != nil {
					sr.errlog(context.Background(), "Ошибка обновления SL: "+err.Error())
				}
			}()
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
		&s.EntryOrderType, &stepsJSON,
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

// AccountRunner owns one Bybit private WS and one trade WS connection for one account.
type AccountRunner struct {
	accountID   string
	creds       trader.Credentials
	pool        *pgxpool.Pool
	mu          sync.RWMutex
	strategies  map[string]*StrategyRunner
	orderIndex  map[string]orderRef // exchangeOrderID → ref
	tradeStream *trader.TradeStream
	cancel      context.CancelFunc
}

func newAccountRunner(accountID string, creds trader.Credentials, pool *pgxpool.Pool, cancel context.CancelFunc) *AccountRunner {
	return &AccountRunner{
		accountID:   accountID,
		creds:       creds,
		pool:        pool,
		strategies:  make(map[string]*StrategyRunner),
		orderIndex:  make(map[string]orderRef),
		tradeStream: trader.NewTradeStream(creds),
		cancel:      cancel,
	}
}

func (ar *AccountRunner) run(ctx context.Context) {
	go ar.tradeStream.Run(ctx)
	go ar.startReconcileLoop(ctx)
	trader.RunPrivateStream(ctx, ar.creds, ar)
}

func (ar *AccountRunner) addStrategy(s Strategy) {
	ar.mu.Lock()
	existing, ok := ar.strategies[s.ID]
	if ok {
		existing.mu.Lock()
		prevStatus := existing.strategy.Status
		if existing.strategy.HedgeMode != s.HedgeMode {
			existing.positionModeVerified = false
		}
		existing.strategy = s
		existing.mu.Unlock()
		ar.mu.Unlock()
		// Re-activate: runner was stopped/idle, now active again — start a fresh cycle.
		if prevStatus != StatusActive && s.Status == StatusActive {
			go existing.loadOrStart(context.Background())
		}
		// Stop: cancel placed L-orders but keep TP/SL so the cycle ends naturally.
		if prevStatus != StatusStopped && s.Status == StatusStopped {
			go existing.handleStopRequest(context.Background())
		}
		return
	}
	// tpPlaceSeq and slPlaceSeq start from a time-based offset so linkIds generated
	// after a restart never collide with ones from previous runs (Bybit 110072).
	seqBase := int(time.Now().Unix()) - 1_700_000_000 // ~47M today, 8 digits, safe for 36-char linkId limit
	sr := &StrategyRunner{strategy: s, runner: ar, tpPlaceSeq: seqBase, slPlaceSeq: seqBase}
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
			go sr.handlePositionClose(context.Background())
		} else {
			go sr.handlePartialPositionChange(context.Background(), size)
		}
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
		if hasRef && sr != nil && (ref.refType == "tp" || ref.refType == "sl") {
			go sr.handleTPSLCancelled(context.Background(), ref.refType)
		}
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
		fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		fillQty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		sr.handleTPFill(ctx, fillPrice, fillQty)
	case "sl":
		fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		fillQty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		sr.handleSLFill(ctx, fillPrice, fillQty)
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
