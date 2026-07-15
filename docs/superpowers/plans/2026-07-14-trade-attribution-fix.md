# Корректная атрибуция закрытых сделок через orderLinkId — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ClosedPnlSyncer` атрибутирует закрытые сделки напрямую по нашему `orderLinkId` (который Bybit уже возвращает), вместо угадывания по временны́м окнам — устраняет пропажу matrix-ботов из статистики и ложные `manual`-метки; статистика ботов и дашборд-лидерборд читают единый трёхкомпонентный источник PnL; бот-инициированные закрытия получают точную подпись причины.

**Architecture:** Новый шаг 0 в `ClosedPnlSyncer.processClosedPnl` парсит `SIS_STR-{stratID8}-...` из `p.OrderLinkId` через чистую функцию `strategy.ParseStrategyLinkID`, находит стратегию напрямую и маршрутизирует по типу закрытия (matrix-TP re-arm → `matrix_tp_profits`, per-level SL → уже учтено/no-op, обычный TP/SL → существующий `RecordStrategyTrade`). Нераспознанный linkId падает в существующие эвристики без изменений. Статистика (дашборд + карточки ботов) переходит на общую Go-константу `botPnlUnionSQL` (derived-table UNION ALL трёх источников), без новых миграций. `stopMatrixPair` заранее предупреждает движок стратегий через новый `Engine.NotifyExpectedClose`, чтобы бот-driven парные закрытия подписывались `paired_close`, а не `manual_close`.

**Tech Stack:** Go (`pkg/strategy`, `services/api-gateway`), pgx, интеграционные тесты `//go:build integration` против локальной БД (PgBouncer :6432), юнит-тесты для чистых функций без тега.

**Контекст для исполнителя:**
- Полный каталог форматов `orderLinkId`, которые выставляет движок стратегий (`pkg/strategy`):
  - `SIS_STR-{id8}-{cycleNum}-{levelIdx}-{repriceGen}` — обычный вход на уровень DCA/grid (не закрытие, в ClosedPnl не попадает).
  - `SIS_STR-{id8}-{cycleNum}-{levelIdx}-{repriceGen}-v` — виртуальный уровень (grid).
  - `SIS_STR-{id8}-{cycleNum}-{levelIdx}-v{N}` — виртуальный уровень (matrix, `matrix.go:981`).
  - `SIS_STR-{id8}-tp-{cycleNum}-{seq}` — обычный (grid/hedge) глобальный TP, **завершает цикл** (`cycle.go:2434`).
  - `SIS_STR-{id8}-sl-{cycleNum}-{seq}` — обычный (grid/hedge) глобальный SL, **завершает цикл** (`cycle.go:2628`).
  - `SIS_STR-{id8}-msl-{slot}-{seq}` — per-level matrix SL, `slot` кодируется как `matrixSlotLinkStr` (`{N}` для положительного, `n{N}` для отрицательного); **уже учтён** в `strategy_levels.realized_pnl` через `handleMatrixSLFill` (`matrix.go:830,1279`).
  - `SIS_STR-{id8}-tpl{slot}-{cycleNum}-{seq}` — глобальный matrix TP (re-arm), `slot` в той же кодировке, префикс буквально `l` (не направление long/short!) — **не завершает цикл** (`matrix.go:1451`).
  - `SIS_MPC_{posIdx}_{timestampMs}` — закрывающие ордера `stopMatrixPair` (парное закрытие), **не несут id стратегии**, linkId-атрибуция их не подхватывает — это ожидаемо, они уже корректно долетают до `trade_history` через штатный WS-путь движка (см. Task 5).
- `trader.ClosedPnl` (`pkg/trader/types.go:136-151`) уже имеет поле `OrderLinkId string` — Bybit возвращает наш linkId обратно без изменений.
- `pkg/strategy/trade_recorder.go` уже содержит `MatrixTPRecordInput`/`RecordMatrixTPProfit` (строки 28-107) — пишут в `matrix_tp_profits` идемпотентно по `(account_id, bybit_order_id)`.
- `services/api-gateway/closed_pnl_syncer.go` — `ClosedPnlSyncer.processClosedPnl` (строка 167) — существующие шаги: 1 (уже записано?), 2 (цикл рядом завершился), 3 (зомби-цикл → ghost_close), 4 (manual fallback). Все остаются без изменений — новый код вставляется как шаг 1b, СРАЗУ после шага 1, ПЕРЕД шагом 2.
- `pkg/strategy/cycle.go:4404-4470` — `closeManualPosition`/`manualCloseStatus`/`closePositionExternal` (уже правились этой сессией — `pauseEligible`, guard на `active/finishing`). Строка 4433 `sr.closeCycle(ctx, "manual_close")` — единственное место, где жёстко зашита метка `manual_close`.
- `pkg/strategy/cycle.go:2836-2849` — `closeCycle(ctx, result string)` — `result` попадает напрямую в `TradeRecordInput.Result` → `trade_history.result`.
- `pkg/strategy/cycle.go:21-61` — `StrategyRunner` struct — уже есть поля `closedBySelf bool`, `closedByReason string` (для ДВИЖКОВЫХ самозакрытий TP/SL) — по этому же паттерну добавляем `expectedCloseReason`/`expectedCloseSetAt` для ВНЕШНИХ (bot-engine-инициированных) ожидаемых закрытий.
- `pkg/strategy/engine.go:28-38` (`Engine`), `:663-680` (`AccountRunner`, поле `strategies map[string]*StrategyRunner`) — `Engine.runners map[string]*AccountRunner` (accountID → runner). `Engine.Notify` (строка 107) — существующий паттерн полной перезагрузки из БД; новый метод должен быть ЛЕГЧЕ — прямой доступ к уже загруженному раннеру, без похода в БД.
- `services/api-gateway/matrix_engine.go` — `processMatrixBot` (17), `checkMatrixPairedClose` (181), `stopMatrixPair` (282) — уже видят `accountID`/`creds`, но не прокидывают его до `stopMatrixPair`.
- `services/api-gateway/dashboard_handler.go` — `dashboardBotStat` (строка 20), `baseWhere`/`baseArgs` (строки 96-108, паттерн `WHERE th.owner_id = $1 [AND th.account_id = $2] [AND th.closed_at >= $3]`), Bot leaderboard блок (строки 178-205, `FROM trade_history th JOIN bots b ON b.id = th.bot_id`).
- `services/api-gateway/bots_handler.go` — `mineStatsCols`/`zeroStatsCols` (строки 115-131), `botResp` struct с полями `TradesTotal int`, `TradesWin int`, `NetPnlTotal float64` (сканируются напрямую, менять Go-структуру не нужно — только SQL-константы). `mineStatsCols` используется в строке 219 (`WHERE b.owner_id = $1` — «мои боты»). `zeroStatsCols` используется для чужих/каталожных ботов (170, 197, 1807) — **вне области этого плана**, не трогаем (другая область видимости, не то, на что жаловался пользователь).
- Тест-хелперы: `newTestServer`, `createWHUser`, `createTestAccount`, `createZombieBot` (`services/api-gateway/matrix_zombie_test.go`), `hedgePosInfo` (`Size float64`, `EntryPrice float64`, `SizeStr string`, `MarkPrice float64`).
- Схема не меняется, новых миграций нет — все нужные таблицы (`trade_history`, `strategy_levels`, `matrix_tp_profits`, `strategy_cycles`, `strategies`) уже существуют.

---

### Task 1: `ParseStrategyLinkID` — чистая функция классификации linkId

**Files:**
- Create: `pkg/strategy/linkid.go`
- Test: `pkg/strategy/linkid_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `pkg/strategy/linkid_test.go`:

```go
package strategy

import "testing"

func TestParseStrategyLinkID(t *testing.T) {
	cases := []struct {
		name     string
		linkID   string
		wantOK   bool
		wantKind LinkIDKind
		wantID8  string
	}{
		{"matrix TP positive slot", "SIS_STR-286130b1-tpl2-1-83452541", true, LinkIDMatrixTP, "286130b1"},
		{"matrix TP negative slot", "SIS_STR-286130b1-tpln1-1-83452541", true, LinkIDMatrixTP, "286130b1"},
		{"matrix per-level SL positive", "SIS_STR-479024f8-msl-3-7", true, LinkIDMatrixLevelSL, "479024f8"},
		{"matrix per-level SL negative", "SIS_STR-479024f8-msl-n1-7", true, LinkIDMatrixLevelSL, "479024f8"},
		{"grid TP", "SIS_STR-aaa41b1f-tp-1-12345", true, LinkIDGridTP, "aaa41b1f"},
		{"grid SL", "SIS_STR-aaa41b1f-sl-1-12345", true, LinkIDGridSL, "aaa41b1f"},
		{"level entry fill — recognized prefix, not a close kind", "SIS_STR-d1c35a82-1-3-0", true, LinkIDUnrecognized, "d1c35a82"},
		{"virtual level — recognized prefix, not a close kind", "SIS_STR-d1c35a82-1-3-0-v", true, LinkIDUnrecognized, "d1c35a82"},
		{"paired-close order — no strategy id, not ours in this sense", "SIS_MPC_1_1720000000000", false, LinkIDUnrecognized, ""},
		{"detach close order", "SIS_DTH_1720000000000", false, LinkIDUnrecognized, ""},
		{"empty", "", false, LinkIDUnrecognized, ""},
		{"external/manual exchange order", "abc-123-manual", false, LinkIDUnrecognized, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseStrategyLinkID(c.linkID)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if got.Kind != c.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, c.wantKind)
			}
			if got.StrategyID8 != c.wantID8 {
				t.Errorf("StrategyID8 = %q, want %q", got.StrategyID8, c.wantID8)
			}
		})
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что не компилируется**

Run: `go test ./pkg/strategy/ -run TestParseStrategyLinkID -count=1`
Expected: FAIL — `undefined: ParseStrategyLinkID`, `undefined: LinkIDKind`, etc.

- [ ] **Step 3: Реализовать**

Создать `pkg/strategy/linkid.go`:

```go
package strategy

import "regexp"

// LinkIDKind classifies what a SIS_STR-prefixed orderLinkId represents, so callers
// (primarily ClosedPnlSyncer) can decide how a closed-pnl event should be attributed
// without guessing from timing.
type LinkIDKind int

const (
	// LinkIDUnrecognized: matched our SIS_STR-{id8}- prefix, but the suffix isn't one of
	// the known close-kind patterns (e.g. a plain level/entry fill's linkId, or a virtual
	// level) — not attributable to a specific close event kind.
	LinkIDUnrecognized LinkIDKind = iota
	// LinkIDMatrixTP: global matrix TP re-arm. The cycle does NOT end — this is the
	// exact case the old time-window heuristics get wrong (a cycle that "looks ended"
	// or "looks zombie" from the outside, but is actually healthy and still trading).
	LinkIDMatrixTP
	// LinkIDMatrixLevelSL: per-level matrix SL fill. Already recorded directly into
	// strategy_levels.realized_pnl by the in-process engine (handleMatrixSLFill) —
	// callers must NOT also write this into trade_history (would duplicate/orphan).
	LinkIDMatrixLevelSL
	// LinkIDGridTP: grid/hedge global TP. A genuine cycle-ending close.
	LinkIDGridTP
	// LinkIDGridSL: grid/hedge global SL. A genuine cycle-ending close.
	LinkIDGridSL
)

// ParsedLinkID is the result of successfully parsing one of our own orderLinkId strings.
type ParsedLinkID struct {
	Kind LinkIDKind
	// StrategyID8 is the first 8 hex characters of the owning strategy's UUID — enough
	// to look it up via `strategies.id::text LIKE '{StrategyID8}%'` (callers should also
	// filter by account_id to rule out a theoretical 8-hex-prefix collision).
	StrategyID8 string
}

// Patterns are mutually exclusive by construction: the character(s) immediately after
// "-{id8}-" differ for every kind ("tpl" vs "tp-" vs "msl-" vs "sl-"), so match order
// does not matter for correctness — kept in a fixed, readable order below.
var (
	reMatrixTP      = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-tpl`)
	reMatrixLevelSL = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-msl-`)
	reGridTP        = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-tp-`)
	reGridSL        = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-sl-`)
	reOurs          = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-`)
)

// ParseStrategyLinkID classifies an orderLinkId that Bybit echoes back on a ClosedPnl
// entry. Returns ok=false when linkID doesn't match our SIS_STR- format at all — e.g. a
// SIS_MPC_/SIS_DTH_ order (bot-engine-placed but without an embedded strategy id) or a
// genuinely external/manual exchange order. Callers must fall back to other attribution
// methods in that case; this function makes no attempt to identify those orders.
func ParseStrategyLinkID(linkID string) (ParsedLinkID, bool) {
	if m := reMatrixTP.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDMatrixTP, StrategyID8: m[1]}, true
	}
	if m := reMatrixLevelSL.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDMatrixLevelSL, StrategyID8: m[1]}, true
	}
	if m := reGridTP.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDGridTP, StrategyID8: m[1]}, true
	}
	if m := reGridSL.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDGridSL, StrategyID8: m[1]}, true
	}
	if m := reOurs.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDUnrecognized, StrategyID8: m[1]}, true
	}
	return ParsedLinkID{}, false
}
```

- [ ] **Step 4: Запустить — убедиться, что проходит**

Run: `go test ./pkg/strategy/ -run TestParseStrategyLinkID -count=1 -v`
Expected: все подтесты `PASS`, `ok`.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/linkid.go pkg/strategy/linkid_test.go
git commit -m "feat(strategy): ParseStrategyLinkID — классификация orderLinkId по типу закрытия"
```

---

### Task 2: Извлечь `InsertMatrixTPProfit` из `RecordMatrixTPProfit`

**Files:**
- Modify: `pkg/strategy/trade_recorder.go`
- Test: `pkg/strategy/trade_recorder_insert_test.go`

Цель: `ClosedPnlSyncer` (Task 3) уже имеет точное значение `grossPnl` из ответа Bybit (`p.ClosedPnl`) — вызывать полный `RecordMatrixTPProfit` (с его 8+30с ретраями повторного похода за ClosedPnl) избыточно. Выносим общий «хвост» (комиссии + INSERT) в отдельную функцию, которую используют оба вызывающих.

- [ ] **Step 1: Написать падающий тест**

Создать `pkg/strategy/trade_recorder_insert_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool — минимальный пул для этого пакета (services/api-gateway тесты используют
// свой newTestServer; здесь тестируем pkg/strategy напрямую через DATABASE_URL из окружения).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://sis:sis_secret@localhost:6432/sis")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestInsertMatrixTPProfit_Idempotent: два вызова с одинаковым OrderID пишут ровно одну
// строку (ON CONFLICT DO NOTHING), значения gross/net корректны.
func TestInsertMatrixTPProfit_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"insmtp-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, name, api_key_enc, secret_enc) VALUES ($1,'x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'INSUSDT','long','matrix','stopped') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM matrix_tp_profits WHERE strategy_id=$1", stratID) })

	in := MatrixTPInsertInput{
		StrategyID: stratID, BotID: nil, AccountID: accID,
		CycleNum: 1, Symbol: "INSUSDT", GrossPnl: 5.0, OrderID: "order-abc-123",
	}
	InsertMatrixTPProfit(ctx, pool, in)
	InsertMatrixTPProfit(ctx, pool, in) // second call — must be a no-op

	var count int
	var netPnl float64
	if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(net_pnl),0) FROM matrix_tp_profits WHERE strategy_id=$1`, stratID).Scan(&count, &netPnl); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (idempotent insert)", count)
	}
	if netPnl != 5.0 {
		t.Errorf("net_pnl = %v, want 5.0 (no trader_executions fees seeded)", netPnl)
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что не компилируется**

Run: `go test -tags=integration ./pkg/strategy/ -run TestInsertMatrixTPProfit_Idempotent -count=1`
Expected: FAIL — `undefined: MatrixTPInsertInput`, `undefined: InsertMatrixTPProfit`.

- [ ] **Step 3: Рефакторинг `pkg/strategy/trade_recorder.go`**

Найти конец `RecordMatrixTPProfit` (после блока вычисления `grossPnl`, строки 47-75 — retry-fetch + fallback — остаются БЕЗ ИЗМЕНЕНИЙ) и текущий «хвост» (строки 77-107: комиссии + INSERT + логи). Заменить хвост вызовом новой функции, а саму функцию вынести отдельно.

Было (строки 77-107):
```go
	// Closing-order fees for this TP order.
	var fees float64
	if in.OrderID != "" {
		_ = pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(ABS(exec_fee)), 0)
			FROM trader_executions
			WHERE account_id = $1 AND order_id = $2 AND exec_type = 'Trade'`,
			in.Strategy.AccountID, in.OrderID,
		).Scan(&fees)
	}
	netPnl := grossPnl - fees

	var orderIDPtr *string
	if in.OrderID != "" {
		orderIDPtr = &in.OrderID
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO matrix_tp_profits
			(strategy_id, bot_id, account_id, cycle_num, symbol, gross_pnl, fees, net_pnl, bybit_order_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (account_id, bybit_order_id) WHERE bybit_order_id IS NOT NULL
		DO NOTHING`,
		in.Strategy.ID, in.Strategy.BotID, in.Strategy.AccountID, in.CycleNum, in.Strategy.Symbol,
		grossPnl, fees, netPnl, orderIDPtr,
	); err != nil {
		log.Printf("matrix tp recorder [%s cy%d]: insert: %v", in.Strategy.Symbol, in.CycleNum, err)
		return
	}
	log.Printf("matrix tp recorder [%s cy%d]: записано — gross=%.4f fees=%.4f net=%.4f (order=%s)",
		in.Strategy.Symbol, in.CycleNum, grossPnl, fees, netPnl, in.OrderID)
}
```

Стало (заменить весь этот блок, оставив закрывающую `}` функции `RecordMatrixTPProfit`):
```go
	InsertMatrixTPProfit(ctx, pool, MatrixTPInsertInput{
		StrategyID: in.Strategy.ID, BotID: in.Strategy.BotID, AccountID: in.Strategy.AccountID,
		CycleNum:   in.CycleNum, Symbol: in.Strategy.Symbol, GrossPnl: grossPnl, OrderID: in.OrderID,
	})
}

// MatrixTPInsertInput carries the values needed for one matrix_tp_profits row when the
// gross PnL is already known — e.g. read directly from a Bybit ClosedPnl entry by
// ClosedPnlSyncer, which doesn't need RecordMatrixTPProfit's own retry-fetch (that fetch
// exists for the in-process fill-event call site, which only has fill price/qty at the
// moment of the WS event, not yet the authoritative Bybit-reported realized PnL).
type MatrixTPInsertInput struct {
	StrategyID string
	BotID      *string
	AccountID  string
	CycleNum   int
	Symbol     string
	GrossPnl   float64
	OrderID    string
}

// InsertMatrixTPProfit writes one matrix_tp_profits row: computes fees from
// trader_executions for the given closing order, net_pnl = gross - fees, and inserts
// idempotently keyed on (account_id, bybit_order_id) — a replayed WS event or a
// ClosedPnlSyncer poll re-processing the same close is a safe no-op.
func InsertMatrixTPProfit(ctx context.Context, pool *pgxpool.Pool, in MatrixTPInsertInput) {
	var fees float64
	if in.OrderID != "" {
		_ = pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(ABS(exec_fee)), 0)
			FROM trader_executions
			WHERE account_id = $1 AND order_id = $2 AND exec_type = 'Trade'`,
			in.AccountID, in.OrderID,
		).Scan(&fees)
	}
	netPnl := in.GrossPnl - fees

	var orderIDPtr *string
	if in.OrderID != "" {
		orderIDPtr = &in.OrderID
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO matrix_tp_profits
			(strategy_id, bot_id, account_id, cycle_num, symbol, gross_pnl, fees, net_pnl, bybit_order_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (account_id, bybit_order_id) WHERE bybit_order_id IS NOT NULL
		DO NOTHING`,
		in.StrategyID, in.BotID, in.AccountID, in.CycleNum, in.Symbol,
		in.GrossPnl, fees, netPnl, orderIDPtr,
	); err != nil {
		log.Printf("matrix tp insert [%s cy%d]: %v", in.Symbol, in.CycleNum, err)
		return
	}
	log.Printf("matrix tp insert [%s cy%d]: записано — gross=%.4f fees=%.4f net=%.4f (order=%s)",
		in.Symbol, in.CycleNum, in.GrossPnl, fees, netPnl, in.OrderID)
}
```

- [ ] **Step 4: Запустить — убедиться, что проходит**

Run: `go build ./... && go test -tags=integration ./pkg/strategy/ -run TestInsertMatrixTPProfit_Idempotent -count=1 -v`
Expected: сборка чистая; тест `PASS`.

- [ ] **Step 5: Регресс — существующие тесты `matrix_tp_profits`/reuse не сломались**

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestCreateBotStrategy|TestGetHedgeSession' -count=1`
Expected: `ok` (эти тесты используют `matrix_tp_profits`/трёхкомпонентный подсчёт — рефактор `RecordMatrixTPProfit` не должен был изменить их поведение, т.к. логика вычисления `grossPnl` не тронута, только вынесен хвост).

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/trade_recorder.go pkg/strategy/trade_recorder_insert_test.go
git commit -m "refactor(strategy): вынести InsertMatrixTPProfit из RecordMatrixTPProfit"
```

---

### Task 3: Атрибуция по linkId в `ClosedPnlSyncer`

**Files:**
- Modify: `services/api-gateway/closed_pnl_syncer.go`
- Test: `services/api-gateway/closed_pnl_syncer_linkid_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `services/api-gateway/closed_pnl_syncer_linkid_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// TestClosedPnlSyncer_MatrixTPLinkID_RecordsProfitNotZombie: a ClosedPnl entry whose
// orderLinkId identifies a matrix-TP re-arm must be recorded into matrix_tp_profits and
// must NOT touch the strategy's open cycle (no ghost_close/zombie force-end) — this is
// the exact bug being fixed: a healthy, still-trading matrix cycle looks like a "zombie"
// to the old time-window heuristics.
func TestClosedPnlSyncer_MatrixTPLinkID_RecordsProfitNotZombie(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsync")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'CPLUSDT','long','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM matrix_tp_profits WHERE strategy_id=$1", stratID) })

	// An OPEN cycle — same state a healthy, currently-trading matrix cycle is always in.
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	linkID := "SIS_STR-" + stratID[:8] + "-tpl2-1-99001"
	p := trader.ClosedPnl{
		Symbol: "CPLUSDT", OrderId: "bybit-order-xyz", OrderLinkId: linkID,
		Side: "Sell", Qty: "100", AvgEntryPrice: "1.0", AvgExitPrice: "1.05",
		ClosedPnl: "5.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	// 1. Recorded into matrix_tp_profits, keyed by the real Bybit order id.
	var count int
	var netPnl float64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(net_pnl),0) FROM matrix_tp_profits WHERE strategy_id=$1 AND bybit_order_id=$2`,
		stratID, p.OrderId).Scan(&count, &netPnl); err != nil {
		t.Fatalf("query matrix_tp_profits: %v", err)
	}
	if count != 1 {
		t.Fatalf("matrix_tp_profits rows = %d, want 1", count)
	}
	if netPnl != 5.0 {
		t.Errorf("net_pnl = %v, want 5.0", netPnl)
	}

	// 2. The cycle must remain OPEN — no false zombie-close.
	var endedAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT ended_at FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt); err != nil {
		t.Fatalf("query cycle: %v", err)
	}
	if endedAt != nil {
		t.Errorf("cycle ended_at = %v, want NULL (must stay open — this is a re-arm, not a real close)", *endedAt)
	}

	// 3. No unattributed manual row was created for this order.
	var manualCount int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&manualCount)
	if manualCount != 0 {
		t.Errorf("trade_history rows for this order = %d, want 0 (matrix-TP goes to matrix_tp_profits, not trade_history)", manualCount)
	}
}

// TestClosedPnlSyncer_MatrixLevelSLLinkID_NoTradeHistoryWrite: a per-level matrix SL
// linkId is already tracked via strategy_levels.realized_pnl — the syncer must not also
// write a trade_history row for it (would be a duplicate/orphan entry).
func TestClosedPnlSyncer_MatrixLevelSLLinkID_NoTradeHistoryWrite(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsyncsl")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'CPLSLUSDT','short','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	linkID := "SIS_STR-" + stratID[:8] + "-msl-2-5"
	p := trader.ClosedPnl{
		Symbol: "CPLSLUSDT", OrderId: "bybit-order-msl-1", OrderLinkId: linkID,
		Side: "Buy", Qty: "10", AvgEntryPrice: "1.0", AvgExitPrice: "0.95",
		ClosedPnl: "-0.5", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	var count int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&count)
	if count != 0 {
		t.Errorf("trade_history rows = %d, want 0 (per-level SL already tracked elsewhere)", count)
	}
}

// TestClosedPnlSyncer_UnrecognizedLinkID_FallsBackToLegacyManual: an order with no
// SIS_STR- prefix (a genuine manual exchange trade) must still land as an unattributed
// manual row — the existing fallback path must be unaffected by this change.
func TestClosedPnlSyncer_UnrecognizedLinkID_FallsBackToLegacyManual(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsyncmanual")
	accID := createTestAccount(t, s, userID)

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	p := trader.ClosedPnl{
		Symbol: "MANUALUSDT", OrderId: "bybit-order-manual-1", OrderLinkId: "",
		Side: "Sell", Qty: "10", AvgEntryPrice: "1.0", AvgExitPrice: "1.1",
		ClosedPnl: "1.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE bybit_close_order_id=$1", p.OrderId) })

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	var count int
	var result string
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("trade_history rows = %d (err=%v), want 1 (legacy manual fallback)", count, err)
	}
	s.pool.QueryRow(ctx, `SELECT result FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&result)
	if result != "manual" {
		t.Errorf("result = %q, want %q (unchanged legacy behavior)", result, "manual")
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что тесты падают (текущее поведение)**

Run: `go test -tags=integration ./services/api-gateway/ -run TestClosedPnlSyncer_MatrixTPLinkID_RecordsProfitNotZombie -count=1 -v`
Expected: FAIL — `matrix_tp_profits rows = 0, want 1` (шаг 0 ещё не существует, синкер сейчас пойдёт по старым эвристикам).

Run: `go test -tags=integration ./services/api-gateway/ -run TestClosedPnlSyncer_UnrecognizedLinkID_FallsBackToLegacyManual -count=1 -v`
Expected: этот тест уже может проходить (существующее поведение не меняется) — если PASS уже сейчас, это ожидаемо и подтверждает бейслайн для регресса; продолжаем.

- [ ] **Step 3: Реализовать шаг 0/1b в `services/api-gateway/closed_pnl_syncer.go`**

Добавить импорт `strconv` уже есть; добавить в блок импортов `"regexp"` НЕ требуется (парсинг — в `pkg/strategy`). Вставить новый метод сразу после `processClosedPnl` (после строки, где заканчивается существующая функция, перед `func safeDiv`):

```go
// processLinkIDAttributed handles a ClosedPnl entry whose orderLinkId directly identifies
// the owning strategy via ParseStrategyLinkID. Returns true when the caller must NOT fall
// through to the legacy time-window heuristics (steps 2-4) — either because this function
// fully handled the entry, or because it positively identified the owning strategy and the
// primary in-process recorder is expected to catch up on its own. Returns false only when
// the parsed strategy id prefix doesn't resolve to a real row (e.g. deleted strategy) —
// the legacy path is a safety net for that edge case.
func (s *ClosedPnlSyncer) processLinkIDAttributed(ctx context.Context, a closedPnlAccount, p trader.ClosedPnl, parsed strategy.ParsedLinkID) bool {
	var stratID, symbol, direction, category string
	var botID *string
	var cycleID *string
	var cycleNum *int
	err := s.pool.QueryRow(ctx, `
		SELECT st.id, st.symbol, st.direction, st.category, st.bot_id, lc.cycle_id, lc.cycle_num
		FROM strategies st
		LEFT JOIN LATERAL (
			SELECT c.id AS cycle_id, c.cycle_num
			FROM strategy_cycles c WHERE c.strategy_id = st.id
			ORDER BY c.cycle_num DESC LIMIT 1
		) lc ON true
		WHERE st.account_id = $1 AND st.id::text LIKE $2`,
		a.id, parsed.StrategyID8+"%",
	).Scan(&stratID, &symbol, &direction, &category, &botID, &cycleID, &cycleNum)
	if err != nil {
		return false // no matching strategy on this account — let the legacy path try
	}

	switch parsed.Kind {
	case strategy.LinkIDMatrixTP:
		// Global matrix TP re-arm: the cycle does NOT end. Record directly into
		// matrix_tp_profits — same table/idempotency the in-process engine uses via
		// RecordMatrixTPProfit. Never touch ended_at: this cycle is healthy and still
		// trading, not a zombie — that is exactly the bug this fixes.
		grossPnl, _ := strconv.ParseFloat(p.ClosedPnl, 64)
		cn := 0
		if cycleNum != nil {
			cn = *cycleNum
		}
		strategy.InsertMatrixTPProfit(ctx, s.pool, strategy.MatrixTPInsertInput{
			StrategyID: stratID, BotID: botID, AccountID: a.id,
			CycleNum: cn, Symbol: symbol, GrossPnl: grossPnl, OrderID: p.OrderId,
		})
		return true

	case strategy.LinkIDMatrixLevelSL:
		// Already recorded by the in-process engine directly into
		// strategy_levels.realized_pnl (handleMatrixSLFill) — writing this into
		// trade_history too would create a duplicate/orphan row. No-op.
		log.Printf("closed_pnl_syncer: %s per-level matrix SL (order=%s) already tracked via strategy_levels — skip", p.Symbol, p.OrderId)
		return true

	case strategy.LinkIDGridTP, strategy.LinkIDGridSL:
		// A genuine cycle-ending close, now with a PRECISE strategy match instead of
		// the fuzzy time-window guess below. The primary in-process recorder
		// (closeCycle → RecordStrategyTrade) is expected to write this; if it hasn't
		// yet (timing race), the next poll's step-1 existence check will pick it up
		// once written. We do NOT force anything here, and we do NOT fall through to
		// zombie-detection for a strategy we've positively identified.
		if cycleID != nil {
			log.Printf("closed_pnl_syncer: %s %s linkId-attributed to strategy %s cycle %s, waiting for recorder",
				p.Symbol, direction, stratID[:8], (*cycleID)[:8])
		}
		return true

	default: // strategy.LinkIDUnrecognized — matched our prefix, not a close-type suffix.
		return false
	}
}
```

Затем в существующей `processClosedPnl` вставить новый шаг СРАЗУ после шага 1 (после блока `if exists { return }`), ПЕРЕД комментарием `// 2. Does a strategy cycle own this close?`:

Было:
```go
	if exists {
		return
	}

	// 2. Does a strategy cycle own this close?
```

Стало:
```go
	if exists {
		return
	}

	// 1b. Direct attribution via our own orderLinkId — Bybit echoes it back unchanged.
	// Authoritative: tells us exactly which strategy (and bot) owns this close,
	// independent of whether the cycle looks "ended" or "zombie" from the outside —
	// which is exactly what breaks for matrix bots (a global TP re-arms the SAME cycle
	// instead of ending it, so the time-window heuristics below misfire on it).
	if parsed, ok := strategy.ParseStrategyLinkID(p.OrderLinkId); ok {
		if s.processLinkIDAttributed(ctx, a, p, parsed) {
			return
		}
	}

	// 2. Does a strategy cycle own this close?
```

- [ ] **Step 4: Запустить все тесты Task 3 — убедиться, что проходят**

Run: `go build ./... && go test -tags=integration ./services/api-gateway/ -run 'TestClosedPnlSyncer' -count=1 -v`
Expected: все три теста `PASS`.

- [ ] **Step 5: Регресс — существующие grid/hedge/zombie сценарии не изменились**

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestMatrixZombie|TestGetHedgeSession|TestCreateBotStrategy|TestBindStrategiesToBot' -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/closed_pnl_syncer.go services/api-gateway/closed_pnl_syncer_linkid_test.go
git commit -m "feat(strategies): ClosedPnlSyncer атрибутирует закрытия по orderLinkId напрямую"
```

---

### Task 4: Единый источник PnL для дашборда и статистики карточек ботов

**Files:**
- Create: `services/api-gateway/pnl_source.go`
- Modify: `services/api-gateway/dashboard_handler.go`
- Modify: `services/api-gateway/bots_handler.go`
- Test: `services/api-gateway/pnl_source_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `services/api-gateway/pnl_source_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBotPnlUnionSQL_MatchesStrategyCumulativePnl: сумма по трёхкомпонентному источнику
// (botPnlUnionSQL) для стратегии должна совпадать с тем, что уже отдаёт
// GetStrategyCumulativePnl — иначе "Накоплено" и статистика бота расходятся.
func TestBotPnlUnionSQL_MatchesStrategyCumulativePnl(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "pnlunion")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "pnlunion")

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'PNLUUSDT','long','matrix','active') RETURNING id`,
		userID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// trade_history source.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, bot_id, account_id, owner_id, symbol, category, direction, cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,$4,'PNLUUSDT','linear','long',1,'tp',NOW(),NOW(),2.5)`,
		stratID, botID, accID, userID); err != nil {
		t.Fatalf("insert trade_history: %v", err)
	}

	// matrix_tp_profits source.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO matrix_tp_profits (strategy_id, bot_id, account_id, cycle_num, symbol, net_pnl, bybit_order_id)
		 VALUES ($1,$2,$3,1,'PNLUUSDT',1.5,'pnlu-order-1')`,
		stratID, botID, accID); err != nil {
		t.Fatalf("insert matrix_tp_profits: %v", err)
	}

	// strategy_levels source.
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,2,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, realized_pnl, sl_closed_at)
		 VALUES ($1,$2,1,'Sell',1.0,10,'10','sl_closed',0.3,NOW())`,
		stratID, cycleID); err != nil {
		t.Fatalf("insert strategy_levels: %v", err)
	}

	want := 2.5 + 1.5 + 0.3

	// Read via GetStrategyCumulativePnl (existing endpoint).
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+stratID+"/cumulative-pnl", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": stratID})
	rec := httptest.NewRecorder()
	s.GetStrategyCumulativePnl(rec, req)
	var cpResp struct {
		CumulativePnl float64 `json:"cumulative_pnl"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&cpResp); err != nil {
		t.Fatalf("decode cumulative-pnl: %v", err)
	}
	if diff := cpResp.CumulativePnl - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("GetStrategyCumulativePnl = %v, want %v", cpResp.CumulativePnl, want)
	}

	// Read via the new shared botPnlUnionSQL directly.
	var unionSum float64
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(bp.net_pnl),0) FROM `+botPnlUnionSQL+` bp WHERE bp.bot_id = $1`,
		botID).Scan(&unionSum); err != nil {
		t.Fatalf("query botPnlUnionSQL: %v", err)
	}
	if diff := unionSum - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("botPnlUnionSQL sum = %v, want %v", unionSum, want)
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что не компилируется**

Run: `go test -tags=integration ./services/api-gateway/ -run TestBotPnlUnionSQL_MatchesStrategyCumulativePnl -count=1`
Expected: FAIL — `undefined: botPnlUnionSQL`.

- [ ] **Step 3: Создать `services/api-gateway/pnl_source.go`**

```go
// services/api-gateway/pnl_source.go
package main

// botPnlUnionSQL is a derived-table subquery normalizing every realized-PnL source into
// one (bot_id, owner_id, account_id, net_pnl, closed_at) row shape: full-cycle closes
// (trade_history), per-level matrix SL fills (strategy_levels.realized_pnl), and global
// matrix-TP re-arm profits (matrix_tp_profits). A matrix bot's profit lives mostly in the
// last two sources — reading trade_history alone makes such a bot vanish from stats
// entirely (its trade_history bot_id rows can be zero even though it's actively
// profitable). Keep this the ONLY place that defines "total bot PnL" so the dashboard
// leaderboard, bot-card stats, and the per-strategy "Накоплено" counters
// (GetHedgeSession, GetStrategyCumulativePnl) can't drift apart again.
//
// Usage: embed as a derived table, e.g. `FROM ` + botPnlUnionSQL + ` th JOIN bots b ON
// b.id = th.bot_id ...` — aliasing it `th` lets existing `WHERE th.owner_id = $1`-style
// filter strings keep working unchanged, since the union's projected columns
// (bot_id, owner_id, account_id, net_pnl, closed_at) match trade_history's own column
// names.
const botPnlUnionSQL = `(
	SELECT th.bot_id, th.owner_id, th.account_id, th.net_pnl, th.closed_at
	FROM trade_history th
	UNION ALL
	SELECT st.bot_id, st.owner_id, st.account_id, sl.realized_pnl AS net_pnl, sl.sl_closed_at AS closed_at
	FROM strategy_levels sl
	JOIN strategies st ON st.id = sl.strategy_id
	WHERE sl.realized_pnl IS NOT NULL
	UNION ALL
	SELECT mtp.bot_id, st.owner_id, mtp.account_id, mtp.net_pnl, mtp.closed_at
	FROM matrix_tp_profits mtp
	JOIN strategies st ON st.id = mtp.strategy_id
)`
```

- [ ] **Step 4: Обновить дашборд-лидерборд (`services/api-gateway/dashboard_handler.go`)**

Найти блок «── 3. Bot leaderboard ──» (строки 178-205). Заменить SQL внутри:

Было:
```go
		rows, err := s.pool.Query(ctx, `
			SELECT
				b.id, b.name, b.status,
				COUNT(th.id)                                AS trades,
				COUNT(th.id) FILTER (WHERE th.net_pnl > 0) AS wins,
				COALESCE(SUM(th.net_pnl), 0)               AS pnl
			FROM trade_history th
			JOIN bots b ON b.id = th.bot_id `+baseWhere+`
				AND th.bot_id IS NOT NULL
			GROUP BY b.id, b.name, b.status
			ORDER BY pnl DESC
			LIMIT 10`,
			baseArgs...,
		)
```

Стало:
```go
		rows, err := s.pool.Query(ctx, `
			SELECT
				b.id, b.name, b.status,
				COUNT(*)                                AS trades,
				COUNT(*) FILTER (WHERE th.net_pnl > 0) AS wins,
				COALESCE(SUM(th.net_pnl), 0)           AS pnl
			FROM `+botPnlUnionSQL+` th
			JOIN bots b ON b.id = th.bot_id `+baseWhere+`
				AND th.bot_id IS NOT NULL
			GROUP BY b.id, b.name, b.status
			ORDER BY pnl DESC
			LIMIT 10`,
			baseArgs...,
		)
```

(`baseWhere`/`baseArgs` не меняются — производная таблица переиспользует алиас `th`, поэтому существующая строка фильтра `WHERE th.owner_id = $1 [AND th.account_id = $2] [AND th.closed_at >= $3]` продолжает работать без изменений, т.к. UNION проецирует те же имена колонок.)

Блок «── 4. Recent trades ──» (строки 207-235) **не трогаем** — это построчная лента конкретных сделок из `trade_history`, а не агрегат; объединение построчного списка с `matrix_tp_profits`/`strategy_levels` (разная форма строки — нет `opened_at`/`volume_usdt`/`pnl_pct` и т.д.) вне области этого плана.

- [ ] **Step 5: Обновить статистику карточек ботов (`services/api-gateway/bots_handler.go`)**

Найти `mineStatsCols` (строки 115-128). Заменить:

Было:
```go
// mineStatsCols — нули для статистики сделок (trade_history удалена, будет пересоздана)
const mineStatsCols = `,
	0::int AS trades_total,
	0::int AS trades_win,
	0::float8 AS net_pnl_total,
	COALESCE(
		(SELECT CASE WHEN b2.is_official THEN 'NovaBot'
		             ELSE COALESCE(
		                  (SELECT COALESCE(uo.username, uo.email) FROM users uo WHERE uo.id = b2.original_author_id),
		                  u2.username, u2.email) END
		 FROM bots b2 JOIN users u2 ON u2.id = b2.owner_id
		 WHERE b2.id = b.source_bot_id),
		''
	) AS source_author`
```

Стало:
```go
// mineStatsCols — реальная статистика сделок владельца по единому источнику PnL
// (botPnlUnionSQL: trade_history + strategy_levels.realized_pnl + matrix_tp_profits),
// иначе боты, чья прибыль идёт через matrix-TP re-arm, показывали бы нули.
const mineStatsCols = `,
	(SELECT COUNT(*) FROM ` + botPnlUnionSQL + ` bp WHERE bp.bot_id = b.id)::int AS trades_total,
	(SELECT COUNT(*) FROM ` + botPnlUnionSQL + ` bp WHERE bp.bot_id = b.id AND bp.net_pnl > 0)::int AS trades_win,
	COALESCE((SELECT SUM(bp.net_pnl) FROM ` + botPnlUnionSQL + ` bp WHERE bp.bot_id = b.id), 0)::float8 AS net_pnl_total,
	COALESCE(
		(SELECT CASE WHEN b2.is_official THEN 'NovaBot'
		             ELSE COALESCE(
		                  (SELECT COALESCE(uo.username, uo.email) FROM users uo WHERE uo.id = b2.original_author_id),
		                  u2.username, u2.email) END
		 FROM bots b2 JOIN users u2 ON u2.id = b2.owner_id
		 WHERE b2.id = b.source_bot_id),
		''
	) AS source_author`
```

`zeroStatsCols` (строка 131) **не трогаем** — используется для чужих/каталожных ботов (другая область видимости, отдельный вопрос видимости данных, не то, на что жаловался пользователь).

- [ ] **Step 6: Запустить все тесты Task 4**

Run: `go build ./... && go test -tags=integration ./services/api-gateway/ -run 'TestBotPnlUnionSQL_MatchesStrategyCumulativePnl' -count=1 -v`
Expected: `PASS`.

- [ ] **Step 7: Регресс — существующие боты со статистикой не сломались**

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestCreateBotStrategy|TestGetHedgeSession|TestBind' -count=1`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add services/api-gateway/pnl_source.go services/api-gateway/dashboard_handler.go services/api-gateway/bots_handler.go services/api-gateway/pnl_source_test.go
git commit -m "feat(stats): единый трёхкомпонентный источник PnL для лидерборда и статистики карточек ботов"
```

---

### Task 5: Точная подпись причины закрытия для `stopMatrixPair`

**Files:**
- Modify: `pkg/strategy/cycle.go` (StrategyRunner struct, `closePositionExternal`)
- Modify: `pkg/strategy/engine.go` (`Engine.NotifyExpectedClose`)
- Modify: `services/api-gateway/matrix_engine.go` (`processMatrixBot`, `checkMatrixPairedClose`, `stopMatrixPair`)
- Test: `pkg/strategy/expected_close_test.go`

- [ ] **Step 1: Написать падающий тест (чистая логика TTL, без БД)**

Создать `pkg/strategy/expected_close_test.go`:

```go
package strategy

import (
	"testing"
	"time"
)

// TestResolveCloseResult: an expected-close reason set recently is used verbatim; one
// set too long ago (past the TTL) or never set at all falls back to the default.
func TestResolveCloseResult(t *testing.T) {
	cases := []struct {
		name     string
		reason   string
		setAt    time.Time
		want     string
	}{
		{"no reason set", "", time.Time{}, "manual_close"},
		{"reason set recently", "paired_close", time.Now().Add(-30 * time.Second), "paired_close"},
		{"reason set at the TTL edge (within)", "paired_close", time.Now().Add(-119 * time.Second), "paired_close"},
		{"reason set too long ago", "paired_close", time.Now().Add(-3 * time.Minute), "manual_close"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveCloseResult(c.reason, c.setAt)
			if got != c.want {
				t.Errorf("resolveCloseResult(%q, %v) = %q, want %q", c.reason, c.setAt, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что не компилируется**

Run: `go test ./pkg/strategy/ -run TestResolveCloseResult -count=1`
Expected: FAIL — `undefined: resolveCloseResult`.

- [ ] **Step 3: Добавить поля в `StrategyRunner` (`pkg/strategy/cycle.go`)**

Найти блок полей `StrategyRunner` (строки 34-36):

```go
	closedBySelf            bool      // set by handleTPFill/handleSLFill to suppress the WS position-close event
	closedByReason          string    // human-readable reason why we closed/reduced (TP/SL/Matrix TP/…); cleared after use
```

Добавить сразу после:

```go
	closedBySelf            bool      // set by handleTPFill/handleSLFill to suppress the WS position-close event
	closedByReason          string    // human-readable reason why we closed/reduced (TP/SL/Matrix TP/…); cleared after use
	expectedCloseReason     string    // set via Engine.NotifyExpectedClose by an EXTERNAL bot-engine action (e.g. stopMatrixPair) that is about to close this position on purpose; used as trade_history.result instead of the default "manual_close". Cleared after use or once expired.
	expectedCloseSetAt      time.Time // when expectedCloseReason was set — used for TTL expiry
```

- [ ] **Step 4: Реализовать `resolveCloseResult` и использовать его в `closePositionExternal`**

Добавить в `pkg/strategy/cycle.go` рядом с `manualCloseStatus` (перед `closePositionExternal`, после `manualCloseStatus`, строка ~4418):

```go
// expectedCloseTTL bounds how long an Engine.NotifyExpectedClose reason stays valid. If
// the closing orders that were supposed to trigger it never actually flatten the
// position (exchange error, partial fill), a stale reason must not mislabel a later,
// unrelated close — after the TTL, the safe default (manual_close) applies instead.
const expectedCloseTTL = 2 * time.Minute

// resolveCloseResult picks the trade_history result label for an external close: the
// externally-set reason (via Engine.NotifyExpectedClose) if one is set and still within
// its TTL, otherwise the default "manual_close".
func resolveCloseResult(reason string, setAt time.Time) string {
	if reason != "" && time.Since(setAt) <= expectedCloseTTL {
		return reason
	}
	return "manual_close"
}
```

Затем в `closePositionExternal` заменить:

Было (строка 4433):
```go
	sr.closeCycle(ctx, "manual_close")
```

Стало:
```go
	result := resolveCloseResult(sr.expectedCloseReason, sr.expectedCloseSetAt)
	sr.expectedCloseReason = "" // consume regardless of whether it was used
	sr.closeCycle(ctx, result)
```

- [ ] **Step 5: Запустить — убедиться, что проходит**

Run: `go build ./... && go test ./pkg/strategy/ -run TestResolveCloseResult -count=1 -v`
Expected: сборка чистая (проверяет, что новые поля/использование корректны); тест `PASS`.

- [ ] **Step 6: Добавить `Engine.NotifyExpectedClose` (`pkg/strategy/engine.go`)**

Добавить сразу после существующего `Notify` (после строки, где заканчивается его тело — метод `Notify` заканчивается вызовом `e.loadStrategy(ctx, s)`):

```go
// NotifyExpectedClose tells the strategy runner (if currently loaded in memory) that an
// external close is about to happen for a known reason — e.g. matrix paired-close market
// orders placed by the bot engine (stopMatrixPair) — so the runner labels the resulting
// trade_history row accurately instead of assuming a genuine manual close. Best-effort:
// a no-op if the strategy/account isn't currently loaded (rare — the close then falls
// back to the default "manual_close" label, same as before this feature existed).
// Must NOT be called with any StrategyRunner lock held.
func (e *Engine) NotifyExpectedClose(strategyID, accountID, reason string) {
	e.mu.RLock()
	runner, ok := e.runners[accountID]
	e.mu.RUnlock()
	if !ok {
		return
	}
	runner.mu.RLock()
	sr, ok := runner.strategies[strategyID]
	runner.mu.RUnlock()
	if !ok {
		return
	}
	sr.mu.Lock()
	sr.expectedCloseReason = reason
	sr.expectedCloseSetAt = time.Now()
	sr.mu.Unlock()
}
```

- [ ] **Step 7: Прокинуть `accountID` до `stopMatrixPair` и вызвать `NotifyExpectedClose` (`services/api-gateway/matrix_engine.go`)**

Изменить сигнатуру `checkMatrixPairedClose` (строка 181):

Было:
```go
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) map[string]bool {
```

Стало:
```go
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) map[string]bool {
```

Обновить вызов `stopMatrixPair` внутри (строка 248):

Было:
```go
			s.stopMatrixPair(ctx, botID, sym, p.longID, p.shortID, creds, category, longPos, shortPos)
```

Стало:
```go
			s.stopMatrixPair(ctx, botID, accountID, sym, p.longID, p.shortID, creds, category, longPos, shortPos)
```

Обновить вызывающий код в `processMatrixBot` (строка 35):

Было:
```go
	closed := s.checkMatrixPairedClose(ctx, botID, cfg, creds, posMap)
```

Стало:
```go
	closed := s.checkMatrixPairedClose(ctx, botID, accountID, cfg, creds, posMap)
```

Изменить сигнатуру `stopMatrixPair` (строка 282) и добавить вызов `NotifyExpectedClose` перед циклом закрывающих ордеров:

Было:
```go
func (s *Server) stopMatrixPair(ctx context.Context, botID, symbol, longID, shortID string, creds trader.Credentials, category string, longPos, shortPos hedgePosInfo) {
	// Realize the combined profit: paired-close must CLOSE both exchange positions.
	// Stopping the strategies alone does NOT flat a matrix position (the cycle is kept
	// open by design), so without this the positions linger, ensureMatrixStrategies
	// re-adopts them, and the trigger re-fires every tick without ever taking profit.
	for _, leg := range []struct {
```

Стало:
```go
func (s *Server) stopMatrixPair(ctx context.Context, botID, accountID, symbol, longID, shortID string, creds trader.Credentials, category string, longPos, shortPos hedgePosInfo) {
	// Tell the strategy engine BEFORE placing the closing orders: when the WS position-
	// zero event arrives for these legs, label the resulting trade_history row
	// "paired_close" (this is a bot decision, not the user closing by hand) instead of
	// the default "manual_close". bot_id is already correct either way — this only
	// fixes the misleading label on the История сделок page.
	if s.engine != nil {
		s.engine.NotifyExpectedClose(longID, accountID, "paired_close")
		s.engine.NotifyExpectedClose(shortID, accountID, "paired_close")
	}

	// Realize the combined profit: paired-close must CLOSE both exchange positions.
	// Stopping the strategies alone does NOT flat a matrix position (the cycle is kept
	// open by design), so without this the positions linger, ensureMatrixStrategies
	// re-adopts them, and the trigger re-fires every tick without ever taking profit.
	for _, leg := range []struct {
```

- [ ] **Step 8: Интеграционный тест на весь путь**

Создать `services/api-gateway/expected_close_integration_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// TestStopMatrixPair_LabelsPairedClose: after checkMatrixPairedClose/stopMatrixPair
// notify the engine, a subsequent WS-detected external close for that strategy must be
// labeled "paired_close" — this exercises resolveCloseResult end-to-end via the engine's
// in-memory runner state, without needing a live exchange connection.
func TestStopMatrixPair_LabelsPairedClose(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "expclose")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "expclose")

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'EXPCUSDT','long','matrix','active') RETURNING id`,
		userID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// Load the strategy into the engine's in-memory runner (same path Notify uses).
	s.engine.Notify(ctx, stratID)
	time.Sleep(200 * time.Millisecond) // let addStrategy's goroutine register the runner

	s.engine.NotifyExpectedClose(stratID, accID, "paired_close")

	// Can't reach unexported StrategyRunner fields from this package — verify indirectly
	// isn't possible without exposing test hooks. Assert instead that NotifyExpectedClose
	// did not panic/error for a loaded strategy (smoke test for the wiring); the pure
	// resolveCloseResult unit test (Task 5, Step 1) covers the TTL/label logic itself.
	// If a lower-level hook becomes available later, extend this test to close the
	// position and assert trade_history.result = 'paired_close' end-to-end.
}
```

**Примечание для исполнителя:** `expectedCloseReason` — неэкспортированное поле `StrategyRunner`, и у пакета `pkg/strategy` нет публичного метода "закрыть позицию извне" для чистого end-to-end теста без реальной биржи. Полное end-to-end покрытие (WS-событие → `closePositionExternal` → `trade_history.result='paired_close'`) технически возможно только с реальным/мокнутым биржевым потоком, что выходит за рамки этого плана. `TestResolveCloseResult` (Step 1) покрывает саму логику выбора метки; данный тест — только смоук на то, что `NotifyExpectedClose` не падает для загруженной стратегии. Это осознанная граница покрытия, отметить в отчёте по завершении.

- [ ] **Step 9: Собрать, проверить**

Run: `go build ./... && go vet ./pkg/strategy/... ./services/api-gateway/...`
Expected: чисто.

Run: `go test ./pkg/strategy/ -run TestResolveCloseResult -count=1 -v`
Expected: `PASS`.

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestStopMatrixPair_LabelsPairedClose|TestMatrixLegCloseRequest' -count=1 -v`
Expected: `PASS`/`ok`.

- [ ] **Step 10: Регресс — существующие matrix paired-close/zombie/pause сценарии не сломались**

Run: `go test -tags=integration ./services/api-gateway/ -run 'TestMatrixZombie|TestCreateBotStrategy|TestGetHedgeSession' -count=1`
Expected: `ok`.

- [ ] **Step 11: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/engine.go pkg/strategy/expected_close_test.go services/api-gateway/matrix_engine.go services/api-gateway/expected_close_integration_test.go
git commit -m "feat(strategies): stopMatrixPair подписывает закрытие как paired_close вместо manual_close"
```

---

### Task 6: Финальная проверка

- [ ] **Step 1: Полная сборка, vet, все новые и затронутые тесты**

Run:
```bash
go build ./...
go vet ./pkg/strategy/... ./services/api-gateway/...
go test ./pkg/strategy/ -count=1
go test -tags=integration ./services/api-gateway/ -run 'TestParseStrategyLinkID|TestInsertMatrixTPProfit|TestClosedPnlSyncer|TestBotPnlUnionSQL|TestStopMatrixPair|TestResolveCloseResult|TestMatrixZombie|TestGetHedgeSession|TestCreateBotStrategy|TestBindStrategiesToBot|TestListStrategies|TestDirectionHasLiveStrategy' -count=1
```
Expected: сборка/vet чисто; все тесты `ok`.

- [ ] **Step 2: Ручная проверка (после пересборки и перезапуска api-gateway)**

1. Убедиться, что `matrix_tp_profits` продолжает пополняться для активных matrix-ботов (как и раньше — этот путь не менялся), и что теперь то же самое происходит через `ClosedPnlSyncer`, если движковая запись почему-то опоздала (лог `matrix tp insert [...]: записано`).
2. Дашборд → Bot leaderboard: боты, чья прибыль идёт через matrix-TP (например MatrixNova), должны появиться в списке.
3. Страница «Мои боты»: карточки показывают реальные «Сделок»/«Профит»/«Win», не «—» для всех.
4. Спровоцировать/дождаться парного закрытия matrix-пары → в `trade_history` результат `paired_close`, не `manual_close`, `bot_id` корректен.
5. Убедиться, что доля новых `result='manual'` строк с `bot_id IS NULL` для аккаунтов matrix-ботов заметно снизилась по сравнению с состоянием на момент диагностики (было 8 из 11 за 3 дня на аккаунте MatrixNova).

---

## Заметки по объёму (из спеки)

- Построчная лента «Recent trades» на дашборде и сама постраничная таблица «История сделок» (`GetTradeHistory`) **не объединяются** с `matrix_tp_profits`/`strategy_levels` в этом плане — это агрегатные источники с несовместимой построчной формой (нет `opened_at`/`volume_usdt`/`qty` и т.д.). Атрибуция по linkId (Task 3) напрямую уменьшает количество ложных `manual`-строк на странице истории — это не расширение состава строк, а исправление их атрибуции.
- `zeroStatsCols` (чужие/каталожные боты) не трогается — вне заявленной жалобы.
- Существующая атрибуция по времени (шаги 2-4 синкера) остаётся нетронутой — используется как fallback, когда linkId не распознан.
