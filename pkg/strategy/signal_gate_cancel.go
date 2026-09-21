package strategy

import (
	"context"
	"fmt"

	"sis/pkg/trader"
)

// cancelSignalLostLevels cancels the resting exchange order for every UseSignal=true level
// currently LevelPlaced (unfilled) whose signal no longer agrees, reverting each to Pending
// so the existing virtual-level price-tick monitors (gridVirtualPriceTick, matrixPriceTick)
// resume silently watching it — it re-places automatically once the signal returns (and the
// price condition still holds). Shared by both strategy types: nothing here is grid- or
// matrix-specific, since GridLevel's Status/UseSignal/ExchangeOrderID fields are uniform
// across both.
//
// Deliberately does NOT touch LevelFilled levels — signal loss withholds/removes only
// not-yet-filled orders, never closes an already-open position.
//
// Must be called with sr.mu held (same requirement as its callers in the price-tick paths).
func (sr *StrategyRunner) cancelSignalLostLevels(ctx context.Context) {
	if sr.cycle == nil {
		return
	}
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPlaced || !l.UseSignal || l.ExchangeOrderID == "" {
			continue
		}
		if sr.signalGateAllows() {
			continue // signal still agrees — leave the resting order in place
		}
		// Unregister BEFORE sending cancel so the incoming WS cancel event does not
		// trigger any fill/cancel handler for our own intentional cancel.
		oldOrderID := l.ExchangeOrderID
		oldLinkID := l.ExchangeLinkID
		sr.runner.UnregisterOrder(oldOrderID)
		if oldLinkID != "" {
			sr.runner.UnregisterOrder(oldLinkID)
		}
		if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
			Symbol:   sr.strategy.Symbol,
			Category: sr.strategy.Category,
			OrderId:  oldOrderID,
		}); err != nil && !isOrderGone(err) {
			// Cancel failed — restore registration so reconcile/fill handling still
			// recognizes this order, and retry on the next price tick.
			sr.runner.RegisterOrder(oldOrderID, orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"})
			if oldLinkID != "" {
				sr.runner.RegisterOrder(oldLinkID, orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"})
			}
			sr.warn(ctx, fmt.Sprintf("Signal-gated L%d: отмена ордера не удалась: %v", l.LevelIdx, err))
			continue
		}
		l.Status = LevelPending
		l.ExchangeOrderID = ""
		l.ExchangeLinkID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, exchange_link_id=NULL WHERE id=$1`, l.ID)
		sr.info(ctx, fmt.Sprintf("Signal-gated L%d: сигнал пропал — ордер снят с биржи", l.LevelIdx))
	}
}
