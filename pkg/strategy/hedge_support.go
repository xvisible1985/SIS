// pkg/strategy/hedge_support.go
//
// Helper functions called by the strategy engine when a hedge bot
// activates or deactivates TP/SL suppression on a main strategy.

package strategy

import (
	"context"
	"fmt"

	"sis/pkg/trader"
)

// cancelTPForHedge cancels the current TP order (if any) and clears it from DB.
// Called when a hedge bot activates TP suppression on this strategy.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) cancelTPForHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.tpOrderID == "" {
		return
	}
	old := sr.tpOrderID
	sr.runner.UnregisterOrder(old)
	sr.tpOrderID = ""
	if sr.cycle != nil {
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)
	}
	if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
		Symbol:   sr.strategy.Symbol,
		Category: sr.strategy.Category,
		OrderId:  old,
	}); err != nil && !isOrderGone(err) {
		sr.warn(ctx, fmt.Sprintf("cancelTPForHedge: %v", err))
	} else {
		sr.info(ctx, "Хедж: TP-ордер отменён (подавление активно)")
	}
}

// cancelSLForHedge cancels all SL orders (cycle-level + per-level matrix) and clears them from DB.
// Called when a hedge bot activates SL suppression on this strategy.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) cancelSLForHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	// Cancel cycle-level SL (grid strategies).
	if sr.slOrderID != "" {
		old := sr.slOrderID
		sr.runner.UnregisterOrder(old)
		sr.slOrderID = ""
		if sr.cycle != nil {
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET sl_order_id=NULL WHERE id=$1`, sr.cycle.ID)
		}
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:   sr.strategy.Symbol,
			Category: sr.strategy.Category,
			OrderId:  old,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("cancelSLForHedge (cycle): %v", err))
		}
	}
	// Cancel all per-level SLs (matrix strategies).
	sr.matrixCancelPerLevelSLs(ctx)
	sr.info(ctx, "Хедж: все SL-ордера отменены (подавление активно)")
}

// restoreTPAfterHedge re-places the TP order after a hedge deactivates TP suppression.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) restoreTPAfterHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status != StatusActive {
		return
	}
	if err := sr.updateTPByType(ctx); err != nil {
		sr.warn(ctx, fmt.Sprintf("restoreTPAfterHedge: %v", err))
	} else {
		sr.info(ctx, "Хедж деактивирован: TP-ордер восстановлен")
	}
}

// restoreSLAfterHedge re-places SL orders after a hedge deactivates SL suppression.
// For grid: re-places cycle-level SL. For matrix: re-places all filled per-level SLs.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) restoreSLAfterHedge(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status != StatusActive {
		return
	}
	// Grid strategies: re-place cycle-level SL.
	if sr.strategy.StrategyType != "matrix" {
		if err := sr.updateSL(ctx); err != nil {
			sr.warn(ctx, fmt.Sprintf("restoreSLAfterHedge (cycle): %v", err))
		} else {
			sr.info(ctx, "Хедж деактивирован: SL-ордер восстановлен")
		}
		return
	}
	// Matrix strategies: re-place per-level SLs for all filled levels that lost their SL.
	restored := 0
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.SLOrderID != "" || l.Slot == nil {
			continue
		}
		_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
		if stopPct == nil || l.FilledPrice <= 0 {
			continue
		}
		sr.matrixPlacePerLevelSL(ctx, l, l.FilledPrice, *stopPct)
		restored++
	}
	if restored > 0 {
		sr.info(ctx, fmt.Sprintf("Хедж деактивирован: SL восстановлен на %d уровнях", restored))
	}
}
