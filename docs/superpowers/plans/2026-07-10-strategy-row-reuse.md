# Переиспользование строки стратегии при пересоздании — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `createBotStrategy` переиспользует самую свежую `stopped`-строку бота по слоту (реактивация с перезаписью конфига) вместо создания новой строки — так история и «Накоплено» продолжаются на том же `strategy_id`, а строки-дубли перестают плодиться.

**Architecture:** Единственная точка изменения — `createBotStrategy` (общий вход создания стратегий для matrix/hedge/signal). После вычисления конфига, перед `INSERT`, ищем самую свежую `stopped`-строку по (bot_id, account_id, symbol, direction). Если есть — реактивируем её `UPDATE`-ом (все конфиг-колонки + `status='active'`, `cycle_count=0`), guard `WHERE id=$1 AND status='stopped'`. Нарушение уникального индекса хеджа (23505) и проигранную гонку (0 строк) обрабатываем как пропуск (`return "", nil`), как это делает существующая `ON CONFLICT DO NOTHING`-ветка INSERT. Если stopped-строки нет — обычный `INSERT`.

**Tech Stack:** Go (services/api-gateway), pgx v5 (+ pgconn для кода ошибки), интеграционные тесты с тегом `integration` против локальной БД (PgBouncer :6432).

**Ключевой контекст для исполнителя:**
- `createBotStrategy` — `services/api-gateway/bot_engine.go:906`. В конце (после вычисления `category`, `gridLevels`, `gridActive`, `gridStep`, `gridSize`, `tpMode`, `tpPct`, `slType`, `slPct`, `signalFilter`, `leverage`, `marginType`, `stratType`, `entryType`, `scJSON`, `stepsParam`, `trailingActPct`, `trailingCallPct`, `matrixLevelsParam`, `matrixEntryParam`) делает `INSERT ... VALUES (...,'active',...) ON CONFLICT DO NOTHING RETURNING id` (строки ~1025-1059), затем `go s.engine.Notify(context.Background(), id)` и `return id, nil`. Есть ветка `if errors.Is(err, pgx.ErrNoRows) { return "", nil }` для hedge-конфликта.
- `s.engine` в тестах НЕ nil (`NewServer` всегда делает `s.engine = strategy.New(...)`, `server.go:98`), поэтому `createBotStrategy` вызывается в тесте напрямую; `Notify` уходит в горутину и нефатален; `GetPublicInstrumentInfo` при сбое сети тоже нефатальна (см. `bot_engine.go:956-967`).
- Импорты в `bot_engine.go` уже включают `errors` и `github.com/jackc/pgx/v5` (`pgx`). Нужно добавить `github.com/jackc/pgx/v5/pgconn` для проверки `*pgconn.PgError`.
- Тест-хелперы: `newTestServer`, `createWHUser`, `createTestAccount` (в тест-файлах api-gateway), `createZombieBot` (`services/api-gateway/matrix_zombie_test.go`). Все интеграционные тесты — `//go:build integration`, `package main`.
- Типы: `botEngineRow` имеет поля `id, ownerID, accountID string` (см. использование в `matrix_engine.go`); `botCfgJSON` — конфиг бота (поля `StrategyType`, `GridSizeUSDT`, `HedgeMode` и т.д.).

---

### Task 1: Реюз stopped-строки в createBotStrategy

**Files:**
- Modify: `services/api-gateway/bot_engine.go` (`createBotStrategy` — ветка реюза перед INSERT; импорт `pgconn`)
- Test: `services/api-gateway/bot_strategy_reuse_test.go` (создать)

- [ ] **Step 1: Написать падающий тест**

Создать `services/api-gateway/bot_strategy_reuse_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"
)

// TestCreateBotStrategy_ReusesStoppedRow: при наличии остановленной строки бота по
// слоту createBotStrategy реактивирует ЕЁ (тот же id, status→active, конфиг перезаписан),
// не создавая новую. Без stopped-строки — создаётся новая.
func TestCreateBotStrategy_ReusesStoppedRow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "reuse")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "reuse")
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID)
	})

	// Seed a stopped matrix leg for the slot (older row, low grid size).
	var stoppedID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, grid_size_usdt, created_at)
		 VALUES ($1,$2,$3,'REUUSDT','linear','long','matrix','stopped',5,NOW()-INTERVAL '1 hour') RETURNING id`,
		userID, accID, botID,
	).Scan(&stoppedID); err != nil {
		t.Fatalf("seed stopped: %v", err)
	}

	cfg := botCfgJSON{StrategyType: "matrix", GridSizeUSDT: 20, HedgeMode: true}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}

	// (1) Reuse path: an existing stopped row → reactivate it, no new row.
	id, err := s.createBotStrategy(ctx, b, cfg, "REUUSDT", "long", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy: %v", err)
	}
	if id != stoppedID {
		t.Errorf("expected reuse of %s, got %s", stoppedID[:8], id)
	}
	var cnt int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='REUUSDT' AND direction='long'`, botID).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("expected 1 row after reuse, got %d", cnt)
	}
	var status string
	var gridSize float64
	s.pool.QueryRow(ctx, `SELECT status, grid_size_usdt FROM strategies WHERE id=$1`, stoppedID).Scan(&status, &gridSize)
	if status != "active" {
		t.Errorf("reused row status=%q, want active", status)
	}
	if gridSize < 20 {
		t.Errorf("reused row grid_size_usdt=%v, want config value overwritten (>=20)", gridSize)
	}

	// (2) Insert path: no stopped row for a different slot → a brand-new row is created.
	newID, err := s.createBotStrategy(ctx, b, cfg, "REUUSDT", "short", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy insert: %v", err)
	}
	if newID == "" || newID == stoppedID {
		t.Errorf("expected a new row id for the short slot, got %q", newID)
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что падает**

Run: `go test -tags=integration ./services/api-gateway/ -run TestCreateBotStrategy_ReusesStoppedRow -count=1`
Expected: FAIL — реюза нет, `id != stoppedID` (создаётся новая строка) и `cnt == 2`.

- [ ] **Step 3: Добавить импорт pgconn**

В `services/api-gateway/bot_engine.go` в блок импортов добавить:
```go
	"github.com/jackc/pgx/v5/pgconn"
```
(рядом с существующим `"github.com/jackc/pgx/v5"`).

- [ ] **Step 4: Вставить ветку реюза перед INSERT**

В `createBotStrategy`, СРАЗУ ПЕРЕД строкой `var id string` (которая предшествует `INSERT INTO strategies`), вставить блок реюза. Он использует уже вычисленные локальные переменные (`category`, `gridLevels`, …, `matrixEntryParam`, `signalFilter`) и параметры функции (`b`, `sym`, `dir`, `hedgedStrategyID`, `adoptJSON`):

```go
	// Reuse the most-recent stopped strategy row for this slot instead of inserting a
	// brand-new one — keeps history and the "Накоплено" counter continuous on the same
	// strategy_id, and stops stopped-row duplicates from accumulating.
	var reuseID string
	if selErr := s.pool.QueryRow(ctx,
		`SELECT id FROM strategies
		 WHERE bot_id=$1 AND account_id=$2 AND symbol=$3 AND direction=$4 AND status='stopped'
		 ORDER BY created_at DESC LIMIT 1`,
		b.id, b.accountID, sym, dir,
	).Scan(&reuseID); selErr == nil && reuseID != "" {
		var rid string
		reErr := s.pool.QueryRow(ctx, `
			UPDATE strategies SET
			   status='active', cycle_count=0, manual_alert=NULL, updated_at=NOW(),
			   category=$2,
			   grid_levels=$3, grid_active=$4, grid_step_pct=$5, grid_size_usdt=$6,
			   tp_mode=$7, tp_pct=$8, sl_type=$9, sl_pct=$10, signal_filter=$11,
			   leverage=$12, margin_type=$13, hedge_mode=$14, strategy_type=$15, entry_order_type=$16,
			   signal_configs=$17::jsonb, steps=($18::text)::jsonb,
			   trailing_stop_enabled=$19, trailing_activation_pct=$20, trailing_callback_pct=$21,
			   max_cycles=$22, size_as_main=$23,
			   matrix_levels=$24, matrix_entry_level=$25, safe_zone_pct=$26,
			   protected_build=$27, matrix_rebuild_on_sl=$28, matrix_rebuild_from_entry=$29, relative_slots=$30,
			   hedged_strategy_id=NULLIF($31,'')::uuid, adopt_position_data=($32::text)::jsonb
			 WHERE id=$1 AND status='stopped'
			 RETURNING id`,
			reuseID, category,
			gridLevels, gridActive, gridStep, gridSize,
			tpMode, tpPct, slType, slPct, signalFilter,
			leverage, marginType, cfg.HedgeMode, stratType, entryType,
			string(scJSON), stepsParam,
			cfg.TrailingEnabled, trailingActPct, trailingCallPct,
			cfg.MaxCycles, cfg.SizeAsMain,
			matrixLevelsParam, matrixEntryParam, cfg.SafeZonePct,
			cfg.ProtectedBuild, cfg.MatrixRebuildOnSL, cfg.MatrixRebuildFromEntry, cfg.RelativeSlots,
			hedgedStrategyID, adoptJSON,
		).Scan(&rid)
		if reErr == nil {
			go s.engine.Notify(context.Background(), rid)
			return rid, nil
		}
		// Hedge slot already claimed by another bot (unique partial index on
		// hedged_strategy_id) — skip, mirroring the INSERT ON CONFLICT DO NOTHING path.
		var pgErr *pgconn.PgError
		if errors.As(reErr, &pgErr) && pgErr.Code == "23505" {
			return "", nil
		}
		// Race: another tick reactivated this row first (guard matched 0 rows) — skip
		// rather than INSERT a duplicate.
		if errors.Is(reErr, pgx.ErrNoRows) {
			return "", nil
		}
		return "", reErr
	}

```

(Существующий код `var id string` + `INSERT ...` остаётся ниже без изменений — это ветка «нет stopped-строки».)

- [ ] **Step 5: Запустить тест — убедиться, что проходит**

Run: `go test -tags=integration ./services/api-gateway/ -run TestCreateBotStrategy_ReusesStoppedRow -count=1`
Expected: PASS (`ok  sis/services/api-gateway`).

- [ ] **Step 6: Сборка, vet, соседние тесты, gofmt**

Run:
```
go build ./...
go vet ./services/api-gateway/...
go test -tags=integration ./services/api-gateway/ -run 'TestMatrixZombie|TestDirectionHasLiveStrategy|TestGetHedgeSession|TestListStrategies|TestCreateBotStrategy' -count=1
gofmt -w services/api-gateway/bot_engine.go services/api-gateway/bot_strategy_reuse_test.go
```
Expected: сборка/vet чисто; все тесты `ok`.

- [ ] **Step 7: Commit**

```
git add services/api-gateway/bot_engine.go services/api-gateway/bot_strategy_reuse_test.go
git commit -m "feat(strategies): переиспользовать stopped-строку бота вместо новой при пересоздании"
```

---

### Task 2: Финальная проверка

- [ ] **Step 1: Полная сборка и тесты**

Run: `go build ./... && go vet ./services/api-gateway/... ./pkg/strategy/... && go test -tags=integration ./services/api-gateway/ -run 'TestCreateBotStrategy|TestMatrixZombie|TestDirectionHasLiveStrategy|TestGetHedgeSession|TestListStrategies' -count=1`
Expected: чисто; все `ok`.

- [ ] **Step 2: Ручная проверка (после пересборки+перезапуска api-gateway)**

1. У matrix-бота дождаться/спровоцировать пересоздание леги (например, ручное закрытие → пауза → возобновление, или paired_close → пересоздание).
2. В БД убедиться, что НОВАЯ строка НЕ появилась: `SELECT count(*) FROM strategies WHERE bot_id=<bot> AND symbol=<sym> AND direction=<dir>` не растёт от пересоздания; вместо этого та же строка снова `active` с новым циклом (`SELECT max(cycle_num) FROM strategy_cycles WHERE strategy_id=<id>` увеличивается).
3. «Накоплено» по леге продолжается (не обнуляется) после пересоздания.

---

## Заметки

- Уже накопленные дубли (например, текущая скрытая пара MatrixNova) НЕ трогаются — реюз берёт самую свежую stopped-строку, старые замороженные остаются скрытыми (их прячет `hideSupersededStopped`). Это вариант A из спеки.
- UPDATE-список колонок должен соответствовать INSERT-списку в той же функции; при будущих изменениях набора колонок стратегии — править оба места.
