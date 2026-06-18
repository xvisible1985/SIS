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
		apiKeyEnc string
		secretEnc string
	}

	rows, err := e.pool.Query(ctx, `
		SELECT sc.id, sc.cycle_num, sc.started_at,
		       COALESCE(sc.tp_order_id, ''), COALESCE(sc.sl_order_id, ''),
		       s.id, s.account_id, s.owner_id, s.symbol, s.category,
		       s.direction, COALESCE(s.hedge_mode, true),
		       s.bot_id,
		       ea.api_key_enc, ea.secret_enc
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
			&r.apiKeyEnc, &r.secretEnc,
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

	// Cache decrypted credentials and fetched positions per account.
	type creds struct{ apiKey, secret string }
	credCache := make(map[string]*creds)
	posCache  := make(map[string][]trader.Position)

	for _, c := range cycles {
		// ── decrypt credentials (once per account) ────────────────────────────
		if _, seen := credCache[c.accountID]; !seen {
			apiKey, err := crypto.Decrypt(c.apiKeyEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				credCache[c.accountID] = nil
				continue
			}
			secret, err := crypto.Decrypt(c.secretEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				credCache[c.accountID] = nil
				continue
			}
			credCache[c.accountID] = &creds{apiKey: apiKey, secret: secret}
		}
		cr := credCache[c.accountID]
		if cr == nil {
			continue
		}

		// ── fetch all positions for this account (once per account) ───────────
		if _, seen := posCache[c.accountID]; !seen {
			positions, err := trader.FetchPositions(ctx, trader.Credentials{
				APIKey: cr.apiKey, SecretKey: cr.secret,
			})
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
		go RecordStrategyTrade(e.pool, trader.Credentials{APIKey: cr.apiKey, SecretKey: cr.secret}, in)
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
		apiKeyEnc string
		secretEnc string
		strats    []stratInfo
	}

	// One query: accounts that have relevant stopped strategies + their strategies.
	rows, err := e.pool.Query(ctx, `
		SELECT s.id, s.account_id, s.symbol, s.direction,
		       COALESCE(s.hedge_mode, false),
		       ea.api_key_enc, ea.secret_enc
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
		var accountID, apiKeyEnc, secretEnc string
		if err := rows.Scan(&s.stratID, &accountID, &s.symbol, &s.direction,
			&s.hedgeMode, &apiKeyEnc, &secretEnc); err != nil {
			continue
		}
		if accounts[accountID] == nil {
			accounts[accountID] = &accountInfo{apiKeyEnc: apiKeyEnc, secretEnc: secretEnc}
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

		positions, err := trader.FetchPositions(ctx, trader.Credentials{
			APIKey: apiKey, SecretKey: secret,
		})
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
