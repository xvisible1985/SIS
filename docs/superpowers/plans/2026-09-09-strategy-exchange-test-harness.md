# Тестовый харнес pkg/strategy ↔ trader.Exchange — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Дать `pkg/strategy` тестовую инфраструктуру (fake `trader.Exchange` + билдеры `AccountRunner`/`StrategyRunner`), которая проверяет реально вычисленные значения (цену TP, тип ордера, направление триггера), а не только факт, что код дошёл до нужной строки — и закрепить ею два бага этой сессии: устаревший WS-кэш ТВХ при филе и выбор Limit/Stop для TP.

**Architecture:** `fakeExchange` реализует весь интерфейс `trader.Exchange` (15 методов) с логом вызовов и очередью канонических ответов на каждый метод — по образцу уже существующего `pkg/trader/exchange_test.go:fakeWSOrderClient`, но с логами (не только «последний вызов») и очередями (не только одно канонicheskoe значение), поскольку `matrixUpdateTP`/`resolveExchangeAvgEntry` могут вызывать `PlaceOrder`/`FetchPositions` больше одного раза за тест. Тестовый `*pgxpool.Pool` создаётся как настоящий (не мок) объект на заведомо недоступный адрес — `pgxpool.New` не подключается eagerly, а `sr.info`/`sr.warn` глотают ошибку `Exec`, так что паники нет и реальная БД не нужна.

**Tech Stack:** Go 1.25 (generics), `go test` (без build-тегов — обычный юнит-тест пакета `strategy`), `github.com/jackc/pgx/v5/pgxpool`.

**Спека:** `docs/superpowers/specs/2026-09-09-strategy-exchange-test-harness-design.md`

---

### Task 1: `fakeExchange` — полная реализация `trader.Exchange`

**Files:**
- Create: `pkg/strategy/exchange_fake_test.go`

- [ ] **Step 1: Создать файл с generic-очередью ответов и полной реализацией интерфейса**

Создать `pkg/strategy/exchange_fake_test.go`:

```go
package strategy

import (
	"context"
	"sync"
	"time"

	"sis/pkg/trader"
)

// respQueue holds canned (value, error) pairs for one fakeExchange method, consumed in
// call order. Once exhausted it keeps replaying the last pushed pair, so a test that only
// cares about the first call's response doesn't have to pre-fill one entry per expected
// call. An empty queue (nothing pushed) always returns the zero value and a nil error.
type respQueue[T any] struct {
	items []T
	errs  []error
	next  int
}

func (q *respQueue[T]) push(item T, err error) {
	q.items = append(q.items, item)
	q.errs = append(q.errs, err)
}

func (q *respQueue[T]) take() (T, error) {
	var zero T
	if len(q.items) == 0 {
		return zero, nil
	}
	i := q.next
	if i >= len(q.items) {
		i = len(q.items) - 1
	} else {
		q.next++
	}
	return q.items[i], q.errs[i]
}

type walletResp struct {
	equity    float64
	available float64
}

type openOrdersReq struct {
	Category string
	Symbol   string
}

type closedPnlReq struct {
	Category string
	Symbol   string
	Limit    int
}

type recentClosedPnlReq struct {
	Category string
	Since    time.Time
}

type positionModeReq struct {
	Category string
	Symbol   string
	Mode     int
}

type markPriceReq struct {
	Category string
	Symbol   string
}

// fakeExchange is a hand-written stand-in for trader.Exchange, used only by pkg/strategy
// tests (no live network/WS). Modeled on pkg/trader/exchange_test.go's fakeWSOrderClient,
// but extended with a call LOG (slice, not just the last call) and a response QUEUE (not
// just one canned value) per method — pkg/strategy's flows call PlaceOrder/FetchPositions
// more than once in a single test (cancel-then-place, or a WS-cache staleness fallback to
// FetchPositions), and tests need to assert on the exact fields of each call, in order —
// not just that a call happened at some point with some content.
type fakeExchange struct {
	mu sync.Mutex

	placeOrderCalls []trader.OrderRequest
	placeOrderQ     respQueue[trader.OrderResult]

	placeOrderBatchCalls []trader.BatchPlaceRequest
	placeOrderBatchQ     respQueue[[]trader.BatchPlaceResult]

	cancelOrderCalls []trader.CancelRequest
	cancelOrderQ     respQueue[struct{}]

	cancelOrderBatchCalls []trader.BatchCancelRequest
	cancelOrderBatchQ     respQueue[struct{}]

	placeOrderRESTCalls []trader.OrderRequest
	placeOrderRESTQ     respQueue[trader.OrderResult]

	cancelOrderRESTCalls []trader.CancelRequest
	cancelOrderRESTQ     respQueue[struct{}]

	cancelAllOrdersCalls []trader.CancelAllRequest
	cancelAllOrdersQ     respQueue[struct{}]

	fetchPositionsCalls int
	fetchPositionsQ     respQueue[[]trader.Position]

	fetchOpenOrdersCalls []openOrdersReq
	fetchOpenOrdersQ     respQueue[[]trader.Order]

	fetchClosedPnlCalls []closedPnlReq
	fetchClosedPnlQ     respQueue[[]trader.ClosedPnl]

	fetchRecentClosedPnlCalls []recentClosedPnlReq
	fetchRecentClosedPnlQ     respQueue[[]trader.ClosedPnl]

	getWalletBalanceCalls int
	getWalletBalanceQ     respQueue[walletResp]

	setLeverageCalls []trader.LeverageRequest
	setLeverageQ     respQueue[struct{}]

	switchPositionModeCalls []positionModeReq
	switchPositionModeQ     respQueue[struct{}]

	getMarkPriceCalls []markPriceReq
	getMarkPriceQ     respQueue[float64]
}

var _ trader.Exchange = (*fakeExchange)(nil)

func (f *fakeExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeOrderCalls = append(f.placeOrderCalls, req)
	return f.placeOrderQ.take()
}

func (f *fakeExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeOrderBatchCalls = append(f.placeOrderBatchCalls, req)
	return f.placeOrderBatchQ.take()
}

func (f *fakeExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelOrderCalls = append(f.cancelOrderCalls, req)
	_, err := f.cancelOrderQ.take()
	return err
}

func (f *fakeExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelOrderBatchCalls = append(f.cancelOrderBatchCalls, req)
	_, err := f.cancelOrderBatchQ.take()
	return err
}

func (f *fakeExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeOrderRESTCalls = append(f.placeOrderRESTCalls, req)
	return f.placeOrderRESTQ.take()
}

func (f *fakeExchange) CancelOrderREST(ctx context.Context, req trader.CancelRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelOrderRESTCalls = append(f.cancelOrderRESTCalls, req)
	_, err := f.cancelOrderRESTQ.take()
	return err
}

func (f *fakeExchange) CancelAllOrders(ctx context.Context, req trader.CancelAllRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelAllOrdersCalls = append(f.cancelAllOrdersCalls, req)
	_, err := f.cancelAllOrdersQ.take()
	return err
}

func (f *fakeExchange) FetchPositions(ctx context.Context) ([]trader.Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchPositionsCalls++
	return f.fetchPositionsQ.take()
}

func (f *fakeExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]trader.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchOpenOrdersCalls = append(f.fetchOpenOrdersCalls, openOrdersReq{Category: category, Symbol: symbol})
	return f.fetchOpenOrdersQ.take()
}

func (f *fakeExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]trader.ClosedPnl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchClosedPnlCalls = append(f.fetchClosedPnlCalls, closedPnlReq{Category: category, Symbol: symbol, Limit: limit})
	return f.fetchClosedPnlQ.take()
}

func (f *fakeExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]trader.ClosedPnl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchRecentClosedPnlCalls = append(f.fetchRecentClosedPnlCalls, recentClosedPnlReq{Category: category, Since: since})
	return f.fetchRecentClosedPnlQ.take()
}

func (f *fakeExchange) GetWalletBalance(ctx context.Context) (float64, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getWalletBalanceCalls++
	resp, err := f.getWalletBalanceQ.take()
	return resp.equity, resp.available, err
}

func (f *fakeExchange) SetLeverage(ctx context.Context, req trader.LeverageRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLeverageCalls = append(f.setLeverageCalls, req)
	_, err := f.setLeverageQ.take()
	return err
}

func (f *fakeExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switchPositionModeCalls = append(f.switchPositionModeCalls, positionModeReq{Category: category, Symbol: symbol, Mode: mode})
	_, err := f.switchPositionModeQ.take()
	return err
}

func (f *fakeExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMarkPriceCalls = append(f.getMarkPriceCalls, markPriceReq{Category: category, Symbol: symbol})
	return f.getMarkPriceQ.take()
}
```

- [ ] **Step 2: Собрать и проверить, что `fakeExchange` реализует интерфейс**

Run: `go build ./pkg/strategy/...`
Expected: без ошибок — `var _ trader.Exchange = (*fakeExchange)(nil)` компилируется, значит все 15 методов реализованы с точными сигнатурами.

Run: `go vet ./pkg/strategy/...`
Expected: без ошибок.

- [ ] **Step 3: Коммит**

```bash
git add pkg/strategy/exchange_fake_test.go
git commit -m "test(strategy): add fakeExchange test double for trader.Exchange

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: Билдеры `AccountRunner`/`StrategyRunner` для тестов

**Files:**
- Create: `pkg/strategy/testrunner_test.go`

- [ ] **Step 1: Создать хелперы сборки**

Создать `pkg/strategy/testrunner_test.go`:

```go
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
```

- [ ] **Step 2: Собрать**

Run: `go build ./pkg/strategy/...`
Expected: без ошибок.

Run: `go vet ./pkg/strategy/...`
Expected: без ошибок.

- [ ] **Step 3: Коммит**

```bash
git add pkg/strategy/testrunner_test.go
git commit -m "test(strategy): add AccountRunner/StrategyRunner test builders

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: Регресс-тесты на устаревший WS-кэш ТВХ

**Files:**
- Create: `pkg/strategy/resolve_avg_entry_test.go`

Контекст бага (уже исправлен в `pkg/strategy/cycle.go:resolveExchangeAvgEntry` в этой же сессии, до этого плана): после фила уровня движок сразу пересчитывает TP, беря ТВХ из WS-кэша позиции биржи. Если WS-событие с обновлённой ТВХ (учитывающей только что исполненный фил) ещё не пришло, кэш содержит СТАРОЕ значение — движок использовал его вместо того, чтобы понять, что кэш устарел, и запросить актуальные данные напрямую. Исправление: `resolveExchangeAvgEntry` теперь принимает `computedQty` (внутренний расчёт объёма) и сравнивает с `GetPositionSizeCoins()` — если кэш показывает МЕНЬШЕ монет, чем уже должно быть по нашим филам, кэш считается устаревшим и вместо него делается `FetchPositions`.

- [ ] **Step 1: Написать тест «свежий кэш — используется без FetchPositions»**

Создать `pkg/strategy/resolve_avg_entry_test.go`:

```go
package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

func TestResolveExchangeAvgEntry_FreshWSCache_TrustsCacheWithoutFetchingPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// WS cache already reflects the full 150-coin position — computedQty (150) matches
	// exactly, so it must be trusted as-is.
	setWSPosition(ar, "TESTUSDT", 0, 150, 102.5)
	// If resolveExchangeAvgEntry wrongly falls back to FetchPositions anyway, this canned
	// value (999) would leak into the result — proves the fallback path was NOT taken.
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "150", EntryPrice: "999"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 101.0, 150)

	if got != 102.5 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 102.5 (the fresh WS-cached value)", got)
	}
	if fake.fetchPositionsCalls != 0 {
		t.Errorf("FetchPositions called %d times, want 0 — a fresh WS cache must not trigger a fallback", fake.fetchPositionsCalls)
	}
}
```

- [ ] **Step 2: Прогнать — убедиться, что проходит**

Run: `go test ./pkg/strategy/... -run TestResolveExchangeAvgEntry_FreshWSCache -v`
Expected: `PASS`.

- [ ] **Step 3: Написать тест «устаревший кэш — форсируется FetchPositions» (сам баг)**

Добавить в `pkg/strategy/resolve_avg_entry_test.go`:

```go
func TestResolveExchangeAvgEntry_StaleWSCache_FallsBackToFetchPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// WS cache still shows the PRE-fill size (50 coins, stale) — the fill that just
	// happened brought the real position to 150 coins (computedQty below), but the WS
	// position-update event carrying that hasn't arrived yet. This is the exact race
	// found live on NIULAIUSDT (2026-09-07) and 1000NEIROCTOUSDT (2026-09-08/09).
	setWSPosition(ar, "TESTUSDT", 0, 50, 100.0)
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "150", EntryPrice: "102.5"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 101.0, 150)

	if got != 102.5 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 102.5 (from FetchPositions) — a stale WS cache (qty=50 < computedQty=150) must not be trusted, got the stale cached value 100.0 instead", got)
	}
	if fake.fetchPositionsCalls != 1 {
		t.Errorf("FetchPositions called %d times, want 1 — a stale WS cache must trigger exactly one fallback call", fake.fetchPositionsCalls)
	}
}
```

- [ ] **Step 4: Прогнать — убедиться, что проходит**

Run: `go test ./pkg/strategy/... -run TestResolveExchangeAvgEntry_StaleWSCache -v`
Expected: `PASS`.

- [ ] **Step 5: Написать тест «холодный кэш» (существующее поведение, не должно было измениться)**

Добавить в `pkg/strategy/resolve_avg_entry_test.go`:

```go
func TestResolveExchangeAvgEntry_ColdWSCache_FallsBackToFetchPositions(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	// No setWSPosition call at all — cache is cold (avg=0), e.g. right after a restart
	// before any position event has arrived. This is the pre-existing fallback path the
	// staleness fix must not have broken.
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TESTUSDT", PositionIdx: 0, Size: "150", EntryPrice: "103.0"},
	}, nil)

	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT"},
		runner:   ar,
	}

	got := sr.resolveExchangeAvgEntry(context.Background(), 0, 101.0, 150)

	if got != 103.0 {
		t.Errorf("resolveExchangeAvgEntry = %v, want 103.0 (from FetchPositions on a cold cache)", got)
	}
	if fake.fetchPositionsCalls != 1 {
		t.Errorf("FetchPositions called %d times, want 1", fake.fetchPositionsCalls)
	}
}
```

- [ ] **Step 6: Прогнать все три теста файла**

Run: `go test ./pkg/strategy/... -run TestResolveExchangeAvgEntry -v`
Expected: все три `PASS`.

- [ ] **Step 7: Коммит**

```bash
git add pkg/strategy/resolve_avg_entry_test.go
git commit -m "test(strategy): regression tests for stale-WS-avg-entry-on-fill bug

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: Регресс-тесты на выбор Limit/Stop для matrix TP

**Files:**
- Create: `pkg/strategy/matrix_tp_order_type_test.go`

Контекст: `matrixUpdateTP` выставляет TP как обычный лимитный ордер, если цена ещё не «пересекла» расчётную цену TP (`matrixTPCrossed` возвращает `false`) — но если цена уже прошла TP (резкий разворот), лимитка исполнилась бы мгновенно, поэтому вместо неё ставится条 условный (`OrderType=Market`, `OrderFilter=StopOrder`) ордер с триггером. Оба теста ниже фиксируют оба ответвления с точными полями запроса к бирже — не просто «PlaceOrder вызван».

- [ ] **Step 1: Написать общий хелпер сборки стратегии для этих тестов**

Создать `pkg/strategy/matrix_tp_order_type_test.go`:

```go
package strategy

import (
	"context"
	"strconv"
	"testing"

	"sis/pkg/trader"
)

// newMatrixTPTestStrategy builds a short matrix StrategyRunner with exactly one filled
// level (slot 1, tp_pct=1%, filled at 100.0 for 10 coins — so avgEntry()=100.0,
// computedQty=10, and the TP price for a 1% short target is 100*(1-1/100)=99.0), backed
// by fake and with a fresh WS cache (avoids exercising the staleness fallback from Task
// 3 — that's a separate concern, covered separately). lastMatrixPrice is the caller's to
// set afterward to control whether matrixTPCrossed is true or false.
func newMatrixTPTestStrategy(t *testing.T, fake *fakeExchange) *StrategyRunner {
	t.Helper()
	ar := newTestAccountRunner(t, fake)
	setWSPosition(ar, "TESTUSDT", 0, 10, 100.0)

	tpPct := 1.0
	slot1 := 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			Symbol:       "TESTUSDT",
			Category:     "linear",
			Direction:    DirectionShort,
			StrategyType: "matrix",
			HedgeMode:    false,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", PriceStepPct: 3.0, SizePct: 10, TPPct: &tpPct},
			},
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 100.0, Qty: "10", SizeUSDT: 100.0},
		},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001},
	}
	return sr
}

// placedOrderPrice parses req.Price (limit orders) or req.TriggerPrice (stop orders) back
// to float64, tolerating FormatPrice's tick-rounding rather than asserting an exact string.
func placedOrderPrice(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("placedOrderPrice: %q: %v", s, err)
	}
	return v
}
```

- [ ] **Step 2: Написать тест «цена не дошла до TP — лимитный ордер»**

Добавить в `pkg/strategy/matrix_tp_order_type_test.go`:

```go
func TestMatrixUpdateTP_PriceNotCrossed_PlacesLimitOrder(t *testing.T) {
	fake := &fakeExchange{}
	sr := newMatrixTPTestStrategy(t, fake)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "tp-order-1"}, nil)
	// Mark price is 99.5 — above the 99.0 TP target, so a plain limit BUY at 99.0 would
	// rest normally (short TP: buy back cheaper). matrixTPCrossed(short, 99.5, 99.0) =
	// 99.5 <= 99.0 = false → not crossed.
	sr.lastMatrixPrice = 99.5

	sr.matrixUpdateTP(context.Background())

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.OrderType != "Limit" {
		t.Errorf("OrderType = %q, want %q (price hasn't crossed TP yet)", req.OrderType, "Limit")
	}
	if req.Side != "Buy" {
		t.Errorf("Side = %q, want %q (short position TP closes by buying back)", req.Side, "Buy")
	}
	if req.OrderFilter == "StopOrder" {
		t.Errorf("OrderFilter = %q, want empty — a not-yet-crossed TP must be a plain limit order, not a conditional stop", req.OrderFilter)
	}
	if got := placedOrderPrice(t, req.Price); got < 98.99 || got > 99.01 {
		t.Errorf("Price = %v, want ~99.0 (avgEntry 100.0 * (1 - 1%%))", got)
	}
}
```

- [ ] **Step 3: Прогнать — убедиться, что проходит**

Run: `go test ./pkg/strategy/... -run TestMatrixUpdateTP_PriceNotCrossed -v`
Expected: `PASS`.

- [ ] **Step 4: Написать тест «цена уже прошла TP — условный (стоп) ордер»**

Добавить в `pkg/strategy/matrix_tp_order_type_test.go`:

```go
func TestMatrixUpdateTP_PriceCrossed_PlacesStopOrder(t *testing.T) {
	fake := &fakeExchange{}
	sr := newMatrixTPTestStrategy(t, fake)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "tp-order-2"}, nil)
	// Mark price is 98.0 — at/below the 99.0 TP target already: a plain limit BUY at 99.0
	// would fill instantly against the current market instead of waiting for a genuine
	// reversal. matrixTPCrossed(short, 98.0, 99.0) = 98.0 <= 99.0 = true → crossed.
	sr.lastMatrixPrice = 98.0

	sr.matrixUpdateTP(context.Background())

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.OrderType != "Market" {
		t.Errorf("OrderType = %q, want %q (a crossed TP must be a conditional trigger order, not a resting limit)", req.OrderType, "Market")
	}
	if req.OrderFilter != "StopOrder" {
		t.Errorf("OrderFilter = %q, want %q", req.OrderFilter, "StopOrder")
	}
	if req.Side != "Buy" {
		t.Errorf("Side = %q, want %q", req.Side, "Buy")
	}
	// Short TP-as-stop must fire when price RISES back up to the trigger (1), never on a
	// fall (2, that's the long-side convention) — see the trigger-direction comment in
	// matrixUpdateTP and matrixPlacePerLevelSL.
	if req.TriggerDirection != 1 {
		t.Errorf("TriggerDirection = %d, want 1 (short: fires on rise to/above trigger)", req.TriggerDirection)
	}
	if req.TriggerBy != "LastPrice" {
		t.Errorf("TriggerBy = %q, want %q", req.TriggerBy, "LastPrice")
	}
	if got := placedOrderPrice(t, req.TriggerPrice); got < 98.99 || got > 99.01 {
		t.Errorf("TriggerPrice = %v, want ~99.0 (avgEntry 100.0 * (1 - 1%%))", got)
	}
}
```

- [ ] **Step 5: Прогнать оба теста файла**

Run: `go test ./pkg/strategy/... -run TestMatrixUpdateTP -v`
Expected: оба `PASS`.

- [ ] **Step 6: Коммит**

```bash
git add pkg/strategy/matrix_tp_order_type_test.go
git commit -m "test(strategy): regression tests for matrix TP limit-vs-stop order selection

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: Финальное тест-ревью

**Files:** нет новых файлов — только прогон.

- [ ] **Step 1: Полная сборка и vet**

Run: `go build ./...`
Expected: без ошибок.

Run: `go vet ./...`
Expected: без ошибок.

- [ ] **Step 2: Полный прогон пакета `pkg/strategy`**

Run: `go test ./pkg/strategy/... -v 2>&1 | tail -150`
Expected: все тесты пакета `PASS`, включая:
- 5 новых тестов из Task 3/4 (`TestResolveExchangeAvgEntry_*` ×3, `TestMatrixUpdateTP_*` ×2);
- все существующие тесты, в частности те, что доходят до `resolveExchangeAvgEntry`/`matrixUpdateTP` через nil-runner-панику (`TestHandleTPFill_MatrixFallsThroughToSharedClose`, `TestHandlePartialPositionChange_MatrixGhostFallsThroughToSharedClose`, `TestMatrixHandleSLFlattenOrContinue_LastLevelClosesCycle`, `TestMatrixHandleSLFlattenOrContinue_OtherLevelsStillFilled_CycleStaysOpen`) — новые файлы не должны были их сломать, это дополнение к существующей инфраструктуре, не замена.

Run: `go test ./pkg/strategy/... -run "TestHandleTPFill_MatrixFallsThroughToSharedClose|TestHandlePartialPositionChange_MatrixGhostFallsThroughToSharedClose|TestMatrixHandleSLFlattenOrContinue" -v`
Expected: все 4 по-прежнему `PASS` — явное подтверждение отсутствия регресса на существующей nil-runner-панической технике.

- [ ] **Step 3: Доложить результат пользователю**

Явно перечислить (по правилу CLAUDE.md проекта): что проверялось на регресс (весь пакет `pkg/strategy`, включая существующие nil-runner-тесты) и с каким результатом; какие новые баги теперь закреплены регресс-тестами (устаревший WS-кэш ТВХ, выбор Limit/Stop для TP) и где именно (файлы, имена тестов) — чтобы это было видно в истории репозитория, а не только в этой сессии.
