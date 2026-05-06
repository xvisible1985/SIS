package strategy

import (
	"context"
	"log"
	"time"

	"sis/pkg/trader"
)

// startReconcileLoop runs reconcile() every 60 seconds until ctx is cancelled.
func (ar *AccountRunner) startReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ar.reconcile(ctx)
		}
	}
}

// reconcile checks that every strategy_level with status='placed' still exists
// on the exchange. Missing orders are reset to 'pending' so placeNextLevels
// re-places them on the next fill event or next reconcile cycle.
func (ar *AccountRunner) reconcile(ctx context.Context) {
	rows, err := ar.pool.Query(ctx,
		`SELECT sl.id, sl.exchange_order_id, sl.strategy_id
		 FROM strategy_levels sl
		 JOIN strategies s ON s.id = sl.strategy_id
		 WHERE s.account_id = $1 AND sl.status = 'placed'`,
		ar.accountID,
	)
	if err != nil {
		log.Printf("strategy reconcile %s: query: %v", ar.accountID, err)
		return
	}
	type placed struct{ levelID, orderID, strategyID string }
	var dbPlaced []placed
	for rows.Next() {
		var p placed
		if err := rows.Scan(&p.levelID, &p.orderID, &p.strategyID); err == nil {
			dbPlaced = append(dbPlaced, p)
		}
	}
	rows.Close()

	if len(dbPlaced) == 0 {
		return
	}

	exchangeOrders, err := trader.FetchOpenOrders(ctx, ar.creds)
	if err != nil {
		log.Printf("strategy reconcile %s: fetch exchange orders: %v", ar.accountID, err)
		return
	}
	live := make(map[string]bool, len(exchangeOrders))
	for _, o := range exchangeOrders {
		live[o.OrderId] = true
	}

	for _, p := range dbPlaced {
		if live[p.orderID] {
			continue
		}
		log.Printf("strategy reconcile: level %s order %s missing from exchange — resetting to pending", p.levelID, p.orderID)
		ar.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
			p.levelID,
		)
		ar.UnregisterOrder(p.orderID)

		ar.mu.RLock()
		sr := ar.strategies[p.strategyID]
		ar.mu.RUnlock()
		if sr == nil {
			continue
		}
		sr.mu.Lock()
		for i := range sr.levels {
			if sr.levels[i].ID == p.levelID {
				sr.levels[i].Status = LevelPending
				sr.levels[i].ExchangeOrderID = ""
				break
			}
		}
		sr.placeNextLevels(ctx) //nolint:errcheck
		sr.mu.Unlock()
	}
}
