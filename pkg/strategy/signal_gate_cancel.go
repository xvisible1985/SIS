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
// Every UseSignal=true level places as a Market order exclusively (gridVirtualPriceTick,
// matrixTriggerVirtualLevel, matrixPlaceRelativeVirtualOrder all use OrderType: "Market"),
// which fills essentially instantly — there is no meaningful "resting on the book" phase.
// So a still-LevelPlaced level here more often means "the WS fill confirmation just hasn't
// arrived yet" than "genuinely still open," and an isOrderGone (Bybit retCode=110001)
// response to our cancel is therefore ambiguous: it's at least as likely to mean "already
// filled" as "genuinely cancelled." Only a genuine err==nil cancel success is treated as
// confirmation the order is gone-and-untraded; on isOrderGone the level is left exactly as
// it was (still LevelPlaced, still registered) so the normal WS fill-handling path — which
// needs the order to still be registered to find it — resolves it correctly if it really
// filled. Reverting to Pending and dropping tracking on an ambiguous "gone" would risk
// silently abandoning a real, untracked open position (and later re-triggering the level,
// doubling it).
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
		if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
			Symbol:   sr.strategy.Symbol,
			Category: sr.strategy.Category,
			OrderId:  l.ExchangeOrderID,
		}); err != nil {
			if isOrderGone(err) {
				// Ambiguous for a Market order — see doc comment above. Leave the level
				// tracked as-is; don't touch DB, don't log a "removed" message.
				continue
			}
			sr.warn(ctx, fmt.Sprintf("Signal-gated L%d: отмена ордера не удалась: %v", l.LevelIdx, err))
			continue
		}
		// Only reaches here on a genuine, unambiguous cancel success.
		sr.runner.UnregisterOrder(l.ExchangeOrderID)
		if l.ExchangeLinkID != "" {
			sr.runner.UnregisterOrder(l.ExchangeLinkID)
		}
		l.Status = LevelPending
		l.ExchangeOrderID = ""
		l.ExchangeLinkID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, exchange_link_id=NULL WHERE id=$1`, l.ID)
		sr.info(ctx, fmt.Sprintf("Signal-gated L%d: сигнал пропал — ордер снят с биржи", l.LevelIdx))
	}
}
