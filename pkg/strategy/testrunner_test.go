package strategy

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool returns a real (not mocked) *pgxpool.Pool pointed at a permanently
// unreachable address. pgxpool.New does not dial eagerly (connections are established
// lazily, on first use), so this never blocks or errors at construction time. Code paths
// that call sr.info/sr.warn/sr.errlog do a fire-and-forget pool.Exec whose error is
// discarded (see pkg/strategy/events.go:logEvent) — against this pool that Exec fails
// fast with "connection refused" instead of panicking, so tests can exercise those code
// paths without a live Postgres.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://fake:fake@127.0.0.1:1/fake")
	if err != nil {
		t.Fatalf("newTestPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newTestAccountRunner returns an *AccountRunner wired to fake instead of a real
// Bybit/Binance connection, with its order-index and WS-position-cache maps initialized
// (empty) — RegisterOrder/UnregisterOrder and the posMu-guarded maps panic or behave
// incorrectly against a nil map on write, so every test-constructed AccountRunner must
// go through this rather than a bare &AccountRunner{}.
func newTestAccountRunner(t *testing.T, fake *fakeExchange) *AccountRunner {
	t.Helper()
	return &AccountRunner{
		pool:                newTestPool(t),
		exchange:            fake,
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
}

// setWSPosition seeds the WS-cached position size/avg-entry for symbol+positionIdx,
// using the same "SYMBOL:positionIdx" key format OnPositionEvent writes
// (pkg/strategy/engine.go). Call this to set up the WS-cache state a test scenario needs
// (fresh, stale, or cold — omit the call entirely for cold/absent).
func setWSPosition(ar *AccountRunner, symbol string, positionIdx int, size, avgEntry float64) {
	key := symbol + ":" + strconv.Itoa(positionIdx)
	ar.posMu.Lock()
	defer ar.posMu.Unlock()
	ar.positions[key] = size
	ar.posAvgEntry[key] = avgEntry
}
