package strategy

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// logEvent writes a strategy event to the DB and to stdout.
func logEvent(ctx context.Context, pool *pgxpool.Pool, strategyID, level, msg string) {
	log.Printf("strategy %s [%s]: %s", strategyID[:8], level, msg)
	pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, $3)`,
		strategyID, msg, level,
	)
}

func (sr *StrategyRunner) info(ctx context.Context, msg string) {
	logEvent(ctx, sr.runner.pool, sr.strategy.ID, "info", msg)
}

func (sr *StrategyRunner) warn(ctx context.Context, msg string) {
	logEvent(ctx, sr.runner.pool, sr.strategy.ID, "warn", msg)
}

func (sr *StrategyRunner) errlog(ctx context.Context, msg string) {
	logEvent(ctx, sr.runner.pool, sr.strategy.ID, "error", msg)
}
