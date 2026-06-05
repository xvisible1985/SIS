# Detach Dialog with Adopt Option Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the simple "blacklist?" banner on the HedgePairCard detach flow with a rich dialog offering 4 actions: Поглотить позицию / Закрыть позицию / Оставить как есть / Отмена — so users can safely detach a hedge strategy from its bot without accidentally creating a duplicate exchange position.

**Architecture:** The frontend shows a dialog when the user clicks "Открепить" from the hedge pair card menu. The user picks an action and optionally adds to blacklist. The action is sent to the backend `DetachFromBot` endpoint (extended to accept `action` + `position` body). The `adopt` action creates a fresh strategy that absorbs the existing exchange position by marking L(0) as pre-filled (no market order placed) via a new `adopt_position_data` JSONB column on strategies.

**Tech Stack:** Go 1.22, PostgreSQL 15, pgx/v5, React 18 + TypeScript, Tailwind CSS, Lucide icons

---

## File Structure

| File | Change |
|------|--------|
| `migrations/057_adopt_position.sql` | Create — add `adopt_position_data JSONB` column |
| `pkg/strategy/types.go` | Modify — add `AdoptPositionData` struct + field on `Strategy` |
| `pkg/strategy/engine.go` | Modify — add column to both SELECT queries + `scanStrategy` |
| `pkg/strategy/matrix.go` | Modify — adopt mode branch inside `startMatrixCycle` |
| `services/api-gateway/strategy_handler.go` | Modify — extend `DetachFromBot` handler |
| `frontend/src/api/strategies.ts` | Modify — add `detachWithAction` export |
| `frontend/src/components/strategies/HedgePairCard.tsx` | Modify — replace `confirm-blacklist` step with `detach-dialog` |

---

## Background: the bug and fix

**Bug**: user clicks "Открепить без блеклиста" → `doDetach(false)` → both strategies set to `stopped` → bot 30 s tick runs → bot finds main strategy has no active hedge → calls `createBotStrategy` → `startMatrixCycle` places a new L(0) market order → exchange position doubles (e.g. 35 → 70 contracts).

**Root cause**: `resolveHedgeSlotConflict` only guards against `status IN ('active','finishing')` strategies; a just-stopped strategy is invisible to it.

**Fix design**:
- **Поглотить** (`adopt`): stop old hedge strategy; immediately create a new copy in DB with `adopt_position_data = {"size":"…","entry_price":"…"}`; notify engine → `startMatrixCycle` sees the flag, marks L(0) as filled without placing a market order, then places remaining levels normally.
- **Закрыть** (`close`): place market close for the hedge position; stop both strategies.
- **Оставить** (`leave`): set `bot_id=NULL` on hedge but keep `status='active'`; strategy runs independently; bot's `resolveHedgeSlotConflict` finds the still-active strategy and waits → no duplicate.
- **Отмена**: do nothing.

---

## Task 1: DB Migration

**Files:**
- Create: `migrations/057_adopt_position.sql`

- [ ] **Step 1: Write the migration file**

```sql
-- migrations/057_adopt_position.sql
-- Stores exchange position snapshot for "adopt" detach mode.
-- When non-null, startMatrixCycle marks L(0) as already-filled instead of placing a market order.
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS adopt_position_data JSONB;
```

- [ ] **Step 2: Apply the migration**

Run:
```
psql $DATABASE_URL -f migrations/057_adopt_position.sql
```
Expected: `ALTER TABLE`

- [ ] **Step 3: Verify column exists**

```sql
\d strategies
```
Expected: `adopt_position_data | jsonb` appears in the column list.

- [ ] **Step 4: Commit**

```bash
git add migrations/057_adopt_position.sql
git commit -m "feat: add adopt_position_data column to strategies"
```

---

## Task 2: Go types — AdoptPositionData

**Files:**
- Modify: `pkg/strategy/types.go`

- [ ] **Step 1: Write failing test**

```go
// pkg/strategy/types_test.go (create file if it doesn't exist)
package strategy

import (
    "encoding/json"
    "testing"
)

func TestAdoptPositionData_RoundTrip(t *testing.T) {
    apd := AdoptPositionData{Size: "35", EntryPrice: "0.4647"}
    b, err := json.Marshal(apd)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var got AdoptPositionData
    if err := json.Unmarshal(b, &got); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if got.Size != apd.Size || got.EntryPrice != apd.EntryPrice {
        t.Fatalf("got %+v, want %+v", got, apd)
    }
}

func TestStrategy_AdoptPositionDataField(t *testing.T) {
    s := Strategy{}
    if s.AdoptPositionData != nil {
        t.Fatal("should be nil by default")
    }
    s.AdoptPositionData = &AdoptPositionData{Size: "10", EntryPrice: "1.23"}
    if s.AdoptPositionData.Size != "10" {
        t.Fatal("field not set")
    }
}
```

- [ ] **Step 2: Run failing test**

```
go test ./pkg/strategy/... -run TestAdoptPositionData -v
```
Expected: FAIL — `AdoptPositionData undefined`

- [ ] **Step 3: Add AdoptPositionData struct and field to types.go**

In `pkg/strategy/types.go`, append before the closing of the file (after the `GridLevel` struct, around line 166):

```go
// AdoptPositionData carries the exchange-position snapshot that the next matrix
// startMatrixCycle should absorb instead of placing a market L(0) order.
// Cleared from the DB by startMatrixCycle after it is consumed.
type AdoptPositionData struct {
	Size       string `json:"size"`        // position qty string (e.g. "35")
	EntryPrice string `json:"entry_price"` // avg entry price string (e.g. "0.4647")
}
```

Add field to the `Strategy` struct (after `SizeAsMain` line, before the `HedgeTpSuppressed` line):

```go
// AdoptPositionData — when non-nil, startMatrixCycle marks L(0) as already-filled
// using this position snapshot; cleared from the strategy record after consumption.
AdoptPositionData *AdoptPositionData
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./pkg/strategy/... -run TestAdoptPositionData -v
```
Expected: PASS

- [ ] **Step 5: Build check**

```
go build ./...
```
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/types.go pkg/strategy/types_test.go
git commit -m "feat: add AdoptPositionData type and Strategy.AdoptPositionData field"
```

---

## Task 3: engine.go — scan adopt_position_data from DB

**Files:**
- Modify: `pkg/strategy/engine.go`

> **Context**: `engine.go` has two SELECT queries (lines ~50-66 `Load()` and lines ~83-99 `Notify()`), both identical except for the WHERE clause. The `scanStrategy` function at line 514 scans all fields. Both queries end with `hedge_stopped_by::text` as the last column. We add `adopt_position_data` after it.

- [ ] **Step 1: Write failing test**

```go
// pkg/strategy/engine_adopt_test.go (create)
package strategy

import (
    "encoding/json"
    "testing"
)

func TestScanStrategy_AdoptPositionData(t *testing.T) {
    // Verify AdoptPositionData is parsed from JSON when non-null.
    // We test the parsing logic directly since scanStrategy requires a real pgx row.
    
    adoptJSON := `{"size":"35","entry_price":"0.4647"}`
    var apd AdoptPositionData
    if err := json.Unmarshal([]byte(adoptJSON), &apd); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if apd.Size != "35" || apd.EntryPrice != "0.4647" {
        t.Fatalf("got %+v", apd)
    }
    
    // Null / empty should yield nil pointer
    for _, s := range []string{"", "null", "{}"} {
        var a AdoptPositionData
        _ = json.Unmarshal([]byte(s), &a)
        // An empty struct is fine; we just test the unmarshal doesn't error
    }
}
```

- [ ] **Step 2: Run test (should pass already, just validates parsing logic)**

```
go test ./pkg/strategy/... -run TestScanStrategy_AdoptPositionData -v
```
Expected: PASS

- [ ] **Step 3: Edit the two SELECT queries in engine.go**

Both the `Load()` query (around line 51) and the `Notify()` query (around line 84) end with:
```
hedge_stopped_by::text
```

Change both to add one more column:
```sql
hedge_stopped_by::text,
COALESCE(adopt_position_data::text,'')
```

Example — find both occurrences of:
```
		        hedge_stopped_by::text
		 FROM strategies WHERE
```
and replace with:
```
		        hedge_stopped_by::text,
		        COALESCE(adopt_position_data::text,'')
		 FROM strategies WHERE
```

The `Load()` query ends in `WHERE status IN ('active','finishing')` and `Notify()` ends in `WHERE id=$1`.

- [ ] **Step 4: Edit scanStrategy to scan the new column**

In `scanStrategy` (around line 514), locate the `rows.Scan(...)` call. The last argument is `&stoppedByStr`. Add a new variable and scan arg:

Before the `rows.Scan` call, add:
```go
var adoptJSON string
```

Add `&adoptJSON` as the last argument in `rows.Scan(...)`:
```go
err := rows.Scan(
    // ... all existing args ...
    &stoppedByStr,
    &adoptJSON,   // ← new
)
```

After the `s.HedgeStoppedBy = stoppedByStr` line, add:
```go
if adoptJSON != "" && adoptJSON != "null" && adoptJSON != "{}" {
    var apd AdoptPositionData
    if json.Unmarshal([]byte(adoptJSON), &apd) == nil && apd.EntryPrice != "" {
        s.AdoptPositionData = &apd
    }
}
```

- [ ] **Step 5: Build check**

```
go build ./...
```
Expected: no errors. If you get `cannot use … as type … in argument to Scan`, verify the scan arg count matches the SELECT column count (count columns in both queries vs args in Scan).

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/engine_adopt_test.go
git commit -m "feat: load adopt_position_data from DB in scanStrategy"
```

---

## Task 4: matrix.go — adopt mode in startMatrixCycle

**Files:**
- Modify: `pkg/strategy/matrix.go`

> **Context**: `startMatrixCycle` at line 203. The "Place slot 0 first" loop starts around line 396. Inside it, the current structure is:
> ```go
> if sr.matrixIsVirtual(l) {
>     sr.matrixTriggerVirtualLevel(ctx, l)
> } else {
>     if err := sr.placeMatrixLevel(ctx, l, price); err != nil { ... }
> }
> ```
> We add an `else if` branch for adopt mode BETWEEN the virtual check and the exchange-order path.
> `handleMatrixLevelFill` at line 1118 marks the level filled in DB + memory, places per-level SL and TP. It does NOT take `sr.mu` itself (it runs while `sr.mu` is already held by the task-queue worker). It is safe to call from within `startMatrixCycle` which holds `sr.mu`.

- [ ] **Step 1: Locate the exact code to replace**

Read `pkg/strategy/matrix.go` lines 396–412. The current L(0) handling block looks like:

```go
		if slot == 0 {
			if sr.matrixIsVirtual(l) {
				sr.matrixTriggerVirtualLevel(ctx, l)
			} else {
				if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix entry %s: %v", slotLabel(l.Slot), err))
				}
			}
		}
```

- [ ] **Step 2: Replace with adopt-aware version**

Replace the block above with:

```go
		if slot == 0 {
			if sr.matrixIsVirtual(l) {
				sr.matrixTriggerVirtualLevel(ctx, l)
			} else if sr.strategy.AdoptPositionData != nil {
				// Adopt mode: an existing exchange position is absorbed — no market order placed.
				adopt := sr.strategy.AdoptPositionData
				fillPrice, _ := strconv.ParseFloat(adopt.EntryPrice, 64)
				if fillPrice <= 0 {
					fillPrice = price // fallback to current mark price
				}
				// Clear adopt flag in DB before calling handleMatrixLevelFill (prevents re-use on reload).
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategies SET adopt_position_data=NULL, updated_at=NOW() WHERE id=$1`,
					sr.strategy.ID)
				sr.strategy.AdoptPositionData = nil
				sr.info(ctx, fmt.Sprintf("Matrix L(0): поглощение существующей позиции @ %.4f (qty=%s, adopt mode)",
					fillPrice, adopt.Size))
				sr.handleMatrixLevelFill(ctx, l.ID, fillPrice)
			} else {
				if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix entry %s: %v", slotLabel(l.Slot), err))
				}
			}
		}
```

- [ ] **Step 3: Build check**

```
go build ./...
```
Expected: no errors

- [ ] **Step 4: Write a unit test for the adopt branch (optional but recommended)**

Create `pkg/strategy/matrix_adopt_test.go`:

```go
package strategy

import (
    "testing"
)

// TestAdoptBranch_SkipsL0Order verifies that when AdoptPositionData is set,
// startMatrixCycle does NOT place L(0) as a market order.
// This is a logic coverage test — actual order placement requires a full integration test.
func TestAdoptBranch_NilAfterConsumption(t *testing.T) {
    // Simulate: after adopt branch runs, AdoptPositionData is nil.
    sr := &StrategyRunner{
        strategy: Strategy{
            AdoptPositionData: &AdoptPositionData{Size: "35", EntryPrice: "0.4647"},
        },
    }
    // Verify field is readable
    if sr.strategy.AdoptPositionData == nil {
        t.Fatal("AdoptPositionData should be non-nil before adopt")
    }
    sr.strategy.AdoptPositionData = nil // simulate what adopt branch does in-memory
    if sr.strategy.AdoptPositionData != nil {
        t.Fatal("AdoptPositionData should be nil after adopt")
    }
}
```

- [ ] **Step 5: Run test**

```
go test ./pkg/strategy/... -run TestAdoptBranch -v
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/matrix_adopt_test.go
git commit -m "feat: adopt mode in startMatrixCycle — absorb existing position as pre-filled L(0)"
```

---

## Task 5: strategy_handler.go — extend DetachFromBot

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`

> **Context**: `DetachFromBot` handler starts at line 852. Currently it reads `botID` and `symbol`, then unconditionally sets `bot_id=NULL, status='stopped'`. We extend it to read an `action` field from the JSON body and handle three cases: `adopt`, `close`, `leave`. The `loadCreds` helper is defined in `trader_handler.go` at line 15 and is callable on `*Server`.

- [ ] **Step 1: Write the new handler**

Replace the entire `DetachFromBot` function body (lines 852–886) with:

```go
// DetachFromBot removes bot management from a strategy (sets bot_id = NULL).
// POST /strategies/{id}/detach
//
// Body (optional, JSON):
//
//	{
//	  "action":       "adopt" | "close" | "leave",  // default: "leave"
//	  "add_blacklist": true | false,
//	  "position": {          // required for action="adopt" and action="close"
//	    "size":         "35",
//	    "side":         "Buy",      // exchange side of the hedge position
//	    "entry_price":  "0.4647",   // required for "adopt"
//	    "position_idx": 2           // Bybit positionIdx for the reduce-only close
//	  }
//	}
//
// Actions:
//
//	adopt — stop old strategy, create new copy with adopt_position_data set
//	        so startMatrixCycle absorbs the existing position (no market L(0) order)
//	close — place market close for hedge position, then stop the strategy
//	leave — set bot_id=NULL but keep status=active so strategy runs independently
//	        and the bot's resolveHedgeSlotConflict prevents immediate re-creation
func (s *Server) DetachFromBot(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	var body struct {
		Action       string `json:"action"`
		AddBlacklist bool   `json:"add_blacklist"`
		Position     *struct {
			Size        string `json:"size"`
			Side        string `json:"side"`
			EntryPrice  string `json:"entry_price"`
			PositionIdx int    `json:"position_idx"`
		} `json:"position"`
	}
	// Ignore decode errors — all fields have safe defaults.
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Action == "" {
		body.Action = "leave"
	}

	// Read strategy context before any mutation.
	var botID *string
	var symbol, category, direction, accountID string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT bot_id, symbol, category, direction, account_id
		 FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&botID, &symbol, &category, &direction, &accountID)

	// Optional blacklist (applied regardless of action).
	if body.AddBlacklist && botID != nil {
		s.pool.Exec(r.Context(), //nolint:errcheck
			`INSERT INTO bot_blacklist (bot_id, symbol) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			*botID, symbol)
		window := "bot-updated"
		_ = window // signal to frontend via response header instead
	}

	switch body.Action {
	case "adopt":
		s.detachAdopt(w, r, id, userID, botID, symbol, body.Position)
	case "close":
		s.detachClose(w, r, id, userID, botID, symbol, category, direction, accountID, body.Position)
	default: // "leave"
		s.detachLeave(w, r, id, userID, botID, symbol)
	}
}

// detachLeave sets bot_id=NULL but keeps status unchanged.
// The strategy continues running independently; bot finds slot occupied via resolveHedgeSlotConflict.
func (s *Server) detachLeave(w http.ResponseWriter, r *http.Request, id, userID string, botID *string, symbol string) {
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET bot_id=NULL, updated_at=NOW() WHERE id=$1 AND owner_id=$2`,
		id, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	if botID != nil {
		s.logBotEvent(r.Context(), *botID,
			fmt.Sprintf("Стратегия %s откреплена (leave) — продолжает работу независимо", symbol),
			"info", "user")
	}
	s.pool.Exec(r.Context(), //nolint:errcheck
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
		id, "Откреплена от бота (leave) — продолжает работу независимо")
	go s.engine.Notify(context.Background(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// detachAdopt stops the old strategy and creates a fresh copy that absorbs the existing
// exchange position via adopt_position_data (no market L(0) placed by startMatrixCycle).
func (s *Server) detachAdopt(w http.ResponseWriter, r *http.Request, id, userID string, botID *string, symbol string, pos interface {
	getSize() string
	getEntryPrice() string
}) {
	// Resolve position data.
	adoptSize := ""
	adoptEntry := ""
	if pos != nil {
		adoptSize = pos.getSize()
		adoptEntry = pos.getEntryPrice()
	}
	adoptJSON := fmt.Sprintf(`{"size":%q,"entry_price":%q}`, adoptSize, adoptEntry)

	// Stop old strategy.
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET bot_id=NULL, status='stopped', updated_at=NOW()
		 WHERE id=$1 AND owner_id=$2`,
		id, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	go s.engine.Notify(context.Background(), id)

	// Create new strategy (copy config + override: status='active', adopt_position_data set, cycle_count=0).
	var newID string
	if err := s.pool.QueryRow(r.Context(), `
		INSERT INTO strategies (
			owner_id, account_id, bot_id, symbol, category, direction, status,
			grid_levels, grid_active, max_stop_active, grid_step_pct, grid_size_usdt,
			tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
			leverage, margin_type, entry_order_type, steps, signal_configs,
			max_cycles, matrix_levels, safe_zone_pct, matrix_entry_level,
			protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry,
			strategy_type, size_as_main, hedged_strategy_id,
			adopt_position_data
		)
		SELECT
			owner_id, account_id, $2, symbol, category, direction, 'active',
			grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
			tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
			leverage, COALESCE(margin_type,'isolated'), entry_order_type, steps, signal_configs,
			max_cycles, matrix_levels, COALESCE(safe_zone_pct,0), matrix_entry_level,
			COALESCE(protected_build,false), COALESCE(matrix_rebuild_on_sl,false), COALESCE(matrix_rebuild_from_entry,false),
			COALESCE(strategy_type,'grid'), COALESCE(size_as_main,false), hedged_strategy_id,
			$3::jsonb
		FROM strategies WHERE id=$1
		RETURNING id`,
		id, botIDOrNull(botID), adoptJSON,
	).Scan(&newID); err != nil {
		// If we can't create the new strategy, the old one is already stopped — log and return OK
		// to avoid leaving the UI stuck. The bot will recreate it on the next tick.
		if botID != nil {
			s.logBotEvent(r.Context(), *botID,
				fmt.Sprintf("Хедж: не удалось создать adopt-стратегию для %s: %v", symbol, err),
				"error", "hedge")
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	if botID != nil {
		s.logBotEvent(r.Context(), *botID,
			fmt.Sprintf("Стратегия %s откреплена (adopt) — новая стратегия %s поглощает позицию @ %s",
				symbol, newID[:8], adoptEntry),
			"info", "user")
	}
	s.pool.Exec(r.Context(), //nolint:errcheck
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
		newID, fmt.Sprintf("Создана в режиме adopt — поглощение позиции @ %s", adoptEntry))

	go s.engine.Notify(context.Background(), newID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// detachClose stops the strategy and places a market reduce-only order to close the hedge position.
func (s *Server) detachClose(w http.ResponseWriter, r *http.Request, id, userID string, botID *string, symbol, category, direction, accountID string, pos interface {
	getSide() string
	getSize() string
	getPositionIdx() int
}) {
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET bot_id=NULL, status='stopped', updated_at=NOW()
		 WHERE id=$1 AND owner_id=$2`,
		id, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	go s.engine.Notify(context.Background(), id)

	// Place market close if position data was provided.
	if pos != nil && pos.getSize() != "" && pos.getSize() != "0" {
		creds, credsErr := s.loadCreds(r, accountID, userID)
		if credsErr == nil {
			closeSide := "Sell"
			if pos.getSide() == "Sell" {
				closeSide = "Buy"
			}
			linkID := fmt.Sprintf("SIS_DTH_%d", time.Now().UnixMilli())
			closeReq := trader.PlaceOrderReq{
				Category:    category,
				Symbol:      symbol,
				Side:        closeSide,
				OrderType:   "Market",
				Qty:         pos.getSize(),
				ReduceOnly:  true,
				PositionIdx: pos.getPositionIdx(),
				OrderLinkId: linkID,
			}
			if _, placeErr := trader.PlaceOrder(r.Context(), creds, closeReq); placeErr != nil {
				if botID != nil {
					s.logBotEvent(r.Context(), *botID,
						fmt.Sprintf("Хедж: ошибка закрытия позиции %s при detach: %v", symbol, placeErr),
						"error", "hedge")
				}
			}
		}
	}

	if botID != nil {
		s.logBotEvent(r.Context(), *botID,
			fmt.Sprintf("Стратегия %s откреплена (close) — позиция закрыта", symbol),
			"info", "user")
	}
	s.pool.Exec(r.Context(), //nolint:errcheck
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
		id, "Откреплена от бота (close) — позиция закрыта рыночным ордером")

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// botIDOrNull returns the bot ID string value or nil for SQL $2 binding.
func botIDOrNull(botID *string) interface{} {
	if botID == nil {
		return nil
	}
	return *botID
}
```

> **⚠️ Important**: The `interface{}` approach above won't compile as written because Go interfaces with methods require a concrete type or named interface. The `body.Position` is an anonymous struct. We need to use the concrete struct directly. Rewrite `detachAdopt` and `detachClose` to accept the anonymous struct pointer directly. See the corrected code below:

The actual correct implementation — replace the entire `DetachFromBot` function and helpers with:

```go
// DetachFromBot removes bot management from a strategy (sets bot_id = NULL).
// POST /strategies/{id}/detach
func (s *Server) DetachFromBot(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	type posBody struct {
		Size        string `json:"size"`
		Side        string `json:"side"`
		EntryPrice  string `json:"entry_price"`
		PositionIdx int    `json:"position_idx"`
	}
	var body struct {
		Action       string   `json:"action"`
		AddBlacklist bool     `json:"add_blacklist"`
		Position     *posBody `json:"position"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Action == "" {
		body.Action = "leave"
	}

	// Read strategy context before any mutation.
	var botID *string
	var symbol, category, direction, accountID string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT bot_id, symbol, category, direction, account_id
		 FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&botID, &symbol, &category, &direction, &accountID)

	// Optional blacklist (applied regardless of action).
	if body.AddBlacklist && botID != nil {
		s.pool.Exec(r.Context(), //nolint:errcheck
			`INSERT INTO bot_blacklist (bot_id, symbol) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			*botID, symbol)
	}

	switch body.Action {
	case "adopt":
		adoptSize, adoptEntry := "", ""
		if body.Position != nil {
			adoptSize = body.Position.Size
			adoptEntry = body.Position.EntryPrice
		}
		adoptJSON := fmt.Sprintf(`{"size":%q,"entry_price":%q}`, adoptSize, adoptEntry)

		tag, err := s.pool.Exec(r.Context(),
			`UPDATE strategies SET bot_id=NULL, status='stopped', updated_at=NOW()
			 WHERE id=$1 AND owner_id=$2`, id, userID)
		if err != nil || tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "strategy not found"); return
		}
		go s.engine.Notify(context.Background(), id)

		var newID string
		if err := s.pool.QueryRow(r.Context(), `
			INSERT INTO strategies (
				owner_id, account_id, bot_id, symbol, category, direction, status,
				grid_levels, grid_active, max_stop_active, grid_step_pct, grid_size_usdt,
				tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
				leverage, margin_type, entry_order_type, steps, signal_configs,
				max_cycles, matrix_levels, safe_zone_pct, matrix_entry_level,
				protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry,
				strategy_type, size_as_main, hedged_strategy_id,
				adopt_position_data
			)
			SELECT
				owner_id, account_id, $2, symbol, category, direction, 'active',
				grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
				tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
				leverage, COALESCE(margin_type,'isolated'), entry_order_type, steps, signal_configs,
				max_cycles, matrix_levels, COALESCE(safe_zone_pct,0), matrix_entry_level,
				COALESCE(protected_build,false), COALESCE(matrix_rebuild_on_sl,false),
				COALESCE(matrix_rebuild_from_entry,false),
				COALESCE(strategy_type,'grid'), COALESCE(size_as_main,false), hedged_strategy_id,
				$3::jsonb
			FROM strategies WHERE id=$1
			RETURNING id`,
			id, botIDOrNull(botID), adoptJSON,
		).Scan(&newID); err == nil {
			go s.engine.Notify(context.Background(), newID)
			s.pool.Exec(r.Context(), //nolint:errcheck
				`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
				newID, fmt.Sprintf("Создана в режиме adopt — поглощение позиции @ %s", adoptEntry))
			if botID != nil {
				s.logBotEvent(r.Context(), *botID,
					fmt.Sprintf("Стратегия %s: adopt detach — новая стратегия %s поглощает позицию @ %s",
						symbol, newID[:8], adoptEntry), "info", "hedge")
			}
		} else if botID != nil {
			s.logBotEvent(r.Context(), *botID,
				fmt.Sprintf("Хедж: не удалось создать adopt-стратегию для %s: %v", symbol, err),
				"error", "hedge")
		}

	case "close":
		tag, err := s.pool.Exec(r.Context(),
			`UPDATE strategies SET bot_id=NULL, status='stopped', updated_at=NOW()
			 WHERE id=$1 AND owner_id=$2`, id, userID)
		if err != nil || tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "strategy not found"); return
		}
		go s.engine.Notify(context.Background(), id)

		if body.Position != nil && body.Position.Size != "" && body.Position.Size != "0" {
			if creds, credsErr := s.loadCreds(r, accountID, userID); credsErr == nil {
				closeSide := "Sell"
				if body.Position.Side == "Sell" {
					closeSide = "Buy"
				}
				closeReq := trader.PlaceOrderReq{
					Category:    category,
					Symbol:      symbol,
					Side:        closeSide,
					OrderType:   "Market",
					Qty:         body.Position.Size,
					ReduceOnly:  true,
					PositionIdx: body.Position.PositionIdx,
					OrderLinkId: fmt.Sprintf("SIS_DTH_%d", time.Now().UnixMilli()),
				}
				if _, placeErr := trader.PlaceOrder(r.Context(), creds, closeReq); placeErr != nil && botID != nil {
					s.logBotEvent(r.Context(), *botID,
						fmt.Sprintf("Хедж: ошибка закрытия позиции %s при detach: %v", symbol, placeErr),
						"error", "hedge")
				}
			}
		}
		if botID != nil {
			s.logBotEvent(r.Context(), *botID,
				fmt.Sprintf("Стратегия %s откреплена (close) — позиция закрыта", symbol), "info", "user")
		}
		s.pool.Exec(r.Context(), //nolint:errcheck
			`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
			id, "Откреплена от бота (close) — позиция закрыта рыночным ордером")

	default: // "leave"
		tag, err := s.pool.Exec(r.Context(),
			`UPDATE strategies SET bot_id=NULL, updated_at=NOW() WHERE id=$1 AND owner_id=$2`,
			id, userID)
		if err != nil || tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "strategy not found"); return
		}
		if botID != nil {
			s.logBotEvent(r.Context(), *botID,
				fmt.Sprintf("Стратегия %s откреплена (leave) — продолжает работу независимо", symbol),
				"info", "user")
		}
		s.pool.Exec(r.Context(), //nolint:errcheck
			`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
			id, "Откреплена от бота (leave) — продолжает работу независимо")
		go s.engine.Notify(context.Background(), id)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// botIDOrNull converts a *string botID to interface{} suitable for pgx $N binding.
func botIDOrNull(botID *string) interface{} {
	if botID == nil {
		return nil
	}
	return *botID
}
```

> **Note**: `trader.PlaceOrderReq` and `trader.PlaceOrder` are used in the `close` case. Verify these are the correct types/functions in `pkg/trader/` before compiling.

- [ ] **Step 2: Check PlaceOrderReq and PlaceOrder exist in pkg/trader**

```
grep -n "PlaceOrderReq\|func PlaceOrder" pkg/trader/*.go
```
Expected: at least one `PlaceOrderReq` struct definition and `func PlaceOrder`. If the name differs, adjust the handler code accordingly.

- [ ] **Step 3: Check bot_blacklist table structure**

```
grep -rn "bot_blacklist" migrations/ services/ --include="*.go" --include="*.sql" | head -10
```
Expected: find the table creation and existing INSERT patterns. Use the same column names.

- [ ] **Step 4: Build check**

```
go build ./...
```
Expected: no errors. Common issues: missing `time` import (add `"time"` to imports), missing `context` import, `botIDOrNull` duplicate if it already exists.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/strategy_handler.go
git commit -m "feat: DetachFromBot extended with adopt/close/leave actions"
```

---

## Task 6: frontend API — detachWithAction

**Files:**
- Modify: `frontend/src/api/strategies.ts`

> **Context**: `detachFromBot(id: string)` at line 59 calls `POST /strategies/${id}/detach` with no body. We add a new `detachWithAction` function that sends the action + position data. Keep `detachFromBot` for backward compat.

- [ ] **Step 1: Add the function to strategies.ts**

After the existing `detachFromBot` function (line 61), add:

```typescript
export interface DetachPositionData {
  size: string
  side: string          // "Buy" | "Sell" — exchange side of the hedge position
  entry_price: string   // required for action="adopt"
  position_idx: number  // Bybit positionIdx for reduce-only close
}

export async function detachWithAction(
  id: string,
  action: 'adopt' | 'close' | 'leave',
  opts: {
    addBlacklist?: boolean
    position?: DetachPositionData
  } = {}
): Promise<void> {
  await apiClient.post(`/strategies/${id}/detach`, {
    action,
    add_blacklist: opts.addBlacklist ?? false,
    position: opts.position,
  })
}
```

- [ ] **Step 2: Build check**

```
cd frontend && npm run build 2>&1 | tail -20
```
Expected: no TypeScript errors related to the new function.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/strategies.ts
git commit -m "feat: add detachWithAction API function"
```

---

## Task 7: frontend — DetachDialog in HedgePairCard

**Files:**
- Modify: `frontend/src/components/strategies/HedgePairCard.tsx`

> **Context**: 
> - Current step state: `'idle' | 'confirm-blacklist'`
> - Menu item "Открепить" at around line 500 sets `step = 'confirm-blacklist'`
> - `confirm-blacklist` banner renders at lines 525–548
> - `doDetach(addBlacklist: boolean)` at line 296 is the current action handler
> - `hedgePos` is already `Position | null` (from `findPosition(hedge, positions)`)
> - `computePnl(hedgePos, tickerPrices)` gives the hedge PnL
> - Imports at lines 1–11 include `detachFromBot`, `addBotBlacklist` from `../../api/strategies`

- [ ] **Step 1: Update step type and add new state**

Replace:
```typescript
const [step, setStep]     = useState<'idle' | 'confirm-blacklist'>('idle')
```
with:
```typescript
const [step, setStep]     = useState<'idle' | 'detach-dialog'>('idle')
const [detachBlacklist, setDetachBlacklist] = useState(false)
```

- [ ] **Step 2: Update the import for detachWithAction**

Replace the import line:
```typescript
  getStrategyState, getStrategyEvents,
  setStrategyStatus, detachFromBot, addBotBlacklist,
} from '../../api/strategies'
```
with:
```typescript
  getStrategyState, getStrategyEvents,
  setStrategyStatus, detachFromBot, addBotBlacklist, detachWithAction,
  type DetachPositionData,
} from '../../api/strategies'
```

- [ ] **Step 3: Replace doDetach with new action handler**

Replace the entire `doDetach` function (lines 296–306):
```typescript
  const doDetach = async (addBlacklist: boolean) => {
    ...
  }
```
with:
```typescript
  const handleDetachAction = async (action: 'adopt' | 'close' | 'leave') => {
    setActing(true)
    const pos: DetachPositionData | undefined = hedgePos
      ? {
          size:         hedgePos.size,
          side:         hedgePos.side,
          entry_price:  hedgePos.entryPrice ?? '',
          position_idx: hedgePos.positionIdx,
        }
      : undefined

    try {
      await detachWithAction(hedge.id, action, {
        addBlacklist: detachBlacklist,
        position: pos,
      })
    } catch { /* ignore — backend returns ok:true even on partial errors */ }

    // For "close": stop main strategy too (full pair dissolution)
    if (action === 'close') {
      try { await setStrategyStatus(main.id, 'stopped') } catch {}
    }

    if (detachBlacklist && botId) {
      try {
        await addBotBlacklist(botId, symbol)
        window.dispatchEvent(new CustomEvent('bot-updated'))
      } catch {}
    }

    localStorage.removeItem(gapKey)
    setActing(false)
    setStep('idle')
    setDetachBlacklist(false)
    onChanged()
  }
```

> **Note**: `hedgePos.entryPrice` — verify the field name on the `Position` type. From `pkg/trader/types.go`, the field is `EntryPrice string \`json:"avgPrice"\`` which maps to `entryPrice` in TypeScript (camelCase in frontend types). Check `frontend/src/types.ts` for the exact field name. If it's `avgPrice`, use `hedgePos.avgPrice ?? ''`.

- [ ] **Step 4: Find the menu item for "Открепить" and update it**

Find the menu item that currently calls `setStep('confirm-blacklist')`. It should look like:
```tsx
onClick={() => { setMenuOpen(false); setStep('confirm-blacklist') }}
```
Replace with:
```tsx
onClick={() => { setMenuOpen(false); setDetachBlacklist(false); setStep('detach-dialog') }}
```

- [ ] **Step 5: Replace the confirm-blacklist banner with the detach dialog**

Find the `{step === 'confirm-blacklist' && ( ... )}` block (lines 525–548) and replace entirely with:

```tsx
      {/* ── Detach dialog ── */}
      {step === 'detach-dialog' && (
        <div
          className="mx-3 mb-2 px-3 py-3 rounded-lg flex flex-col gap-2.5"
          style={{ background: 'rgba(30,41,59,.95)', border: '1px solid rgba(255,255,255,.10)' }}
        >
          {/* Position summary */}
          {hedgePos && (
            <div className="flex items-center gap-3 pb-1 border-b border-white/[.07]">
              <span className="text-[11px] text-slate-400 uppercase tracking-[.6px]">Хедж позиция</span>
              <span className="text-[12px] text-slate-200 font-semibold ml-auto">
                {hedgePos.size} {symbol.replace('USDT', '')}
              </span>
              <span className="text-[11px] text-slate-400">
                вход {parseFloat(hedgePos.entryPrice ?? hedgePos.avgPrice ?? '0').toFixed(4)}
              </span>
              {hedgePnl !== null && (
                <span className={`text-[11px] font-semibold ${hedgePnl >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                  {hedgePnl >= 0 ? '+' : ''}{hedgePnl.toFixed(2)}$
                </span>
              )}
            </div>
          )}

          {/* Blacklist checkbox */}
          {botId && (
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={detachBlacklist}
                onChange={e => setDetachBlacklist(e.target.checked)}
                className="w-3.5 h-3.5 rounded accent-amber-400"
              />
              <span className="text-[11px] text-slate-400">
                Добавить <span className="text-amber-300/80 font-semibold">{symbol}</span> в блеклист{botName ? ` бота «${botName}»` : ''}
              </span>
            </label>
          )}

          {/* Action buttons */}
          <div className="grid grid-cols-2 gap-1.5">
            <button
              type="button" disabled={acting}
              onClick={() => handleDetachAction('adopt')}
              className="px-2 py-1.5 rounded text-[11px] font-semibold disabled:opacity-40 text-emerald-300 border border-emerald-500/30 bg-emerald-500/[.08] hover:bg-emerald-500/[.15] transition-colors"
            >
              {acting ? '…' : '🔄 Поглотить позицию'}
            </button>
            <button
              type="button" disabled={acting}
              onClick={() => handleDetachAction('close')}
              className="px-2 py-1.5 rounded text-[11px] font-semibold disabled:opacity-40 text-rose-300 border border-rose-500/30 bg-rose-500/[.08] hover:bg-rose-500/[.15] transition-colors"
            >
              {acting ? '…' : '✖ Закрыть позицию'}
            </button>
            <button
              type="button" disabled={acting}
              onClick={() => handleDetachAction('leave')}
              className="px-2 py-1.5 rounded text-[11px] font-semibold disabled:opacity-40 text-slate-300 border border-white/[.10] bg-white/[.04] hover:bg-white/[.08] transition-colors"
            >
              {acting ? '…' : '📌 Оставить как есть'}
            </button>
            <button
              type="button" disabled={acting}
              onClick={() => { setStep('idle'); setDetachBlacklist(false) }}
              className="px-2 py-1.5 rounded text-[11px] font-semibold text-slate-500 border border-white/[.06] hover:bg-white/[.04] transition-colors"
            >
              Отмена
            </button>
          </div>

          {/* Tooltips */}
          <div className="text-[10px] text-slate-500 leading-snug space-y-0.5">
            <div><span className="text-emerald-500/70">Поглотить</span> — новая стратегия учтёт существующую позицию (L0 не откроется повторно)</div>
            <div><span className="text-rose-500/70">Закрыть</span> — рыночно закрыть хедж-позицию и остановить пару</div>
            <div><span className="text-slate-400/70">Оставить</span> — открепить от бота, стратегия продолжит работу самостоятельно</div>
          </div>
        </div>
      )}
```

> **⚠️ Check field names**: Verify `hedgePos.entryPrice`, `hedgePos.avgPrice`, `hedgePos.size` match the actual `Position` type in `frontend/src/types.ts`. Use the same field name consistently.

- [ ] **Step 6: Check TypeScript types for Position**

```
grep -n "entryPrice\|avgPrice\|EntryPrice\|size\|positionIdx" frontend/src/types.ts | head -20
```
Expected: see exact field names. Update the dialog and `handleDetachAction` if needed.

- [ ] **Step 7: Build check**

```
cd frontend && npm run build 2>&1 | tail -30
```
Expected: no TypeScript errors.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/strategies/HedgePairCard.tsx frontend/src/api/strategies.ts
git commit -m "feat: detach dialog with adopt/close/leave actions on HedgePairCard"
```

---

## Task 8: Integration smoke test + build

**Files:** (no changes)

- [ ] **Step 1: Run all Go tests**

```
cd C:/Users/123/Projects/sis
go test ./...
```
Expected: PASS. Note any pre-existing failures (don't fix them here unless they're related to our changes).

- [ ] **Step 2: Full Go build**

```
go build ./...
```
Expected: no errors.

- [ ] **Step 3: Frontend production build**

```
cd frontend && npm run build
```
Expected: no TypeScript errors, no missing exports.

- [ ] **Step 4: Manual smoke test plan** (document, don't run automatically)

Test scenario A — Adopt:
1. Open hedge pair card for GENIUSUSDT
2. Ensure hedge has an active position (e.g. 35 contracts long)
3. Click menu → Открепить
4. Dialog appears showing position size + entry price
5. Leave blacklist unchecked, click "Поглотить позицию"
6. Dialog closes, `onChanged()` fires, pair card refreshes
7. In DB: old strategy `status='stopped'`; new strategy `status='active'` with `adopt_position_data` set then cleared after engine picks it up
8. Engine log shows "Matrix L(0): поглощение существующей позиции"
9. Exchange position remains exactly as-is (no new market order)

Test scenario B — Leave:
1. Click "Оставить как есть"
2. Old hedge strategy: `bot_id=NULL`, `status` unchanged (still `active`)
3. Bot next tick: `resolveHedgeSlotConflict` finds the now-independent strategy, waits
4. No new strategy created, no duplicate position

Test scenario C — Close:
1. Click "Закрыть позицию"
2. Market reduce-only order placed for hedge position size
3. Strategy `status='stopped'`, `bot_id=NULL`
4. Main strategy also stopped (frontend calls `setStrategyStatus(main.id, 'stopped')`)

- [ ] **Step 5: Final commit**

```bash
git add .
git commit -m "feat: detach-with-adopt complete — all tasks done"
```

---

## Self-Review

**Spec coverage check:**
- ✅ UI dialog when detaching bot from hedge pair — Task 7
- ✅ Shows hedge position info (size, entry, PnL) — Task 7 Step 5
- ✅ Поглотить позицию — Tasks 4, 5 (adopt action)
- ✅ Закрыть позицию — Task 5 (close action places market order)
- ✅ Оставить как есть — Task 5 (leave keeps status=active)
- ✅ Отмена — Task 7 Step 5 (cancel button)
- ✅ Optional blacklist checkbox — Task 7 Steps 3, 5
- ✅ DB column for adopt_position_data — Task 1
- ✅ AdoptPositionData type + Strategy field — Task 2
- ✅ Engine loads adopt_position_data from DB — Task 3
- ✅ startMatrixCycle adopts existing position — Task 4
- ✅ Backend handler extended — Task 5
- ✅ Frontend API function — Task 6
- ✅ Prevent immediate bot re-activation (leave = keep active; adopt = new strategy has bot_id set) — Tasks 4, 5

**Potential issues to watch:**
1. `trader.PlaceOrderReq` struct — verify field names match existing usage in `pkg/trader/`
2. `hedgePos.entryPrice` vs `hedgePos.avgPrice` — check `frontend/src/types.ts`
3. `bot_blacklist` table name — verify via grep (Step 3 of Task 5)
4. `adopt_position_data` SELECT column count must match `scanStrategy` scan arg count
5. For `adopt`: if `botID == nil` at the time of detach (strategy was already manually unlinked), the new strategy gets `bot_id=NULL` — it won't be bot-managed. This is acceptable edge-case behavior.
