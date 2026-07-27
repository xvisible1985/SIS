# RescueBot Backend (Фаза 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить бэкенд нового типа бота `RescueBot` (`bot_kind: "rescue"`): создаёт хедж как обычный hedge-бот, и дополнительно на каждом тике частично снимает объём мейн-позиции, профинансированный уже реализованным PnL хеджа (`accumulated_pnl`), когда все включённые триггеры (движение цены, абсолютный уровень цены, минимальная накопленная сумма, прикреплённый сигнал) истинны одновременно.

**Архитектура:** Новый `processRescueBot` в `hedge_engine.go`'s тик-диспетчере переиспользует уже существующие, проверенно kind-агностичные хелперы (`checkHedgeDeactivation`, `checkHedgeActivation`, `buildPairedCloseWatches`) для активации/деактивации/paired-close без изменений в них, и добавляет новую, отдельно вынесенную в `rescue_engine.go` логику частичного снятия мейна: чистые функции расчёта объёма/триггеров (юнит-тестируемые без БД) + интеграционная обвязка (запрос к БД, ордер на биржу).

**Tech Stack:** Go, PostgreSQL (миграции `migrations/NNN_*.sql`), pgx, существующий `pkg/trader` (ордера, инструменты), `pkg/signal` (сигналы), stdlib `testing` (без testify — как везде в этом пакете).

Связанные документы: `docs/superpowers/specs/2026-07-26-rescuebot-design.md`

---

## File Structure

```
sis/
  migrations/
    084_rescue_bot.sql                          # новые колонки hedge_sessions
  services/api-gateway/
    bot_engine.go                                 # + RescueBot-поля в botCfgJSON
    bot_engine_rescue_cfg_test.go                  # новый: JSON round-trip новых полей
    hedge_engine.go                                # + 'rescue' в SQL-фильтр и switch-диспетчер
    rescue_engine.go                               # новый: processRescueBot + вся rescue-логика
    rescue_engine_test.go                          # новый: юнит-тесты чистых функций
```

---

## Task 1: Миграция — новые колонки на `hedge_sessions`

**Files:**
- Create: `migrations/084_rescue_bot.sql`

- [ ] **Step 1: Создать файл миграции**

```sql
-- migrations/084_rescue_bot.sql
-- RescueBot: отслеживает совокупный объём, снятый с мейн-позиции частичными
-- закрытиями, профинансированными реализованным PnL хеджа (hedge_sessions.
-- accumulated_pnl), плюс метку времени для кулдауна между шагами. См.
-- docs/superpowers/specs/2026-07-26-rescuebot-design.md.
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS main_reduced_coin NUMERIC(18, 8) NOT NULL DEFAULT 0;
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS main_reduced_usdt NUMERIC(18, 8) NOT NULL DEFAULT 0;
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS last_partial_close_at TIMESTAMPTZ;
```

- [ ] **Step 2: Применить миграцию на dev-БД и проверить колонки**

Запустить: `./migrate.exe` из корня репозитория (эквивалент `make migrate` / `go run ./cmd/migrate/` — все три способа запускают один и тот же инструмент; `migrate.exe` — уже собранный бинарник в корне).
Ожидается: `migrations applied successfully`.

Затем проверить результат:
```sql
\d hedge_sessions
```
Ожидается: `main_reduced_coin`, `main_reduced_usdt` (`numeric(18,8) not null default 0`), `last_partial_close_at` (`timestamp with time zone`) — присутствуют.

- [ ] **Step 3: Commit**

```bash
git add migrations/084_rescue_bot.sql
git commit -m "feat(rescue): add hedge_sessions columns for partial-close tracking"
```

---

## Task 2: RescueBot-поля в `botCfgJSON`

**Files:**
- Modify: `services/api-gateway/bot_engine.go`
- Create: `services/api-gateway/bot_engine_rescue_cfg_test.go`

- [ ] **Step 1: Написать падающий тест**

`services/api-gateway/bot_engine_rescue_cfg_test.go`:
```go
package main

import (
	"encoding/json"
	"testing"
)

// TestBotCfgJSON_RescueFieldsRoundTrip проверяет, что новые RescueBot-поля
// корректно проходят JSON-маршалинг/анмаршалинг, включая nil-указатели
// (означающие "триггер выключен") и вложенный TriggerSignal.
func TestBotCfgJSON_RescueFieldsRoundTrip(t *testing.T) {
	movePct := 1.5
	level := 61200.0
	minAccum := 25.0

	original := botCfgJSON{
		BotKind:                    "rescue",
		RescuePartialCloseEnabled:  true,
		RescueTriggerPriceMovePct:  &movePct,
		RescueTriggerPriceLevel:    &level,
		RescueTriggerAccumulatedMinUsdt: &minAccum,
		RescueMinIntervalSec:       300,
	}
	original.RescueTriggerSignal = &struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	}{Name: "st-flip", Params: map[string]interface{}{"tf": "15"}}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded botCfgJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.BotKind != "rescue" || !decoded.RescuePartialCloseEnabled {
		t.Errorf("BotKind/Enabled mismatch: %+v", decoded)
	}
	if decoded.RescueTriggerPriceMovePct == nil || *decoded.RescueTriggerPriceMovePct != movePct {
		t.Errorf("RescueTriggerPriceMovePct mismatch: %+v", decoded.RescueTriggerPriceMovePct)
	}
	if decoded.RescueTriggerPriceLevel == nil || *decoded.RescueTriggerPriceLevel != level {
		t.Errorf("RescueTriggerPriceLevel mismatch: %+v", decoded.RescueTriggerPriceLevel)
	}
	if decoded.RescueTriggerAccumulatedMinUsdt == nil || *decoded.RescueTriggerAccumulatedMinUsdt != minAccum {
		t.Errorf("RescueTriggerAccumulatedMinUsdt mismatch: %+v", decoded.RescueTriggerAccumulatedMinUsdt)
	}
	if decoded.RescueTriggerSignal == nil || decoded.RescueTriggerSignal.Name != "st-flip" {
		t.Errorf("RescueTriggerSignal mismatch: %+v", decoded.RescueTriggerSignal)
	}
	if decoded.RescueMinIntervalSec != 300 {
		t.Errorf("RescueMinIntervalSec mismatch: %d", decoded.RescueMinIntervalSec)
	}

	// Триггер без указателя (nil) должен остаться nil после round-trip — это
	// "выключенное" состояние тумблера, критично для rescueTriggersMet (Task 5).
	original2 := botCfgJSON{BotKind: "rescue"}
	raw2, _ := json.Marshal(original2)
	var decoded2 botCfgJSON
	if err := json.Unmarshal(raw2, &decoded2); err != nil {
		t.Fatalf("unmarshal (empty): %v", err)
	}
	if decoded2.RescueTriggerPriceMovePct != nil || decoded2.RescueTriggerSignal != nil {
		t.Errorf("expected all rescue triggers nil when unset, got: %+v", decoded2)
	}
}
```

- [ ] **Step 2: Запустить тест — убедиться, что падает**

Запустить: `go test ./services/api-gateway/... -run TestBotCfgJSON_RescueFieldsRoundTrip -v`
Ожидается: FAIL (`unknown field 'RescuePartialCloseEnabled' in struct literal` — поля ещё не существуют).

- [ ] **Step 3: Добавить поля в `botCfgJSON`**

В `services/api-gateway/bot_engine.go`, сразу после существующего поля `HedgeBotBlacklist []string \`json:"hedge_bot_blacklist"\`` (строка 938 на момент написания плана — искать по этому полю, не по номеру строки, если файл успел измениться) добавить:

```go
	// RescueBot: частичное снятие мейна за счёт накопленного PnL хеджа (bot_kind="rescue").
	RescuePartialCloseEnabled       bool     `json:"rescue_partial_close_enabled"`
	RescueTriggerPriceMovePct       *float64 `json:"rescue_trigger_price_move_pct"`  // T1: % от hedge_entry_at_start в сторону мейна
	RescueTriggerPriceLevel         *float64 `json:"rescue_trigger_price_level"`     // T2: абсолютный уровень цены символа
	RescueTriggerAccumulatedMinUsdt *float64 `json:"rescue_trigger_accumulated_min"` // T3: минимальная сумма accumulated_pnl (USDT)
	RescueTriggerSignal             *struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	} `json:"rescue_trigger_signal"` // T4: та же форма, что ActivationSignals — по имени, не по ID
	RescueMinIntervalSec int `json:"rescue_min_interval_sec"` // кулдаун между шагами частичного закрытия
```

- [ ] **Step 4: Запустить тест — убедиться, что проходит**

Запустить: `go test ./services/api-gateway/... -run TestBotCfgJSON_RescueFieldsRoundTrip -v`
Ожидается: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/bot_engine.go services/api-gateway/bot_engine_rescue_cfg_test.go
git commit -m "feat(rescue): add RescueBot fields to shared bot config struct"
```

---

## Task 3: Расчёт объёма частичного снятия — `rescueCalcPartialCloseQty`

**Files:**
- Create: `services/api-gateway/rescue_engine.go`
- Create: `services/api-gateway/rescue_engine_test.go`

- [ ] **Step 1: Написать падающие тесты**

`services/api-gateway/rescue_engine_test.go`:
```go
package main

import "testing"

// TestRescueCalcPartialCloseQty_Long: мейн-лонг вошёл по 100, сейчас 90 (просадка
// 10/юнит). accumulated_pnl=50 -> должны снять 50/10=5 монет (без учёта округления,
// qtyStep=0 отключает округление в trader.FormatQty).
func TestRescueCalcPartialCloseQty_Long(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 50, 100, 0, 0)
	if closesEntirely {
		t.Fatal("expected partial close, not entire")
	}
	if qty < 4.999999 || qty > 5.000001 {
		t.Errorf("qty = %v, want ~5", qty)
	}
}

// TestRescueCalcPartialCloseQty_Short: мейн-шорт вошёл по 100, сейчас 110 (просадка
// 10/юнит — шорт теряет когда цена растёт). accumulated_pnl=30 -> 30/10=3 монеты.
func TestRescueCalcPartialCloseQty_Short(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Sell", 100, 110, 30, 100, 0, 0)
	if closesEntirely {
		t.Fatal("expected partial close, not entire")
	}
	if qty < 2.999999 || qty > 3.000001 {
		t.Errorf("qty = %v, want ~3", qty)
	}
}

// TestRescueCalcPartialCloseQty_NoAccumulatedProfit: accumulated_pnl<=0 — нечем
// финансировать снятие, шаг не должен ничего предлагать снять.
func TestRescueCalcPartialCloseQty_NoAccumulatedProfit(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 0, 100, 0, 0)
	if qty != 0 || closesEntirely {
		t.Errorf("qty=%v closesEntirely=%v, want 0/false when accumulated_pnl<=0", qty, closesEntirely)
	}
}

// TestRescueCalcPartialCloseQty_MainNotAtLoss: цена мейна восстановилась выше входа
// (лонг: markPrice > entryPrice) — на юнит убытка нет, формула объёма не имеет смысла
// (совпадает с обычной деактивацией/paired-close, а не с частичным снятием) — ожидаем
// no-op, а не деление на отрицательное число.
func TestRescueCalcPartialCloseQty_MainNotAtLoss(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 105, 50, 100, 0, 0)
	if qty != 0 || closesEntirely {
		t.Errorf("qty=%v closesEntirely=%v, want 0/false when main is profitable per-unit", qty, closesEntirely)
	}
}

// TestRescueCalcPartialCloseQty_CapsAtRemaining: accumulated_pnl покрывает больше,
// чем осталось от мейна (mainRemainingQty=3, расчётный объём был бы 5) — должны
// закрыть мейн целиком (qty=3, closesEntirely=true), не переливать.
func TestRescueCalcPartialCloseQty_CapsAtRemaining(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 50, 3, 0, 0)
	if !closesEntirely {
		t.Fatal("expected closesEntirely=true when computed qty exceeds remaining size")
	}
	if qty != 3 {
		t.Errorf("qty = %v, want exactly mainRemainingQty=3 (no overflow)", qty)
	}
}

// TestRescueCalcPartialCloseQty_RoundsDownToQtyStep: сырой объём 5.7 при qtyStep=0.5
// должен округлиться вниз до 5.5 (через trader.FormatQty), не до 6.
func TestRescueCalcPartialCloseQty_RoundsDownToQtyStep(t *testing.T) {
	// entryPrice-markPrice=10/юнит убытка, accumulated_pnl=57 -> сырой объём 5.7.
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 57, 100, 0.5, 0)
	if closesEntirely {
		t.Fatal("expected partial close")
	}
	if qty < 5.499999 || qty > 5.500001 {
		t.Errorf("qty = %v, want 5.5 (rounded down from 5.7 by qtyStep=0.5)", qty)
	}
}
```

- [ ] **Step 2: Запустить тесты — убедиться, что падают**

Запустить: `go test ./services/api-gateway/... -run TestRescueCalcPartialCloseQty -v`
Ожидается: FAIL (`undefined: rescueCalcPartialCloseQty`).

- [ ] **Step 3: Создать `rescue_engine.go` с реализацией функции**

```go
package main

import (
	"strconv"

	"sis/pkg/trader"
)

// rescueCalcPartialCloseQty вычисляет объём мейн-позиции для снятия на этом шаге,
// профинансированный реализованным PnL хеджа (accumulatedPnl): реализованный убыток
// от закрытия этого объёма должен примерно равняться accumulatedPnl, так что трата
// накопленной хеджем прибыли на уменьшение экспозиции мейна даёт ~0 изменения
// суммарного реализованного PnL пары, но перманентно снижает риск на мейне.
//
// mainSide — "Buy" (лонг) или "Sell" (шорт). Возвращает (qty, closesEntirely):
// qty округлён вниз до qtyStep через trader.FormatQty; closesEntirely=true, когда
// расчётный объём достиг бы или превысил mainRemainingQty — в этом случае qty
// зажимается ровно в mainRemainingQty (без перелива), и вызывающий код должен
// трактовать это как полное закрытие, а не частичное.
//
// Возвращает (0, false), если финансировать нечем (accumulatedPnl<=0) или если
// мейн сейчас не в убытке по юниту (perUnitLoss<=0 — формула неприменима, обычная
// деактивация/paired-close должны обрабатывать восстановившуюся позицию отдельно).
func rescueCalcPartialCloseQty(mainSide string, mainEntryPrice, markPrice, accumulatedPnl, mainRemainingQty, qtyStep, minQty float64) (qty float64, closesEntirely bool) {
	var perUnitLoss float64
	if mainSide == "Sell" {
		perUnitLoss = markPrice - mainEntryPrice // шорт теряет, когда цена растёт
	} else {
		perUnitLoss = mainEntryPrice - markPrice // лонг теряет, когда цена падает
	}
	if perUnitLoss <= 0 || accumulatedPnl <= 0 {
		return 0, false
	}

	raw := accumulatedPnl / perUnitLoss
	if raw >= mainRemainingQty {
		return mainRemainingQty, true
	}

	formatted := trader.FormatQty(raw, qtyStep, minQty)
	rounded, _ := strconv.ParseFloat(formatted, 64)
	if rounded >= mainRemainingQty {
		return mainRemainingQty, true
	}
	return rounded, false
}
```

- [ ] **Step 4: Запустить тесты — убедиться, что проходят**

Запустить: `go test ./services/api-gateway/... -run TestRescueCalcPartialCloseQty -v`
Ожидается: PASS (все 6 тестов).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/rescue_engine.go services/api-gateway/rescue_engine_test.go
git commit -m "feat(rescue): add partial-close quantity calculation"
```

---

## Task 4: Триггеры T1/T2 — движение цены и абсолютный уровень

**Files:**
- Modify: `services/api-gateway/rescue_engine.go`
- Modify: `services/api-gateway/rescue_engine_test.go`

- [ ] **Step 1: Добавить падающие тесты**

Добавить в `services/api-gateway/rescue_engine_test.go`:
```go
// TestRescuePriceMoveTowardMainPct_Long: мейн-лонг, хедж открылся по 100, сейчас
// 105 -> движение в пользу мейна +5%.
func TestRescuePriceMoveTowardMainPct_Long(t *testing.T) {
	got := rescuePriceMoveTowardMainPct("Buy", 100, 105)
	if got < 4.999999 || got > 5.000001 {
		t.Errorf("got %v, want 5.0", got)
	}
}

// TestRescuePriceMoveTowardMainPct_Short: мейн-шорт, хедж открылся по 100, сейчас
// 95 -> движение в пользу мейна +5% (для шорта падение цены — это плюс).
func TestRescuePriceMoveTowardMainPct_Short(t *testing.T) {
	got := rescuePriceMoveTowardMainPct("Sell", 100, 95)
	if got < 4.999999 || got > 5.000001 {
		t.Errorf("got %v, want 5.0", got)
	}
}

// TestRescuePriceMoveTowardMainPct_AgainstMain: движение цены ПРОТИВ мейна должно
// давать отрицательный процент, не искусственно зажиматься в 0 — так
// rescueTriggersMet корректно сравнивает с положительным порогом и не пропускает
// ложных срабатываний.
func TestRescuePriceMoveTowardMainPct_AgainstMain(t *testing.T) {
	got := rescuePriceMoveTowardMainPct("Buy", 100, 95)
	if got > -4.999999 {
		t.Errorf("got %v, want ~-5.0 (against main)", got)
	}
}

// TestRescuePriceLevelReached_Long: мейн-лонг, уровень 61200, цена 61500 -> reached.
func TestRescuePriceLevelReached_Long(t *testing.T) {
	if !rescuePriceLevelReached("Buy", 61500, 61200) {
		t.Error("expected reached=true when markPrice above level for long main")
	}
	if rescuePriceLevelReached("Buy", 61000, 61200) {
		t.Error("expected reached=false when markPrice below level for long main")
	}
}

// TestRescuePriceLevelReached_Short: мейн-шорт, уровень 61200, цена 61000 -> reached
// (для шорта "в пользу мейна" — цена НИЖЕ уровня).
func TestRescuePriceLevelReached_Short(t *testing.T) {
	if !rescuePriceLevelReached("Sell", 61000, 61200) {
		t.Error("expected reached=true when markPrice below level for short main")
	}
	if rescuePriceLevelReached("Sell", 61500, 61200) {
		t.Error("expected reached=false when markPrice above level for short main")
	}
}
```

- [ ] **Step 2: Запустить тесты — убедиться, что падают**

Запустить: `go test ./services/api-gateway/... -run "TestRescuePriceMoveTowardMainPct|TestRescuePriceLevelReached" -v`
Ожидается: FAIL (функции не существуют).

- [ ] **Step 3: Добавить реализацию в `rescue_engine.go`**

```go
// rescuePriceMoveTowardMainPct возвращает, на сколько процентов цена сдвинулась от
// hedgeEntryAtStart (цена, по которой открылась хедж-нога — момент активации) в
// пользу мейна. Положительное значение = движение в пользу мейна, отрицательное =
// против. Возвращает 0, если точка отсчёта ещё не известна (hedge_entry_at_start
// заполняется асинхронно после фактического открытия хедж-позиции на бирже).
func rescuePriceMoveTowardMainPct(mainSide string, hedgeEntryAtStart, markPrice float64) float64 {
	if hedgeEntryAtStart <= 0 {
		return 0
	}
	if mainSide == "Sell" {
		// мейн-шорт: в его пользу — падение цены ниже точки отсчёта.
		return (hedgeEntryAtStart - markPrice) / hedgeEntryAtStart * 100
	}
	// мейн-лонг: в его пользу — рост цены выше точки отсчёта.
	return (markPrice - hedgeEntryAtStart) / hedgeEntryAtStart * 100
}

// rescuePriceLevelReached сообщает, достигла ли markPrice абсолютного уровня level
// в благоприятном для мейна направлении: для лонга — цена на уровне или выше, для
// шорта — на уровне или ниже.
func rescuePriceLevelReached(mainSide string, markPrice, level float64) bool {
	if mainSide == "Sell" {
		return markPrice <= level
	}
	return markPrice >= level
}
```

- [ ] **Step 4: Запустить тесты — убедиться, что проходят**

Запустить: `go test ./services/api-gateway/... -run "TestRescuePriceMoveTowardMainPct|TestRescuePriceLevelReached" -v`
Ожидается: PASS (все 5 тестов).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/rescue_engine.go services/api-gateway/rescue_engine_test.go
git commit -m "feat(rescue): add price-move and price-level trigger helpers (T1, T2)"
```

---

## Task 5: Комбинирование триггеров по «И» — `rescueTriggersMet`

**Files:**
- Modify: `services/api-gateway/rescue_engine.go`
- Modify: `services/api-gateway/rescue_engine_test.go`

- [ ] **Step 1: Добавить падающие тесты**

Добавить `"time"` в блок импортов `services/api-gateway/rescue_engine_test.go` (файл уже существует с `import "testing"` из Task 3 — новые тесты этой задачи используют `time.Now()`/`time.Time`):
```go
import (
	"testing"
	"time"
)
```

Добавить в `services/api-gateway/rescue_engine_test.go`:
```go
// TestRescueTriggersMet_AllDisabled: ни один триггер не включён (все указатели
// nil) -> "И" по пустому множеству включённых триггеров считается истинным. Это
// осознанное, задокументированное поведение: включение RescuePartialCloseEnabled
// без единого настроенного триггера означает "снимать на каждом тике, ограничено
// только кулдауном" — не баг, форма используется намеренно.
func TestRescueTriggersMet_AllDisabled(t *testing.T) {
	cfg := botCfgJSON{}
	if !rescueTriggersMet(cfg, "Buy", 100, 105, 10, false) {
		t.Error("expected true when no triggers are configured (vacuous AND)")
	}
}

// TestRescueTriggersMet_PriceMoveTrigger: только T1 включён.
func TestRescueTriggersMet_PriceMoveTrigger(t *testing.T) {
	threshold := 3.0
	cfg := botCfgJSON{RescueTriggerPriceMovePct: &threshold}
	// markPrice=104 при hedgeEntryAtStart=100 -> движение +4% >= порога 3% -> true.
	if !rescueTriggersMet(cfg, "Buy", 100, 104, 10, false) {
		t.Error("expected true: price moved 4%% >= 3%% threshold")
	}
	// markPrice=101 -> движение +1% < порога 3% -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 101, 10, false) {
		t.Error("expected false: price moved only 1%% < 3%% threshold")
	}
}

// TestRescueTriggersMet_AccumulatedMinTrigger: только T3 включён.
func TestRescueTriggersMet_AccumulatedMinTrigger(t *testing.T) {
	minAccum := 20.0
	cfg := botCfgJSON{RescueTriggerAccumulatedMinUsdt: &minAccum}
	if !rescueTriggersMet(cfg, "Buy", 100, 105, 25, false) {
		t.Error("expected true: accumulated 25 >= min 20")
	}
	if rescueTriggersMet(cfg, "Buy", 100, 105, 15, false) {
		t.Error("expected false: accumulated 15 < min 20")
	}
}

// TestRescueTriggersMet_SignalTrigger: только T4 включён — зависит целиком от
// переданного currentSignalFired (оценка самого сигнала — забота вызывающего кода,
// см. Task 8).
func TestRescueTriggersMet_SignalTrigger(t *testing.T) {
	cfg := botCfgJSON{}
	cfg.RescueTriggerSignal = &struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	}{Name: "st-flip"}
	if !rescueTriggersMet(cfg, "Buy", 100, 105, 10, true) {
		t.Error("expected true when signal fired")
	}
	if rescueTriggersMet(cfg, "Buy", 100, 105, 10, false) {
		t.Error("expected false when signal did not fire")
	}
}

// TestRescueTriggersMet_MultipleTriggers_AllMustPass: T1+T3 оба включены — «И»:
// один провален -> итог false, даже если другой пройден.
func TestRescueTriggersMet_MultipleTriggers_AllMustPass(t *testing.T) {
	movePct := 3.0
	minAccum := 20.0
	cfg := botCfgJSON{
		RescueTriggerPriceMovePct:       &movePct,
		RescueTriggerAccumulatedMinUsdt: &minAccum,
	}
	// Оба выполнены -> true.
	if !rescueTriggersMet(cfg, "Buy", 100, 104, 25, false) {
		t.Error("expected true: both T1 and T3 satisfied")
	}
	// T1 выполнен, T3 — нет (accumulated=15<20) -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 104, 15, false) {
		t.Error("expected false: T3 not satisfied even though T1 is")
	}
	// T3 выполнен, T1 — нет (движение всего 1%%<3%%) -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 101, 25, false) {
		t.Error("expected false: T1 not satisfied even though T3 is")
	}
}

// TestRescueCooldownElapsed_NoPreviousStep: lastPartialCloseAt=nil (шага ещё не
// было) -> кулдаун всегда пройден.
func TestRescueCooldownElapsed_NoPreviousStep(t *testing.T) {
	if !rescueCooldownElapsed(nil, 300, time.Now()) {
		t.Error("expected true when there is no previous step yet")
	}
}

// TestRescueCooldownElapsed_ZeroInterval: MinIntervalSec<=0 -> кулдаун отключён,
// всегда пройден вне зависимости от lastPartialCloseAt.
func TestRescueCooldownElapsed_ZeroInterval(t *testing.T) {
	now := time.Now()
	if !rescueCooldownElapsed(&now, 0, now) {
		t.Error("expected true when MinIntervalSec<=0 (cooldown disabled)")
	}
}

// TestRescueCooldownElapsed_WithinCooldown: последний шаг был 100с назад,
// MinIntervalSec=300 -> кулдаун ещё не прошёл.
func TestRescueCooldownElapsed_WithinCooldown(t *testing.T) {
	now := time.Now()
	last := now.Add(-100 * time.Second)
	if rescueCooldownElapsed(&last, 300, now) {
		t.Error("expected false: only 100s elapsed of 300s cooldown")
	}
}

// TestRescueCooldownElapsed_AfterCooldown: последний шаг был 400с назад,
// MinIntervalSec=300 -> кулдаун прошёл.
func TestRescueCooldownElapsed_AfterCooldown(t *testing.T) {
	now := time.Now()
	last := now.Add(-400 * time.Second)
	if !rescueCooldownElapsed(&last, 300, now) {
		t.Error("expected true: 400s elapsed >= 300s cooldown")
	}
}
```

- [ ] **Step 2: Запустить тесты — убедиться, что падают**

Запустить: `go test ./services/api-gateway/... -run TestRescueTriggersMet -v`
Ожидается: FAIL (`undefined: rescueTriggersMet`).

- [ ] **Step 3: Добавить реализацию в `rescue_engine.go`**

```go
// rescueTriggersMet оценивает все ВКЛЮЧЁННЫЕ триггеры (T1-T4) и возвращает true,
// только если истинны все включённые (логическое «И»). Триггер, который не
// настроен (nil-указатель / нулевое значение поля), пропускается — не участвует в
// условии. Если ни один триггер не включён, результат истинен (пустое «И») — это
// осознанное поведение: включение RescuePartialCloseEnabled без единого триггера
// означает "срабатывать на каждом тике, ограничено только кулдауном".
//
// currentSignalFired сообщает, сработал ли на этом тике сигнал, настроенный в
// cfg.RescueTriggerSignal, для символа бота — вызывающий код отвечает за оценку
// самого сигнала (через существующий pkg/signal, как ActivationSignals), поскольку
// это требует доступа к движку сигналов/рыночным данным, которых у этой чистой
// функции нет.
func rescueTriggersMet(cfg botCfgJSON, mainSide string, hedgeEntryAtStart, markPrice, accumulatedPnl float64, currentSignalFired bool) bool {
	if cfg.RescueTriggerPriceMovePct != nil {
		moved := rescuePriceMoveTowardMainPct(mainSide, hedgeEntryAtStart, markPrice)
		if moved < *cfg.RescueTriggerPriceMovePct {
			return false
		}
	}
	if cfg.RescueTriggerPriceLevel != nil {
		if !rescuePriceLevelReached(mainSide, markPrice, *cfg.RescueTriggerPriceLevel) {
			return false
		}
	}
	if cfg.RescueTriggerAccumulatedMinUsdt != nil {
		if accumulatedPnl < *cfg.RescueTriggerAccumulatedMinUsdt {
			return false
		}
	}
	if cfg.RescueTriggerSignal != nil {
		if !currentSignalFired {
			return false
		}
	}
	return true
}

// rescueCooldownElapsed сообщает, прошёл ли кулдаун между шагами частичного
// снятия. lastPartialCloseAt=nil (шага ещё не было) или minIntervalSec<=0
// (кулдаун отключён в конфиге) — всегда true.
func rescueCooldownElapsed(lastPartialCloseAt *time.Time, minIntervalSec int, now time.Time) bool {
	if lastPartialCloseAt == nil || minIntervalSec <= 0 {
		return true
	}
	return now.Sub(*lastPartialCloseAt) >= time.Duration(minIntervalSec)*time.Second
}
```

- [ ] **Step 4: Запустить тесты — убедиться, что проходят**

Запустить: `go test ./services/api-gateway/... -run "TestRescueTriggersMet|TestRescueCooldownElapsed" -v`
Ожидается: PASS (5 тестов triggers + 4 теста cooldown = 9 тестов).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/rescue_engine.go services/api-gateway/rescue_engine_test.go
git commit -m "feat(rescue): combine T1-T4 triggers with AND logic, add cooldown check"
```

---

## Task 6: Построение reduce-only ордера — `rescuePartialCloseRequest`

**Files:**
- Modify: `services/api-gateway/rescue_engine.go`
- Modify: `services/api-gateway/rescue_engine_test.go`

- [ ] **Step 1: Добавить падающие тесты**

Добавить в `services/api-gateway/rescue_engine_test.go`:
```go
// TestRescuePartialCloseRequest_Long: мейн-лонг -> закрывающий ордер должен быть
// Sell, reduce-only, тот же формат, что matrixLegCloseRequest.
func TestRescuePartialCloseRequest_Long(t *testing.T) {
	req := rescuePartialCloseRequest("Buy", "BTCUSDT", "linear", 1, 0.5, 0.001, 0)
	if req.Side != "Sell" || req.PositionIdx != 1 || !req.ReduceOnly ||
		req.OrderType != "Market" || req.Symbol != "BTCUSDT" || req.Category != "linear" {
		t.Errorf("long partial-close request wrong: %+v", req)
	}
	if req.Qty != "0.5" {
		t.Errorf("Qty = %q, want %q", req.Qty, "0.5")
	}
}

// TestRescuePartialCloseRequest_Short: мейн-шорт -> закрывающий ордер Buy.
func TestRescuePartialCloseRequest_Short(t *testing.T) {
	req := rescuePartialCloseRequest("Sell", "BTCUSDT", "linear", 2, 0.5, 0.001, 0)
	if req.Side != "Buy" || req.PositionIdx != 2 || !req.ReduceOnly {
		t.Errorf("short partial-close request wrong: %+v", req)
	}
}
```

- [ ] **Step 2: Запустить тесты — убедиться, что падают**

Запустить: `go test ./services/api-gateway/... -run TestRescuePartialCloseRequest -v`
Ожидается: FAIL (`undefined: rescuePartialCloseRequest`).

- [ ] **Step 3: Добавить реализацию в `rescue_engine.go`**

```go
// rescuePartialCloseRequest строит reduce-only рыночный ордер, снимающий qty с
// мейн-позиции. side — ПРОТИВОПОЛОЖНАЯ стороне мейна (гасит позицию, а не
// удваивает), та же конвенция, что в matrixLegCloseRequest (matrix_engine.go).
func rescuePartialCloseRequest(mainSide, symbol, category string, posIdx int, qty, qtyStep, minQty float64) trader.OrderRequest {
	closeSide := "Sell"
	if mainSide == "Sell" {
		closeSide = "Buy"
	}
	return trader.OrderRequest{
		Category:    category,
		Symbol:      symbol,
		Side:        closeSide,
		OrderType:   "Market",
		Qty:         trader.FormatQty(qty, qtyStep, minQty),
		ReduceOnly:  true,
		PositionIdx: posIdx,
	}
}
```

- [ ] **Step 4: Запустить тесты — убедиться, что проходят**

Запустить: `go test ./services/api-gateway/... -run TestRescuePartialCloseRequest -v`
Ожидается: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/rescue_engine.go services/api-gateway/rescue_engine_test.go
git commit -m "feat(rescue): add reduce-only partial-close order builder"
```

---

## Task 7: Запись состояния после исполнения — `recordRescuePartialClose`

**Files:**
- Modify: `services/api-gateway/rescue_engine.go`

Эта функция обновляет БД (`hedge_sessions`) — требует реальной Postgres-транзакции, а не чистая функция. Отдельного юнит-теста с моком БД в этом плане нет (в кодовой базе нет установленного паттерна мокирования `*pgxpool.Pool` — только `//go:build integration`-тесты с реальной БД для DB-зависимого кода, что выходит за рамки этой задачи); корректность проверяется вручную на dev-БД (Step 3) и всем объёмом регрессионных тестов пакета в Task 9.

- [ ] **Step 1: Добавить функцию в `rescue_engine.go`**

```go
// recordRescuePartialClose фиксирует результат одного шага частичного снятия:
// увеличивает счётчики main_reduced_coin/main_reduced_usdt и обновляет
// last_partial_close_at (для кулдауна следующего шага). Матчится по main или
// hedge strategy id — та же конвенция, что AccumulateHedgeSessionPnl
// (pkg/strategy/trade_recorder.go), которой снятие накопленного PnL хеджа
// пользуется отдельно (см. Task 8).
func (s *Server) recordRescuePartialClose(ctx context.Context, stratID string, coinDelta, usdtDelta float64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE hedge_sessions
		 SET main_reduced_coin = main_reduced_coin + $1,
		     main_reduced_usdt = main_reduced_usdt + $2,
		     last_partial_close_at = NOW()
		 WHERE (main_strategy_id = $3 OR hedge_strategy_id = $3) AND ended_at IS NULL`,
		coinDelta, usdtDelta, stratID)
	return err
}
```

- [ ] **Step 2: Проверить, что пакет компилируется**

Запустить: `go build ./services/api-gateway/...`
Ожидается: успешная сборка без ошибок (на этом этапе функция ещё нигде не вызывается, кроме компиляции — это нормально для Go, неиспользуемые методы не вызывают ошибку сборки в отличие от неиспользуемых локальных переменных).

- [ ] **Step 3: Ручная проверка на dev-БД**

После Task 1 (миграция применена) и наличия хотя бы одной строки в `hedge_sessions` — выполнить вручную временный вызов (например, через `go run` небольшого scratch-файла или psql напрямую с теми же параметрами) эквивалентного SQL:
```sql
UPDATE hedge_sessions
SET main_reduced_coin = main_reduced_coin + 1.5,
    main_reduced_usdt = main_reduced_usdt + 150,
    last_partial_close_at = NOW()
WHERE hedge_strategy_id = '<любой существующий id из hedge_sessions>' AND ended_at IS NULL;

SELECT main_reduced_coin, main_reduced_usdt, last_partial_close_at FROM hedge_sessions WHERE hedge_strategy_id = '<тот же id>';
```
Ожидается: значения корректно увеличились, `last_partial_close_at` не NULL.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/rescue_engine.go
git commit -m "feat(rescue): add DB helper to record partial-close state"
```

---

## Task 8: Интеграция — `processRescueBot`, `checkRescuePartialClose`, диспетчер

**Files:**
- Modify: `services/api-gateway/hedge_engine.go`
- Modify: `services/api-gateway/rescue_engine.go`

Это интеграционная обвязка (БД + биржа + существующий движок сигналов) — без нового юнит-теста в этой задаче по тем же причинам, что в Task 7; проверяется сборкой, `go vet` и полным прогоном регрессии в Task 9, как того требует CLAUDE.md для доработок, затрагивающих основные механики (`hedge_engine.go`).

- [ ] **Step 1: Добавить `'rescue'` в SQL-фильтр и диспетчер `hedge_engine.go`**

Найти (текущая строка ~82):
```go
		  AND strategy_config->>'bot_kind' IN ('hedge', 'matrix')`)
```
Заменить на:
```go
		  AND strategy_config->>'bot_kind' IN ('hedge', 'matrix', 'rescue')`)
```

Найти switch-диспетчер (текущие строки ~118-123):
```go
			switch cfg.BotKind {
			case "hedge":
				s.processHedgeBot(ctx, b.id, b.ownerID, b.accountID, b.whitelist, b.blacklist, cfg, newWatches, newPairedWatches)
			case "matrix":
				s.processMatrixBot(ctx, b.id, b.ownerID, b.accountID, b.whitelist, b.blacklist, cfg, newPairedWatches)
			}
```
Заменить на:
```go
			switch cfg.BotKind {
			case "hedge":
				s.processHedgeBot(ctx, b.id, b.ownerID, b.accountID, b.whitelist, b.blacklist, cfg, newWatches, newPairedWatches)
			case "matrix":
				s.processMatrixBot(ctx, b.id, b.ownerID, b.accountID, b.whitelist, b.blacklist, cfg, newPairedWatches)
			case "rescue":
				s.processRescueBot(ctx, b.id, b.ownerID, b.accountID, b.whitelist, b.blacklist, cfg, newWatches, newPairedWatches)
			}
```

- [ ] **Step 2: Добавить `processRescueBot` и `checkRescuePartialClose` в `rescue_engine.go`**

Добавить необходимые импорты в начало файла (`context`, `fmt`, `time`, `sis/pkg/signal`, `sis/pkg/trader` — часть уже добавлена в Task 3/6, добавить недостающие):
```go
import (
	"context"
	"fmt"
	"strconv"
	"time"

	"sis/pkg/signal"
	"sis/pkg/trader"
)
```

```go
// processRescueBot обрабатывает один RescueBot-бот за тик: те же проверки
// активации/деактивации/paired-close, что hedge-бот (checkHedgeDeactivation,
// checkHedgeActivation, buildPairedCloseWatches — проверенно kind-агностичны, не
// содержат ветвлений по cfg.BotKind, см. docs/superpowers/specs/2026-07-26-
// rescuebot-design.md), плюс дополнительный шаг частичного снятия мейна.
func (s *Server) processRescueBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, watches map[string]hedgeWatchEntry, pairedWatches map[string]pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("RescueBot: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("RescueBot: ошибка получения позиций: %v", err), "error", "system")
		return
	}

	posMap, badPositions := buildHedgePosMap(rawPositions)
	for _, p := range badPositions {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("RescueBot: позиция %s %s (size=%s) отфильтрована — невалидный avgPrice=%q или markPrice=%q",
				p.Symbol, p.Side, p.Size, p.EntryPrice, p.MarkPrice),
			"warn", "system")
	}

	s.checkHedgeDeactivation(ctx, botID, accountID, cfg, posMap)
	s.checkHedgeActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap, watches)
	s.buildPairedCloseWatches(ctx, botID, accountID, "rescue", cfg, posMap, pairedWatches)

	if cfg.RescuePartialCloseEnabled {
		s.checkRescuePartialClose(ctx, botID, cfg, creds, posMap)
	}
}

// rescueSessionRow — одна активная сессия хеджа этого бота, читаемая для оценки
// частичного снятия. Та же join-форма, что buildPairedCloseWatches, плюс поля,
// нужные только RescueBot (hedge_entry_at_start, last_partial_close_at,
// main_reduced_coin).
type rescueSessionRow struct {
	hedgeStrategyID    string
	accumulatedPnl     float64
	hedgeEntryAtStart  *float64
	lastPartialCloseAt *time.Time
	symbol             string
	mainDir            string
}

// checkRescuePartialClose оценивает и, при срабатывании всех включённых триггеров,
// исполняет один шаг частичного снятия мейна для каждой активной rescue-сессии
// этого бота. Работает независимо от paired-close/обычной деактивации — они
// продолжают проверяться в processRescueBot на каждом тике вне зависимости от
// состояния этой функции (см. дизайн, раздел «Архитектура»).
func (s *Server) checkRescuePartialClose(ctx context.Context, botID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) {
	rows, err := s.pool.Query(ctx, `
		SELECT hs.hedge_strategy_id, hs.accumulated_pnl, hs.hedge_entry_at_start,
		       hs.last_partial_close_at, ms.symbol, ms.direction
		FROM hedge_sessions hs
		JOIN strategies ms ON ms.id = hs.main_strategy_id
		JOIN strategies hst ON hst.id = hs.hedge_strategy_id
		WHERE hs.bot_id = $1 AND hs.ended_at IS NULL
		  AND ms.status IN ('active','finishing') AND hst.status IN ('active','finishing')`,
		botID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("RescueBot: ошибка запроса сессий: %v", err), "error", "rescue")
		return
	}
	var sessions []rescueSessionRow
	for rows.Next() {
		var rs rescueSessionRow
		if rows.Scan(&rs.hedgeStrategyID, &rs.accumulatedPnl, &rs.hedgeEntryAtStart,
			&rs.lastPartialCloseAt, &rs.symbol, &rs.mainDir) == nil {
			sessions = append(sessions, rs)
		}
	}
	rows.Close()

	for _, rs := range sessions {
		if !rescueCooldownElapsed(rs.lastPartialCloseAt, cfg.RescueMinIntervalSec, time.Now()) {
			continue
		}

		bySymbol, ok := posMap[rs.symbol]
		if !ok {
			continue
		}
		mainSide := hedgeDirToSide(rs.mainDir)
		mainPos, hasMain := bySymbol[mainSide]
		if !hasMain || mainPos.Size <= 0 {
			continue
		}

		var hedgeEntryAtStart float64
		if rs.hedgeEntryAtStart != nil {
			hedgeEntryAtStart = *rs.hedgeEntryAtStart
		}

		signalFired := false
		if cfg.RescueTriggerSignal != nil {
			signalFired = s.rescueEvaluateTriggerSignal(rs.symbol, mainSide, *cfg.RescueTriggerSignal)
		}

		if !rescueTriggersMet(cfg, mainSide, hedgeEntryAtStart, mainPos.MarkPrice, rs.accumulatedPnl, signalFired) {
			continue
		}

		instr, err := trader.GetPublicInstrumentInfo(ctx, cfg.Category, rs.symbol)
		if err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("RescueBot: %s — ошибка получения параметров инструмента: %v", rs.symbol, err),
				"error", "rescue")
			continue
		}

		qty, _ := rescueCalcPartialCloseQty(mainSide, mainPos.EntryPrice, mainPos.MarkPrice, rs.accumulatedPnl, mainPos.Size, instr.QtyStep, instr.MinQty)
		if qty <= 0 {
			continue
		}

		posIdx := 1
		if mainSide == "Sell" {
			posIdx = 2
		}
		req := rescuePartialCloseRequest(mainSide, rs.symbol, cfg.Category, posIdx, qty, instr.QtyStep, instr.MinQty)

		if _, err := trader.PlaceOrder(ctx, creds, req); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("RescueBot: %s — ошибка частичного закрытия мейна (%s qty=%s): %v",
					rs.symbol, req.Side, req.Qty, err),
				"error", "rescue")
			continue
		}

		realizedLoss := qty * (mainPos.EntryPrice - mainPos.MarkPrice)
		if mainSide == "Sell" {
			realizedLoss = qty * (mainPos.MarkPrice - mainPos.EntryPrice)
		}
		strategy.AccumulateHedgeSessionPnl(ctx, s.pool, rs.hedgeStrategyID, -realizedLoss)

		if err := s.recordRescuePartialClose(ctx, rs.hedgeStrategyID, qty, qty*mainPos.MarkPrice); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("RescueBot: %s — ордер исполнен, но не удалось записать состояние: %v", rs.symbol, err),
				"error", "rescue")
		}

		s.logBotEvent(ctx, botID,
			fmt.Sprintf("RescueBot: %s — частично снято %s (%.8f) с мейна, накоплено хеджа уменьшено на %.4g",
				rs.symbol, req.Qty, qty, realizedLoss),
			"info", "rescue")
	}
}

// rescueEvaluateTriggerSignal оценивает сигнал T4 тем же способом, что
// checkHedgeActivation оценивает ActivationSignals (сигнал должен подтверждать
// направление, благоприятное для мейна — если мейн-лонг, ждём Buy/бычий сигнал;
// если мейн-шорт, ждём Sell).
func (s *Server) rescueEvaluateTriggerSignal(symbol, mainSide string, trig struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
}) bool {
	sc := signal.Config{Name: trig.Name, Params: trig.Params}
	if _, err := signal.Build(sc); err != nil {
		return false
	}
	interval := "15"
	if v, ok := trig.Params["tf"].(string); ok && v != "" {
		interval = v
	}
	state := s.signalEngine.ComputeStateForce(symbol, interval, []signal.Config{sc})
	want := signal.Buy
	if mainSide == "Sell" {
		want = signal.Sell
	}
	return state == want
}
```

**Важно про тип поля `RescueTriggerSignal`:** в Task 2 оно объявлено как анонимная inline-структура (`*struct{ Name string; Params map[string]interface{} }`). Функция `rescueEvaluateTriggerSignal` выше принимает параметр того же анонимного типа по значению — Go позволяет так передавать анонимные структуры, если поля совпадают дословно. Если компилятор укажет на несовпадение типов (анонимные структуры в Go должны быть структурно идентичны, включая теги полей) — самый надёжный фикс: вынести именованный тип `type rescueSignalTrigger struct { Name string \`json:"name"\`; Params map[string]interface{} \`json:"params"\` }` в `rescue_engine.go`, использовать его и в поле `botCfgJSON.RescueTriggerSignal *rescueSignalTrigger` (поправить Task 2 соответствующим образом, включая round-trip тест), и в сигнатуре `rescueEvaluateTriggerSignal`. Это чище и однозначно совместимо — при реализации Task 8 сразу используйте именованный тип, не анонимный, чтобы не столкнуться с этим на месте.

- [ ] **Step 3: Добавить импорт `sis/pkg/strategy` в `rescue_engine.go`** (для `strategy.AccumulateHedgeSessionPnl`)

```go
import (
	...
	"sis/pkg/strategy"
)
```

- [ ] **Step 4: Собрать и проверить статически**

Запустить: `go build ./services/api-gateway/...`
Ожидается: успешная сборка. Если есть ошибки типов (см. примечание про анонимную структуру в Step 2) — исправить по описанному пути, пересобрать.

Запустить: `go vet ./services/api-gateway/...`
Ожидается: без предупреждений.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/hedge_engine.go services/api-gateway/rescue_engine.go
git commit -m "feat(rescue): wire processRescueBot into the hedge/matrix engine tick loop"
```

---

## Task 9: Регрессия — прогон существующих механик

По правилам проекта (CLAUDE.md, «Тест-ревью после реализации») — обязательный явный прогон и отчёт, не просто «тесты прошли».

- [ ] **Step 1: Полный прогон пакета**

Запустить: `go test ./services/api-gateway/... -v`

- [ ] **Step 2: Явно проверить соседние механики, которые может задеть новый код**

Прогнать отдельно существующие тесты hedge/matrix/paired-close механик (полный список актуальных имён на момент написания плана; если после Task 1-8 в этих файлах появились новые тесты — регэксп `-run` ниже всё равно захватит их по префиксу):
```bash
go test ./services/api-gateway/... -run "TestBuildPairedCloseWatches_OnePerCompletePair|TestBuildPairedCloseWatches_SkipsIncompletePair|TestGetHedgeSession_IncludesCloseTypeAndThreshold|TestGetHedgeSession_IncludesRealizedPnl|TestGetHedgeSession_ReadsAccumulatedPnl|TestGetHedgeSession_ResetsOnlyOnPairedClose|TestMatrixActivationSignalOK_EmptyAlwaysPasses|TestMatrixBatchCheckActivation_EmptySignalsAllTrueNoNetwork|TestMatrixLegCloseRequest|TestMatrixRepairCandidates_FindsOneSidedSymbols|TestMatrixZombie_|TestMeetsPairedCloseCriteria_|TestPairedCloseCurrentValue_|TestPairedCloseInFlight_|TestPairedCloseSemaphore_CapsConcurrencyPerAccount|TestPairedCloseTargetPrice_|TestRecomputeAndPushPairedClose_UsesLivePriceForCurrentAndPct" -v
```
Ожидается: все PASS, без изменений в поведении (эти тесты покрывают ровно те функции — `buildPairedCloseWatches`, `meetsPairedCloseCriteria`, `pairedCloseTargetPrice`, `GetHedgeSession` — которые Task 8 переиспользует без изменений их кода).

- [ ] **Step 3: Прогнать весь модуль целиком**

Запустить: `go build ./... && go vet ./...`
Ожидается: чистая сборка всего модуля (не только api-gateway) — новый файл `rescue_engine.go` не должен ломать соседние пакеты.

- [ ] **Step 4: Доложить результат пользователю**

По правилам CLAUDE.md — явно перечислить: какие тесты запускались (команды выше), что подтвердилось как неизменное (hedge/matrix/paired-close тесты остались зелёными), что нового покрыто (Task 2-6 юнит-тесты), что проверено только вручную и почему (Task 7-8 — DB/биржа-зависимый код, нет стенда для мокирования в этом пакете).

---

## Что дальше

Эта Фаза 1 (бэкенд) даёт полностью рабочую и протестированную (на уровне чистых функций) логику RescueBot, управляемую напрямую через БД/конфиг бота — без UI. Не покрыто этим планом:

- **Фаза 2 (фронтенд):** `RescueBotForm.tsx` (форк `HedgeBotForm.tsx` с вкладкой «Закрытие»), `BOT_KIND_META`/`KIND_ICONS`, интеграция в `BotForm.tsx`/`MyBotCard.tsx` — отдельный план после проверки Фазы 1 в работе.
- **WS-трансляция новых счётчиков** (`main_reduced_coin`/`main_reduced_usdt`) — сейчас доступны только через прямой запрос к `hedge_sessions`; решение «добавить в `pairedCloseMsg`/`s.broadcast` или в `GetHedgeSession` REST-ответ» — на усмотрение при работе над Фазой 2, когда появится реальный UI-потребитель.
