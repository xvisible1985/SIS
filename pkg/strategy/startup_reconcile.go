package strategy

import (
	"context"
	"log"
	"strconv"
	"time"

	"sis/pkg/crypto"
	"sis/pkg/trader"
)

// reconcileStoppedCycles runs once at Engine.Start() after active strategies are loaded.
//
// Problem it solves: when a strategy is stopped while its cycle is still open,
// no StrategyRunner exists to call closeCycle(). If the Bybit position closed
// during that time the cycle stays with ended_at=NULL forever, and the
// ClosedPnlSyncer marks those trades as 'manual' instead of 'strategy'.
//
// Fix: for each strategy_cycle with ended_at=NULL whose strategy is not
// active/finishing, fetch the real Bybit position. If it's gone → close the
// cycle in DB and trigger RecordStrategyTrade so the trade is attributed
// correctly (upgrades 'manual' rows via the UPDATE path in trade_recorder.go).
func (e *Engine) reconcileStoppedCycles(ctx context.Context) {
	type cycleRow struct {
		cycleID   string
		cycleNum  int
		startedAt time.Time
		tpOrderID string
		slOrderID string
		// strategy fields (only what RecordStrategyTrade needs)
		stratID   string
		accountID string
		ownerID   string
		symbol    string
		category  string
		direction string
		hedgeMode bool
		botID     *string
		// credentials (encrypted)
		exchangeName   string
		apiKeyEnc      string
		secretEnc      string
		whitelistedIPs []string
	}

	rows, err := e.pool.Query(ctx, `
		SELECT sc.id, sc.cycle_num, sc.started_at,
		       COALESCE(sc.tp_order_id, ''), COALESCE(sc.sl_order_id, ''),
		       s.id, s.account_id, s.owner_id, s.symbol, s.category,
		       s.direction, COALESCE(s.hedge_mode, true),
		       s.bot_id,
		       ea.exchange, ea.api_key_enc, ea.secret_enc, ea.whitelisted_ips
		FROM strategy_cycles sc
		JOIN strategies       s  ON s.id  = sc.strategy_id
		JOIN exchange_accounts ea ON ea.id = s.account_id
		WHERE sc.ended_at IS NULL
		  AND s.status NOT IN ('active', 'finishing')
		  AND ea.is_active = true`,
	)
	if err != nil {
		log.Printf("startup reconcile (stopped): query: %v", err)
		return
	}
	defer rows.Close()

	var cycles []cycleRow
	for rows.Next() {
		var r cycleRow
		if err := rows.Scan(
			&r.cycleID, &r.cycleNum, &r.startedAt,
			&r.tpOrderID, &r.slOrderID,
			&r.stratID, &r.accountID, &r.ownerID, &r.symbol, &r.category,
			&r.direction, &r.hedgeMode,
			&r.botID,
			&r.exchangeName, &r.apiKeyEnc, &r.secretEnc, &r.whitelistedIPs,
		); err != nil {
			log.Printf("startup reconcile (stopped): scan: %v", err)
			continue
		}
		cycles = append(cycles, r)
	}
	rows.Close()

	if len(cycles) == 0 {
		return
	}
	log.Printf("startup reconcile: %d незакрытых циклов у остановленных стратегий", len(cycles))

	// Cache resolved Exchange (per account) and fetched positions per account.
	// credCache retains the raw trader.Credentials too — RecordStrategyTrade (below,
	// deliberately out of scope for this migration, see the plan) still takes
	// trader.Credentials rather than a trader.Exchange.
	exCache := make(map[string]trader.Exchange)
	credCache := make(map[string]trader.Credentials)
	posCache := make(map[string][]trader.Position)

	for _, c := range cycles {
		// ── decrypt credentials + resolve Exchange (once per account) ─────────
		if _, seen := exCache[c.accountID]; !seen {
			apiKey, err := crypto.Decrypt(c.apiKeyEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				exCache[c.accountID] = nil
				continue
			}
			secret, err := crypto.Decrypt(c.secretEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				exCache[c.accountID] = nil
				continue
			}
			creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: c.accountID, WhitelistedIPs: c.whitelistedIPs}
			credCache[c.accountID] = creds
			exCache[c.accountID] = resolveExchange(c.exchangeName, creds, trader.NewTradeStream(creds))
		}
		ex := exCache[c.accountID]
		if ex == nil {
			continue
		}

		// ── fetch all positions for this account (once per account) ───────────
		if _, seen := posCache[c.accountID]; !seen {
			positions, err := ex.FetchPositions(ctx)
			if err != nil {
				log.Printf("startup reconcile: fetch positions account=%s: %v", c.accountID, err)
				posCache[c.accountID] = nil
				continue
			}
			posCache[c.accountID] = positions
		}
		positions := posCache[c.accountID]
		if positions == nil {
			continue
		}

		// ── check if position still open ──────────────────────────────────────
		dir := Direction(c.direction)
		wantIdx := positionIdxForClose(c.hedgeMode, dir)

		posOpen := false
		for _, p := range positions {
			if p.Symbol != c.symbol {
				continue
			}
			if c.hedgeMode && p.PositionIdx != wantIdx {
				continue
			}
			if sz, err := strconv.ParseFloat(p.Size, 64); err == nil && sz > 0 {
				posOpen = true
				break
			}
		}

		if posOpen {
			log.Printf("startup reconcile [%s %s]: позиция открыта, пропускаем", c.symbol, c.direction)
			continue
		}

		// ── position gone — close cycle in DB ─────────────────────────────────
		tag, err := e.pool.Exec(ctx,
			`UPDATE strategy_cycles SET ended_at = NOW(), result = 'ghost_close'
			 WHERE id = $1 AND ended_at IS NULL`,
			c.cycleID,
		)
		if err != nil {
			log.Printf("startup reconcile [%s cy%d]: update cycle: %v", c.symbol, c.cycleNum, err)
			continue
		}
		if tag.RowsAffected() == 0 {
			continue // already closed concurrently
		}
		log.Printf("startup reconcile [%s %s cy%d]: позиция ушла → ghost_close, запись сделки...",
			c.symbol, c.direction, c.cycleNum)

		// ── async: record trade (will upgrade any manual row by ClosedPnlSyncer) ──
		in := TradeRecordInput{
			Strategy: Strategy{
				ID:        c.stratID,
				AccountID: c.accountID,
				OwnerID:   c.ownerID,
				Symbol:    c.symbol,
				Category:  c.category,
				Direction: dir,
				BotID:     c.botID,
			},
			CycleID:   c.cycleID,
			CycleNum:  c.cycleNum,
			StartedAt: c.startedAt,
			Result:    "ghost_close",
			TPOrderID: c.tpOrderID,
			SLOrderID: c.slOrderID,
		}
		go RecordStrategyTrade(e.pool, credCache[c.accountID], in)
	}
}

// reconcileStoppedNoCycle fetches open positions per account and, for each position,
// checks whether a matching stopped strategy with TP/SL exists but no open cycle.
// If found, notifies the runner so it adopts the position and places TP/SL orders.
func (e *Engine) reconcileStoppedNoCycle(ctx context.Context) {
	type stratInfo struct {
		stratID   string
		symbol    string
		direction string
		hedgeMode bool
	}
	type accountInfo struct {
		exchangeName   string
		apiKeyEnc      string
		secretEnc      string
		whitelistedIPs []string
		strats         []stratInfo
	}

	// One query: accounts that have relevant stopped strategies + their strategies.
	rows, err := e.pool.Query(ctx, `
		SELECT s.id, s.account_id, s.symbol, s.direction,
		       COALESCE(s.hedge_mode, false),
		       ea.exchange, ea.api_key_enc, ea.secret_enc, ea.whitelisted_ips
		FROM strategies s
		JOIN exchange_accounts ea ON ea.id = s.account_id
		WHERE s.status = 'stopped'
		  AND (COALESCE(s.tp_pct, 0) > 0 OR COALESCE(s.sl_pct, 0) < 0)
		  AND s.updated_at > NOW() - INTERVAL '7 days'
		  AND NOT EXISTS (
		      SELECT 1 FROM strategy_cycles sc
		      WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL
		  )
		  AND ea.is_active = true`)
	if err != nil {
		log.Printf("reconcileStoppedNoCycle: query: %v", err)
		return
	}

	accounts := make(map[string]*accountInfo)
	for rows.Next() {
		var s stratInfo
		var accountID, exchangeName, apiKeyEnc, secretEnc string
		var whitelistedIPs []string
		if err := rows.Scan(&s.stratID, &accountID, &s.symbol, &s.direction,
			&s.hedgeMode, &exchangeName, &apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
			continue
		}
		if accounts[accountID] == nil {
			accounts[accountID] = &accountInfo{exchangeName: exchangeName, apiKeyEnc: apiKeyEnc, secretEnc: secretEnc, whitelistedIPs: whitelistedIPs}
		}
		accounts[accountID].strats = append(accounts[accountID].strats, s)
	}
	rows.Close()

	if len(accounts) == 0 {
		return
	}

	// Per account: fetch positions once, then match each open position to a strategy.
	for accountID, acc := range accounts {
		apiKey, err := crypto.Decrypt(acc.apiKeyEnc, e.encKey)
		if err != nil {
			continue
		}
		secret, err := crypto.Decrypt(acc.secretEnc, e.encKey)
		if err != nil {
			continue
		}

		creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: acc.whitelistedIPs}
		ex := resolveExchange(acc.exchangeName, creds, trader.NewTradeStream(creds))
		positions, err := ex.FetchPositions(ctx)
		if err != nil {
			log.Printf("reconcileStoppedNoCycle: fetch positions account=%s: %v", accountID, err)
			continue
		}

		for _, p := range positions {
			sz, _ := strconv.ParseFloat(p.Size, 64)
			if sz == 0 {
				continue
			}
			for _, s := range acc.strats {
				if s.symbol != p.Symbol {
					continue
				}
				wantIdx := positionIdxForClose(s.hedgeMode, Direction(s.direction))
				if s.hedgeMode && p.PositionIdx != wantIdx {
					continue
				}
				log.Printf("reconcileStoppedNoCycle [%s %s]: позиция %.4f → стратегия %s",
					p.Symbol, s.direction, sz, s.stratID[:8])
				e.Notify(ctx, s.stratID)
				break
			}
		}
	}
}

// runReconcileMissingRunnersLoop periodically calls reconcileMissingRunners for as long
// as ctx is alive. Call once in a goroutine after Engine.Start().
func (e *Engine) runReconcileMissingRunnersLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reconcileMissingRunners(ctx)
		}
	}
}

// reconcileMissingRunners re-Notifies any active/finishing strategy that is not
// currently present in ANY account runner's strategies map — the backstop for a
// strategy that should be live-managed by the engine but somehow never got registered.
//
// Bot-driven strategy creation (services/api-gateway/bot_engine.go createBotStrategy)
// calls Engine.Notify once, fire-and-forget (`go s.engine.Notify(...)`), with no retry
// if it fails or the goroutine loses a race for any reason. Engine.Start() only loads
// strategies that were already active at boot — anything created afterwards by bot
// automation depends entirely on that single, unretried Notify call succeeding.
//
// Found live (2026-07-17): several bot-created matrix strategies kept trading correctly
// (their exchange orders/fills were completely unaffected — whatever created them also
// evidently started their own goroutines fine) but were invisible to every e.runners-based
// lookup (GetSignalState, GetSignalValues, GetMatrixSafeZone, GetMatrixRelativePreview) —
// all silently returned empty/nil for these strategy IDs, indefinitely. Manually detaching
// and reattaching the strategy from its bot happened to fix it, because that action also
// triggers a fresh Notify() call — but only for the specific strategy touched, and only if
// the user thinks to do it. This makes that self-healing automatic and general, mirroring
// the reconcileStoppedCycles/reconcileStoppedNoCycle pattern already used for other
// "DB says one thing, in-memory engine state says another" drift.
func (e *Engine) reconcileMissingRunners(ctx context.Context) {
	rows, err := e.pool.Query(ctx,
		`SELECT id FROM strategies WHERE status IN ('active','finishing')`)
	if err != nil {
		log.Printf("strategy engine: reconcileMissingRunners: query: %v", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return
	}

	e.mu.RLock()
	registered := make(map[string]bool, len(ids))
	for _, runner := range e.runners {
		runner.mu.RLock()
		for id := range runner.strategies {
			registered[id] = true
		}
		runner.mu.RUnlock()
	}
	e.mu.RUnlock()

	for _, id := range missingIDs(ids, registered) {
		log.Printf("strategy engine: reconcileMissingRunners: %s has no runner — re-registering", id)
		e.Notify(ctx, id)
	}
}

// missingIDs returns the entries of activeIDs that are absent from registered. Pure and
// side-effect-free so reconcileMissingRunners' selection logic is unit-testable without a
// live DB/engine — the DB query and the e.runners scan bracketing this call both require
// real infrastructure this package has no mock for.
func missingIDs(activeIDs []string, registered map[string]bool) []string {
	var missing []string
	for _, id := range activeIDs {
		if !registered[id] {
			missing = append(missing, id)
		}
	}
	return missing
}
