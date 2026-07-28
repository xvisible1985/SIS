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

// rescuePriceMoveTowardMainPct returns how many percent the price has moved
// against the main position (toward the loss side) since its entry. Returns 0
// when the move is in the profitable direction (not a rescue-trigger condition).
//
// mainDir: "buy" = long position, "sell" = short position.
func rescuePriceMoveTowardMainPct(entryAt, currentAt float64, mainDir string) float64 {
	if entryAt <= 0 {
		return 0
	}
	var movePct float64
	switch mainDir {
	case "buy": // long — adverse move is price falling
		movePct = (entryAt - currentAt) / entryAt * 100
	case "sell": // short — adverse move is price rising
		movePct = (currentAt - entryAt) / entryAt * 100
	}
	if movePct < 0 {
		return 0
	}
	return movePct
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
// When no triggers are configured, returns true — the outer gate
// (RescuePartialCloseEnabled check) is the caller's responsibility.
func rescueTriggersMet(cfg botCfgJSON, currentPrice, mainEntryPrice float64, mainDir string, accumulatedPnl float64, signalMet bool) bool {
	if cfg.RescueTriggerPriceMovePct != nil {
		if rescuePriceMoveTowardMainPct(mainEntryPrice, currentPrice, mainDir) < *cfg.RescueTriggerPriceMovePct {
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
	if cfg.RescueTriggerSignal != nil && !signalMet {
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
) *rescueCloseReq {
	if !rescueCooldownElapsed(lastPartialCloseAt, cfg.RescueMinIntervalSec) {
		return nil
	}
	if !rescueTriggersMet(cfg, currentPrice, mainEntryPrice, mainDir, accumulatedPnl, signalMet) {
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
func (s *Server) checkRescuePartialClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, posMap map[string]map[string]hedgePosInfo) {
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

	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		log.Printf("checkRescuePartialClose [bot %s]: creds: %v", botID, err)
		return
	}

	for _, sr := range sessions {
		mainSide := hedgeDirToSide(sr.mainDir)
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

		// Evaluate signal trigger if configured.
		signalMet := true
		if cfg.RescueTriggerSignal != nil {
			trig := cfg.RescueTriggerSignal
			sc := signal.Config{Name: trig.Name, Params: trig.Params}
			interval := "15"
			if v, ok := trig.Params["tf"].(string); ok && v != "" {
				interval = v
			}
			state := s.signalEngine.ComputeStateForce(sr.symbol, interval, []signal.Config{sc})
			// Signal must confirm the rescue direction (adverse to main = beneficial for hedge).
			var want signal.State
			if sr.mainDir == "buy" {
				want = signal.Sell // price falling → sell signal confirms rescue for long main
			} else {
				want = signal.Buy
			}
			signalMet = (state == want)
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

		orderReq := trader.OrderRequest{
			Symbol:      req.Symbol,
			Category:    "linear",
			Side:        apiSide,
			OrderType:   "Market",
			Qty:         trader.FormatQty(req.Qty, pubInfo.QtyStep, pubInfo.MinQty),
			ReduceOnly:  true,
			PositionIdx: posIdx,
		}

		result, err := trader.PlaceOrder(ctx, creds, orderReq)
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
