# Объединение одиночных стратегий в пару привязкой к боту — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Пользователь перетаскивает одиночную карточку стратегии на противоположную и через окно «Привязать к боту» объединяет их в bot-managed пару (конфиг бота, `bot_id`, роли; далее пара сама собирается в `HedgePairCard`).

**Architecture:** Бэкенд: новый столбец `origin_bot_id` (подсветка источника); общий хелпер применения конфига бота к строке стратегии (вынесен из reuse-ветки `createBotStrategy`); bind-эндпоинт (валидация → накат конфига → `bot_id`/`hedged_strategy_id` → `Notify`). Фронт: DnD на одиночных карточках + окно привязки. Группировка не меняется — bind проставляет поля, которые уже используют Методы 1/2/3.

**Tech Stack:** Go (services/api-gateway, pgx), React/TypeScript, интеграционные тесты `//go:build integration` против локальной БД (:6432).

**Контекст для исполнителя:**
- `createBotStrategy` (`services/api-gateway/bot_engine.go:906`) — единый вход создания стратегий. Внутри: (a) вычисление конфиг-значений (defaults + per-symbol minOrder, `scJSON`, `stepsParam`, `matrixLevelsParam` и т.д., строки ~907-1015); (b) **reuse-ветка** — `UPDATE strategies SET ...` по самой свежей мёртвой `stopped`-строке слота (строки ~1029-1080); (c) fallback `INSERT` (строки ~1051 внутри... фактически INSERT ниже reuse). Обе ветки пишут ОДИН набор конфиг-колонок.
- `botCfgJSON` (`bot_engine.go:771`) — структура strategy_config; парсится `json.Unmarshal(stratCfgBytes, &cfg)`.
- detach: `POST /strategies/{id}/detach` → `DetachFromBot` (`strategy_handler.go`), кейс `leave` ставит `bot_id=NULL`, статус active (строки ~1200+). Роуты в `main.go` (`r.Post("/strategies/{id}/detach", ...)` — строка 232).
- Реюз-`UPDATE` НЕ трогает `owner_id/account_id/bot_id/symbol/direction` (identity) — он их сохраняет; для bind нам, наоборот, надо выставить `bot_id`.
- Фронт: группировка `renderItems` (`TerminalPage.tsx:952`, Методы 1/2/3 — пары hedge/matrix). Одиночные рендерятся `StrategyCard` в `renderItems.map` (`TerminalPage.tsx:1114`, ветка `item.type === 'single'`, `<div key={s.id}>…<StrategyCard/>`). `matrixBotIds` (:925), `hedgeInfoMap`.
- `Strategy` тип: `frontend/src/types.ts` (поля `id, symbol, direction, account_id, bot_id, status, ...`).
- Хелперы тестов: `newTestServer`, `createWHUser`, `createTestAccount`, `createZombieBot` (в `*_test.go`, тег `integration`).

---

### Task 1: Столбец `origin_bot_id` и его заполнение

**Files:**
- Create: `migrations/081_strategy_origin_bot.sql`
- Modify: `services/api-gateway/bot_engine.go` (INSERT и reuse-UPDATE в `createBotStrategy`)
- Test: `services/api-gateway/origin_bot_test.go`

- [ ] **Step 1: Миграция**

Создать `migrations/081_strategy_origin_bot.sql`:
```sql
-- 081_strategy_origin_bot.sql
-- Persistent reference to the bot that originally created a strategy. Unlike bot_id
-- (nulled on detach), origin_bot_id is set once at creation and never cleared, so the
-- "Привязать к боту" dialog can highlight the source bot even after detach.
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS origin_bot_id UUID;
```

- [ ] **Step 2: Падающий тест**

Создать `services/api-gateway/origin_bot_test.go`:
```go
//go:build integration

package main

import (
	"context"
	"testing"
)

// TestCreateBotStrategy_SetsOriginBotId: createBotStrategy пишет origin_bot_id=bot_id,
// и detach (leave) его НЕ обнуляет.
func TestCreateBotStrategy_SetsOriginBotId(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "origin")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "origin")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1 OR origin_bot_id=$1", botID) })

	cfg := botCfgJSON{StrategyType: "matrix", GridSizeUSDT: 20, HedgeMode: true}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	id, err := s.createBotStrategy(ctx, b, cfg, "ORGUSDT", "long", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy: %v", err)
	}

	var origin *string
	s.pool.QueryRow(ctx, `SELECT origin_bot_id::text FROM strategies WHERE id=$1`, id).Scan(&origin)
	if origin == nil || *origin != botID {
		t.Errorf("origin_bot_id=%v, want %s", origin, botID)
	}

	// Simulate detach (leave): bot_id -> NULL. origin must survive.
	s.pool.Exec(ctx, `UPDATE strategies SET bot_id=NULL WHERE id=$1`, id)
	s.pool.QueryRow(ctx, `SELECT origin_bot_id::text FROM strategies WHERE id=$1`, id).Scan(&origin)
	if origin == nil || *origin != botID {
		t.Errorf("after detach origin_bot_id=%v, want %s (must not be cleared)", origin, botID)
	}
}
```

- [ ] **Step 3: Запустить — FAIL**

Run: `docker exec -e PGPASSWORD=sis_secret -i sis-timescaledb-1 psql -U sis -d sis < migrations/081_strategy_origin_bot.sql` (применить миграцию к локальной БД), затем
`go test -tags=integration ./services/api-gateway/ -run TestCreateBotStrategy_SetsOriginBotId -count=1`
Expected: FAIL — `origin_bot_id` пустой (createBotStrategy его не пишет).

- [ ] **Step 4: Писать origin_bot_id в INSERT**

В `createBotStrategy` INSERT (`bot_engine.go`, блок `INSERT INTO strategies (...)`): добавить колонку `origin_bot_id` в список и значение `b.id`. Т.е. в список колонок после `bot_id` добавить `origin_bot_id`, а в `VALUES` — соответствующий `$N` со значением `b.id` (тем же, что уже идёт в `bot_id`; можно повторно передать `b.id` новым параметром). Пример: в списке колонок `... owner_id, account_id, bot_id, origin_bot_id, symbol, ...`; добавить `b.id` в аргументы на соответствующей позиции.

- [ ] **Step 5: Писать origin_bot_id в reuse-UPDATE**

В reuse-ветке `createBotStrategy` (`UPDATE strategies SET ... WHERE id=$1 AND status='stopped'`) добавить в SET: `origin_bot_id = COALESCE(origin_bot_id, $2b)` — где значение `b.id`. (COALESCE, чтобы не перетирать уже проставленный origin.) Проще: добавить `origin_bot_id = COALESCE(strategies.origin_bot_id, '<param>'::uuid)` с `b.id`. Убедиться, что номера `$N` не сдвинули существующие (добавляй новый параметр в конец списка аргументов и ссылайся на него новым номером).

- [ ] **Step 6: Запустить — PASS**

Run: `go build ./... && go test -tags=integration ./services/api-gateway/ -run TestCreateBotStrategy_SetsOriginBotId -count=1`
Expected: `ok`.

- [ ] **Step 7: Commit**

```
git add migrations/081_strategy_origin_bot.sql services/api-gateway/bot_engine.go services/api-gateway/origin_bot_test.go
git commit -m "feat(strategies): origin_bot_id — источник стратегии, переживает detach"
```

---

### Task 2: Хелпер `applyBotConfigToStrategy` (вынести из reuse-ветки)

**Files:**
- Modify: `services/api-gateway/bot_engine.go` (вынести общий код)

Цель: чтобы bind-эндпоинт (Task 3) мог применить конфиг бота к существующей строке тем же кодом, что reuse-ветка. Вынести перезапись конфиг-колонок в метод.

- [ ] **Step 1: Определить структуру и хелпер вычисления**

В `bot_engine.go` добавить тип, собирающий все вычисленные конфиг-значения, и функцию, которая их вычисляет из `cfg`/`sym` (перенести существующий блок вычислений из начала `createBotStrategy`):
```go
// botStrategyCols holds the computed strategy config columns shared by INSERT / reuse /
// bind — a single source of truth so all three write identical config.
type botStrategyCols struct {
	category, tpMode, slType, marginType, stratType, entryType string
	gridLevels, gridActive, leverage                           int
	gridStep, gridSize, tpPct, slPct, safeZonePct              float64
	scJSON                                                     string
	stepsParam, matrixLevelsParam, matrixEntryParam            *string
	trailingEnabled, hedgeMode, sizeAsMain, signalFilter       bool
	protectedBuild, matrixRebuildOnSL, matrixRebuildFromEntry  bool
	relativeSlots                                              bool
	trailingActPct, trailingCallPct                            *float64
	maxCycles                                                  int
}

// computeBotStrategyCols builds the config columns from a bot cfg for a given symbol,
// applying the same defaults + per-symbol min-order logic createBotStrategy uses.
func (s *Server) computeBotStrategyCols(ctx context.Context, cfg botCfgJSON, sym string, leverageOverride int) botStrategyCols {
	// ... MOVE the existing computation block from createBotStrategy here (defaults,
	// GetPublicInstrumentInfo min-order raise, scJSON, stepsParam, trailing, matrix params,
	// signalFilter=false) and return them in a botStrategyCols. ...
}
```
Перенести существующие вычисления (строки ~907-1020 `createBotStrategy`) в `computeBotStrategyCols`. В `createBotStrategy` заменить их на `cols := s.computeBotStrategyCols(ctx, cfg, sym, leverageOverride)` и далее ссылаться на `cols.*`.

- [ ] **Step 2: Хелпер применения к существующей строке**

Добавить метод, делающий `UPDATE` конфиг-колонок конкретной строки (тело — как reuse-UPDATE, но БЕЗ guard по статусу и с выставлением `bot_id`/`origin_bot_id`):
```go
// applyBotConfigToStrategy overwrites one strategy row with a bot's config and (re)attaches
// it to the bot: sets status='active', cycle_count=0, bot_id, origin_bot_id (COALESCE),
// strategy_type and all config columns. Used by the bind endpoint (and mirrors reuse).
func (s *Server) applyBotConfigToStrategy(ctx context.Context, strategyID, botID string, cols botStrategyCols, hedgedStrategyID string, adoptJSON *string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE strategies SET
		   status='active', cycle_count=0, manual_alert=NULL, updated_at=NOW(),
		   bot_id=$2::uuid, origin_bot_id=COALESCE(origin_bot_id, $2::uuid),
		   category=$3,
		   grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		   tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		   leverage=$13, margin_type=$14, hedge_mode=$15, strategy_type=$16, entry_order_type=$17,
		   signal_configs=$18::jsonb, steps=($19::text)::jsonb,
		   trailing_stop_enabled=$20, trailing_activation_pct=$21, trailing_callback_pct=$22,
		   max_cycles=$23, size_as_main=$24,
		   matrix_levels=($25::text)::jsonb, matrix_entry_level=($26::text)::jsonb, safe_zone_pct=$27,
		   protected_build=$28, matrix_rebuild_on_sl=$29, matrix_rebuild_from_entry=$30, relative_slots=$31,
		   hedged_strategy_id=NULLIF($32,'')::uuid, adopt_position_data=($33::text)::jsonb
		 WHERE id=$1`,
		strategyID, botID, cols.category,
		cols.gridLevels, cols.gridActive, cols.gridStep, cols.gridSize,
		cols.tpMode, cols.tpPct, cols.slType, cols.slPct, cols.signalFilter,
		cols.leverage, cols.marginType, cols.hedgeMode, cols.stratType, cols.entryType,
		cols.scJSON, cols.stepsParam,
		cols.trailingEnabled, cols.trailingActPct, cols.trailingCallPct,
		cols.maxCycles, cols.sizeAsMain,
		cols.matrixLevelsParam, cols.matrixEntryParam, cols.safeZonePct,
		cols.protectedBuild, cols.matrixRebuildOnSL, cols.matrixRebuildFromEntry, cols.relativeSlots,
		hedgedStrategyID, adoptJSON,
	)
	return err
}
```

- [ ] **Step 3: Сборка/vet + существующие тесты**

Run: `go build ./... && go vet ./services/api-gateway/... && go test -tags=integration ./services/api-gateway/ -run 'TestCreateBotStrategy|TestMatrix' -count=1`
Expected: чисто; `ok` (рефактор не меняет поведение createBotStrategy).

- [ ] **Step 4: Commit**

```
git add services/api-gateway/bot_engine.go
git commit -m "refactor(strategies): общий computeBotStrategyCols + applyBotConfigToStrategy"
```

---

### Task 3: Bind-эндпоинт

**Files:**
- Modify: `services/api-gateway/strategy_handler.go` (обработчик `BindStrategiesToBot`)
- Modify: `services/api-gateway/main.go` (роут)
- Test: `services/api-gateway/bind_strategies_test.go`

- [ ] **Step 1: Падающий тест**

Создать `services/api-gateway/bind_strategies_test.go`:
```go
//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBindStrategiesToBot: две одиночные (bot_id NULL) matrix-леги одного символа с
// противоположными направлениями привязываются к matrix-боту → у обеих bot_id=бот,
// status active. Невалидные комбинации отклоняются.
func TestBindStrategiesToBot(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "bind")
	accID := createTestAccount(t, s, userID)
	// matrix bot on the same account
	var botID string
	s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'bindbot',$2,'active','{"bot_kind":"matrix","hedge_mode":true,"grid_size_usdt":20}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })

	ins := func(dir string) string {
		var id string
		s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
			 VALUES ($1,$2,'BNDUSDT',$3,'matrix','active') RETURNING id`, userID, accID, dir).Scan(&id)
		t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })
		return id
	}
	longID, shortID := ins("long"), ins("short")

	body, _ := json.Marshal(map[string]string{"strategy_a_id": longID, "strategy_b_id": shortID, "bot_id": botID})
	req := httptest.NewRequest(http.MethodPost, "/strategies/bind", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.BindStrategiesToBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bind: got %d: %s", rec.Code, rec.Body.String())
	}

	for _, id := range []string{longID, shortID} {
		var bot *string
		var status string
		s.pool.QueryRow(ctx, `SELECT bot_id::text, status FROM strategies WHERE id=$1`, id).Scan(&bot, &status)
		if bot == nil || *bot != botID {
			t.Errorf("strategy %s bot_id=%v, want %s", id[:8], bot, botID)
		}
		if status != "active" {
			t.Errorf("strategy %s status=%s, want active", id[:8], status)
		}
	}

	// Invalid: same direction → 400.
	long2 := ins("long")
	body2, _ := json.Marshal(map[string]string{"strategy_a_id": longID, "strategy_b_id": long2, "bot_id": botID})
	req2 := httptest.NewRequest(http.MethodPost, "/strategies/bind", bytes.NewReader(body2))
	req2 = withUserID(req2, userID)
	rec2 := httptest.NewRecorder()
	s.BindStrategiesToBot(rec2, req2)
	if rec2.Code == http.StatusOK {
		t.Errorf("same-direction bind should fail, got 200")
	}
}
```

- [ ] **Step 2: Запустить — FAIL** (`BindStrategiesToBot undefined`)

Run: `go test -tags=integration ./services/api-gateway/ -run TestBindStrategiesToBot -count=1`
Expected: build failed — undefined.

- [ ] **Step 3: Реализовать обработчик**

В `strategy_handler.go` добавить:
```go
// BindStrategiesToBot attaches two standalone strategies (same symbol+account, opposite
// direction) to a hedge/matrix bot as a pair: applies the bot's config to both, sets
// bot_id, and for a hedge bot links hedged_strategy_id (hedge→main by position size).
// POST /strategies/bind  { strategy_a_id, strategy_b_id, bot_id }
func (s *Server) BindStrategiesToBot(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		StrategyAID string `json:"strategy_a_id"`
		StrategyBID string `json:"strategy_b_id"`
		BotID       string `json:"bot_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ctx := r.Context()

	// Load both strategies (must be owned by the user).
	type legRow struct{ id, symbol, dir, accountID string }
	loadLeg := func(id string) (legRow, bool) {
		var l legRow
		err := s.pool.QueryRow(ctx,
			`SELECT id, symbol, direction, account_id FROM strategies WHERE id=$1 AND owner_id=$2`,
			id, userID).Scan(&l.id, &l.symbol, &l.dir, &l.accountID)
		return l, err == nil
	}
	a, okA := loadLeg(req.StrategyAID)
	b, okB := loadLeg(req.StrategyBID)
	if !okA || !okB {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	if a.symbol != b.symbol || a.accountID != b.accountID {
		writeError(w, http.StatusBadRequest, "стратегии должны быть по одному символу и аккаунту")
		return
	}
	if a.dir == b.dir {
		writeError(w, http.StatusBadRequest, "нужны противоположные направления (long и short)")
		return
	}

	// Load the bot (owned, same account, kind hedge/matrix) + config.
	var botAccount, botKind string
	var stratCfgBytes []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(account_id::text,''), COALESCE(strategy_config->>'bot_kind',''), strategy_config
		 FROM bots WHERE id=$1 AND owner_id=$2`, req.BotID, userID,
	).Scan(&botAccount, &botKind, &stratCfgBytes); err != nil {
		writeError(w, http.StatusNotFound, "бот не найден")
		return
	}
	if botAccount != a.accountID {
		writeError(w, http.StatusBadRequest, "бот привязан к другому аккаунту")
		return
	}
	if botKind != "hedge" && botKind != "matrix" {
		writeError(w, http.StatusBadRequest, "бот должен быть hedge или matrix")
		return
	}
	// Conflict: bot already manages a pair for this symbol.
	var conflict int
	s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM strategies
		 WHERE bot_id=$1 AND symbol=$2 AND status IN ('active','finishing')`,
		req.BotID, a.symbol).Scan(&conflict)
	if conflict > 0 {
		writeError(w, http.StatusConflict, "у бота уже есть пара по этому символу")
		return
	}

	var cfg botCfgJSON
	if err := json.Unmarshal(stratCfgBytes, &cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось прочитать конфиг бота")
		return
	}
	cols := s.computeBotStrategyCols(ctx, cfg, a.symbol, 0)

	// Roles. Matrix: long=main, short=hedge. Hedge: bigger position=main.
	mainLeg, hedgeLeg := a, b
	if a.dir == "short" { // ensure mainLeg=long, hedgeLeg=short as the default
		mainLeg, hedgeLeg = b, a
	}
	if botKind == "hedge" {
		if creds, cErr := s.loadBotAccountCreds(ctx, a.accountID); cErr == nil {
			if positions, pErr := trader.FetchPositions(ctx, creds); pErr == nil {
				posMap, _ := buildHedgePosMap(positions)
				sz := func(dir string) float64 {
					side := "Buy"
					if dir == "short" {
						side = "Sell"
					}
					if bySym, ok := posMap[a.symbol]; ok {
						if p, ok := bySym[side]; ok {
							return p.Size * p.MarkPrice
						}
					}
					return 0
				}
				if sz(hedgeLeg.dir) > sz(mainLeg.dir) {
					mainLeg, hedgeLeg = hedgeLeg, mainLeg // bigger notional becomes main
				}
			}
		}
	}

	// Apply config + attach. Hedge leg links to main via hedged_strategy_id.
	if err := s.applyBotConfigToStrategy(ctx, mainLeg.id, req.BotID, cols, "", nil); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось привязать main-легу")
		return
	}
	hedgedLink := ""
	if botKind == "hedge" {
		hedgedLink = mainLeg.id
	}
	if err := s.applyBotConfigToStrategy(ctx, hedgeLeg.id, req.BotID, cols, hedgedLink, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось привязать hedge-легу")
		return
	}

	go s.engine.Notify(context.Background(), mainLeg.id)
	go s.engine.Notify(context.Background(), hedgeLeg.id)
	s.logBotEvent(ctx, req.BotID,
		fmt.Sprintf("%s — стратегии объединены в пару привязкой к боту", a.symbol), "info", "user")

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 4: Роут**

В `services/api-gateway/main.go` рядом с `r.Post("/strategies/{id}/detach", s.DetachFromBot)` добавить:
```go
		r.Post("/strategies/bind", s.BindStrategiesToBot)
```

- [ ] **Step 5: Запустить — PASS**

Run: `go build ./... && go test -tags=integration ./services/api-gateway/ -run TestBindStrategiesToBot -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```
git add services/api-gateway/strategy_handler.go services/api-gateway/main.go services/api-gateway/bind_strategies_test.go
git commit -m "feat(strategies): bind-эндпоинт — объединение двух лег в пару под ботом"
```

---

### Task 4: Фронт — API + окно «Привязать к боту»

**Files:**
- Modify: `frontend/src/api/strategies.ts` (функция bind)
- Create: `frontend/src/components/strategies/BindToBotModal.tsx`

- [ ] **Step 1: API-функция**

В `frontend/src/api/strategies.ts` добавить:
```ts
export async function bindStrategiesToBot(
  strategyAId: string,
  strategyBId: string,
  botId: string,
): Promise<void> {
  await apiClient.post('/strategies/bind', {
    strategy_a_id: strategyAId,
    strategy_b_id: strategyBId,
    bot_id: botId,
  })
}
```

- [ ] **Step 2: Компонент окна**

Создать `frontend/src/components/strategies/BindToBotModal.tsx`. Пропсы: `stratA`, `stratB` (`Strategy`), `bots` (список ботов пользователя со `strategy_config.bot_kind`, `account_id`, `id`, `name`), `hasOpenPosition: boolean`, `originBotId?: string | null`, `onClose`, `onBound`. Логика:
- Отфильтровать боты: `account_id === stratA.account_id` и `bot_kind ∈ {hedge, matrix}`.
- Список выбора бота; если `bot.id === originBotId` — пометка «источник» (подсветка).
- Если `hasOpenPosition` — предупреждение: «Конфиг бота применится к живой позиции — сетка/TP/SL перевыставятся».
- Кнопка «Привязать» (disabled, пока бот не выбран) → `bindStrategiesToBot(stratA.id, stratB.id, selectedBotId)` → `onBound()`; ошибки показывать.

Скелет (адаптировать под существующий стиль модалок проекта — см. другие `*Modal.tsx`):
```tsx
import { useState } from 'react'
import { bindStrategiesToBot } from '../../api/strategies'
import type { Strategy } from '../../types'

interface BotOption { id: string; name: string; account_id?: string; bot_kind?: string }

export function BindToBotModal({
  stratA, stratB, bots, hasOpenPosition, originBotId, onClose, onBound,
}: {
  stratA: Strategy; stratB: Strategy; bots: BotOption[]
  hasOpenPosition: boolean; originBotId?: string | null
  onClose: () => void; onBound: () => void
}) {
  const eligible = bots.filter(b => b.account_id === stratA.account_id && (b.bot_kind === 'hedge' || b.bot_kind === 'matrix'))
  const [sel, setSel] = useState<string>(originBotId && eligible.some(b => b.id === originBotId) ? originBotId : '')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true); setErr(null)
    try {
      await bindStrategiesToBot(stratA.id, stratB.id, sel)
      onBound()
    } catch (e: any) {
      setErr(e?.response?.data?.error ?? 'Ошибка привязки')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div className="bg-[#12151c] rounded-lg p-5 w-[420px]" onClick={e => e.stopPropagation()}>
        <div className="text-[15px] font-bold mb-3">Привязать к боту — {stratA.symbol}</div>
        {hasOpenPosition && (
          <div className="text-[12px] text-amber-300 bg-amber-400/10 rounded px-3 py-2 mb-3">
            ⚠ По одной из лег есть открытая позиция — конфиг бота применится к ней (сетка/TP/SL перевыставятся).
          </div>
        )}
        <div className="max-h-[240px] overflow-y-auto flex flex-col gap-1 mb-3">
          {eligible.length === 0 && <div className="text-[12px] text-slate-500">Нет подходящих hedge/matrix ботов на этом аккаунте.</div>}
          {eligible.map(b => (
            <button key={b.id} onClick={() => setSel(b.id)}
              className={`flex items-center justify-between px-3 py-2 rounded text-[13px] ${sel === b.id ? 'bg-emerald-500/20 text-emerald-200' : 'bg-white/5 text-slate-300'}`}>
              <span>{b.name} <span className="text-slate-500">· {b.bot_kind}</span></span>
              {b.id === originBotId && <span className="text-[10px] uppercase text-emerald-400">источник</span>}
            </button>
          ))}
        </div>
        {err && <div className="text-[12px] text-rose-400 mb-2">{err}</div>}
        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="px-3 py-1.5 rounded text-[13px] text-slate-400">Отмена</button>
          <button onClick={submit} disabled={!sel || busy}
            className="px-3 py-1.5 rounded text-[13px] bg-emerald-500/20 text-emerald-200 disabled:opacity-50">Привязать</button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Тайп-чек**

Run: `cd frontend && npx tsc --noEmit`
Expected: без ошибок (если `Strategy` не содержит нужных полей у ботов — использовать фактический тип бота из проекта).

- [ ] **Step 4: Commit**

```
git add frontend/src/api/strategies.ts frontend/src/components/strategies/BindToBotModal.tsx
git commit -m "feat(ui): API + окно «Привязать к боту»"
```

---

### Task 5: Фронт — drag-and-drop одиночных карточек

**Files:**
- Modify: `frontend/src/pages/TerminalPage.tsx` (обёртка одиночных карточек: drag source + drop target + открытие окна)

- [ ] **Step 1: Состояние DnD + окно**

В `TerminalPage` добавить состояние:
```tsx
const [dragStrat, setDragStrat] = useState<Strategy | null>(null)
const [bindPair, setBindPair] = useState<{ a: Strategy; b: Strategy } | null>(null)
```
Хелпер валидной цели:
```tsx
const canBind = (a: Strategy, b: Strategy) =>
  a.id !== b.id && a.symbol === b.symbol && a.account_id === b.account_id && a.direction !== b.direction
```

- [ ] **Step 2: Обернуть одиночную карточку в drag/drop**

В `renderItems.map` в ветке `item.type === 'single'` (`TerminalPage.tsx:~1145`, `<div key={s.id} …>`) добавить на этот `<div>` HTML5-DnD (`s` — стратегия одиночной карточки):
```tsx
            <div
              key={s.id}
              draggable
              onDragStart={() => setDragStrat(s)}
              onDragEnd={() => setDragStrat(null)}
              onDragOver={e => { if (dragStrat && canBind(dragStrat, s)) e.preventDefault() }}
              onDrop={e => {
                e.preventDefault()
                if (dragStrat && canBind(dragStrat, s)) setBindPair({ a: dragStrat, b: s })
                setDragStrat(null)
              }}
              className={`${isMobile ? '' : 'origin-top-left scale-[0.96]'} ${dragStrat && canBind(dragStrat, s) ? 'ring-2 ring-emerald-400/60 rounded-lg' : ''}`}
              style={isMobile ? { zoom: '0.82' } : undefined}
            >
              <StrategyCard … />
            </div>
```
(Сохранить существующие пропсы `StrategyCard`, `key`, стили; добавить только DnD-атрибуты и подсветку валидной цели через ring-класс.)

- [ ] **Step 3: Рендер окна привязки**

В конце разметки `TerminalPage` (рядом с другими модалками) добавить:
```tsx
        {bindPair && (
          <BindToBotModal
            stratA={bindPair.a}
            stratB={bindPair.b}
            bots={myBots}
            hasOpenPosition={[bindPair.a, bindPair.b].some(st => positions.some(p => p.symbol === st.symbol))}
            originBotId={bindPair.a.origin_bot_id ?? bindPair.b.origin_bot_id ?? null}
            onClose={() => setBindPair(null)}
            onBound={() => { setBindPair(null); /* trigger strategies refetch as onChanged does */ }}
          />
        )}
```
Импортировать `BindToBotModal`. `myBots` — уже есть в `TerminalPage` (список ботов). `origin_bot_id` — добавить в тип `Strategy` (`frontend/src/types.ts`) и в бэкенд-ответ `ListStrategies` (SELECT + поле в `listStrategiesQuery` и в структуре `row` — добавить `origin_bot_id`), чтобы фронт его получал. `onBound` должен инициировать перезагрузку стратегий (тем же путём, что `onChanged` в карточках).

- [ ] **Step 4: Пробросить origin_bot_id из бэкенда**

- В `listStrategiesQuery` (`strategy_handler.go`) добавить в SELECT `s.origin_bot_id::text` и в структуру `row` поле `OriginBotID *string \`json:"origin_bot_id"\`` + в скан.
- В `frontend/src/types.ts` в `Strategy` добавить `origin_bot_id?: string | null`.

- [ ] **Step 5: Тайп-чек + сборка бэкенда**

Run: `go build ./... && cd frontend && npx tsc --noEmit`
Expected: чисто.

- [ ] **Step 6: Commit**

```
git add frontend/src/pages/TerminalPage.tsx frontend/src/types.ts services/api-gateway/strategy_handler.go
git commit -m "feat(ui): DnD объединение одиночных карточек + проброс origin_bot_id"
```

---

### Task 6: Финальная проверка

- [ ] **Step 1: Сборка/vet/тесты**

Run: `go build ./... && go vet ./services/api-gateway/... && go test -tags=integration ./services/api-gateway/ -run 'TestBindStrategiesToBot|TestCreateBotStrategy|TestMatrix|TestListStrategies' -count=1 && (cd frontend && npx tsc --noEmit)`
Expected: всё зелёное.

- [ ] **Step 2: Ручной сценарий (после пересборки+рестарта api-gateway и фронта)**

1. Открепить пару бота (leave) → две одиночные карточки.
2. Перетащить long-карточку на short-карточку → появляется окно «Привязать к боту», origin-бот подсвечен, при открытой позиции — предупреждение.
3. Выбрать бота → «Привязать» → карточки объединяются в `HedgePairCard`, бот берёт пару в управление.

---

## Заметки

- Роли hedge по размеру позиции считаются на бэкенде через `FetchPositions` в момент bind; фолбэк при плоских — long=main.
- `applyBotConfigToStrategy` и reuse-ветка `createBotStrategy` должны писать одинаковый набор конфиг-колонок — при изменении набора править обе (или полностью перевести reuse-ветку на общий хелпер).
- Кнопочный фолбэк к DnD — вне области v1.
