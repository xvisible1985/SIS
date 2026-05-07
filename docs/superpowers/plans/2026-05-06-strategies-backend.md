# Strategies Page — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the strategies backend with new DB columns (leverage, margin_type, hedge_mode, strategy_type, signal_configs, steps, trailing_stop), a strategy templates table, and a state endpoint that returns the active cycle's levels.

**Architecture:** Migration 007 adds columns to `strategies` and creates `strategy_templates`. `strategy_handler.go` is updated to read/write all new fields and returns computed `volume_usdt`, `active_levels`, `last_pnl` in the list. A new `GET /strategies/:id/state` returns the current cycle's levels. A new `strategy_template_handler.go` provides CRUD for templates.

**Tech Stack:** Go 1.22, pgx v5, chi router. Working dir: `c:\Users\123\Projects\sis`.

---

### Task 1: Migration 007

**Files:**
- Create: `migrations/007_strategy_extensions.sql`

- [ ] **Step 1: Create migration file**

```sql
-- migrations/007_strategy_extensions.sql

ALTER TABLE strategies
  ADD COLUMN leverage                INT          NOT NULL DEFAULT 1,
  ADD COLUMN margin_type             TEXT         NOT NULL DEFAULT 'isolated',
  ADD COLUMN hedge_mode              BOOLEAN      NOT NULL DEFAULT false,
  ADD COLUMN strategy_type           TEXT         NOT NULL DEFAULT 'grid',
  ADD COLUMN signal_configs          JSONB        NOT NULL DEFAULT '[]',
  ADD COLUMN steps                   JSONB        DEFAULT NULL,
  ADD COLUMN trailing_stop_enabled   BOOLEAN      NOT NULL DEFAULT false,
  ADD COLUMN trailing_activation_pct NUMERIC(10,4),
  ADD COLUMN trailing_callback_pct   NUMERIC(10,4);

CREATE TABLE strategy_templates (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT        NOT NULL,
  config     JSONB       NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_strategy_templates_owner ON strategy_templates(owner_id);
```

- [ ] **Step 2: Verify migration applies**

Run: `go run ./services/api-gateway/... 2>&1 | head -5`

Expected: server starts (or exits on missing env vars), no `migrate:` error. Alternatively connect to DB manually:

```
psql $DATABASE_URL -c "\d strategies" | grep leverage
```

Expected: `leverage | integer | not null | 1`

- [ ] **Step 3: Commit**

```bash
git add migrations/007_strategy_extensions.sql
git commit -m "feat: migration 007 — strategy extensions + templates table"
```

---

### Task 2: Extend pkg/strategy/types.go

**Files:**
- Modify: `pkg/strategy/types.go`

- [ ] **Step 1: Add SignalConfig and GridStep types, extend Strategy struct**

Open `pkg/strategy/types.go`. After the `SLType` const block (around line 43), add:

```go
type SignalConfig struct {
	Name   string             `json:"name"`
	Params map[string]float64 `json:"params"`
}

type GridStep struct {
	PriceMovePct float64 `json:"price_move_pct"`
	Lots         float64 `json:"lots"`
}
```

Then extend the `Strategy` struct (currently ends at `SignalFilter bool`) to:

```go
type Strategy struct {
	ID           string
	OwnerID      string
	AccountID    string
	Symbol       string
	Category     string
	Direction    Direction
	Status       Status
	GridLevels   int
	GridActive   int
	GridStepPct  float64
	GridSizeUSDT float64
	TPMode       TPMode
	TPPct        float64
	SLType       SLType
	SLPct        float64
	SignalFilter  bool
	// New fields
	Leverage              int
	MarginType            string
	HedgeMode             bool
	StrategyType          string
	SignalConfigs         []SignalConfig
	Steps                 []GridStep
	TrailingStopEnabled   bool
	TrailingActivationPct float64
	TrailingCallbackPct   float64
}
```

- [ ] **Step 2: Build to verify**

Run: `go build ./pkg/strategy/...`

Expected: no output (success)

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/types.go
git commit -m "feat: add SignalConfig, GridStep and extend Strategy struct"
```

---

### Task 3: Rewrite strategy_handler.go

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`

This task rewrites the entire file to support new fields and adds the state endpoint.

- [ ] **Step 1: Replace services/api-gateway/strategy_handler.go with the following**

```go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// nullableJSONB converts json.RawMessage to *string for nullable JSONB params.
// Pass the result to pgx with ($n::text)::jsonb in SQL.
func nullableJSONB(raw json.RawMessage) *string {
	if raw == nil {
		return nil
	}
	s := string(raw)
	return &s
}

type strategyPayload struct {
	AccountID             string          `json:"account_id"`
	Symbol                string          `json:"symbol"`
	Category              string          `json:"category"`
	Direction             string          `json:"direction"`
	GridLevels            int             `json:"grid_levels"`
	GridActive            int             `json:"grid_active"`
	GridStepPct           float64         `json:"grid_step_pct"`
	GridSizeUSDT          float64         `json:"grid_size_usdt"`
	TPMode                string          `json:"tp_mode"`
	TPPct                 float64         `json:"tp_pct"`
	SLType                string          `json:"sl_type"`
	SLPct                 float64         `json:"sl_pct"`
	SignalFilter           bool            `json:"signal_filter"`
	Leverage              int             `json:"leverage"`
	MarginType            string          `json:"margin_type"`
	HedgeMode             bool            `json:"hedge_mode"`
	StrategyType          string          `json:"strategy_type"`
	SignalConfigs         json.RawMessage `json:"signal_configs"`
	Steps                 json.RawMessage `json:"steps"`
	TrailingStopEnabled   bool            `json:"trailing_stop_enabled"`
	TrailingActivationPct *float64        `json:"trailing_activation_pct"`
	TrailingCallbackPct   *float64        `json:"trailing_callback_pct"`
}

func (p *strategyPayload) applyDefaults() {
	if p.Category == "" {
		p.Category = "linear"
	}
	if p.Direction == "" {
		p.Direction = "long"
	}
	if p.GridLevels == 0 {
		p.GridLevels = 5
	}
	if p.GridActive == 0 {
		p.GridActive = 3
	}
	if p.TPMode == "" {
		p.TPMode = "total"
	}
	if p.SLType == "" {
		p.SLType = "conditional"
	}
	if p.Leverage == 0 {
		p.Leverage = 1
	}
	if p.MarginType == "" {
		p.MarginType = "isolated"
	}
	if p.StrategyType == "" {
		p.StrategyType = "grid"
	}
	if p.SignalConfigs == nil {
		p.SignalConfigs = json.RawMessage("[]")
	}
}

// ListStrategies returns all strategies for the authenticated user with computed fields.
// GET /strategies
func (s *Server) ListStrategies(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(), `
		SELECT
			s.id, s.account_id, s.symbol, s.category, s.direction, s.status,
			s.grid_levels, s.grid_active, s.grid_step_pct, s.grid_size_usdt,
			s.tp_mode, s.tp_pct, s.sl_type, s.sl_pct, s.signal_filter,
			s.leverage, s.margin_type, s.hedge_mode, s.strategy_type,
			s.signal_configs::text, (s.steps::text),
			s.trailing_stop_enabled, s.trailing_activation_pct, s.trailing_callback_pct,
			s.created_at, s.updated_at,
			COALESCE((
				SELECT SUM(sl.size_usdt)
				FROM strategy_levels sl
				JOIN strategy_cycles sc ON sl.cycle_id = sc.id
				WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
			), 0),
			COALESCE((
				SELECT COUNT(*)::int
				FROM strategy_levels sl
				JOIN strategy_cycles sc ON sl.cycle_id = sc.id
				WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL AND sl.status = 'filled'
			), 0),
			COALESCE((
				SELECT realized_pnl FROM strategy_cycles
				WHERE strategy_id = s.id AND ended_at IS NOT NULL
				ORDER BY cycle_num DESC LIMIT 1
			), 0)
		FROM strategies s
		WHERE s.owner_id = $1
		ORDER BY s.created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type row struct {
		ID                    string          `json:"id"`
		AccountID             string          `json:"account_id"`
		Symbol                string          `json:"symbol"`
		Category              string          `json:"category"`
		Direction             string          `json:"direction"`
		Status                string          `json:"status"`
		GridLevels            int             `json:"grid_levels"`
		GridActive            int             `json:"grid_active"`
		GridStepPct           float64         `json:"grid_step_pct"`
		GridSizeUSDT          float64         `json:"grid_size_usdt"`
		TPMode                string          `json:"tp_mode"`
		TPPct                 float64         `json:"tp_pct"`
		SLType                string          `json:"sl_type"`
		SLPct                 float64         `json:"sl_pct"`
		SignalFilter           bool            `json:"signal_filter"`
		Leverage              int             `json:"leverage"`
		MarginType            string          `json:"margin_type"`
		HedgeMode             bool            `json:"hedge_mode"`
		StrategyType          string          `json:"strategy_type"`
		SignalConfigs         json.RawMessage `json:"signal_configs"`
		Steps                 json.RawMessage `json:"steps"`
		TrailingStopEnabled   bool            `json:"trailing_stop_enabled"`
		TrailingActivationPct *float64        `json:"trailing_activation_pct"`
		TrailingCallbackPct   *float64        `json:"trailing_callback_pct"`
		CreatedAt             time.Time       `json:"created_at"`
		UpdatedAt             time.Time       `json:"updated_at"`
		VolumeUSDT            float64         `json:"volume_usdt"`
		ActiveLevels          int             `json:"active_levels"`
		LastPnl               float64         `json:"last_pnl"`
	}

	var result []row
	for rows.Next() {
		var r row
		var scStr string
		var stepsStr *string
		if err := rows.Scan(
			&r.ID, &r.AccountID, &r.Symbol, &r.Category, &r.Direction, &r.Status,
			&r.GridLevels, &r.GridActive, &r.GridStepPct, &r.GridSizeUSDT,
			&r.TPMode, &r.TPPct, &r.SLType, &r.SLPct, &r.SignalFilter,
			&r.Leverage, &r.MarginType, &r.HedgeMode, &r.StrategyType,
			&scStr, &stepsStr,
			&r.TrailingStopEnabled, &r.TrailingActivationPct, &r.TrailingCallbackPct,
			&r.CreatedAt, &r.UpdatedAt,
			&r.VolumeUSDT, &r.ActiveLevels, &r.LastPnl,
		); err == nil {
			r.SignalConfigs = json.RawMessage(scStr)
			if stepsStr != nil {
				r.Steps = json.RawMessage(*stepsStr)
			}
			result = append(result, r)
		}
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateStrategy creates a new strategy (status=stopped by default).
// POST /strategies
func (s *Server) CreateStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req strategyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "account_id and symbol are required")
		return
	}
	req.applyDefaults()

	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO strategies
		  (owner_id, account_id, symbol, category, direction,
		   grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		   tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
		   leverage, margin_type, hedge_mode, strategy_type,
		   signal_configs, steps,
		   trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
		        $15,$16,$17,$18,
		        $19::jsonb, ($20::text)::jsonb,
		        $21,$22,$23)
		RETURNING id`,
		userID, req.AccountID, req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		req.Leverage, req.MarginType, req.HedgeMode, req.StrategyType,
		string(req.SignalConfigs), nullableJSONB(req.Steps),
		req.TrailingStopEnabled, req.TrailingActivationPct, req.TrailingCallbackPct,
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// UpdateStrategy updates strategy parameters (not status).
// PUT /strategies/{id}
func (s *Server) UpdateStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req strategyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.applyDefaults()

	tag, err := s.pool.Exec(r.Context(), `
		UPDATE strategies SET
		  symbol=$1, category=$2, direction=$3,
		  grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		  tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		  leverage=$13, margin_type=$14, hedge_mode=$15, strategy_type=$16,
		  signal_configs=$17::jsonb, steps=($18::text)::jsonb,
		  trailing_stop_enabled=$19, trailing_activation_pct=$20, trailing_callback_pct=$21,
		  updated_at=NOW()
		WHERE id=$22 AND owner_id=$23`,
		req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		req.Leverage, req.MarginType, req.HedgeMode, req.StrategyType,
		string(req.SignalConfigs), nullableJSONB(req.Steps),
		req.TrailingStopEnabled, req.TrailingActivationPct, req.TrailingCallbackPct,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	s.engine.Notify(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SetStrategyStatus changes strategy status: active | finishing | stopped.
// POST /strategies/{id}/status
func (s *Server) SetStrategyStatus(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Status != "active" && req.Status != "finishing" && req.Status != "stopped" {
		writeError(w, http.StatusBadRequest, "status must be active | finishing | stopped")
		return
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET status=$1, updated_at=NOW() WHERE id=$2 AND owner_id=$3`,
		req.Status, id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	s.engine.Notify(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteStrategy deletes a strategy (only if stopped).
// DELETE /strategies/{id}
func (s *Server) DeleteStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM strategies WHERE id=$1 AND owner_id=$2 AND status='stopped'`,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusBadRequest, "strategy not found or not stopped")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetStrategyState returns the active cycle's levels plus computed volume and avg entry.
// GET /strategies/{id}/state
func (s *Server) GetStrategyState(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// Verify ownership
	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT true FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	// Get latest cycle
	type cycleInfo struct {
		CycleNum   int       `json:"cycle_num"`
		StartPrice float64   `json:"start_price"`
		TPOrderID  string    `json:"tp_order_id"`
		SLOrderID  string    `json:"sl_order_id"`
		StartedAt  time.Time `json:"started_at"`
	}
	var cycleID string
	var cycle cycleInfo
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, cycle_num, COALESCE(start_price,0),
		       COALESCE(tp_order_id,''), COALESCE(sl_order_id,''), started_at
		FROM strategy_cycles
		WHERE strategy_id=$1 ORDER BY cycle_num DESC LIMIT 1`, id,
	).Scan(&cycleID, &cycle.CycleNum, &cycle.StartPrice,
		&cycle.TPOrderID, &cycle.SLOrderID, &cycle.StartedAt)

	type levelInfo struct {
		LevelIdx    int     `json:"level_idx"`
		Side        string  `json:"side"`
		TargetPrice float64 `json:"target_price"`
		SizeUSDT    float64 `json:"size_usdt"`
		Status      string  `json:"status"`
		FilledPrice float64 `json:"filled_price"`
	}
	var levels []levelInfo
	var volumeUSDT, totalValue, totalQty float64

	if err == nil {
		lrows, _ := s.pool.Query(r.Context(), `
			SELECT level_idx, side, target_price, size_usdt, status, COALESCE(filled_price,0)
			FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`, cycleID)
		if lrows != nil {
			defer lrows.Close()
			for lrows.Next() {
				var l levelInfo
				if lrows.Scan(&l.LevelIdx, &l.Side, &l.TargetPrice,
					&l.SizeUSDT, &l.Status, &l.FilledPrice) == nil {
					levels = append(levels, l)
					if l.Status == "filled" && l.FilledPrice > 0 {
						volumeUSDT += l.SizeUSDT
						totalValue += l.SizeUSDT                  // total cost (USDT)
						totalQty += l.SizeUSDT / l.FilledPrice    // total coins bought
					}
				}
			}
		}
	}
	if levels == nil {
		levels = []levelInfo{}
	}
	var avgEntry float64
	if totalQty > 0 {
		avgEntry = totalValue / totalQty
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cycle_num":   cycle.CycleNum,
		"start_price": cycle.StartPrice,
		"tp_order_id": cycle.TPOrderID,
		"sl_order_id": cycle.SLOrderID,
		"started_at":  cycle.StartedAt,
		"levels":      levels,
		"volume_usdt": volumeUSDT,
		"avg_entry":   avgEntry,
	})
}
```

- [ ] **Step 2: Build to verify**

Run: `go build ./services/api-gateway/...`

Expected: no output (success). If there are errors, fix them before continuing.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/strategy_handler.go
git commit -m "feat: extend strategy handler — new fields, computed list columns, state endpoint"
```

---

### Task 4: Strategy template handler + routes

**Files:**
- Create: `services/api-gateway/strategy_template_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Create services/api-gateway/strategy_template_handler.go**

```go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// ListTemplates returns all strategy templates for the authenticated user.
// GET /strategy-templates
func (s *Server) ListTemplates(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, config::text, created_at
		 FROM strategy_templates WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type row struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Config    json.RawMessage `json:"config"`
		CreatedAt time.Time       `json:"created_at"`
	}
	var result []row
	for rows.Next() {
		var r row
		var configStr string
		if rows.Scan(&r.ID, &r.Name, &configStr, &r.CreatedAt) == nil {
			r.Config = json.RawMessage(configStr)
			result = append(result, r)
		}
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateTemplate saves current strategy settings as a named template.
// POST /strategy-templates
func (s *Server) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Config == nil {
		writeError(w, http.StatusBadRequest, "name and config are required")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO strategy_templates (owner_id, name, config)
		 VALUES ($1, $2, $3::jsonb) RETURNING id`,
		userID, req.Name, string(req.Config),
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// DeleteTemplate removes a template.
// DELETE /strategy-templates/{id}
func (s *Server) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM strategy_templates WHERE id=$1 AND owner_id=$2`, id, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 2: Register new routes in services/api-gateway/main.go**

In `main.go`, inside the protected `r.Group` block, after the existing strategy routes, add:

```go
			// Strategy state
			r.Get("/strategies/{id}/state", s.GetStrategyState)

			// Strategy templates
			r.Get("/strategy-templates", s.ListTemplates)
			r.Post("/strategy-templates", s.CreateTemplate)
			r.Delete("/strategy-templates/{id}", s.DeleteTemplate)
```

- [ ] **Step 3: Build to verify**

Run: `go build ./services/api-gateway/...`

Expected: no output (success)

- [ ] **Step 4: Smoke test with curl (optional, if server is running)**

```bash
# Start server in one terminal, then:
curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@test.com","password":"test"}' | jq .token

# Use returned token:
TOKEN=<token>
curl -s http://localhost:8080/strategies \
  -H "Authorization: Bearer $TOKEN" | jq length
# Expected: 0 (empty list)
```

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/strategy_template_handler.go services/api-gateway/main.go
git commit -m "feat: strategy template handler + state endpoint + routes"
```

---

> **Intentionally deferred (out of MVP scope for this plan):**
> The spec lists `pkg/trader/bybit.go` as needing `SetLeverage` and `SetMarginType` methods that the engine calls at cycle start (before the first order). These methods are not implemented here because they require changes to the engine's cycle-start logic in `pkg/strategy/engine.go`. Implement in a follow-up task: add `SetLeverage(ctx, accountID, symbol, leverage int) error` and `SetMarginType(ctx, accountID, symbol, marginType string) error` to the Bybit client, then call them from the engine's `runCycle()` entry point before placing the first grid order.
