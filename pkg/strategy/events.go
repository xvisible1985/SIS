package strategy

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// logEvent writes a strategy event to the DB and to stdout.
func logEvent(ctx context.Context, pool *pgxpool.Pool, strategyID, level, source, msg string) {
	log.Printf("strategy %s [%s] [%s]: %s", strategyID[:8], level, source, msg)
	pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO strategy_events (strategy_id, message, level, source) VALUES ($1, $2, $3, $4)`,
		strategyID, msg, level, source,
	)
}

// setOp marks the start of a named operation for log attribution.
// Usage: defer sr.setOp("updateTP")()
// Restores the previous op on return so nested calls stack correctly.
func (sr *StrategyRunner) setOp(op string) func() {
	prev := sr.currentOp
	sr.currentOp = op
	return func() { sr.currentOp = prev }
}

func (sr *StrategyRunner) source() string {
	if sr.currentOp != "" {
		return sr.currentOp
	}
	return "strategy-runner"
}

func (sr *StrategyRunner) info(ctx context.Context, msg string) {
	logEvent(ctx, sr.runner.pool, sr.strategy.ID, "info", sr.source(), msg)
}

func (sr *StrategyRunner) warn(ctx context.Context, msg string) {
	logEvent(ctx, sr.runner.pool, sr.strategy.ID, "warn", sr.source(), msg)
}

func (sr *StrategyRunner) errlog(ctx context.Context, msg string) {
	logEvent(ctx, sr.runner.pool, sr.strategy.ID, "error", sr.source(), msg)
}
