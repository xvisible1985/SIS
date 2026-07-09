# Пауза ноги matrix-бота при ручном закрытии — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ручное/внешнее закрытие позиции ноги matrix-бота переводит эту ногу в новый статус `paused` (бот её не пересоздаёт), а пользователь возобновляет её кнопкой на карточке.

**Architecture:** Пауза вешается в единственной точке подтверждённого внешнего закрытия — `closePositionExternal` по пути «вручную» (`closeManualPosition ← handlePositionCloseRetry`), который уже отсекает самозакрытия (TP/SL/matrix-TP закрывают цикл и выходят раньше) и подтверждает пропажу позиции через `FetchPositions`. Для бот-леги matrix вместо `stopped` ставится `paused` условным `UPDATE ... WHERE status='active'` (гонка с paired-close безопасна). `ensureMatrixStrategies` пропускает направление, если по нему есть `paused`-нога. Возобновление — существующий endpoint `POST /strategies/{id}/status` со `status='active'` (запускает свежий цикл), плюс кнопка на карточке.

**Tech Stack:** Go (pkg/strategy, services/api-gateway, pgx, chi), интеграционные тесты с тегом `integration` против локальной БД (PgBouncer :6432), React/TypeScript фронтенд.

**Ключевой контекст для исполнителя (прочти перед началом):**
- Статус стратегии — колонка `strategies.status` типа `TEXT` без CHECK-констрейнта (`migrations/006_strategies.sql`, `migrations/040_matrix_sl.sql`), поэтому новое значение `paused` добавляется **без миграции схемы**.
- Константы статусов: `pkg/strategy/types.go:8-10` (`StatusActive="active"`, `StatusFinishing="finishing"`, `StatusStopped="stopped"`).
- `closePositionExternal` (`pkg/strategy/cycle.go:4413`) — обработчик внешнего закрытия; сейчас безусловно ставит `stopped`. Вызывается из четырёх мест: `closeManualPosition` (путь «вручную», cycle.go:4407) и три пути «биржей …» (cycle.go:5284, 5303, 5614).
- `handlePositionCloseRetry` (`cycle.go:4342`) вызывает `closeManualPosition` только когда позиция подтверждённо ушла (`FetchPositions`) и это НЕ самозакрытие (самозакрытия закрывают цикл → `cycle==nil` → ранний выход).
- `ensureMatrixStrategies` (`services/api-gateway/matrix_engine.go:195`) — проверка существующей стратегии (matrix_engine.go:207-215) со `status IN ('active','finishing')`; пропускает направление, если стратегия уже есть.
- Тест-хелперы для интеграционных тестов: `newTestServer`, `createWHUser`, `createTestAccount` (используются в `services/api-gateway/matrix_zombie_test.go`), `createZombieBot` (там же). Все интеграционные тесты помечены `//go:build integration`.
- `Strategy` (pkg/strategy/types.go) имеет поля `BotID *string` и `StrategyType string`.
- `ListStrategies` не фильтрует по статусу (`WHERE s.account_id=$1` / `WHERE s.owner_id=$1`) — `paused` стратегии возвращаются во фронтенд.
- `SetStrategyStatus` (`services/api-gateway/strategy_handler.go:560`) принимает `active|finishing|stopped`; при `active` проверяет дубли по `status IN ('active','finishing')` (paused-нога не считается дублем) и вызывает `engine.Notify` → свежий цикл. Отдельный endpoint для возобновления НЕ нужен.

---

### Task 1: Статус `paused` — константа и блокировка пересоздания

**Files:**
- Modify: `pkg/strategy/types.go:10` (добавить константу)
- Modify: `services/api-gateway/matrix_engine.go:203-215` (вынести проверку в хелпер, включить `paused`)
- Test: `services/api-gateway/matrix_pause_test.go` (создать)

- [ ] **Step 1: Написать падающий тест**

Создать `services/api-gateway/matrix_pause_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"
)

// TestDirectionHasLiveStrategy_PausedBlocks: нога в статусе paused должна
// блокировать пересоздание этого направления (ensureMatrixStrategies его пропустит),
// а направление без стратегии — не блокировать.
func TestDirectionHasLiveStrategy_PausedBlocks(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "pauseblk")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "pb")

	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'PBUSDT','long','matrix','paused',$3) RETURNING id`,
		userID, accID, botID,
	).Scan(&id); err != nil {
		t.Fatalf("insert paused strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })

	if !s.directionHasLiveStrategy(ctx, accID, "PBUSDT", "long", botID) {
		t.Errorf("paused-нога должна блокировать пересоздание long")
	}
	if s.directionHasLiveStrategy(ctx, accID, "PBUSDT", "short", botID) {
		t.Errorf("short без стратегии не должен блокироваться")
	}
}
```

- [ ] **Step 2: Запустить тест — убедиться, что не компилируется/падает**

Run: `go test -tags=integration ./services/api-gateway/ -run TestDirectionHasLiveStrategy_PausedBlocks -count=1`
Expected: FAIL — `s.directionHasLiveStrategy undefined`.

- [ ] **Step 3: Добавить константу статуса**

В `pkg/strategy/types.go` после строки `StatusStopped   Status = "stopped"` добавить:

```go
	StatusPaused    Status = "paused"
```

- [ ] **Step 4: Добавить хелпер и использовать его в ensureMatrixStrategies**

В `services/api-gateway/matrix_engine.go` добавить хелпер (например, сразу перед `ensureMatrixStrategies`):

```go
// directionHasLiveStrategy сообщает, есть ли по (account, symbol, direction) стратегия,
// которая должна блокировать пересоздание боту: активная/завершающаяся, ПРИОСТАНОВЛЕННАЯ
// пользователем (paused), либо отцепленная (bot_id IS NULL — пользователь оставил её сам).
func (s *Server) directionHasLiveStrategy(ctx context.Context, accountID, symbol, dir, botID string) bool {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM strategies
			WHERE account_id=$1 AND symbol=$2 AND direction=$3
			  AND status IN ('active','finishing','paused')
			  AND (bot_id=$4 OR bot_id IS NULL))`,
		accountID, symbol, dir, botID,
	).Scan(&exists); err != nil {
		return false
	}
	return exists
}
```

Заменить в `ensureMatrixStrategies` инлайн-проверку (matrix_engine.go:207-215):

```go
			var existingID string
			// Skip if bot already owns an active strategy for this slot,
			// or if a detached (bot_id=NULL) strategy is still active on this account —
			// the user detached it intentionally, don't create a duplicate.
			if err := s.pool.QueryRow(ctx,
				`SELECT id FROM strategies
				 WHERE account_id=$1 AND symbol=$2 AND direction=$3
				   AND status IN ('active','finishing')
				   AND (bot_id=$4 OR bot_id IS NULL)
				 LIMIT 1`,
				accountID, symbol, dir, botID).Scan(&existingID); err == nil {
				continue
			}
```

на:

```go
			// Skip if the bot already owns a live strategy for this slot, if the user
			// paused this leg (manual close → paused, don't recreate), or if a detached
			// (bot_id=NULL) strategy is still live on this account.
			if s.directionHasLiveStrategy(ctx, accountID, symbol, dir, botID) {
				continue
			}
```

- [ ] **Step 5: Запустить тест — убедиться, что проходит**

Run: `go test -tags=integration ./services/api-gateway/ -run TestDirectionHasLiveStrategy_PausedBlocks -count=1`
Expected: PASS (`ok  sis/services/api-gateway`).

- [ ] **Step 6: Прогнать соседние тесты и сборку**

Run: `go build ./... && go test -tags=integration ./services/api-gateway/ -run 'TestMatrixZombie' -count=1`
Expected: `BUILD` без ошибок, `ok  sis/services/api-gateway`.

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/types.go services/api-gateway/matrix_engine.go services/api-gateway/matrix_pause_test.go
git commit -m "feat(matrix): статус paused и блокировка пересоздания paused-ноги"
```

---

### Task 2: Пауза при ручном закрытии в `closePositionExternal`

**Files:**
- Modify: `pkg/strategy/cycle.go` (`closePositionExternal` сигнатура + логика; вызовы `closeManualPosition` и трёх «биржей …»)
- Test: `pkg/strategy/pause_test.go` (создать)

- [ ] **Step 1: Написать падающий тест на чистый хелпер**

Создать `pkg/strategy/pause_test.go`:

```go
package strategy

import "testing"

func strPtr(s string) *string { return &s }

// TestManualCloseStatus: только нога matrix-бота уходит в paused при ручном закрытии;
// grid-бот, ручная стратегия без бота и не-matrix остаются stopped.
func TestManualCloseStatus(t *testing.T) {
	cases := []struct {
		name  string
		strat Strategy
		want  Status
	}{
		{"matrix bot leg → paused",
			Strategy{BotID: strPtr("bot"), StrategyType: "matrix"}, StatusPaused},
		{"grid bot leg → stopped",
			Strategy{BotID: strPtr("bot"), StrategyType: "grid"}, StatusStopped},
		{"matrix without bot (manual) → stopped",
			Strategy{BotID: nil, StrategyType: "matrix"}, StatusStopped},
		{"hedge bot leg → stopped",
			Strategy{BotID: strPtr("bot"), StrategyType: "hedge"}, StatusStopped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := manualCloseStatus(c.strat); got != c.want {
				t.Errorf("manualCloseStatus() = %q, want %q", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Запустить тест — убедиться, что не компилируется/падает**

Run: `go test ./pkg/strategy/ -run TestManualCloseStatus -count=1`
Expected: FAIL — `manualCloseStatus undefined`.

- [ ] **Step 3: Добавить чистый хелпер**

В `pkg/strategy/cycle.go` (рядом с `closePositionExternal`) добавить:

```go
// manualCloseStatus возвращает статус, в который переводится нога после ПОДТВЕРЖДЁННОГО
// ручного/внешнего закрытия. Нога matrix-бота уходит в paused (пользователь закрыл — бот
// не пересоздаёт), всё остальное — в stopped (прежнее поведение).
func manualCloseStatus(strategy Strategy) Status {
	if strategy.BotID != nil && strategy.StrategyType == "matrix" {
		return StatusPaused
	}
	return StatusStopped
}
```

- [ ] **Step 4: Запустить тест — убедиться, что проходит**

Run: `go test ./pkg/strategy/ -run TestManualCloseStatus -count=1`
Expected: PASS.

- [ ] **Step 5: Прокинуть `pauseEligible` в `closePositionExternal` и применить статус**

В `pkg/strategy/cycle.go` изменить сигнатуру `closePositionExternal` и финальную установку статуса.

Было (`cycle.go:4406-4413` и хвост функции):

```go
func (sr *StrategyRunner) closeManualPosition(ctx context.Context) {
	sr.closePositionExternal(ctx, "вручную")
}
```
```go
func (sr *StrategyRunner) closePositionExternal(ctx context.Context, source string) {
```
```go
	sr.strategy.Status = StatusStopped
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID,
	); err != nil {
		sr.errlog(ctx, fmt.Sprintf("closePositionExternal: DB update status→stopped: %v", err))
	}
}
```

Стало:

```go
func (sr *StrategyRunner) closeManualPosition(ctx context.Context) {
	sr.closePositionExternal(ctx, "вручную", true)
}
```
```go
func (sr *StrategyRunner) closePositionExternal(ctx context.Context, source string, pauseEligible bool) {
```
```go
	newStatus := StatusStopped
	if pauseEligible {
		newStatus = manualCloseStatus(sr.strategy)
	}
	sr.strategy.Status = newStatus
	if newStatus == StatusPaused {
		// Пауза только пока нога ещё active — иначе не перетираем stopped, который мог
		// проставить paired-close (тогда пара штатно пересоздастся).
		if _, err := sr.runner.pool.Exec(ctx,
			`UPDATE strategies SET status='paused', updated_at=NOW() WHERE id=$1 AND status='active'`, sr.strategy.ID,
		); err != nil {
			sr.errlog(ctx, fmt.Sprintf("closePositionExternal: DB update status→paused: %v", err))
		}
	} else {
		if _, err := sr.runner.pool.Exec(ctx,
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID,
		); err != nil {
			sr.errlog(ctx, fmt.Sprintf("closePositionExternal: DB update status→stopped: %v", err))
		}
	}
}
```

- [ ] **Step 6: Обновить остальные три вызова `closePositionExternal` (они НЕ ставят паузу)**

В `pkg/strategy/cycle.go` заменить:

```go
				sr.closePositionExternal(ctx, "биржей (TP отменён, позиция уже закрыта)")
```
на
```go
				sr.closePositionExternal(ctx, "биржей (TP отменён, позиция уже закрыта)", false)
```

```go
				sr.closePositionExternal(ctx, "биржей (SL отменён, позиция уже закрыта)")
```
на
```go
				sr.closePositionExternal(ctx, "биржей (SL отменён, позиция уже закрыта)", false)
```

```go
		sr.closePositionExternal(ctx, "позиция закрыта на бирже")
```
на
```go
		sr.closePositionExternal(ctx, "позиция закрыта на бирже", false)
```

- [ ] **Step 7: Сборка, vet, тесты**

Run: `go build ./... && go vet ./pkg/strategy/... && go test ./pkg/strategy/ -run TestManualCloseStatus -count=1`
Expected: сборка/vet без ошибок, `ok  sis/pkg/strategy`.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/pause_test.go
git commit -m "feat(matrix): ручное закрытие ноги matrix-бота → paused (вместо stopped)"
```

---

### Task 3: Фронтенд — статус `paused` и кнопка «Возобновить»

**Files:**
- Modify: `frontend/src/types.ts:259` (union статуса)
- Modify: `frontend/src/components/strategies/StrategyCard.tsx` (тип StratStatus, приглушённый вид, кнопка «Возобновить»)

- [ ] **Step 1: Расширить тип статуса стратегии**

В `frontend/src/types.ts:259` заменить:

```ts
  status: 'active' | 'finishing' | 'stopped'
```
на
```ts
  status: 'active' | 'finishing' | 'stopped' | 'paused'
```

- [ ] **Step 2: Приглушить заголовок для paused как для stopped**

В `frontend/src/components/strategies/StrategyCard.tsx:785` заменить:

```tsx
            <span className={`font-display font-bold text-[15px] tracking-[-0.2px] leading-none truncate ${s.status === 'stopped' ? 'text-slate-500' : 'text-[#f2f5fb]'}`}>{s.symbol}</span>
```
на
```tsx
            <span className={`font-display font-bold text-[15px] tracking-[-0.2px] leading-none truncate ${s.status === 'stopped' || s.status === 'paused' ? 'text-slate-500' : 'text-[#f2f5fb]'}`}>{s.symbol}</span>
```

- [ ] **Step 3: Добавить кнопку «Возобновить» для paused**

В `frontend/src/components/strategies/StrategyCard.tsx` сразу после блока `{s.status === 'stopped' ? ( ... ) : ( ... )}` (заканчивается около строки 800; ищи закрывающую `)}` после ветки не-stopped) добавить:

```tsx
            {s.status === 'paused' && (
              <button
                onClick={e => { e.stopPropagation(); handleStatus('active') }}
                disabled={cs.acting}
                title="Возобновить монету (пауза после ручного закрытия)"
                className="shrink-0 inline-flex items-center gap-1 px-2 py-[3px] rounded-[4px] text-[10px] font-bold uppercase tracking-[.4px] leading-none disabled:opacity-50"
                style={{ background: 'rgba(65,210,139,.18)', color: '#5be0a0' }}
              >
                ▶ Возобновить
              </button>
            )}
```

Примечание: `handleStatus` (StrategyCard.tsx:561) и `cs.acting` уже в области видимости; `handleStatus('active')` шлёт `POST /strategies/{id}/status {status:'active'}`, что для paused-ноги проходит dup-check и запускает свежий цикл через `engine.Notify`.

- [ ] **Step 4: Проверить сборку фронтенда (типы)**

Run: `cd frontend && npx tsc --noEmit`
Expected: без ошибок типов (в частности, `s.status === 'paused'` валиден).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts frontend/src/components/strategies/StrategyCard.tsx
git commit -m "feat(matrix): UI — статус paused и кнопка Возобновить на карточке ноги"
```

---

### Task 4: Финальная проверка

- [ ] **Step 1: Полная сборка и тесты**

Run: `go build ./... && go vet ./pkg/strategy/... ./services/api-gateway/... && go test ./pkg/strategy/ -count=1 && go test -tags=integration ./services/api-gateway/ -run 'TestMatrixZombie|TestDirectionHasLiveStrategy|TestGetHedgeSession' -count=1`
Expected: сборка/vet чисто; все тесты `ok`.

- [ ] **Step 2: Ручная проверка сценария (после пересборки и перезапуска api-gateway)**

1. У активной matrix-пары бота вручную закрыть одну ногу (из приложения или на бирже).
2. Убедиться в БД: `SELECT direction, status FROM strategies WHERE bot_id=<bot> ORDER BY direction` — закрытая нога `paused`, вторая `active`.
3. Убедиться, что `ensureMatrixStrategies` не пересоздал закрытую ногу (нет новой строки с этим направлением).
4. На карточке paused-ноги нажать «Возобновить» → нога снова `active`, стартует новый цикл.

---

## Заметки по объёму (из спеки)

- offline-закрытия (движок был выключен, ловится в `startup_reconcile` как `ghost_close`) в v1 паузу НЕ ставят — оставлено как есть.
- Пометка `closedBySelf` на paired-close/полном SL отдельно НЕ требуется: пауза висит только на пути `closeManualPosition` (после `handlePositionCloseRetry`, который уже отсекает самозакрытия), а условие `WHERE status='active'` защищает от гонки с paired-close.
- Семантика кнопки «Остановить» на карточке (сейчас ставит `stopped`, а бот-легу это пересоздаёт) в этом плане не меняется — при желании «ручной паузы второй ноги» через ту же кнопку это отдельная доработка.
