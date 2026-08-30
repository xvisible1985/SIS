package strategy

import (
	"context"
	"fmt"
	"strconv"

	"sis/pkg/trader"
)

// This file wires the pure relative-slot helpers (matrix_relative.go) into the live
// matrix engine: it decides which config side accumulates for a given direction, finds
// the current entry price, and places the next progressive accumulation slot when price
// reaches its relative target. See docs/superpowers/specs/2026-07-05-matrix-relative-slots-design.md.

// matrixAccumSide returns the MatrixLevel.Direction ("above"/"below") that holds the
// accumulation (DCA/hedge) configs for the given strategy direction.
//
// Evidence (matrix.go matrixIsVirtual, ~line 163-178):
//   - Short: slot > 0 is "in-direction (below entry)" and reads its config from
//     filterMatrixLevels(..., "above"); slot < 0 lands above entry (against direction).
//   - Long: slot < 0 reads its config from filterMatrixLevels(..., "below") and is the
//     in-direction accumulation side (price drops, DCA buys more); slot > 0 is the
//     counter side (always virtual, triggered on price rise).
//
// So: short accumulates via "above"-configured (positive) slots; long via
// "below"-configured (negative) slots.
func matrixAccumSide(dir Direction) string {
	if dir == DirectionShort {
		return "above"
	}
	return "below"
}

// matrixEntryPrice returns the L(0) fill price, falling back to the cycle's start price,
// or 0 if neither is available. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixEntryPrice() float64 {
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot != nil && *l.Slot == 0 && l.FilledPrice > 0 {
			return l.FilledPrice
		}
	}
	if sr.cycle != nil {
		return sr.cycle.StartPrice
	}
	return 0
}

// matrixNextLevelIdx returns max(existing level_idx)+1, avoiding OrderLinkID collisions
// (mirrors the counter logic in matrixReplaceSlots). Must be called with sr.mu held.
func (sr *StrategyRunner) matrixNextLevelIdx() int {
	next := 0
	for i := range sr.levels {
		if sr.levels[i].LevelIdx > next {
			next = sr.levels[i].LevelIdx
		}
	}
	return next + 1
}

// matrixRelativeSlotSizing computes size_usdt/qty for a new relative slot from
// cfg.SizePct of the current effective deposit, bumped up if needed to meet the
// exchange's minimum notional value. Shared by matrixPlaceRelativeSlot and
// matrixTriggerRelativeVirtualLevel so both size new slots identically.
func (sr *StrategyRunner) matrixRelativeSlotSizing(cfg MatrixLevel, target, currentPrice float64) (sizeUSDT float64, qty string) {
	sizeUSDT = cfg.SizePct / 100 * sr.effectiveDeposit(currentPrice)
	// Price the qty (and the minimum-notional bump below) off currentPrice, not target.
	// matrixRelativeExpand only calls this after matrixSlotReached has already confirmed
	// price reached/passed target, so target is stale by the time execution actually
	// happens: a virtual slot fires as an immediate Market order at currentPrice, and a
	// non-virtual slot is forced to Market too whenever price has gapped past target (see
	// the target==currentPrice shortcut comment on matrixPlaceRelativeSlot's placeMatrixLevel
	// call) — in both cases the real fill price is currentPrice, not target.
	// Sizing off a stale target left qty computed for the price AT INSERTION TIME: correct
	// notional back then, but far short of the exchange minimum once price had run far past
	// target — the slot would fail isMinOrderValue, get cancelled, and immediately retry
	// with the exact same undersized qty forever, since nothing ever recomputed it against
	// the price that mattered. Found live (2026-08-18): BEATUSDT L(-4), target=1.8486 vs.
	// currentPrice=0.24 after an 8-day ~90% decline — 61 cancel/retry cycles, the slot never
	// actually opened.
	priceForQty := currentPrice
	if priceForQty == 0 {
		priceForQty = target
	}
	rawQty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
	// Bump up if needed so qty*price meets exchange minimum notional value (mirrors
	// matrixReplaceSlots).
	if sr.instr.MinNotionalValue > 0 {
		if qf, _ := strconv.ParseFloat(rawQty, 64); qf > 0 {
			bumped := trader.EnsureMinNotional(qf, sr.instr.QtyStep, priceForQty, sr.instr.MinNotionalValue)
			if bumped != qf {
				rawQty = trader.FormatQty(bumped, sr.instr.QtyStep, sr.instr.MinQty)
			}
		}
	}
	return sizeUSDT, rawQty
}

// matrixPlaceRelativeSlot inserts a new pending accumulation level at relative index idx
// (config idx-1 of the side's config array) and places it via placeMatrixLevel, mirroring
// matrixReplaceSlots's insert+append+place pattern. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlaceRelativeSlot(ctx context.Context, side string, idx int, target float64, cfg MatrixLevel, currentPrice float64) {
	slot := idx
	if side == "below" {
		slot = -idx
	}

	sizeUSDT, qty := sr.matrixRelativeSlotSizing(cfg, target, currentPrice)

	side2 := matrixLevelSide(sr.strategy.Direction)
	levelIdx := sr.matrixNextLevelIdx()
	s := slot

	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, levelIdx, side2, target, sizeUSDT, qty, slot,
	).Scan(&levelID); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REL] вставка слота L(%d): %v", slot, err))
		return
	}

	newLevel := GridLevel{
		ID: levelID, LevelIdx: levelIdx, Side: side2,
		TargetPrice: target, SizeUSDT: sizeUSDT, Qty: qty,
		Status: LevelPending, Slot: &s,
	}
	sr.levels = append(sr.levels, newLevel)
	placed := &sr.levels[len(sr.levels)-1]
	// Pass target (not currentPrice) so matrixEntryOrderType's targetPrice==currentPrice
	// shortcut forces Market — matrixRelativeExpand only calls this after matrixSlotReached
	// already confirmed price reached/passed target, so there is no "wait passively" phase
	// left to serve with a resting Limit/StopMarket order. Without this, a fast price gap
	// past target between ticks left the order resting as a Limit at the stale target price,
	// unreachable without a reversal — found live (2026-07-21, HEMIUSDT HEDGE leg).
	if err := sr.placeMatrixLevel(ctx, placed, target); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REL] выставление слота L(%d): %v", slot, err))
	} else {
		orderType := "market"
		if placed.ExchangeOrderID == "" {
			orderType = "virtual"
		}
		sr.info(ctx, fmt.Sprintf("[REL] L%d: %s %s qty=%s @ %.4f (цена_рынка=%.4f)",
			slot, side2, orderType, qty, target, currentPrice))
	}
}

// matrixTriggerRelativeVirtualLevel inserts and immediately fires a relative slot as a
// market order — the relative-slots counterpart of matrixTriggerVirtualLevel. Used for
// (a) the counter/against-direction side, which can only ever be virtual (a resting
// exchange order can't represent "add more against-direction exposure if price moves
// against the position" in a placeable way — see matrixIsVirtual), and (b) the
// accumulation side when the level's config explicitly requests order_type=virtual.
// Unlike matrixPlaceRelativeSlot, there is no resting-order phase: the row is inserted
// as 'pending' only for bookkeeping/orderRef registration, then transitions to 'placed'
// immediately once the market order is accepted — the fill itself arrives via the normal
// WS execution flow, same as any other order. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixTriggerRelativeVirtualLevel(ctx context.Context, side string, idx int, target float64, cfg MatrixLevel, currentPrice float64) {
	slot := idx
	if side == "below" {
		slot = -idx
	}

	sizeUSDT, qty := sr.matrixRelativeSlotSizing(cfg, target, currentPrice)

	side2 := matrixLevelSide(sr.strategy.Direction)
	levelIdx := sr.matrixNextLevelIdx()
	s := slot

	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, levelIdx, side2, target, sizeUSDT, qty, slot,
	).Scan(&levelID); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REL-V] вставка виртуального слота L(%d): %v", slot, err))
		return
	}

	newLevel := GridLevel{
		ID: levelID, LevelIdx: levelIdx, Side: side2,
		TargetPrice: target, SizeUSDT: sizeUSDT, Qty: qty,
		Status: LevelPending, Slot: &s,
	}
	sr.levels = append(sr.levels, newLevel)
	placed := &sr.levels[len(sr.levels)-1]
	sr.matrixPlaceRelativeVirtualOrder(ctx, placed, currentPrice)
}

// matrixPlaceRelativeVirtualOrder places the market order for an already-inserted
// relative-slot level (l.Status must be LevelPending, l.ExchangeOrderID empty). Shared by
// matrixTriggerRelativeVirtualLevel (fresh insert+trigger, the normal path) and
// matrixRetryStuckRelativeSlot (retry after a prior attempt failed or was interrupted by
// a runner restart between insert and placement) — a stuck slot retries through the exact
// same order-placement logic as the original attempt, not a parallel copy of it.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlaceRelativeVirtualOrder(ctx context.Context, l *GridLevel, currentPrice float64) {
	// Risk gate — same account-wide margin pause / per-symbol notional cap as
	// placeMatrixLevel (see risk.go). Virtual/relative-slot orders bypass placeMatrixLevel
	// entirely (it no-ops for virtual levels — "handled by the price monitor" — see its
	// matrixIsVirtual early return), so without this check here a relative-slots strategy
	// could keep pyramiding via market orders past the configured notional cap while its
	// sibling absolute-mode levels stayed correctly blocked. Found live (2026-08-20):
	// BIOUSDT's hedge leg ran three full cycles entirely through this path on an account
	// at ~$11 equity / $2.75 cap, while its $10 non-virtual L(0) sibling stayed blocked.
	{
		var qtyFloat float64
		fmt.Sscanf(l.Qty, "%f", &qtyFloat)
		gatePositionIdx := positionIdxForOpen(sr.strategy.HedgeMode, l.Side)
		if allowed, reason := sr.runner.riskGate(sr.strategy.Symbol, gatePositionIdx, qtyFloat*currentPrice); !allowed {
			if sr.riskBlockReason != reason {
				sr.warn(ctx, fmt.Sprintf("Matrix relative %s: вход заблокирован (%s)", slotLabel(l.Slot), reason))
			}
			sr.riskBlockReason = reason
			return
		} else if sr.riskBlockReason != "" {
			sr.info(ctx, fmt.Sprintf("Matrix relative %s: блокировка входа снята (%s)", slotLabel(l.Slot), sr.riskBlockReason))
			sr.riskBlockReason = ""
		}
	}

	linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-v%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        l.Side,
		OrderType:   "Market",
		Qty:         l.Qty,
		PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
		OrderLinkId: linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		if isMinOrderValue(err) {
			sr.warn(ctx, fmt.Sprintf("Matrix relative virtual %s: объём ордера слишком мал (qty=%s) — слот пропущен", slotLabel(l.Slot), l.Qty))
			l.Status = LevelCancelled
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='cancelled' WHERE id=$1`, l.ID)
		} else {
			sr.errlog(ctx, fmt.Sprintf("[REL-V] выставление виртуального слота %s: %v", slotLabel(l.Slot), err))
		}
		return
	}
	sr.markLevelPlaced(ctx, l, result.OrderId, linkID)
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.info(ctx, fmt.Sprintf("[REL] %s виртуальный запущен @ market (цена_рынка=%.4f)", slotLabel(l.Slot), currentPrice))
}

// matrixRetryStuckRelativeSlot looks for an already-inserted relative slot on this side
// that never completed its transition out of 'pending' — a prior PlaceOrder attempt
// failed (e.g. transient exchange error), or the runner restarted between insert and
// placement — and retries it. Without this, matrixNextRelativeSlot's "blocked while a
// pending/placed slot exists" guard leaves that side stuck forever: matrixRelativeExpand
// only ever tries to create the NEXT slot, it never re-checks an existing stuck one.
// Mirrors the self-healing retry absolute-mode matrix already has for missing per-level
// SLs (matrixPriceTick step 4), applied here to the relative-slots insert path. Found
// live (2026-07-17): a hedge leg's counter side stopped expanding entirely — permanently,
// with price running far past the target — after one virtual-trigger PlaceOrder attempt
// didn't complete.
// Returns true if a stuck slot was found (and a retry attempted), so the caller knows not
// to also try creating a brand-new slot on the same tick. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixRetryStuckRelativeSlot(ctx context.Context, side string, currentPrice float64) bool {
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil || *l.Slot == 0 {
			continue
		}
		if side == "below" && *l.Slot > 0 {
			continue
		}
		if side == "above" && *l.Slot < 0 {
			continue
		}
		if l.Status != LevelPending || l.ExchangeOrderID != "" {
			continue
		}
		if sr.matrixIsVirtual(l) {
			sr.matrixPlaceRelativeVirtualOrder(ctx, l, currentPrice)
			return true
		}
		// l.TargetPrice, not currentPrice: this level was only ever inserted because
		// matrixSlotReached already confirmed price reached/passed it (see
		// matrixPlaceRelativeSlot), so force Market via placeMatrixLevel's
		// targetPrice==currentPrice shortcut rather than risk a stale resting Limit.
		if err := sr.placeMatrixLevel(ctx, l, l.TargetPrice); err != nil {
			sr.errlog(ctx, fmt.Sprintf("[REL] повтор выставления слота %s: %v", slotLabel(l.Slot), err))
		}
		return true
	}
	return false
}

// matrixNextRelativeSlot computes the next relative slot's config index/slot number/
// target price/virtual-ness for the given side ("above" or "below"), or ok=false when
// expansion is currently blocked (a slot on this side is already pending/placed) or the
// concurrency cap is reached. Pure computation, no side effects — the single source of
// truth shared by matrixRelativeExpand (which places/triggers the slot once price
// reaches target) and matrixNextRelativeSlotPreview (read-only, for the chart preview
// before price arrives). Keeping this logic in one place is deliberate: the short-
// direction sign-inversion bug (2026-07-15) happened because the same computation was
// duplicated and only one copy got fixed.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixNextRelativeSlot(side string) (idx, slot int, target float64, cfg MatrixLevel, virtual, ok bool) {
	if sr.cycle == nil {
		return 0, 0, 0, MatrixLevel{}, false, false
	}
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil || *l.Slot == 0 {
			continue
		}
		if side == "below" && *l.Slot > 0 {
			continue
		}
		if side == "above" && *l.Slot < 0 {
			continue
		}
		if l.Status == LevelPending || l.Status == LevelPlaced {
			return 0, 0, 0, MatrixLevel{}, false, false
		}
	}

	entry := sr.matrixEntryPrice()
	cfgLevels := filterMatrixLevels(sr.strategy.MatrixLevels, side)
	n := len(cfgLevels)
	if n == 0 {
		return 0, 0, 0, MatrixLevel{}, false, false
	}
	idx = nextConfigIndex(sr.levels, side, n)
	if idx == 0 {
		return 0, 0, 0, MatrixLevel{}, false, false // cap reached
	}
	cfg = cfgLevels[idx-1]
	target = nextSlotPrice(sr.levels, entry, side, sr.strategy.Direction, cfg.PriceStepPct)
	slot = idx
	if side == "below" {
		slot = -idx
	}
	s := slot
	virtual = sr.matrixIsVirtual(&GridLevel{Slot: &s})
	return idx, slot, target, cfg, virtual, true
}

// matrixNextRelativeSlotPreview is the read-only counterpart of matrixNextRelativeSlot,
// exposed to the API/chart so the upcoming (not-yet-reached) relative target is visible
// in advance — mirroring how absolute-mode virtual levels are pre-inserted and visible
// before they trigger. Returns ok=false when the strategy isn't in relative-slots mode
// or no preview is currently available. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixNextRelativeSlotPreview(side string) (slot int, price float64, virtual bool, ok bool) {
	if !sr.strategy.RelativeSlots {
		return 0, 0, false, false
	}
	_, slot, price, _, virtual, ok = sr.matrixNextRelativeSlot(side)
	return
}

// matrixRelativeExpand places (or triggers, if virtual) the next relative accumulation
// slot on the given side ("above" or "below") once price reaches its target. Only one
// new slot per side is placed at a time. Called once per side per price tick — see
// matrixPriceTick, which calls it for both the strategy direction's accumulation side
// (matrixAccumSide) and the opposite/counter side, so relative-slots strategies build a
// full symmetric chain in both directions, not just the primary accumulation side.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixRelativeExpand(ctx context.Context, side string, currentPrice float64) {
	// A slot already sitting on this side without ever reaching 'placed' means a prior
	// attempt didn't complete — retry it instead of trying to create a new one (which
	// matrixNextRelativeSlot below would refuse to do anyway while it's still pending).
	if sr.matrixRetryStuckRelativeSlot(ctx, side, currentPrice) {
		return
	}

	idx, _, target, cfg, isVirtual, ok := sr.matrixNextRelativeSlot(side)
	if !ok {
		return
	}
	if !matrixSlotReached(sr.strategy.Direction, cfg.PriceStepPct, currentPrice, target) {
		return
	}

	suffix := ""
	if isVirtual {
		suffix = " [виртуальный]"
	}
	sr.info(ctx, fmt.Sprintf("[REL] расширение: слот L(%s%d) @ %.4f (шаг %.2f%%)%s",
		signForSide(side), idx, target, cfg.PriceStepPct, suffix))

	if isVirtual {
		sr.matrixTriggerRelativeVirtualLevel(ctx, side, idx, target, cfg, currentPrice)
	} else {
		sr.matrixPlaceRelativeSlot(ctx, side, idx, target, cfg, currentPrice)
	}
}

// signForSide returns "-" for the below side (negative slot labels) and "+" otherwise,
// purely for log message formatting.
func signForSide(side string) string {
	if side == "below" {
		return "-"
	}
	return "+"
}
