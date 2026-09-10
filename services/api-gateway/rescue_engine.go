package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

// rescueSignalTrigger describes a signal-based condition for the RescueBot
// partial-close trigger. Name is the signal name (e.g. "st-flip"); Params are
// passed to signal.ComputeStateForce as signal.Config.Params.
type rescueSignalTrigger struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// rescuePriceMovePct returns the signed percentage move of a position relative to
// its entry price, from the perspective of that position's profit/loss.
// Positive = move toward profit (in the direction of the position).
// Negative = move against the position (adverse, toward loss).
//
// For long (buy): positive when price rose above entry.
// For short (sell): positive when price fell below entry.
func rescuePriceMovePct(entryAt, currentAt float64, dir string) float64 {
	if entryAt <= 0 {
		return 0
	}
	switch dir {
	case "buy":
		return (currentAt - entryAt) / entryAt * 100
	case "sell":
		return (entryAt - currentAt) / entryAt * 100
	}
	return 0
}

// rescuePriceMoveExceeds reports whether movePct has crossed threshold in the
// direction indicated by threshold's sign:
//   - negative threshold (-2): fire when loss ≥ 2% (movePct ≤ threshold)
//   - positive threshold (+2): fire when profit ≥ 2% (movePct ≥ threshold)
//   - zero: always true
func rescuePriceMoveExceeds(movePct, threshold float64) bool {
	if threshold == 0 {
		return true
	}
	if threshold < 0 {
		return movePct <= threshold
	}
	return movePct >= threshold
}

// rescuePriceLevelReached returns true when the current price has reached or
// passed the configured rescue level in the direction adverse to the main position.
//
// For a long (buy) main: the rescue level is a downside price, reached when
// currentAt <= level. For a short (sell) main: it's an upside price, reached
// when currentAt >= level.
func rescuePriceLevelReached(level, currentAt float64, mainDir string) bool {
	switch mainDir {
	case "buy":
		return currentAt <= level
	case "sell":
		return currentAt >= level
	}
	return false
}

// rescueTriggersMet returns true when ALL active (non-nil) triggers are
// satisfied simultaneously (AND logic). signalMet is pre-computed by the caller
// (requires signal.Engine, so it's injected rather than evaluated here to keep
// this function pure/testable).
//
// Signed price-move triggers: negative threshold = fire when loss reached N%
// (price moved against the position); positive = fire when profit reached N%.
//
// When no triggers are configured, returns true — the outer gate
// (RescuePartialCloseEnabled check) is the caller's responsibility.
func rescueTriggersMet(
	cfg botCfgJSON,
	currentPrice, mainEntryPrice float64, mainDir string,
	accumulatedPnl float64, signalMet bool,
	hedgeEntryPrice, hedgeCurrentPrice float64, hedgeDir string,
) bool {
	if cfg.RescueTriggerPriceMovePct != nil {
		if !rescuePriceMoveExceeds(rescuePriceMovePct(mainEntryPrice, currentPrice, mainDir), *cfg.RescueTriggerPriceMovePct) {
			return false
		}
	}
	if cfg.RescueTriggerHedgePriceMovePct != nil {
		if !rescuePriceMoveExceeds(rescuePriceMovePct(hedgeEntryPrice, hedgeCurrentPrice, hedgeDir), *cfg.RescueTriggerHedgePriceMovePct) {
			return false
		}
	}
	if cfg.RescueTriggerPriceLevel != nil {
		if !rescuePriceLevelReached(*cfg.RescueTriggerPriceLevel, currentPrice, mainDir) {
			return false
		}
	}
	if cfg.RescueTriggerAccumulatedMinUsdt != nil {
		if accumulatedPnl < *cfg.RescueTriggerAccumulatedMinUsdt {
			return false
		}
	}
	if len(cfg.RescueTriggerSignal) > 0 && !signalMet {
		return false
	}
	return true
}

// rescueCooldownElapsed returns true when enough time has passed since the last
// partial close to allow another one. lastPartialCloseAt nil means "never closed"
// → always allowed. minIntervalSec=0 means no cooldown.
func rescueCooldownElapsed(lastPartialCloseAt *time.Time, minIntervalSec int) bool {
	if lastPartialCloseAt == nil {
		return true
	}
	if minIntervalSec <= 0 {
		return true
	}
	return time.Since(*lastPartialCloseAt) >= time.Duration(minIntervalSec)*time.Second
}

// rescueCloseReq is returned by rescuePartialCloseRequest when all conditions
// are met. It carries everything needed to place the reduce-only market order.
type rescueCloseReq struct {
	Symbol string
	Side   string  // "buy" (closing short) or "sell" (closing long)
	Qty    float64 // coin quantity, already rounded to qtyStep
}

// rescuePartialCloseRequest evaluates all rescue conditions and returns a close
// request when every gate is open, or nil when any gate blocks. The caller is
// responsible for checking cfg.RescuePartialCloseEnabled before calling.
func rescuePartialCloseRequest(
	cfg botCfgJSON,
	symbol, mainDir string,
	currentPrice, mainEntryPrice float64,
	accumulatedPnl, mainReducedUsdt float64,
	qtyStep, minQty float64,
	lastPartialCloseAt *time.Time,
	signalMet bool,
	hedgeEntryPrice, hedgeCurrentPrice float64, hedgeDir string,
) *rescueCloseReq {
	if !rescueCooldownElapsed(lastPartialCloseAt, cfg.RescueMinIntervalSec) {
		return nil
	}
	if !rescueTriggersMet(cfg, currentPrice, mainEntryPrice, mainDir, accumulatedPnl, signalMet, hedgeEntryPrice, hedgeCurrentPrice, hedgeDir) {
		return nil
	}
	qty := rescueCalcPartialCloseQty(accumulatedPnl, mainReducedUsdt, currentPrice, qtyStep, minQty)
	if qty <= 0 {
		return nil
	}
	// closing side is opposite to main position direction
	closeSide := "sell"
	if mainDir == "sell" {
		closeSide = "buy"
	}
	return &rescueCloseReq{Symbol: symbol, Side: closeSide, Qty: qty}
}

// recordRescuePartialClose persists a successful partial close into
// hedge_sessions: increments main_reduced_coin and main_reduced_usdt (coin × price)
// and stamps last_partial_close_at = NOW(). The session is identified by its
// hedge_strategy_id (the same ID used everywhere else in the hedge engine to
// locate the open session row).
func recordRescuePartialClose(ctx context.Context, pool *pgxpool.Pool, hedgeStratID string, coinQty, priceAtClose float64) error {
	usdtValue := coinQty * priceAtClose
	_, err := pool.Exec(ctx, `
		UPDATE hedge_sessions
		SET main_reduced_coin  = main_reduced_coin  + $1,
		    main_reduced_usdt  = main_reduced_usdt  + $2,
		    last_partial_close_at = NOW()
		WHERE hedge_strategy_id = $3 AND ended_at IS NULL`,
		coinQty, usdtValue, hedgeStratID,
	)
	if err != nil {
		log.Printf("recordRescuePartialClose [%s]: %v", hedgeStratID, err)
	}
	return err
}

// rescueCalcPartialCloseQty returns the coin quantity to partially close on the
// main position, funded by the hedge's accumulated PnL minus what was already
// reduced. Returns 0 when the available USDT is insufficient (below minQty value
// at current price) or when price is zero.
//
// The result is floored to qtyStep precision (same rounding used by FormatQty in
// pkg/trader/instruments.go) and checked against minQty before returning.
func rescueCalcPartialCloseQty(accumulatedPnl, mainReducedUsdt, price, qtyStep, minQty float64) float64 {
	if price <= 0 || qtyStep <= 0 {
		return 0
	}
	availableUsdt := accumulatedPnl - mainReducedUsdt
	if availableUsdt <= 0 {
		return 0
	}
	rawQty := availableUsdt / price
	// Floor to qtyStep (same as trader.FormatQty semantics).
	qty := math.Floor(rawQty/qtyStep) * qtyStep
	if qty < minQty {
		return 0
	}
	return qty
}

// checkRescuePartialClose is called once per hedge-engine tick for hedge bots
// with RescuePartialCloseEnabled=true. It queries all open hedge sessions for
// the bot, evaluates rescue conditions for each pair, and places a reduce-only
// market order on the main position when all gates are open.
func (s *Server) checkRescuePartialClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, ex trader.Exchange, posMap map[string]map[string]hedgePosInfo) {
	type sessionRow struct {
		hedgeStratID    string
		accumulatedPnl  float64
		mainReducedUsdt float64
		lastCloseAt     *time.Time
		symbol          string
		mainDir         string
	}

	rows, err := s.pool.Query(ctx, `
		SELECT hs.hedge_strategy_id, hs.accumulated_pnl, hs.main_reduced_usdt,
		       hs.last_partial_close_at, ms.symbol, ms.direction
		FROM hedge_sessions hs
		JOIN strategies ms ON ms.id = hs.main_strategy_id
		JOIN strategies hst ON hst.id = hs.hedge_strategy_id
		WHERE hs.bot_id = $1 AND hs.ended_at IS NULL
		  AND ms.status IN ('active','finishing') AND hst.status IN ('active','finishing')`,
		botID)
	if err != nil {
		log.Printf("checkRescuePartialClose [bot %s]: query: %v", botID, err)
		return
	}
	defer rows.Close()

	var sessions []sessionRow
	for rows.Next() {
		var sr sessionRow
		if err := rows.Scan(&sr.hedgeStratID, &sr.accumulatedPnl, &sr.mainReducedUsdt,
			&sr.lastCloseAt, &sr.symbol, &sr.mainDir); err != nil {
			continue
		}
		sessions = append(sessions, sr)
	}
	rows.Close()

	for _, sr := range sessions {
		mainSide := hedgeDirToSide(sr.mainDir)
		hedgeDir := "sell"
		if sr.mainDir == "sell" {
			hedgeDir = "buy"
		}
		hedgeSide := hedgeDirToSide(hedgeDir)

		bySymbol, ok := posMap[sr.symbol]
		if !ok {
			continue
		}
		mainPos, ok := bySymbol[mainSide]
		if !ok {
			continue
		}

		currentPrice := mainPos.MarkPrice
		mainEntryPrice := mainPos.EntryPrice

		hedgeCurrentPrice := 0.0
		hedgeEntryPrice := 0.0
		if hp, ok := bySymbol[hedgeSide]; ok {
			hedgeCurrentPrice = hp.MarkPrice
			hedgeEntryPrice = hp.EntryPrice
		}

		// Evaluate signal triggers (AND logic — all must match rescue direction).
		signalMet := true
		var want signal.State
		if sr.mainDir == "buy" {
			want = signal.Sell // price falling → sell signal confirms rescue for long main
		} else {
			want = signal.Buy
		}
		for _, trig := range cfg.RescueTriggerSignal {
			sc := signal.Config{Name: trig.Name, Params: trig.Params}
			interval := "15"
			if v, ok := trig.Params["tf"].(string); ok && v != "" {
				interval = v
			}
			state := s.signalEngine.ComputeStateForce(sr.symbol, interval, []signal.Config{sc})
			if state != want {
				signalMet = false
				break
			}
		}

		// Fetch instrument constraints (cached 5m by trader package).
		pubInfo, err := trader.GetPublicInstrumentInfo(ctx, "linear", sr.symbol)
		if err != nil {
			log.Printf("checkRescuePartialClose [%s %s]: GetPublicInstrumentInfo: %v", botID, sr.symbol, err)
			continue
		}

		req := rescuePartialCloseRequest(cfg,
			sr.symbol, sr.mainDir,
			currentPrice, mainEntryPrice,
			sr.accumulatedPnl, sr.mainReducedUsdt,
			pubInfo.QtyStep, pubInfo.MinQty,
			sr.lastCloseAt, signalMet,
			hedgeEntryPrice, hedgeCurrentPrice, hedgeDir,
		)
		if req == nil {
			continue
		}

		// Capitalize side for Bybit API: "sell" → "Sell", "buy" → "Buy".
		apiSide := strings.Title(req.Side) //nolint:staticcheck // simple capitalisation, locale-insensitive
		posIdx := 1                         // long main (Buy position) → positionIdx 1
		if sr.mainDir == "sell" {
			posIdx = 2 // short main (Sell position) → positionIdx 2
		}

		// Use SizeStr verbatim when the calculated qty meets or exceeds the remaining
		// position — same convention as matrixLegCloseRequest — so the order closes the
		// position exactly rather than leaving sub-step dust from float rounding.
		qtyStr := trader.FormatQty(req.Qty, pubInfo.QtyStep, pubInfo.MinQty)
		if req.Qty >= mainPos.Size && mainPos.SizeStr != "" {
			qtyStr = mainPos.SizeStr
		}

		orderReq := trader.OrderRequest{
			Symbol:      req.Symbol,
			Category:    "linear",
			Side:        apiSide,
			OrderType:   "Market",
			Qty:         qtyStr,
			ReduceOnly:  true,
			PositionIdx: posIdx,
		}

		result, err := ex.PlaceOrderREST(ctx, orderReq)
		if err != nil {
			log.Printf("checkRescuePartialClose [%s %s]: PlaceOrder: %v", botID, sr.symbol, err)
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж-рескью: частичное закрытие %s %s — ошибка ордера: %v", sr.symbol, sr.mainDir, err),
				"error", "hedge")
			continue
		}

		log.Printf("checkRescuePartialClose [%s %s]: placed reduce-only %s qty=%s orderID=%s",
			botID, sr.symbol, apiSide, orderReq.Qty, result.OrderId)

		if err := recordRescuePartialClose(ctx, s.pool, sr.hedgeStratID, req.Qty, currentPrice); err != nil {
			log.Printf("checkRescuePartialClose [%s %s]: recordRescuePartialClose: %v", botID, sr.symbol, err)
		}

		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж-рескью: частичное закрытие мейна %s %s — qty=%s по цене %.4f (hedge PnL=%.2f USDT)",
				sr.symbol, sr.mainDir, orderReq.Qty, currentPrice, sr.accumulatedPnl),
			"info", "hedge")
	}
}
