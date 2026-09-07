# Синхронизация настроек «бот ↔ открытая стратегия» — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Дать пользователю явный выбор — синхронизировать точечную правку открытой bot-стратегии обратно в шаблон бота, и синхронизировать правку шаблона бота в уже открытые стратегии — вместо текущего одностороннего молчаливого перезаписывания.

**Architecture:** Два новых опциональных булевых флага в существующих эндпоинтах (`applyToBot` на `PATCH /strategies/{id}`, `applyToActive` на `PATCH /bots/{id}`), два новых confirm-диалога на фронте (по образцу уже существующего `ResetStatsConfirmModal`), без новых элементов интерфейса на карточке.

**Tech Stack:** Go (api-gateway, chi, pgx), React/TypeScript (существующие `StrategyModal.tsx`, `BotForm.tsx`, `HedgeBotForm.tsx`, `MultiBotForm.tsx`).

**Спека:** `docs/superpowers/specs/2026-09-07-bot-strategy-settings-sync-design.md`

---

## Общий список синхронизируемых полей

Используется во всех задачах ниже как единый источник истины:

```
grid_levels, grid_active, grid_step_pct, grid_size_usdt,
tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
leverage, margin_type, hedge_mode, entry_order_type,
signal_configs, steps,
trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct,
matrix_levels, matrix_entry_level, safe_zone_pct,
protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry, relative_slots
```

---

### Task 1: Backend — Strategy → Bot (`applyToBot`)

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`
- Test: `services/api-gateway/strategy_apply_to_bot_test.go`

- [ ] **Step 1: Добавить поле `ApplyToBot` в `strategyPayload`**

В `services/api-gateway/strategy_handler.go`, в структуру `strategyPayload` (строка ~93-133), добавить последним полем:

```go
	AdoptPositionData      json.RawMessage `json:"adopt_position_data,omitempty"`
	ApplyToBot             bool            `json:"applyToBot,omitempty"`
}
```

- [ ] **Step 2: Получать `bot_id` вместе с текущими значениями стратегии**

В `UpdateStrategy` (строка ~463-467) заменить:

```go
	// Prevent updating into a duplicate (same account+symbol+direction already exists).
	var curAccID, curSymbol, curDirection string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT account_id, symbol, direction FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&curAccID, &curSymbol, &curDirection)
```

на:

```go
	// Prevent updating into a duplicate (same account+symbol+direction already exists).
	var curAccID, curSymbol, curDirection string
	var curBotID *string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT account_id, symbol, direction, bot_id FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&curAccID, &curSymbol, &curDirection, &curBotID)
```

- [ ] **Step 3: Добавить SQL-константу для мёржа полей стратегии в шаблон бота**

В `services/api-gateway/strategy_handler.go`, сразу после объявления `strategyPayload` (после закрывающей `}` структуры, перед `func (p *strategyPayload) applyDefaults()`), добавить:

```go
// applyStrategyToBotConfigSQL merges the synchronizable subset of one strategy row's
// current values into its owning bot's strategy_config template (shallow jsonb merge —
// every other key already in strategy_config, e.g. bot_kind or hedge activation fields,
// is left untouched). Run only after the strategy's own UPDATE has committed, so the
// subquery reads the just-saved values.
const applyStrategyToBotConfigSQL = `
	UPDATE bots SET strategy_config = strategy_config || (
		SELECT jsonb_build_object(
			'grid_levels', s.grid_levels,
			'grid_active', s.grid_active,
			'grid_step_pct', s.grid_step_pct,
			'grid_size_usdt', s.grid_size_usdt,
			'tp_mode', s.tp_mode,
			'tp_pct', s.tp_pct,
			'sl_type', s.sl_type,
			'sl_pct', s.sl_pct,
			'signal_filter', s.signal_filter,
			'leverage', s.leverage,
			'margin_type', s.margin_type,
			'hedge_mode', s.hedge_mode,
			'entry_order_type', s.entry_order_type,
			'signal_configs', s.signal_configs,
			'steps', COALESCE(s.steps, '[]'::jsonb),
			'trailing_stop_enabled', s.trailing_stop_enabled,
			'trailing_activation_pct', s.trailing_activation_pct,
			'trailing_callback_pct', s.trailing_callback_pct,
			'matrix_levels', s.matrix_levels,
			'matrix_entry_level', s.matrix_entry_level,
			'safe_zone_pct', s.safe_zone_pct,
			'protected_build', s.protected_build,
			'matrix_rebuild_on_sl', s.matrix_rebuild_on_sl,
			'matrix_rebuild_from_entry', s.matrix_rebuild_from_entry,
			'relative_slots', s.relative_slots
		) FROM strategies s WHERE s.id = $1
	), updated_at = NOW()
	WHERE id = $2`
```

- [ ] **Step 4: Вызвать мёрдж после успешного сохранения стратегии**

В `UpdateStrategy`, сразу после блока проверки `tag.RowsAffected()` (найти существующую проверку сразу после `Exec` из Step 2 предыдущего чтения — в текущем коде это происходит после `tag, err := s.pool.Exec(...)` из строки ~503 и последующей проверки `err`/`RowsAffected`; посмотреть на реальный код рядом со строкой ~535-545, где `tag.RowsAffected()==0` уже проверяется), добавить сразу после этой проверки (до вычисления `gridChanged`):

```go
	if req.ApplyToBot && curBotID != nil {
		if _, err := s.pool.Exec(r.Context(), applyStrategyToBotConfigSQL, id, *curBotID); err != nil {
			log.Printf("strategy: applyToBot merge strategy=%s bot=%s: %v", id, *curBotID, err)
		} else {
			s.logBotEvent(r.Context(), *curBotID,
				fmt.Sprintf("Настройки синхронизированы из открытой стратегии %s", curSymbol),
				"info", "user")
		}
	}
```

Добавить `"log"` в импорты файла, если его там ещё нет (проверить блок `import` в начале `strategy_handler.go` — сейчас там нет `"log"`, нужно добавить).

- [ ] **Step 5: Написать интеграционный тест**

Создать `services/api-gateway/strategy_apply_to_bot_test.go`:

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

// TestUpdateStrategy_ApplyToBot_MergesIntoTemplate: правка открытой bot-стратегии с
// applyToBot=true записывает изменённые синхронизируемые поля (tp_pct) в
// bots.strategy_config, не трогая другие поля шаблона (bot_kind остаётся).
func TestUpdateStrategy_ApplyToBot_MergesIntoTemplate(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "applytobot")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "applytobot")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"bot_kind":"signal","tp_pct":2.0}'::jsonb WHERE id=$1`,
		botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, tp_mode, sl_pct, sl_type)
		 VALUES ($1,$2,$3,'APPLYUSDT','linear','long','grid','active',2.0,'total',-5.0,'conditional') RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"account_id": accID, "symbol": "APPLYUSDT", "category": "linear", "direction": "long",
		"strategy_type": "grid", "grid_levels": 5, "grid_active": 3, "grid_step_pct": 1.0,
		"grid_size_usdt": 100, "tp_mode": "total", "tp_pct": 3.5, "sl_type": "conditional",
		"sl_pct": -5.0, "leverage": 1, "margin_type": "isolated", "entry_order_type": "limit",
		"applyToBot": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+stratID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": stratID})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var cfgBytes []byte
	if err := s.pool.QueryRow(ctx, `SELECT strategy_config FROM bots WHERE id=$1`, botID).Scan(&cfgBytes); err != nil {
		t.Fatalf("read bot config: %v", err)
	}
	var cfg map[string]interface{}
	json.Unmarshal(cfgBytes, &cfg)

	if got := cfg["tp_pct"]; got != 3.5 {
		t.Errorf("expected tp_pct=3.5 merged into bot template, got %v", got)
	}
	if got := cfg["bot_kind"]; got != "signal" {
		t.Errorf("expected unrelated key bot_kind to survive merge, got %v", got)
	}
}

// TestUpdateStrategy_NoApplyToBot_LeavesTemplateUntouched: без applyToBot (или false)
// шаблон бота не меняется вообще, даже если стратегия принадлежит боту.
func TestUpdateStrategy_NoApplyToBot_LeavesTemplateUntouched(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "noapplytobot")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "noapplytobot")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"tp_pct":2.0}'::jsonb WHERE id=$1`, botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, tp_mode, sl_pct, sl_type)
		 VALUES ($1,$2,$3,'NOAPPLYUSDT','linear','long','grid','active',2.0,'total',-5.0,'conditional') RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"account_id": accID, "symbol": "NOAPPLYUSDT", "category": "linear", "direction": "long",
		"strategy_type": "grid", "grid_levels": 5, "grid_active": 3, "grid_step_pct": 1.0,
		"grid_size_usdt": 100, "tp_mode": "total", "tp_pct": 9.9, "sl_type": "conditional",
		"sl_pct": -5.0, "leverage": 1, "margin_type": "isolated", "entry_order_type": "limit",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+stratID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": stratID})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var cfgBytes []byte
	s.pool.QueryRow(ctx, `SELECT strategy_config FROM bots WHERE id=$1`, botID).Scan(&cfgBytes)
	var cfg map[string]interface{}
	json.Unmarshal(cfgBytes, &cfg)
	if got := cfg["tp_pct"]; got != 2.0 {
		t.Errorf("expected bot template tp_pct to stay 2.0, got %v", got)
	}
}
```

- [ ] **Step 6: Прогнать тесты**

Run: `go test -tags=integration ./services/api-gateway/... -run TestUpdateStrategy_ApplyToBot -v`
Expected: `PASS` для обоих тестов.

- [ ] **Step 7: Коммит**

```bash
git add services/api-gateway/strategy_handler.go services/api-gateway/strategy_apply_to_bot_test.go
git commit -m "feat(strategies): add applyToBot flag to sync open strategy settings into bot template

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: Backend — Bot → Strategies (`applyToActive`)

**Files:**
- Modify: `services/api-gateway/bots_handler.go`
- Test: `services/api-gateway/bots_apply_to_active_test.go`

- [ ] **Step 1: Прочитать текущий код блока `changingStrategy` в `PatchBot`**

Открыть `services/api-gateway/bots_handler.go`, найти блок начинающийся `if rawCfg, changingStrategy := body["strategyConfig"]; changingStrategy && !isOfficial {` (строка ~511) и блок `changingStrategy := false` / `go s.syncBotStrategies(...)` (строки ~631-682) — это будет заменяться в следующих шагах.

- [ ] **Step 2: Захватить `oldCfg`/`newCfg` во внешнюю область видимости**

Заменить блок (строки ~511-530):

```go
	// Block only strategy_type changes when active strategies exist (different engine, incompatible state).
	if rawCfg, changingStrategy := body["strategyConfig"]; changingStrategy && !isOfficial {
		var newCfg botCfgJSON
		if json.Unmarshal(rawCfg, &newCfg) == nil && newCfg.StrategyType != "" {
			var oldStratCfgBytes []byte
			var oldCfg botCfgJSON
			if s.pool.QueryRow(ctx, `SELECT strategy_config FROM bots WHERE id = $1`, botID).Scan(&oldStratCfgBytes) == nil &&
				json.Unmarshal(oldStratCfgBytes, &oldCfg) == nil &&
				newCfg.StrategyType != oldCfg.StrategyType {
				var hotCount int
				if s.pool.QueryRow(ctx,
					`SELECT COUNT(*) FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing')`,
					botID).Scan(&hotCount); hotCount > 0 {
					writeError(w, http.StatusUnprocessableEntity,
						"Нельзя менять тип стратегии при наличии активных стратегий. Дождитесь их завершения.")
					return
				}
			}
		}
	}
```

на:

```go
	// oldBotCfg/newBotCfg/haveBotCfgDiff are captured here (before the main UPDATE below
	// overwrites strategy_config) so both the strategy_type guard AND the later
	// applyToActive structural-change decision can reuse the same parsed configs instead
	// of querying twice.
	var oldBotCfg, newBotCfg botCfgJSON
	var haveBotCfgDiff bool
	if rawCfg, changingCfg := body["strategyConfig"]; changingCfg && !isOfficial {
		if json.Unmarshal(rawCfg, &newBotCfg) == nil {
			var oldStratCfgBytes []byte
			if s.pool.QueryRow(ctx, `SELECT strategy_config FROM bots WHERE id = $1`, botID).Scan(&oldStratCfgBytes) == nil &&
				json.Unmarshal(oldStratCfgBytes, &oldBotCfg) == nil {
				haveBotCfgDiff = true
				if newBotCfg.StrategyType != "" && newBotCfg.StrategyType != oldBotCfg.StrategyType {
					var hotCount int
					if s.pool.QueryRow(ctx,
						`SELECT COUNT(*) FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing')`,
						botID).Scan(&hotCount); hotCount > 0 {
						writeError(w, http.StatusUnprocessableEntity,
							"Нельзя менять тип стратегии при наличии активных стратегий. Дождитесь их завершения.")
						return
					}
				}
			}
		}
	}
```

- [ ] **Step 3: Читать `applyToActive` из тела запроса и передавать структурный флаг в `syncBotStrategies`**

Заменить блок (строки ~663-682):

```go
	// If strategyConfig changed: reset trade stats, update in-memory snapshot, and sync active strategies.
	if changingStrategy {
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM trade_history WHERE bot_id = $1 AND owner_id = $2`,
			botID, ownerID,
		); err != nil {
			_ = err
		}
		// Immediately update in-memory bot snapshot so reactive signals use the new config
		// without waiting up to 30 seconds for the next periodic tick.
		if rawCfg, ok := body["strategyConfig"]; ok {
			var newCfg botCfgJSON
			if json.Unmarshal(rawCfg, &newCfg) == nil {
				s.botSnapshotMu.Lock()
				s.botSnapshotCfgs[botID] = newCfg
				s.botSnapshotMu.Unlock()
			}
		}
		go s.syncBotStrategies(context.Background(), botID)
	}
```

на:

```go
	// If strategyConfig changed: reset trade stats, update in-memory snapshot, and
	// (only if the caller explicitly asked) sync active strategies.
	if changingStrategy {
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM trade_history WHERE bot_id = $1 AND owner_id = $2`,
			botID, ownerID,
		); err != nil {
			_ = err
		}
		// Immediately update in-memory bot snapshot so reactive signals use the new config
		// without waiting up to 30 seconds for the next periodic tick.
		if rawCfg, ok := body["strategyConfig"]; ok {
			var snapCfg botCfgJSON
			if json.Unmarshal(rawCfg, &snapCfg) == nil {
				s.botSnapshotMu.Lock()
				s.botSnapshotCfgs[botID] = snapCfg
				s.botSnapshotMu.Unlock()
			}
		}
		applyToActive := false
		if v, ok := body["applyToActive"]; ok {
			json.Unmarshal(v, &applyToActive) //nolint:errcheck
		}
		if applyToActive {
			structural := haveBotCfgDiff && botConfigStructuralFieldsChanged(oldBotCfg, newBotCfg)
			go s.syncBotStrategies(context.Background(), botID, structural)
		}
	}
```

- [ ] **Step 4: Добавить хелпер сравнения структурных полей**

В `services/api-gateway/bots_handler.go`, перед `func (s *Server) syncBotStrategies(...)` (строка ~748), добавить:

```go
// botConfigStructuralFieldsChanged reports whether the fields that require a full cycle
// restart (not just a TP/SL reprice) differ between two bot strategy_config snapshots.
// Mirrors the equivalent gridChanged check in strategy_handler.go's UpdateStrategy, so a
// bot-level edit and a single-strategy edit restart cycles under the same conditions.
func botConfigStructuralFieldsChanged(oldCfg, newCfg botCfgJSON) bool {
	return oldCfg.GridLevels != newCfg.GridLevels ||
		oldCfg.GridActive != newCfg.GridActive ||
		diffFloat(oldCfg.GridStepPct, newCfg.GridStepPct) ||
		diffFloat(oldCfg.GridSizeUSDT, newCfg.GridSizeUSDT) ||
		oldCfg.Direction != newCfg.Direction ||
		oldCfg.EntryOrderType != newCfg.EntryOrderType ||
		oldCfg.Leverage != newCfg.Leverage ||
		!reflect.DeepEqual(oldCfg.Steps, newCfg.Steps) ||
		!jsonRawEqual(oldCfg.MatrixLevels, newCfg.MatrixLevels) ||
		!jsonRawEqual(oldCfg.MatrixEntryLevel, newCfg.MatrixEntryLevel)
}

// jsonRawEqual compares two json.RawMessage values structurally (ignoring key order and
// whitespace) rather than byte-for-byte — both sides may have travelled through different
// marshal paths (raw client bytes vs. a previous DB round-trip) even when semantically equal.
func jsonRawEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var x, y interface{}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(x, y)
}
```

Добавить `"reflect"` в импорты `bots_handler.go`, если его там ещё нет.

- [ ] **Step 5: Обновить сигнатуру и тело `syncBotStrategies`**

Найти `func (s *Server) syncBotStrategies(ctx context.Context, botID string) {` (строка ~750) и:

1. Заменить сигнатуру на:
```go
func (s *Server) syncBotStrategies(ctx context.Context, botID string, structural bool) {
```

2. В конце функции найти:
```go
	for _, id := range ids {
		s.engine.Notify(ctx, id)
	}
}
```
заменить на:
```go
	for _, id := range ids {
		s.engine.Notify(ctx, id)
		if structural {
			s.engine.RestartCycle(ctx, id)
		} else {
			s.engine.UpdateTPSL(ctx, id)
		}
	}
}
```

- [ ] **Step 6: Добавить `applyToActive` в `CreateBotInput` на фронте (подготовка к Task 6/7)**

В `frontend/src/features/bots/types.ts`, в `export type CreateBotInput = {` (строка ~150), добавить после `strategyConfig?: StrategyConfig;`:

```ts
  applyToActive?: boolean;
```

- [ ] **Step 7: Написать интеграционные тесты**

Создать `services/api-gateway/bots_apply_to_active_test.go`:

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
	"time"
)

// TestPatchBot_ApplyToActiveFalse_LeavesStrategiesUntouched: strategyConfig меняется в
// шаблоне бота, но без applyToActive=true уже активная стратегия этого бота не трогается.
func TestPatchBot_ApplyToActiveFalse_LeavesStrategiesUntouched(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "noactive")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "noactive")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"tp_pct":2.0,"grid_levels":5}'::jsonb WHERE id=$1`, botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, grid_levels)
		 VALUES ($1,$2,$3,'NOACTUSDT','linear','long','grid','active',2.0,5) RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body := `{"strategyConfig":{"tp_pct":7.7,"grid_levels":5}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	// syncBotStrategies (if it ran) is fire-and-forget — give it a moment, then assert it
	// did NOT touch the strategy row.
	time.Sleep(200 * time.Millisecond)
	var tpPct float64
	s.pool.QueryRow(ctx, `SELECT tp_pct FROM strategies WHERE id=$1`, stratID).Scan(&tpPct)
	if tpPct != 2.0 {
		t.Errorf("expected strategy tp_pct to stay 2.0 without applyToActive, got %v", tpPct)
	}
}

// TestPatchBot_ApplyToActiveTrue_UpdatesActiveStrategy: с applyToActive=true изменённые
// синхронизируемые поля из шаблона бота применяются к активной стратегии этого бота.
func TestPatchBot_ApplyToActiveTrue_UpdatesActiveStrategy(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "yesactive")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "yesactive")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"tp_pct":2.0,"grid_levels":5,"grid_active":3,"grid_step_pct":1.0,"grid_size_usdt":100}'::jsonb WHERE id=$1`,
		botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, grid_levels, grid_active, grid_step_pct, grid_size_usdt)
		 VALUES ($1,$2,$3,'YESACTUSDT','linear','long','grid','active',2.0,5,3,1.0,100) RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body := `{"strategyConfig":{"tp_pct":7.7,"grid_levels":5,"grid_active":3,"grid_step_pct":1.0,"grid_size_usdt":100},"applyToActive":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	// syncBotStrategies runs as `go ...` — poll briefly for the async UPDATE to land.
	deadline := time.Now().Add(2 * time.Second)
	var tpPct float64
	for time.Now().Before(deadline) {
		s.pool.QueryRow(ctx, `SELECT tp_pct FROM strategies WHERE id=$1`, stratID).Scan(&tpPct)
		if tpPct == 7.7 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tpPct != 7.7 {
		t.Errorf("expected strategy tp_pct=7.7 after applyToActive=true, got %v", tpPct)
	}
}
```

- [ ] **Step 8: Прогнать тесты**

Run: `go test -tags=integration ./services/api-gateway/... -run "TestPatchBot_ApplyToActive" -v`
Expected: `PASS` для обоих.

Run: `go test -tags=integration ./services/api-gateway/... -run TestPatchBot -v`
Expected: все существующие `TestPatchBot_*` тесты (`OwnerCanUpdate`, `LinkedSubscriptionBlocked`) по-прежнему `PASS` — регресс на существующее поведение `PatchBot`.

- [ ] **Step 9: Коммит**

```bash
git add services/api-gateway/bots_handler.go services/api-gateway/bots_apply_to_active_test.go frontend/src/features/bots/types.ts
git commit -m "feat(bots): add applyToActive flag — bot template edits no longer silently cascade to open strategies

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: Frontend — общий хелпер сравнения синхронизируемых полей

**Files:**
- Create: `frontend/src/features/bots/syncableSettingsFields.ts`
- Test: `frontend/src/__tests__/syncableSettingsFields.test.ts`

- [ ] **Step 1: Написать падающий тест**

Создать `frontend/src/__tests__/syncableSettingsFields.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { SYNCABLE_SETTINGS_FIELDS, hasSyncableDiff } from '../features/bots/syncableSettingsFields'

describe('hasSyncableDiff', () => {
  it('returns false when nothing in the synchronizable set changed', () => {
    const a = { tp_pct: 2, symbol: 'BTCUSDT', status: 'active' }
    const b = { tp_pct: 2, symbol: 'ETHUSDT', status: 'stopped' }
    expect(hasSyncableDiff(a, b)).toBe(false)
  })

  it('returns true when a synchronizable scalar field changed', () => {
    const a = { tp_pct: 2 }
    const b = { tp_pct: 3 }
    expect(hasSyncableDiff(a, b)).toBe(true)
  })

  it('returns true when a synchronizable array field changed', () => {
    const a = { matrix_levels: [{ tp_pct: 2, size_pct: 10 }] }
    const b = { matrix_levels: [{ tp_pct: 3, size_pct: 10 }] }
    expect(hasSyncableDiff(a, b)).toBe(true)
  })

  it('returns false when an array field is deep-equal but a different object reference', () => {
    const a = { matrix_levels: [{ tp_pct: 2, size_pct: 10 }] }
    const b = { matrix_levels: [{ tp_pct: 2, size_pct: 10 }] }
    expect(hasSyncableDiff(a, b)).toBe(false)
  })

  it('SYNCABLE_SETTINGS_FIELDS does not include instance-specific fields', () => {
    expect(SYNCABLE_SETTINGS_FIELDS).not.toContain('symbol')
    expect(SYNCABLE_SETTINGS_FIELDS).not.toContain('direction')
    expect(SYNCABLE_SETTINGS_FIELDS).not.toContain('status')
  })
})
```

- [ ] **Step 2: Прогнать тест — убедиться что падает**

Run: `cd frontend && npx vitest run src/__tests__/syncableSettingsFields.test.ts`
Expected: FAIL — `Cannot find module '../features/bots/syncableSettingsFields'`

- [ ] **Step 3: Реализовать хелпер**

Создать `frontend/src/features/bots/syncableSettingsFields.ts`:

```ts
// Fields shared between an open strategy's row and a bot's strategy_config template —
// used to decide whether an edit is worth offering to sync in either direction (see
// docs/superpowers/specs/2026-09-07-bot-strategy-settings-sync-design.md). Deliberately
// excludes instance-specific fields (symbol, direction, category, status) that exist only
// on one side, or mean something different there (a bot's `direction` is a template
// default, not a live position's direction).
export const SYNCABLE_SETTINGS_FIELDS = [
  'grid_levels', 'grid_active', 'grid_step_pct', 'grid_size_usdt',
  'tp_mode', 'tp_pct', 'sl_type', 'sl_pct', 'signal_filter',
  'leverage', 'margin_type', 'hedge_mode', 'entry_order_type',
  'signal_configs', 'steps',
  'trailing_stop_enabled', 'trailing_activation_pct', 'trailing_callback_pct',
  'matrix_levels', 'matrix_entry_level', 'safe_zone_pct',
  'protected_build', 'matrix_rebuild_on_sl', 'matrix_rebuild_from_entry', 'relative_slots',
] as const

export type SyncableSettingsField = typeof SYNCABLE_SETTINGS_FIELDS[number]

/** True if any field in SYNCABLE_SETTINGS_FIELDS differs (deep) between a and b. */
export function hasSyncableDiff(
  a: Record<string, unknown>,
  b: Record<string, unknown>,
): boolean {
  return SYNCABLE_SETTINGS_FIELDS.some(
    field => JSON.stringify(a[field] ?? null) !== JSON.stringify(b[field] ?? null),
  )
}
```

- [ ] **Step 4: Прогнать тест — убедиться что проходит**

Run: `cd frontend && npx vitest run src/__tests__/syncableSettingsFields.test.ts`
Expected: `PASS` (5/5).

- [ ] **Step 5: Коммит**

```bash
git add frontend/src/features/bots/syncableSettingsFields.ts frontend/src/__tests__/syncableSettingsFields.test.ts
git commit -m "feat(bots): add shared helper to detect synchronizable-settings diffs

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: Frontend — общая модалка подтверждения

**Files:**
- Create: `frontend/src/features/bots/components/SettingsSyncConfirmModal.tsx`

- [ ] **Step 1: Создать компонент по образцу `ResetStatsConfirmModal.tsx`**

Создать `frontend/src/features/bots/components/SettingsSyncConfirmModal.tsx`:

```tsx
import { useEffect } from 'react';
import { GitBranch } from 'lucide-react';

type Props = {
  title:        string;
  description:  string;
  cancelLabel:  string;
  confirmLabel: string;
  onConfirm:    () => void;
  onCancel:     () => void;
};

/** Общая модалка подтверждения для обоих направлений синхронизации настроек
 *  «бот ↔ открытая стратегия» — см. docs/superpowers/specs/2026-09-07-bot-strategy-settings-sync-design.md */
export function SettingsSyncConfirmModal({ title, description, cancelLabel, confirmLabel, onConfirm, onCancel }: Props) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-[10000] flex items-center justify-center"
      style={{ background: 'rgba(0,0,0,0.72)', backdropFilter: 'blur(4px)' }}
      onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
    >
      <div
        className="relative w-full max-w-[420px] rounded-2xl border p-6"
        style={{
          background:  'linear-gradient(180deg,#10141f 0%,#0c1018 100%)',
          borderColor: 'rgba(255,255,255,0.08)',
          boxShadow:   '0 32px 80px -20px rgba(0,0,0,0.8)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-[#5b8cff]/25 bg-[#5b8cff]/[.12]">
            <GitBranch size={16} className="text-[#5b8cff]" strokeWidth={2} />
          </div>
          <div>
            <div className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              {title}
            </div>
            <div className="mt-0.5 text-[12px] leading-[1.5] text-slate-400">
              {description}
            </div>
          </div>
        </div>

        <div className="flex gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="flex-1 rounded-lg border border-white/[.08] bg-white/[.04] py-2.5 text-sm font-semibold text-slate-300 hover:bg-white/[.08] transition-colors"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className="flex-1 rounded-lg border border-[#5b8cff]/40 bg-[#5b8cff]/[.18] py-2.5 text-sm font-semibold text-[#5b8cff] hover:bg-[#5b8cff]/[.28] transition-colors"
          >
            {confirmLabel}
          </button>
        </div>

        <button
          type="button"
          onClick={onCancel}
          className="absolute right-4 top-4 flex h-7 w-7 items-center justify-center rounded-full text-slate-500 hover:bg-white/[.06] hover:text-slate-300 transition-colors"
        >
          <svg width="13" height="13" viewBox="0 0 13 13" fill="none">
            <path d="M1 1l11 11M12 1L1 12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"/>
          </svg>
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Коммит**

```bash
git add frontend/src/features/bots/components/SettingsSyncConfirmModal.tsx
git commit -m "feat(bots): add shared settings-sync confirm modal component

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: Frontend — «Применить к боту?» в `StrategyModal.tsx`

**Files:**
- Modify: `frontend/src/api/strategies.ts`
- Modify: `frontend/src/components/strategies/StrategyModal.tsx`
- Test: `frontend/src/__tests__/StrategyModal.applyToBot.test.tsx`

- [ ] **Step 1: Расширить `updateStrategy`, чтобы принимать `applyToBot`**

В `frontend/src/api/strategies.ts`, заменить:

```ts
export async function updateStrategy(id: string, data: StrategyFormData): Promise<void> {
  await apiClient.put(`/strategies/${id}`, data)
}
```

на:

```ts
export async function updateStrategy(
  id: string,
  data: StrategyFormData,
  opts?: { applyToBot?: boolean },
): Promise<void> {
  await apiClient.put(`/strategies/${id}`, opts?.applyToBot ? { ...data, applyToBot: true } : data)
}
```

- [ ] **Step 2: Сделать поле TP % и кнопку «Сохранить» адресуемыми для теста**

`StrategyModal.tsx` — большая форма с кастомными виджетами без `htmlFor`/`aria-label` (визуальный `<label>` не связан с полем через атрибуты), поэтому для надёжного теста добавляем `data-testid`.

В `frontend/src/components/strategies/StrategyModal.tsx`, найти `function NumericInput({ value, onChange, className, step, placeholder, disabled, errorMsg }: {` (строка 131) и добавить проброс `data-testid`. Заменить:

```ts
function NumericInput({ value, onChange, className, step, placeholder, disabled, errorMsg }: {
  value: number
  onChange: (v: number) => void
  className?: string
  step?: string | number
  placeholder?: string
  disabled?: boolean
  errorMsg?: string | null
}) {
```

на:

```ts
function NumericInput({ value, onChange, className, step, placeholder, disabled, errorMsg, testId }: {
  value: number
  onChange: (v: number) => void
  className?: string
  step?: string | number
  placeholder?: string
  disabled?: boolean
  errorMsg?: string | null
  testId?: string
}) {
```

и внутри JSX (строка ~160-171) добавить `data-testid={testId}` к `<input`:

```ts
      <input
        type="text"
        inputMode="decimal"
        step={step}
        placeholder={placeholder}
        disabled={disabled}
        data-testid={testId}
        value={draft !== null ? draft : value}
        onChange={handleChange}
        onBlur={handleBlur}
        onFocus={() => setDraft(String(value))}
        className={className}
      />
```

Найти использование для основного TP-поля (строка 1241):

```tsx
                            ? <NumericInput step="0.1" value={form.tp_pct} onChange={v => patch({ tp_pct: v })} className={errCls(fieldErrors.tp_pct)} />
```

заменить на:

```tsx
                            ? <NumericInput step="0.1" value={form.tp_pct} onChange={v => patch({ tp_pct: v })} className={errCls(fieldErrors.tp_pct)} testId="strategy-tp-pct-input" />
```

Найти кнопку сохранения (строка ~1373, `{saving ? 'Сохранение…' : strategy ? 'Сохранить' : 'Создать стратегию'}`) и добавить `data-testid="strategy-save-button"` на её родительский `<button>` (найти открывающий тег `<button` этой кнопки чуть выше строки 1373 и добавить атрибут `data-testid="strategy-save-button"` в его список атрибутов).

- [ ] **Step 3: Добавить состояние и диалог в `StrategyModal.tsx`**

В `frontend/src/components/strategies/StrategyModal.tsx`, добавить импорт (рядом с остальными импортами компонентов в начале файла):

```ts
import { SettingsSyncConfirmModal } from '../../features/bots/components/SettingsSyncConfirmModal'
import { hasSyncableDiff } from '../../features/bots/syncableSettingsFields'
```

Найти объявления состояния формы (рядом с `const [saving, setSaving] = useState(...)` или аналогичным) и добавить:

```ts
  const [showApplyToBotConfirm, setShowApplyToBotConfirm] = useState(false)
  const [pendingPayload, setPendingPayload] = useState<typeof form | null>(null)
```

- [ ] **Step 4: Врезать проверку перед сохранением**

Найти текущий блок сохранения (строка ~387-406):

```ts
    setSaving(true)
    setError(null)
    try {
      const isMatrix = form.strategy_type === 'matrix'
      const totalMatrixLevels = aboveLevels.length + 1 + belowLevels.length
      const payload = {
        ...form,
        grid_levels: isMatrix ? totalMatrixLevels : (form.steps.length || 1),
        grid_active: isMatrix
          ? (form.grid_active > 0 ? form.grid_active : totalMatrixLevels)
          : (form.grid_active > 0 ? form.grid_active : form.steps.length || 1),
        grid_step_pct: isMatrix ? (belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0) : (form.steps[0]?.price_move_pct ?? 0),
        signal_filter: form.signal_configs.length > 0,
      }
      if (strategy) {
        await updateStrategy(strategy.id, payload as any)
      } else {
        await createStrategy(payload as any)
      }
      onSaved()
    } catch (e: any) {
```

заменить на:

```ts
    setSaving(true)
    setError(null)
    try {
      const isMatrix = form.strategy_type === 'matrix'
      const totalMatrixLevels = aboveLevels.length + 1 + belowLevels.length
      const payload = {
        ...form,
        grid_levels: isMatrix ? totalMatrixLevels : (form.steps.length || 1),
        grid_active: isMatrix
          ? (form.grid_active > 0 ? form.grid_active : totalMatrixLevels)
          : (form.grid_active > 0 ? form.grid_active : form.steps.length || 1),
        grid_step_pct: isMatrix ? (belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0) : (form.steps[0]?.price_move_pct ?? 0),
        signal_filter: form.signal_configs.length > 0,
      }
      if (strategy) {
        if (strategy.bot_id && hasSyncableDiff(payload as Record<string, unknown>, strategy as unknown as Record<string, unknown>)) {
          setPendingPayload(payload)
          setShowApplyToBotConfirm(true)
          setSaving(false)
          return
        }
        await updateStrategy(strategy.id, payload as any)
      } else {
        await createStrategy(payload as any)
      }
      onSaved()
    } catch (e: any) {
```

- [ ] **Step 5: Добавить обработчики диалога и вынести финальный сейв в отдельную функцию**

Сразу после функции, содержащей блок из Step 3 (закрывающая `}` перед `finally`/концом `handleSave`-подобной функции — использовать существующие `catch`/`finally` этой же функции, не создавать новый try/catch), добавить рядом два новых обработчика (можно сразу под определением этой функции):

```ts
  async function finishApplyToBot(applyToBot: boolean) {
    setShowApplyToBotConfirm(false)
    if (!strategy || !pendingPayload) return
    setSaving(true)
    setError(null)
    try {
      await updateStrategy(strategy.id, pendingPayload as any, { applyToBot })
      onSaved()
    } catch (e: any) {
      const status = e?.response?.status
      const data = e?.response?.data
      const serverMsg = typeof data === 'object' ? data?.error : typeof data === 'string' ? data : null
      setError(serverMsg ? `${serverMsg}${status ? ` (HTTP ${status})` : ''}` : (e?.message ?? 'Неизвестная ошибка'))
    } finally {
      setSaving(false)
      setPendingPayload(null)
    }
  }
```

- [ ] **Step 6: Отрендерить модалку**

В JSX-возврате компонента (перед закрывающим тегом корневого элемента формы, там же где обычно рендерятся другие условные модалки/порталы этого файла) добавить:

```tsx
      {showApplyToBotConfirm && strategy && (
        <SettingsSyncConfirmModal
          title={`Применить к боту «${strategy.bot_name ?? 'Bot'}»?`}
          description="Изменённые параметры можно сохранить как настройки по умолчанию для бота — тогда новые сделки бота будут открываться с этими же значениями. Другие уже открытые стратегии бота это не затронет."
          cancelLabel="Нет, только эта стратегия"
          confirmLabel="Да, применить к боту"
          onCancel={() => finishApplyToBot(false)}
          onConfirm={() => finishApplyToBot(true)}
        />
      )}
```

- [ ] **Step 7: Написать тест**

Создать `frontend/src/__tests__/StrategyModal.applyToBot.test.tsx`. `StrategyModal`'s `Props` — только `strategy?, filledLevels?, defaultAccountId?, onClose, onSaved, liveSignal?` (нет `accounts`). На монтировании компонент вызывает `getStrategyState`/`getInstrumentConstraints` — мокаем их вместе с `updateStrategy`, чтобы тест не делал реальных сетевых запросов:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StrategyModal } from '../components/strategies/StrategyModal'
import * as strategiesApi from '../api/strategies'
import type { Strategy } from '../types'

vi.mock('../api/strategies', async (importOriginal) => {
  const actual = await importOriginal<typeof strategiesApi>()
  return {
    ...actual,
    updateStrategy: vi.fn().mockResolvedValue(undefined),
    getStrategyState: vi.fn().mockResolvedValue({ cycle_num: 1, start_price: 0, levels: [] }),
    getInstrumentConstraints: vi.fn().mockResolvedValue({ max_leverage: 100, min_order_usdt: 0, tick_size: 0.01, qty_step: 0.001, min_qty: 0 }),
  }
})

const baseStrategy: Strategy = {
  id: 'strat-1', account_id: 'acc-1', symbol: 'BTCUSDT', category: 'linear', direction: 'long',
  status: 'active', grid_levels: 5, grid_active: 3, max_stop_active: 0, grid_step_pct: 1,
  grid_size_usdt: 100, tp_mode: 'total', tp_pct: 2, sl_type: 'conditional', sl_pct: -5,
  signal_filter: false, leverage: 1, margin_type: 'isolated', hedge_mode: false,
  strategy_type: 'grid', entry_order_type: 'limit', signal_configs: [], steps: [],
  trailing_stop_enabled: false, trailing_activation_pct: null, trailing_callback_pct: null,
  created_at: '', updated_at: '', volume_usdt: 0, active_levels: 0, last_pnl: 0,
  bot_id: 'bot-1', bot_name: 'Gonchar 2.0',
}

describe('StrategyModal — apply to bot confirm', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('shows the confirm dialog when a bot-owned strategy changes a syncable field', async () => {
    render(<StrategyModal strategy={baseStrategy} onClose={() => {}} onSaved={() => {}} />)

    const tpInput = await screen.findByTestId('strategy-tp-pct-input')
    fireEvent.change(tpInput, { target: { value: '5' } })
    fireEvent.blur(tpInput)
    fireEvent.click(screen.getByTestId('strategy-save-button'))

    expect(await screen.findByText(/применить к боту/i)).toBeInTheDocument()
  })

  it('calls updateStrategy with applyToBot=true when confirmed', async () => {
    render(<StrategyModal strategy={baseStrategy} onClose={() => {}} onSaved={() => {}} />)

    const tpInput = await screen.findByTestId('strategy-tp-pct-input')
    fireEvent.change(tpInput, { target: { value: '5' } })
    fireEvent.blur(tpInput)
    fireEvent.click(screen.getByTestId('strategy-save-button'))
    fireEvent.click(await screen.findByRole('button', { name: /да, применить к боту/i }))

    await waitFor(() => {
      expect(strategiesApi.updateStrategy).toHaveBeenCalledWith(
        'strat-1',
        expect.objectContaining({ tp_pct: 5 }),
        { applyToBot: true },
      )
    })
  })

  it('does not show the confirm dialog for a manual (non-bot) strategy', async () => {
    render(<StrategyModal strategy={{ ...baseStrategy, bot_id: null, bot_name: null }} onClose={() => {}} onSaved={() => {}} />)

    const tpInput = await screen.findByTestId('strategy-tp-pct-input')
    fireEvent.change(tpInput, { target: { value: '5' } })
    fireEvent.blur(tpInput)
    fireEvent.click(screen.getByTestId('strategy-save-button'))

    await waitFor(() => expect(strategiesApi.updateStrategy).toHaveBeenCalled())
    expect(screen.queryByText(/применить к боту/i)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 8: Прогнать тесты**

Run: `cd frontend && npx vitest run src/__tests__/StrategyModal.applyToBot.test.tsx`
Expected: `PASS` (3/3).

Run: `cd frontend && npx vitest run src/__tests__/StrategyModal`
Expected: остальные существующие тесты `StrategyModal` (если есть) по-прежнему `PASS`.

- [ ] **Step 9: Коммит**

```bash
git add frontend/src/api/strategies.ts frontend/src/components/strategies/StrategyModal.tsx frontend/src/__tests__/StrategyModal.applyToBot.test.tsx
git commit -m "feat(strategies): confirm before syncing an open bot-strategy edit back into the bot template

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 6: Frontend — «Применить к активным стратегиям?» в `BotForm.tsx` / `HedgeBotForm.tsx` (standalone)

**Files:**
- Modify: `frontend/src/features/bots/components/BotForm.tsx`
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx`

- [ ] **Step 1: `BotForm.tsx` — импорт и состояние**

В `frontend/src/features/bots/components/BotForm.tsx`, добавить импорт рядом с `import { ResetStatsConfirmModal } from './ResetStatsConfirmModal';` (строка ~16):

```ts
import { SettingsSyncConfirmModal } from './SettingsSyncConfirmModal';
import { hasSyncableDiff } from '../syncableSettingsFields';
```

Рядом с `const [showResetStatsConfirm, setShowResetStatsConfirm] = useState(false);` (строка ~163) добавить:

```ts
  const [showApplyActiveConfirm, setShowApplyActiveConfirm] = useState(false);
  const applyActiveDecisionRef = useRef<boolean | null>(null);
```

(добавить `useRef` в импорт из `'react'` наверху файла, если его там ещё нет).

- [ ] **Step 2: `BotForm.tsx` — врезать проверку в `handleSubmit`, не показывать при `embedded`**

Найти в `handleSubmit` (строка ~461-481):

```ts
  const handleSubmit = async () => {
    if (showCoinFilterConfirm) return;
    // Мультибот owns name/description itself (Основное tab is hidden when embedded — see
    // outerTabs above) and overwrites them in the merged payload, so this form's own copies
    // never get edited and would otherwise block submission by staying empty forever.
    if (!embedded) {
      if (!name.trim()) { resolveEmbedded(null); return; }
      if (!description.trim()) {
        setSubmitError('Добавьте описание бота — оно отображается в карточке');
        resolveEmbedded(null);
        return;
      }
    }

    // Warn about stats reset when editing a bot that already has trade history.
    if (bot && (bot.tradesTotal ?? 0) > 0) {
      setShowResetStatsConfirm(true);
      return;
    }
    await doSubmit();
  };
```

заменить на:

```ts
  const handleSubmit = async () => {
    if (showCoinFilterConfirm) return;
    // Мультибот owns name/description itself (Основное tab is hidden when embedded — see
    // outerTabs above) and overwrites them in the merged payload, so this form's own copies
    // never get edited and would otherwise block submission by staying empty forever.
    if (!embedded) {
      if (!name.trim()) { resolveEmbedded(null); return; }
      if (!description.trim()) {
        setSubmitError('Добавьте описание бота — оно отображается в карточке');
        resolveEmbedded(null);
        return;
      }
    }

    // Warn about stats reset when editing a bot that already has trade history.
    if (bot && (bot.tradesTotal ?? 0) > 0) {
      setShowResetStatsConfirm(true);
      return;
    }

    // Ask whether to cascade the new template into the bot's currently open strategies.
    // Skipped when embedded (part of a МультиБот save) — MultiBotForm asks this once for
    // both legs combined instead, see Task 7.
    if (!embedded && applyActiveDecisionRef.current === null &&
        bot && (bot.activeStrategiesCount ?? 0) > 0 &&
        hasSyncableDiff(buildPayload().strategyConfig ?? {}, bot.strategyConfig ?? {})) {
      setShowApplyActiveConfirm(true);
      return;
    }
    await doSubmit();
  };
```

- [ ] **Step 3: `BotForm.tsx` — прокинуть решение в `buildPayload`/`doSubmit`**

Найти конец `buildPayload()` (строка ~424, `return { ... ignoreCoinFilter: false, };`) и заменить закрывающую строку:

```ts
      ignoreCoinFilter: false,
    };
  }
```

на:

```ts
      ignoreCoinFilter: false,
      ...(applyActiveDecisionRef.current !== null ? { applyToActive: applyActiveDecisionRef.current } : {}),
    };
  }
```

Добавить обработчики диалога сразу после `handleSubmit`:

```ts
  const handleApplyActiveConfirm = (apply: boolean) => {
    applyActiveDecisionRef.current = apply;
    setShowApplyActiveConfirm(false);
    void handleSubmit();
  };
```

- [ ] **Step 4: `BotForm.tsx` — отрендерить модалку**

Рядом с существующим рендером `showResetStatsConfirm` (строка ~1757-1766) добавить:

```tsx
      {showApplyActiveConfirm && bot && createPortal(
        <SettingsSyncConfirmModal
          title="Применить к активным стратегиям?"
          description={`У бота «${bot.name}» сейчас ${bot.activeStrategiesCount} открытые стратегии. Применить новые настройки к ним прямо сейчас (TP/SL и ордера на бирже будут пересчитаны немедленно), или сохранить только для новых стратегий, не трогая уже открытые?`}
          cancelLabel="Нет, только новые стратегии"
          confirmLabel="Да, применить сейчас"
          onCancel={() => handleApplyActiveConfirm(false)}
          onConfirm={() => handleApplyActiveConfirm(true)}
        />,
        document.body
      )}
```

- [ ] **Step 5: `HedgeBotForm.tsx` — те же четыре изменения**

`useState`/`useRef`/`createPortal` уже импортированы в `frontend/src/features/bots/components/HedgeBotForm.tsx` (строки 1-2) — новых импортов React не требуется, только сама модалка:

Добавить импорт рядом с `import { ResetStatsConfirmModal } from './ResetStatsConfirmModal';` (строка 13):

```ts
import { SettingsSyncConfirmModal } from './SettingsSyncConfirmModal';
import { hasSyncableDiff } from '../syncableSettingsFields';
```

Рядом с объявлением `showResetStatsConfirm` (строка 225) добавить:

```ts
  const [showApplyActiveConfirm, setShowApplyActiveConfirm] = useState(false);
  const applyActiveDecisionRef = useRef<boolean | null>(null);
```

Заменить `handleSubmit` (строки 472-483):

```ts
  const handleSubmit = async () => {
    // Мультибот owns name itself (Основное tab hidden when embedded, see outerTabs below)
    // and overwrites it in the merged payload — this form's own copy never gets edited.
    if (!embedded && !name.trim()) { resolveEmbedded(null); return; }

    // Warn about stats reset when editing a bot that already has trade history.
    if (bot && (bot.tradesTotal ?? 0) > 0) {
      setShowResetStatsConfirm(true);
      return;
    }
    await doSubmit();
  };
```

на:

```ts
  const handleSubmit = async () => {
    // Мультибот owns name itself (Основное tab hidden when embedded, see outerTabs below)
    // and overwrites it in the merged payload — this form's own copy never gets edited.
    if (!embedded && !name.trim()) { resolveEmbedded(null); return; }

    // Warn about stats reset when editing a bot that already has trade history.
    if (bot && (bot.tradesTotal ?? 0) > 0) {
      setShowResetStatsConfirm(true);
      return;
    }

    // Ask whether to cascade the new template into the bot's currently open strategies.
    // Skipped when embedded (part of a МультиБот save) — MultiBotForm asks this once for
    // both legs combined instead, see Task 7.
    if (!embedded && applyActiveDecisionRef.current === null &&
        bot && (bot.activeStrategiesCount ?? 0) > 0 &&
        hasSyncableDiff(buildPayload().strategyConfig ?? {}, bot.strategyConfig ?? {})) {
      setShowApplyActiveConfirm(true);
      return;
    }
    await doSubmit();
  };

  const handleApplyActiveConfirm = (apply: boolean) => {
    applyActiveDecisionRef.current = apply;
    setShowApplyActiveConfirm(false);
    void handleSubmit();
  };
```

Заменить конец `buildPayload()` (строки 440-446):

```ts
      maxShortStrategies,
      maxMarginUsdt,
      maxSymConsecutiveRuns,
      accountId: selectedAccountId || null,
      autoMode,
    };
  }
```

на:

```ts
      maxShortStrategies,
      maxMarginUsdt,
      maxSymConsecutiveRuns,
      accountId: selectedAccountId || null,
      autoMode,
      ...(applyActiveDecisionRef.current !== null ? { applyToActive: applyActiveDecisionRef.current } : {}),
    };
  }
```

Добавить рендер модалки рядом с существующим `showResetStatsConfirm` порталом (строки 1978-1987):

```tsx
      {showApplyActiveConfirm && bot && createPortal(
        <SettingsSyncConfirmModal
          title="Применить к активным стратегиям?"
          description={`У бота «${bot.name}» сейчас ${bot.activeStrategiesCount} открытые стратегии. Применить новые настройки к ним прямо сейчас (TP/SL и ордера на бирже будут пересчитаны немедленно), или сохранить только для новых стратегий, не трогая уже открытые?`}
          cancelLabel="Нет, только новые стратегии"
          confirmLabel="Да, применить сейчас"
          onCancel={() => handleApplyActiveConfirm(false)}
          onConfirm={() => handleApplyActiveConfirm(true)}
        />,
        document.body
      )}
```

- [ ] **Step 6: Ручная проверка (нет автотеста на этот шаг — визуальная форма)**

Так как `BotForm.tsx`/`HedgeBotForm.tsx` — большие интерактивные формы без существующей vitest-обвязки для полного flow сохранения (в отличие от `StrategyModal`, где Task 5 уже завёл минимальный тест), проверить руками:
1. Открыть фронт (`npm run dev` в `frontend/`), зайти на страницу ботов.
2. Отредактировать бота с `activeStrategiesCount > 0`, изменить TP.
3. Убедиться, что при сохранении показывается диалог «Применить к активным стратегиям?».
4. Нажать «Да, применить сейчас» — убедиться, что запрос `PATCH /bots/{id}` в Network содержит `"applyToActive":true`.
5. Повторить с ботом без активных стратегий — диалог не должен появляться.

- [ ] **Step 7: Коммит**

```bash
git add frontend/src/features/bots/components/BotForm.tsx frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "feat(bots): confirm before cascading a bot template edit into its open strategies

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 7: Frontend — единый диалог для `MultiBotForm.tsx`

**Files:**
- Modify: `frontend/src/features/bots/components/MultiBotForm.tsx`

- [ ] **Step 1: Добавить импорт и состояние**

В `frontend/src/features/bots/components/MultiBotForm.tsx`, добавить импорт:

```ts
import { SettingsSyncConfirmModal } from './SettingsSyncConfirmModal';
import { hasSyncableDiff } from '../syncableSettingsFields';
```

Рядом с остальными `useState` в компоненте добавить:

```ts
  const [showApplyActiveConfirm, setShowApplyActiveConfirm] = useState(false);
  const applyActiveDecisionRef = useRef<boolean | null>(null);
```

(добавить `useRef` в импорт из `'react'`, если его там ещё нет).

- [ ] **Step 2: Врезать проверку в `handleSave` перед PATCH-запросами (только режим редактирования)**

Найти в `handleSave` (строка ~112-134):

```ts
      if (!signalPayload || !hedgePayload) {
        setSubmitting(false);
        return;
      }

      const identity = {
        name: name.trim(),
        description: description.trim(),
        fullDescription: fullDescription.trim() || undefined,
        avatarUrl: avatarUrl || undefined,
        isPublic: false, // publishing a Мультибот to the catalog isn't wired up yet
      };

      if (isEdit && signalBot && hedgeBot) {
        await Promise.all([
          apiClient.patch(`/bots/${signalBot.id}`, {
            ...signalPayload, ...identity,
            autoMode, maxStrategies, maxLongStrategies, maxShortStrategies, maxMarginUsdt, maxSymConsecutiveRuns,
          }),
          apiClient.patch(`/bots/${hedgeBot.id}`, {
            ...hedgePayload, ...identity,
          }),
        ]);
      } else {
```

заменить на:

```ts
      if (!signalPayload || !hedgePayload) {
        setSubmitting(false);
        return;
      }

      const identity = {
        name: name.trim(),
        description: description.trim(),
        fullDescription: fullDescription.trim() || undefined,
        avatarUrl: avatarUrl || undefined,
        isPublic: false, // publishing a Мультибот to the catalog isn't wired up yet
      };

      if (isEdit && signalBot && hedgeBot) {
        const hasActive = (signalBot.activeStrategiesCount ?? 0) > 0 || (hedgeBot.activeStrategiesCount ?? 0) > 0;
        const settingsChanged =
          hasSyncableDiff(signalPayload.strategyConfig ?? {}, signalBot.strategyConfig ?? {}) ||
          hasSyncableDiff(hedgePayload.strategyConfig ?? {}, hedgeBot.strategyConfig ?? {});
        if (applyActiveDecisionRef.current === null && hasActive && settingsChanged) {
          setPendingSavePayloads({ signalPayload, hedgePayload, identity });
          setShowApplyActiveConfirm(true);
          setSubmitting(false);
          return;
        }
        const applyToActive = applyActiveDecisionRef.current ?? false;
        await Promise.all([
          apiClient.patch(`/bots/${signalBot.id}`, {
            ...signalPayload, ...identity, applyToActive,
            autoMode, maxStrategies, maxLongStrategies, maxShortStrategies, maxMarginUsdt, maxSymConsecutiveRuns,
          }),
          apiClient.patch(`/bots/${hedgeBot.id}`, {
            ...hedgePayload, ...identity, applyToActive,
          }),
        ]);
      } else {
```

- [ ] **Step 3: Добавить состояние для отложенных payload'ов и обработчик подтверждения**

Рядом с остальными `useState` (там же, где добавлено в Step 1):

```ts
  const [pendingSavePayloads, setPendingSavePayloads] = useState<{
    signalPayload: CreateBotInput; hedgePayload: CreateBotInput; identity: Record<string, unknown>;
  } | null>(null);
```

После `handleSave`, добавить:

```ts
  const handleApplyActiveConfirm = (apply: boolean) => {
    applyActiveDecisionRef.current = apply;
    setShowApplyActiveConfirm(false);
    void handleSave();
  };
```

**Примечание для исполнителя:** `handleSave` в Step 2 обращается к `pendingSavePayloads` только для рендера текста диалога (количество стратегий); повторный вызов `handleSave()` из `handleApplyActiveConfirm` заново вызывает `trySubmit()` на обеих встроенных формах — это ожидаемо (тот же паттерн, что уже использует `BotForm.tsx` для `showCoinFilterConfirm`/`showResetStatsConfirm`), и должно быть безопасно, поскольку встроенные формы хранят своё собственное состояние полей независимо от количества сохранений.

- [ ] **Step 4: Отрендерить модалку**

В JSX-возврате `MultiBotForm.tsx`, рядом с закрывающим тегом корневого `<div>` модалки, добавить:

```tsx
      {showApplyActiveConfirm && pendingSavePayloads && signalBot && hedgeBot && createPortal(
        <SettingsSyncConfirmModal
          title="Применить к активным стратегиям?"
          description={`У этого МультиБота сейчас ${(signalBot.activeStrategiesCount ?? 0) + (hedgeBot.activeStrategiesCount ?? 0)} открытые стратегии (обе ноги). Применить новые настройки к ним прямо сейчас (TP/SL и ордера на бирже будут пересчитаны немедленно), или сохранить только для новых стратегий, не трогая уже открытые?`}
          cancelLabel="Нет, только новые стратегии"
          confirmLabel="Да, применить сейчас"
          onCancel={() => handleApplyActiveConfirm(false)}
          onConfirm={() => handleApplyActiveConfirm(true)}
        />,
        document.body
      )}
```

Добавить `import { createPortal } from 'react-dom';` наверху файла, если его там ещё нет (проверить существующие импорты — `BotForm.tsx`/`HedgeBotForm.tsx` уже его используют, `MultiBotForm.tsx`, скорее всего, ещё нет, так как раньше не показывал порталов).

- [ ] **Step 5: Ручная проверка**

1. Открыть страницу ботов, отредактировать существующий МультиБот, у которого есть открытые стратегии хотя бы на одной ноге.
2. Изменить TP на вкладке «Мэйн позиция» или «Хедж позиция».
3. Нажать «Сохранить» — убедиться, что диалог показывается **один раз** (не дважды).
4. Подтвердить — убедиться в Network, что **оба** `PATCH /bots/{signalId}` и `PATCH /bots/{hedgeId}` содержат `"applyToActive":true`.

- [ ] **Step 6: Коммит**

```bash
git add frontend/src/features/bots/components/MultiBotForm.tsx
git commit -m "feat(bots): single combined applyToActive confirm for МультиБот edits

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 8: Финальное тест-ревью

**Files:** нет новых файлов — только прогон.

- [ ] **Step 1: Полный прогон бэкенд-тестов затронутых пакетов**

Run: `go build ./...`
Expected: без ошибок.

Run: `go test -tags=integration ./services/api-gateway/... -v 2>&1 | tail -100`
Expected: все тесты `PASS`, включая новые из Task 1/2 и существующие `TestPatchBot_*`, `TestUpdateStrategy*` (если такие есть), `TestCreateBotStrategy_ReusesStoppedRow` и другие в этом пакете.

Run: `go test ./pkg/strategy/...`
Expected: `ok` — `Engine.RestartCycle`/`Engine.UpdateTPSL` не менялись, но подтвердить отсутствие регресса.

- [ ] **Step 2: Полный прогон фронтовых тестов**

Run: `cd frontend && npx vitest run`
Expected: все тесты `PASS`, включая новые (`syncableSettingsFields`, `StrategyModal.applyToBot`) и существующие (`HedgeBotForm.embeddedResetConfirm.test.tsx`, `BotForm.embeddedResetConfirm.test.tsx`, `MultiBotForm.test.tsx` и т.д. — особенно важно, что `MultiBotForm.test.tsx` не сломался из-за нового портала/состояния).

- [ ] **Step 3: Доложить результат пользователю**

Явно перечислить (по правилу CLAUDE.md проекта): какие механики проверялись на регресс (`PatchBot` без `applyToActive` = поведение как раньше кроме отсутствия автосинка; `UpdateStrategy` без `applyToBot` = байт-в-байт как раньше; `syncBotStrategies` теперь требует явного вызова; фронтовые формы ботов/стратегий) и с каким результатом.
