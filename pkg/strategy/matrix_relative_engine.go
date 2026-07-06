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

// matrixPlaceRelativeSlot inserts a new pending accumulation level at relative index idx
// (config idx-1 of the side's config array) and places it via placeMatrixLevel, mirroring
// matrixReplaceSlots's insert+append+place pattern. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlaceRelativeSlot(ctx context.Context, side string, idx int, target float64, cfg MatrixLevel, currentPrice float64) {
	slot := idx
	if side == "below" {
		slot = -idx
	}

	sizeUSDT := cfg.SizePct / 100 * sr.effectiveDeposit(currentPrice)
	priceForQty := target
	if priceForQty == 0 {
		priceForQty = currentPrice
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
	qty := rawQty

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
	if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REL] выставление слота L(%d): %v", slot, err))
	} else {
		orderType := "limit"
		if placed.ExchangeOrderID == "" {
			orderType = "virtual"
		}
		sr.info(ctx, fmt.Sprintf("[REL] L%d: %s %s qty=%s @ %.4f (цена_рынка=%.4f)",
			slot, side2, orderType, qty, target, currentPrice))
	}
}

// matrixRelativeExpand places the next relative accumulation slot when price reaches its
// target. Only one new slot is placed at a time (skipped if one is already
// pending/placed). Must be called with sr.mu held.
func (sr *StrategyRunner) matrixRelativeExpand(ctx context.Context, currentPrice float64) {
	if sr.cycle == nil {
		return
	}
	side := matrixAccumSide(sr.strategy.Direction)
	entry := sr.matrixEntryPrice()
	cfgLevels := filterMatrixLevels(sr.strategy.MatrixLevels, side)
	n := len(cfgLevels)
	if n == 0 {
		return
	}

	// Skip if an accumulation slot on this side is already pending/placed — only one new
	// slot is placed at a time.
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
			return
		}
	}

	idx := nextConfigIndex(sr.levels, side, n)
	if idx == 0 {
		return // cap reached
	}
	cfg := cfgLevels[idx-1]
	target := nextSlotPrice(sr.levels, entry, side, cfg.PriceStepPct)

	reached := (side == "below" && currentPrice <= target) || (side == "above" && currentPrice >= target)
	if !reached {
		return
	}

	sr.info(ctx, fmt.Sprintf("[REL] расширение: слот L(%s%d) @ %.4f (шаг %.2f%%)",
		signForSide(side), idx, target, cfg.PriceStepPct))
	sr.matrixPlaceRelativeSlot(ctx, side, idx, target, cfg, currentPrice)
}

// signForSide returns "-" for the below side (negative slot labels) and "+" otherwise,
// purely for log message formatting.
func signForSide(side string) string {
	if side == "below" {
		return "-"
	}
	return "+"
}
