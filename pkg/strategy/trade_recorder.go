// services/api-gateway/trade_recorder.go
package strategy

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/trader"
)

// TradeRecordInput carries all data needed to write one trade_history row.
// Captured at closeCycle() time before in-memory state is cleared.
type TradeRecordInput struct {
	Strategy  Strategy
	CycleID   string
	CycleNum  int
	StartedAt time.Time
	Result    string // "tp","sl","ghost_close","manual_close","position_gone", …
	TPOrderID string // tpOrderID at cycle close (may be empty)
	SLOrderID string // slOrderID at cycle close (may be empty)
}

// MatrixTPRecordInput carries the data needed to record one global matrix-TP re-arm.
// Captured in handleMatrixTPFill before the grid is re-armed.
type MatrixTPRecordInput struct {
	Strategy  Strategy
	CycleNum  int
	OrderID   string  // filled global-TP order id (for Bybit ClosedPnl matching)
	AvgEntry  float64 // position VWAP at TP time
	FillPrice float64 // TP fill price
	FillQty   float64 // TP fill quantity
}

// RecordMatrixTPProfit records the realized PnL of one global matrix-TP fill into
// matrix_tp_profits so the cumulative "Накоплено" counter includes it. A matrix TP
// re-arms in place (same cycle) rather than ending the cycle, so this profit is never
// captured by closeCycle — which records only the single final close event. Must be
// called as a goroutine: it waits for Bybit to finalize before reading the closed PnL.
func RecordMatrixTPProfit(pool *pgxpool.Pool, creds trader.Credentials, in MatrixTPRecordInput) {
	time.Sleep(8 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Authoritative gross PnL from Bybit, matched by the closing order id.
	grossPnl := 0.0
	matched := false
	for attempt := 0; attempt < 3 && !matched; attempt++ {
		if attempt > 0 {
			time.Sleep(10 * time.Second)
		}
		pnls, err := trader.FetchClosedPnlForSymbol(ctx, creds, in.Strategy.Category, in.Strategy.Symbol, 20)
		if err != nil {
			log.Printf("matrix tp recorder [%s cy%d]: fetch closed pnl (attempt %d): %v",
				in.Strategy.Symbol, in.CycleNum, attempt+1, err)
			continue
		}
		for _, p := range pnls {
			if in.OrderID != "" && p.OrderId == in.OrderID {
				grossPnl, _ = strconv.ParseFloat(p.ClosedPnl, 64)
				matched = true
				break
			}
		}
	}

	// Fallback: compute from avg vs fill price when Bybit has no matching entry.
	if !matched {
		grossPnl = (in.FillPrice - in.AvgEntry) * in.FillQty
		if in.Strategy.Direction == DirectionShort {
			grossPnl = (in.AvgEntry - in.FillPrice) * in.FillQty
		}
	}

	InsertMatrixTPProfit(ctx, pool, MatrixTPInsertInput{
		StrategyID: in.Strategy.ID, BotID: in.Strategy.BotID, AccountID: in.Strategy.AccountID,
		CycleNum: in.CycleNum, Symbol: in.Strategy.Symbol, GrossPnl: grossPnl, OrderID: in.OrderID,
	})
}

// MatrixTPInsertInput carries the values needed for one matrix_tp_profits row when the
// gross PnL is already known — e.g. read directly from a Bybit ClosedPnl entry by
// ClosedPnlSyncer, which doesn't need RecordMatrixTPProfit's own retry-fetch (that fetch
// exists for the in-process fill-event call site, which only has fill price/qty at the
// moment of the WS event, not yet the authoritative Bybit-reported realized PnL).
type MatrixTPInsertInput struct {
	StrategyID string
	BotID      *string
	AccountID  string
	CycleNum   int
	Symbol     string
	GrossPnl   float64
	OrderID    string
}

// InsertMatrixTPProfit writes one matrix_tp_profits row: computes fees from
// trader_executions for the given closing order, net_pnl = gross - fees, and inserts
// idempotently keyed on (account_id, bybit_order_id) — a replayed WS event or a
// ClosedPnlSyncer poll re-processing the same close is a safe no-op.
func InsertMatrixTPProfit(ctx context.Context, pool *pgxpool.Pool, in MatrixTPInsertInput) {
	var fees float64
	if in.OrderID != "" {
		_ = pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(ABS(exec_fee)), 0)
			FROM trader_executions
			WHERE account_id = $1 AND order_id = $2 AND exec_type = 'Trade'`,
			in.AccountID, in.OrderID,
		).Scan(&fees)
	}
	netPnl := in.GrossPnl - fees

	var orderIDPtr *string
	if in.OrderID != "" {
		orderIDPtr = &in.OrderID
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO matrix_tp_profits
			(strategy_id, bot_id, account_id, cycle_num, symbol, gross_pnl, fees, net_pnl, bybit_order_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (account_id, bybit_order_id) WHERE bybit_order_id IS NOT NULL
		DO NOTHING`,
		in.StrategyID, in.BotID, in.AccountID, in.CycleNum, in.Symbol,
		in.GrossPnl, fees, netPnl, orderIDPtr,
	); err != nil {
		log.Printf("matrix tp insert [%s cy%d]: %v", in.Symbol, in.CycleNum, err)
		return
	}
	log.Printf("matrix tp insert [%s cy%d]: записано — gross=%.4f fees=%.4f net=%.4f (order=%s)",
		in.Symbol, in.CycleNum, in.GrossPnl, fees, netPnl, in.OrderID)
}

// AccumulateHedgeSessionPnl adds netPnl to the accumulated_pnl of whichever currently-open
// (ended_at IS NULL) hedge_sessions row matches stratID — as either the main or the
// hedge/matrix leg. No-op if stratID isn't part of any open session (a standalone
// strategy, or one between sessions). Called directly from the code path that just
// realized this PnL (RecordStrategyTrade on cycle close, handleMatrixSLFill on a
// per-level SL fill) — never reconstructed later from trade_history, so it isn't exposed
// to that table's close-attribution fragility. See design doc Section 2.
// OnAccumulate, if set, is called after AccumulateHedgeSessionPnl successfully finishes
// writing — used by services/api-gateway to react to накопление changes in near-real-time
// (see docs/superpowers/specs/2026-07-23-paired-close-realtime-design.md component 3).
// nil by default; registered once at startup. Deliberately does not run when the write
// itself failed — only a successful накопление change is worth reacting to.
var OnAccumulate func(stratID string, netPnl float64)

func AccumulateHedgeSessionPnl(ctx context.Context, pool *pgxpool.Pool, stratID string, netPnl float64) {
	if _, err := pool.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl = accumulated_pnl + $1
		 WHERE (main_strategy_id = $2 OR hedge_strategy_id = $2) AND ended_at IS NULL`,
		netPnl, stratID,
	); err != nil {
		log.Printf("AccumulateHedgeSessionPnl: strategy %s: %v", stratID, err)
		return
	}
	if OnAccumulate != nil {
		OnAccumulate(stratID, netPnl)
	}
}

// ApplyUnappliedFunding claims every not-yet-applied Funding execution recorded for
// account+symbol (trader_executions.applied_to_session=false) and, if an active
// (ended_at IS NULL) hedge_sessions pairing currently exists for that account+symbol,
// feeds their total into it — same accumulated_pnl this bot's paired-close (incl.
// breakeven) decision reads. If no pairing is active right now, the funding rows are
// left unclaimed: a standalone (non-paired) strategy simply doesn't track funding in
// accumulated_pnl, and a pairing that activates later can still pick up funding that
// happened just before it did.
//
// Called from two places: AccountRunner.OnExecutionEvent (pkg/strategy/engine.go) reacts
// within moments of the real Bybit funding settlement via the WS "execution" topic; a
// periodic sweep (runFundingReconcileLoop) catches whatever the WS path missed during a
// disconnect. Both share this one function, and the atomic claim (UPDATE ... WHERE
// applied_to_session=false, inside the same transaction as the accumulate) guarantees
// each execution is ever applied exactly once regardless of which path gets there first
// or how many times either runs.
//
// Root cause this exists for (2026-08-14): feesAndFundingInRange's Funding sum has no
// per-leg attribution at all (Bybit doesn't tie funding to an order — see its doc
// comment) — two legs of a pair with overlapping close-time windows could each
// independently re-sum the same funding executions into their own cycle's net_pnl,
// inflating the pair's true cost. Funding is no longer part of per-cycle net_pnl at all
// (see RecordStrategyTrade) — this is now the only path that feeds it into
// accumulated_pnl, exactly once per execution, account+symbol-wide rather than
// per-leg-guessed.
func ApplyUnappliedFunding(ctx context.Context, pool *pgxpool.Pool, accountID, symbol string) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Printf("ApplyUnappliedFunding %s/%s: begin: %v", accountID, symbol, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback(ctx) //nolint:errcheck
		}
	}()

	var stratID string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(hs.hedge_strategy_id::text, hs.main_strategy_id::text)
		 FROM hedge_sessions hs
		 JOIN strategies s ON s.id = hs.main_strategy_id OR s.id = hs.hedge_strategy_id
		 WHERE hs.ended_at IS NULL AND s.account_id = $1 AND s.symbol = $2
		 LIMIT 1`,
		accountID, symbol,
	).Scan(&stratID); err != nil || stratID == "" {
		return // no active pair for this symbol right now — leave funding unclaimed
	}

	rows, err := tx.Query(ctx,
		`UPDATE trader_executions SET applied_to_session = true
		 WHERE account_id = $1 AND symbol = $2 AND exec_type = 'Funding' AND applied_to_session = false
		 RETURNING exec_fee`,
		accountID, symbol,
	)
	if err != nil {
		log.Printf("ApplyUnappliedFunding %s/%s: claim: %v", accountID, symbol, err)
		return
	}
	var total float64
	for rows.Next() {
		var fee float64
		if rows.Scan(&fee) == nil {
			total += fee
		}
	}
	rows.Close()
	if total == 0 {
		if err := tx.Commit(ctx); err == nil {
			committed = true
		}
		return
	}

	// Funding paid (positive exec_fee, Bybit convention) reduces PnL; funding received
	// (negative exec_fee) adds to it — same sign as AccumulateHedgeSessionPnl's netPnl.
	delta := -total
	if _, err := tx.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl = accumulated_pnl + $1
		 WHERE (main_strategy_id = $2 OR hedge_strategy_id = $2) AND ended_at IS NULL`,
		delta, stratID,
	); err != nil {
		log.Printf("ApplyUnappliedFunding %s/%s: accumulate: %v", accountID, symbol, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("ApplyUnappliedFunding %s/%s: commit: %v", accountID, symbol, err)
		return
	}
	committed = true
	if OnAccumulate != nil {
		OnAccumulate(stratID, delta)
	}
}

// MatrixLevelSLAccumulateInput carries the data needed to fee-adjust and accumulate one
// per-level matrix SL close into hedge_sessions.accumulated_pnl.
type MatrixLevelSLAccumulateInput struct {
	StrategyID string
	AccountID  string
	OrderID    string  // the filled SL order's id, for trader_executions fee lookup
	GrossPnl   float64 // already computed synchronously in handleMatrixSLFill
}

// AccumulateMatrixLevelSLPnl fee-adjusts one per-level matrix SL's gross PnL and adds the
// net result to hedge_sessions.accumulated_pnl. Must be called as a goroutine — waits for
// the exchange to record the closing order's fee executions before querying
// trader_executions, mirroring InsertMatrixTPProfit's fee-lookup pattern. Unlike
// RecordStrategyTrade (which re-derives PnL from Bybit's ClosedPnl API, since a full cycle
// close can span multiple entry fills), this only needs the fee side — a single level's SL
// gross PnL is already known precisely from the fill price recorded synchronously in
// handleMatrixSLFill.
func AccumulateMatrixLevelSLPnl(pool *pgxpool.Pool, in MatrixLevelSLAccumulateInput) {
	time.Sleep(8 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accumulateMatrixLevelSLPnlNow(ctx, pool, in)
}

// accumulateMatrixLevelSLPnlNow does the actual fee lookup + accumulate with no delay —
// split out from AccumulateMatrixLevelSLPnl so it's directly testable without an 8s sleep
// per test case (mirrors the RecordMatrixTPProfit/InsertMatrixTPProfit split above).
func accumulateMatrixLevelSLPnlNow(ctx context.Context, pool *pgxpool.Pool, in MatrixLevelSLAccumulateInput) {
	var fees float64
	if in.OrderID != "" {
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(ABS(exec_fee)), 0)
			FROM trader_executions
			WHERE account_id = $1 AND order_id = $2 AND exec_type = 'Trade'`,
			in.AccountID, in.OrderID,
		).Scan(&fees); err != nil {
			log.Printf("accumulateMatrixLevelSLPnlNow: fee lookup for strategy %s order %s: %v", in.StrategyID, in.OrderID, err)
		}
	}
	netPnl := in.GrossPnl - fees
	log.Printf("matrix level SL накопление [strategy %s]: gross=%.4f fees=%.4f net=%.4f (order=%s)",
		in.StrategyID, in.GrossPnl, fees, netPnl, in.OrderID)
	AccumulateHedgeSessionPnl(ctx, pool, in.StrategyID, netPnl)
}

// FeesAndFundingInRange sums Trade/Funding execution fees for account+symbol within
// [from, to]. Deliberately does NOT filter by position_idx: Bybit's execution-list REST
// endpoint (synced into trader_executions by pkg/trader.Syncer) does not reliably report
// it — observed in production always 0 or NULL, even for accounts genuinely holding
// simultaneous long+short positions on the same symbol (Bybit hedge mode). A prior
// position_idx = 1/2 filter here never matched real rows, silently summing to zero for
// every automatic TP/SL close.
//
// stratID8 is this strategy's ID prefix (matches the "SIS_STR-{id8}-..." order-link-id
// convention every order-placement call site uses — see pkg/strategy/cycle.go/matrix.go).
// Trade executions are excluded when their order_link_id is tagged as belonging to a
// DIFFERENT SIS_STR strategy — the concurrent-legs case (a matrix/hedge pair's two legs,
// or two unrelated bots, trading the same symbol at overlapping times) where the blunt
// account+symbol+time query would otherwise double-count or cross-attribute a fee that
// really belongs to the other leg's cycle close. Anything NOT tagged as ours (manual
// fills, no link at all) is still included — fail-open, same as before, since we can't
// prove it belongs to someone else. Funding executions never carry an order_link_id at
// all (Bybit ties funding to the position, not an order), so this filter is a no-op for
// them — funding keeps the old account+symbol+time-only behavior, same trade-off as
// before, accepted as strictly better than the prior always-zero regression.
func FeesAndFundingInRange(ctx context.Context, pool *pgxpool.Pool, accountID, symbol, stratID8 string, from, to time.Time) (fees, funding float64) {
	notOthers := "(order_link_id LIKE $5 OR order_link_id NOT LIKE 'SIS_STR-%' OR order_link_id IS NULL)"
	linkPrefix := "SIS_STR-" + stratID8 + "-%"
	pool.QueryRow(ctx, //nolint:errcheck
		`SELECT COALESCE(SUM(ABS(exec_fee)), 0)
		 FROM trader_executions
		 WHERE account_id = $1 AND symbol = $2 AND exec_type = 'Trade' AND exec_time BETWEEN $3 AND $4
		   AND `+notOthers,
		accountID, symbol, from, to, linkPrefix,
	).Scan(&fees)
	pool.QueryRow(ctx, //nolint:errcheck
		`SELECT COALESCE(SUM(ABS(exec_fee)), 0)
		 FROM trader_executions
		 WHERE account_id = $1 AND symbol = $2 AND exec_type = 'Funding' AND exec_time BETWEEN $3 AND $4
		   AND `+notOthers,
		accountID, symbol, from, to, linkPrefix,
	).Scan(&funding)
	return
}

// RecordStrategyTrade writes a trade_history row for a closed strategy cycle.
// Must be called as a goroutine — it waits for Bybit to process the close
// before querying the authoritative PnL.
func RecordStrategyTrade(pool *pgxpool.Pool, creds trader.Credentials, in TradeRecordInput) {
	// Give Bybit time to finalize the closed position.
	time.Sleep(8 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// ── 1. Entry data from strategy_levels ────────────────────────────────────
	// VWAP of all filled levels in this cycle.
	type levelRow struct {
		filledPrice float64
		sizeUSDT    float64
	}
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(filled_price, target_price), size_usdt
		FROM strategy_levels
		WHERE cycle_id = $1 AND status = 'filled'
		ORDER BY level_idx`, in.CycleID)
	if err != nil {
		log.Printf("trade recorder [%s cy%d]: query levels: %v", in.Strategy.Symbol, in.CycleNum, err)
	}

	var totalValue, totalQty, totalUSDT float64
	if rows != nil {
		for rows.Next() {
			var price, sizeUSDT float64
			if err := rows.Scan(&price, &sizeUSDT); err != nil || price == 0 {
				continue
			}
			qty := sizeUSDT / price
			totalValue += price * qty
			totalQty += qty
			totalUSDT += sizeUSDT
		}
		rows.Close()
	}

	avgEntry := 0.0
	if totalQty > 0 {
		avgEntry = totalValue / totalQty
	}

	// ── 2. Authoritative PnL from Bybit ClosedPnl API ─────────────────────────
	// Retry up to 3 times with backoff.
	var bybitPnl *trader.ClosedPnl
	for attempt := 0; attempt < 3 && bybitPnl == nil; attempt++ {
		if attempt > 0 {
			time.Sleep(10 * time.Second)
		}
		pnls, err := trader.FetchClosedPnlForSymbol(ctx, creds, in.Strategy.Category, in.Strategy.Symbol, 10)
		if err != nil {
			log.Printf("trade recorder [%s cy%d]: fetch closed pnl (attempt %d): %v",
				in.Strategy.Symbol, in.CycleNum, attempt+1, err)
			continue
		}
		// Exact match by the closing order ID (TP or SL order placed by our bot).
		for i, p := range pnls {
			if (in.TPOrderID != "" && p.OrderId == in.TPOrderID) ||
				(in.SLOrderID != "" && p.OrderId == in.SLOrderID) {
				bybitPnl = &pnls[i]
				break
			}
		}
		// Fallback: take the most recent close for this symbol+direction within the cycle.
		// Bybit returns results newest-first.
		if bybitPnl == nil && len(pnls) > 0 {
			wantSide := "Sell" // closing a long = Sell on Bybit
			if in.Strategy.Direction == DirectionShort {
				wantSide = "Buy" // closing a short = Buy on Bybit
			}
			for i, p := range pnls {
				if p.Side == wantSide {
					ms, _ := strconv.ParseInt(p.CreatedTime, 10, 64)
					closeTime := time.UnixMilli(ms)
					// Accept any close that happened at or after the cycle started —
					// covers ghost_close where the gateway was down for >5 minutes.
					if !closeTime.Before(in.StartedAt) {
						bybitPnl = &pnls[i]
						break
					}
				}
			}
		}
	}

	// ── 3. Parse Bybit PnL fields ─────────────────────────────────────────────
	grossPnl := 0.0
	exitPrice := 0.0
	closedQty := totalQty
	var bybitCloseOrderID *string

	if bybitPnl != nil {
		grossPnl, _ = strconv.ParseFloat(bybitPnl.ClosedPnl, 64)
		exitPrice, _ = strconv.ParseFloat(bybitPnl.AvgExitPrice, 64)
		if q, err := strconv.ParseFloat(bybitPnl.Qty, 64); err == nil && q > 0 {
			closedQty = q
		}
		if bybitPnl.OrderId != "" {
			oid := bybitPnl.OrderId
			bybitCloseOrderID = &oid
		}
	}

	// ── 4/5. Fees + funding (Trade/Funding executions) by time range ───────────
	closedAt := time.Now()
	fees, funding := FeesAndFundingInRange(ctx, pool, in.Strategy.AccountID, in.Strategy.Symbol, in.Strategy.ID[:8], in.StartedAt, closedAt)

	// ── 6. Fix result attribution ─────────────────────────────────────────────
	// If cycle was ghost_close but our TP order is the one that fired → "tp".
	// If our SL fired → "sl".
	finalResult := in.Result
	if bybitPnl != nil {
		if in.TPOrderID != "" && bybitPnl.OrderId == in.TPOrderID {
			finalResult = "tp"
		} else if in.SLOrderID != "" && bybitPnl.OrderId == in.SLOrderID {
			finalResult = "sl"
		}
	}

	// ── 7. Derived metrics ────────────────────────────────────────────────────
	// funding is stored in its own column (below) but deliberately NOT subtracted here
	// — see ApplyUnappliedFunding's doc comment for why per-cycle funding attribution
	// can't be trusted for a paired (matrix/hedge) symbol. Funding now only reaches
	// accumulated_pnl (the value paired-close/breakeven decisions read) via that path.
	netPnl := grossPnl - fees
	pnlPct := 0.0
	if totalUSDT > 0 {
		pnlPct = grossPnl / totalUSDT * 100
	}

	// ── 7a. Backfill cycle realized_pnl ──────────────────────────────────────
	// ghost_close sets ended_at but not realized_pnl; fix it here so the terminal
	// P&L column reflects the correct value.
	if grossPnl != 0 && in.CycleID != "" {
		_, _ = pool.Exec(ctx,
			`UPDATE strategy_cycles SET realized_pnl = $1 WHERE id = $2 AND realized_pnl IS NULL`,
			grossPnl, in.CycleID)
	}

	// ── 8. Write to trade_history ────────────────────────────────────────────
	// If ClosedPnlSyncer already wrote a manual row for this bybit close order
	// (race: ghost_close detected after syncer ran), upgrade that row in-place
	// instead of inserting a duplicate that would violate the unique index.
	if bybitCloseOrderID != nil {
		tag, _ := pool.Exec(ctx, `
			UPDATE trade_history SET
				strategy_id  = $1,
				bot_id       = $2,
				owner_id     = $3,
				cycle_num    = $4,
				result       = $5,
				source       = 'strategy',
				avg_entry    = $6,
				exit_price   = $7,
				qty          = $8,
				volume_usdt  = $9,
				pnl          = $10,
				pnl_pct      = $11,
				opened_at    = $12,
				fees         = $13,
				funding      = $14,
				net_pnl      = $15,
				closed_at    = NOW()
			WHERE account_id = $16 AND bybit_close_order_id = $17
			  AND strategy_id IS NULL`,
			in.Strategy.ID, in.Strategy.BotID, in.Strategy.OwnerID,
			in.CycleNum, finalResult,
			avgEntry, exitPrice, closedQty, totalUSDT,
			grossPnl, pnlPct, in.StartedAt,
			fees, funding, netPnl,
			in.Strategy.AccountID, *bybitCloseOrderID,
		)
		if tag.RowsAffected() > 0 {
			log.Printf("trade recorder [%s cy%d]: ручная → стратегия result=%s gross=%.4f net=%.4f",
				in.Strategy.Symbol, in.CycleNum, finalResult, grossPnl, netPnl)
			AccumulateHedgeSessionPnl(ctx, pool, in.Strategy.ID, netPnl)
			return
		}
	}

	// Normal path: INSERT, or re-upsert if we already wrote this cycle earlier.
	var freshInsert bool
	err = pool.QueryRow(ctx, `
		INSERT INTO trade_history (
			strategy_id, bot_id, account_id, owner_id,
			symbol, category, direction, cycle_num, result, source,
			avg_entry, exit_price, qty, volume_usdt,
			pnl, pnl_pct, opened_at, closed_at,
			fees, funding, net_pnl, bybit_close_order_id
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, 'strategy',
			$10, $11, $12, $13,
			$14, $15, $16, NOW(),
			$17, $18, $19, $20
		)
		ON CONFLICT (strategy_id, cycle_num) WHERE strategy_id IS NOT NULL
		DO UPDATE SET
			result               = EXCLUDED.result,
			exit_price           = EXCLUDED.exit_price,
			qty                  = EXCLUDED.qty,
			pnl                  = EXCLUDED.pnl,
			pnl_pct              = EXCLUDED.pnl_pct,
			fees                 = EXCLUDED.fees,
			funding              = EXCLUDED.funding,
			net_pnl              = EXCLUDED.net_pnl,
			bybit_close_order_id = EXCLUDED.bybit_close_order_id,
			closed_at            = NOW()
		RETURNING (xmax = 0)`,
		in.Strategy.ID, in.Strategy.BotID, in.Strategy.AccountID, in.Strategy.OwnerID,
		in.Strategy.Symbol, in.Strategy.Category, string(in.Strategy.Direction),
		in.CycleNum, finalResult,
		avgEntry, exitPrice, closedQty, totalUSDT,
		grossPnl, pnlPct, in.StartedAt,
		fees, funding, netPnl, bybitCloseOrderID,
	).Scan(&freshInsert)
	if err != nil {
		log.Printf("trade recorder [%s cy%d]: upsert: %v", in.Strategy.Symbol, in.CycleNum, err)
		return
	}
	log.Printf("trade recorder [%s cy%d]: записано — result=%s gross=%.4f fees=%.4f funding=%.4f net=%.4f",
		in.Strategy.Symbol, in.CycleNum, finalResult, grossPnl, fees, funding, netPnl)
	if freshInsert {
		AccumulateHedgeSessionPnl(ctx, pool, in.Strategy.ID, netPnl)
	}
}
