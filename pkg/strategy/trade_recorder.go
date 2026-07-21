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
func AccumulateHedgeSessionPnl(ctx context.Context, pool *pgxpool.Pool, stratID string, netPnl float64) {
	if _, err := pool.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl = accumulated_pnl + $1
		 WHERE (main_strategy_id = $2 OR hedge_strategy_id = $2) AND ended_at IS NULL`,
		netPnl, stratID,
	); err != nil {
		log.Printf("AccumulateHedgeSessionPnl: strategy %s: %v", stratID, err)
	}
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

	// ── 4. Fees (Trade executions) by time range ──────────────────────────────
	closedAt := time.Now()
	posIdx := 1
	if in.Strategy.Direction == DirectionShort {
		posIdx = 2
	}
	var fees float64
	_ = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(ABS(exec_fee)), 0)
		FROM trader_executions
		WHERE account_id = $1 AND symbol = $2
		  AND exec_type = 'Trade'
		  AND exec_time BETWEEN $3 AND $4
		  AND (position_idx IS NULL OR position_idx = $5)`,
		in.Strategy.AccountID, in.Strategy.Symbol, in.StartedAt, closedAt, posIdx,
	).Scan(&fees)

	// ── 5. Funding by time range ──────────────────────────────────────────────
	var funding float64
	_ = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(ABS(exec_fee)), 0)
		FROM trader_executions
		WHERE account_id = $1 AND symbol = $2
		  AND exec_type = 'Funding'
		  AND exec_time BETWEEN $3 AND $4
		  AND (position_idx IS NULL OR position_idx = $5)`,
		in.Strategy.AccountID, in.Strategy.Symbol, in.StartedAt, closedAt, posIdx,
	).Scan(&funding)

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
	netPnl := grossPnl - fees - funding
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
