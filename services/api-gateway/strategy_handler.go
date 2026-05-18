package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/trader"
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

// normSteps normalises a steps JSON pointer for comparison: nil, "null", "", and "[]"
// are all treated as "no steps" so that strategies created with NULL and later edited
// with an empty array don't falsely trigger a grid restart.
func normSteps(s *string) string {
	if s == nil {
		return ""
	}
	v := strings.TrimSpace(*s)
	if v == "" || v == "null" || v == "[]" {
		return ""
	}
	return v
}

// diffFloat reports whether two float64 values differ by more than a tiny epsilon.
// Prevents false positives from JSON/DB float representation noise (e.g. 3.5 vs 3.5000000000000004).
func diffFloat(a, b float64) bool {
	const eps = 1e-9
	diff := a - b
	return diff > eps || diff < -eps
}

// stepsEqual reports whether two steps JSON strings represent the same grid steps.
// Uses structural comparison to avoid false positives from PostgreSQL JSONB key ordering
// (JSONB returns keys alphabetically: "lots" before "price_move_pct", but the frontend
// sends them in declaration order, making normSteps string comparison always unequal).
func stepsEqual(a, b *string) bool {
	sa, sb := normSteps(a), normSteps(b)
	if sa == sb {
		return true
	}
	if sa == "" || sb == "" {
		return false
	}
	type step struct {
		PriceMovePct float64 `json:"price_move_pct"`
		Lots         float64 `json:"lots"`
	}
	var as, bs []step
	if json.Unmarshal([]byte(sa), &as) != nil || json.Unmarshal([]byte(sb), &bs) != nil {
		return false
	}
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if math.Abs(as[i].PriceMovePct-bs[i].PriceMovePct) > 1e-9 {
			return false
		}
		if math.Abs(as[i].Lots-bs[i].Lots) > 1e-9 {
			return false
		}
	}
	return true
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
	SignalFilter          bool            `json:"signal_filter"`
	Leverage              int             `json:"leverage"`
	MarginType            string          `json:"margin_type"`
	HedgeMode             bool            `json:"hedge_mode"`
	StrategyType          string          `json:"strategy_type"`
	SignalConfigs         json.RawMessage `json:"signal_configs"`
	Steps                 json.RawMessage `json:"steps"`
	TrailingStopEnabled   bool            `json:"trailing_stop_enabled"`
	TrailingActivationPct *float64        `json:"trailing_activation_pct"`
	TrailingCallbackPct   *float64        `json:"trailing_callback_pct"`
	EntryOrderType        string          `json:"entry_order_type"`
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
	if p.EntryOrderType == "" {
		p.EntryOrderType = "limit"
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
			s.leverage, s.margin_type, s.hedge_mode, s.strategy_type, s.entry_order_type,
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
		SignalFilter          bool            `json:"signal_filter"`
		Leverage              int             `json:"leverage"`
		MarginType            string          `json:"margin_type"`
		HedgeMode             bool            `json:"hedge_mode"`
		StrategyType          string          `json:"strategy_type"`
		EntryOrderType        string          `json:"entry_order_type"`
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
			&r.Leverage, &r.MarginType, &r.HedgeMode, &r.StrategyType, &r.EntryOrderType,
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
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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

	var existing int
	if err := s.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM strategies WHERE owner_id=$1 AND symbol=$2 AND direction=$3 AND status != 'deleted'`,
		userID, req.Symbol, req.Direction,
	).Scan(&existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing > 0 {
		writeError(w, http.StatusConflict, "стратегия "+req.Symbol+"/"+req.Direction+" уже существует")
		return
	}

	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO strategies
		  (owner_id, account_id, symbol, category, direction,
		   grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		   tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
		   leverage, margin_type, hedge_mode, strategy_type, entry_order_type,
		   signal_configs, steps,
		   trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
		        $15,$16,$17,$18,$19,
		        $20::jsonb, ($21::text)::jsonb,
		        $22,$23,$24)
		RETURNING id`,
		userID, req.AccountID, req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		req.Leverage, req.MarginType, req.HedgeMode, req.StrategyType, req.EntryOrderType,
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

	// Read current grid fields so we can detect whether the grid actually changed.
	var oldGridLevels, oldGridActive int
	var oldGridStepPct, oldGridSizeUSDT float64
	var oldDirection, oldEntryOrderType string
	var oldStepsJSON *string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        direction, entry_order_type, steps::text
		 FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&oldGridLevels, &oldGridActive, &oldGridStepPct, &oldGridSizeUSDT,
		&oldDirection, &oldEntryOrderType, &oldStepsJSON)

	tag, err := s.pool.Exec(r.Context(), `
		UPDATE strategies SET
		  symbol=$1, category=$2, direction=$3,
		  grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		  tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		  leverage=$13, margin_type=$14, hedge_mode=$15, strategy_type=$16, entry_order_type=$17,
		  signal_configs=$18::jsonb, steps=($19::text)::jsonb,
		  trailing_stop_enabled=$20, trailing_activation_pct=$21, trailing_callback_pct=$22,
		  updated_at=NOW()
		WHERE id=$23 AND owner_id=$24`,
		req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		req.Leverage, req.MarginType, req.HedgeMode, req.StrategyType, req.EntryOrderType,
		string(req.SignalConfigs), nullableJSONB(req.Steps),
		req.TrailingStopEnabled, req.TrailingActivationPct, req.TrailingCallbackPct,
		id, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	newStepsJSON := nullableJSONB(req.Steps)
	gridChanged := oldGridLevels != req.GridLevels ||
		oldGridActive != req.GridActive ||
		diffFloat(oldGridStepPct, req.GridStepPct) ||
		diffFloat(oldGridSizeUSDT, req.GridSizeUSDT) ||
		oldDirection != req.Direction ||
		oldEntryOrderType != req.EntryOrderType ||
		!stepsEqual(oldStepsJSON, newStepsJSON)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	// Notify the engine asynchronously so the HTTP response is not blocked by
	// strategy runner mutexes that may be held during live Bybit API calls.
	logMsg := fmt.Sprintf(
		"Настройки обновлены (TP/SL): tp=%.2f%% sl=%.2f%%",
		req.TPPct, req.SLPct,
	)
	if gridChanged {
		logMsg = fmt.Sprintf(
			"Настройки обновлены (сетка): symbol=%s dir=%s step=%.2f%% size=%.2f USDT active=%d entryType=%s tp=%.2f%% sl=%.2f%%",
			req.Symbol, req.Direction, req.GridStepPct, req.GridSizeUSDT, req.GridActive, req.EntryOrderType, req.TPPct, req.SLPct,
		)
	}
	go func() {
		ctx := context.Background()
		s.engine.Notify(ctx, id)
		if gridChanged {
			s.engine.RestartCycle(ctx, id)
		} else {
			s.engine.UpdateTPSL(ctx, id)
		}
		s.engine.LogUserAction(ctx, id, logMsg)
	}()
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
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	statusLabel := map[string]string{
		"active":    "запущена",
		"finishing": "завершение",
		"stopped":   "остановлена",
	}[req.Status]
	logMsg := fmt.Sprintf("Статус изменён пользователем: %s", statusLabel)
	go func() {
		ctx := context.Background()
		s.engine.Notify(ctx, id)
		s.engine.LogUserAction(ctx, id, logMsg)
	}()
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

// GetStrategyEvents returns the last 200 events for a strategy.
// GET /strategies/{id}/events
func (s *Server) GetStrategyEvents(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT true FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&exists); err != nil {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT message, level, created_at
		FROM strategy_events
		WHERE strategy_id=$1
		ORDER BY created_at DESC LIMIT 200`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type eventRow struct {
		Message   string    `json:"message"`
		Level     string    `json:"level"`
		CreatedAt time.Time `json:"created_at"`
	}
	var result []eventRow
	for rows.Next() {
		var e eventRow
		if rows.Scan(&e.Message, &e.Level, &e.CreatedAt) == nil {
			result = append(result, e)
		}
	}
	if result == nil {
		result = []eventRow{}
	}
	writeJSON(w, http.StatusOK, result)
}

// GetStrategyState returns the active cycle's levels plus computed volume and avg entry.
// GET /strategies/{id}/state
func (s *Server) GetStrategyState(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// Verify ownership
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT true FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&exists); err != nil {
		// pgx returns an error for no rows; treat any error as not found
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
	var cycleEnded bool
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, cycle_num, COALESCE(start_price,0),
		       COALESCE(tp_order_id,''), COALESCE(sl_order_id,''), started_at,
		       ended_at IS NOT NULL
		FROM strategy_cycles
		WHERE strategy_id=$1 ORDER BY cycle_num DESC LIMIT 1`, id,
	).Scan(&cycleID, &cycle.CycleNum, &cycle.StartPrice,
		&cycle.TPOrderID, &cycle.SLOrderID, &cycle.StartedAt, &cycleEnded)
	if cycleEnded {
		cycle.TPOrderID = ""
		cycle.SLOrderID = ""
		cycle.StartPrice = 0
	}

	type levelInfo struct {
		LevelIdx        int     `json:"level_idx"`
		Side            string  `json:"side"`
		TargetPrice     float64 `json:"target_price"`
		SizeUSDT        float64 `json:"size_usdt"`
		Status          string  `json:"status"`
		FilledPrice     float64 `json:"filled_price"`
		ExchangeOrderID string  `json:"exchange_order_id"`
	}
	var levels []levelInfo
	var volumeUSDT, totalCost, totalCoins float64

	if err == nil {
		lrows, lErr := s.pool.Query(r.Context(), `
			SELECT level_idx, side, target_price, size_usdt, status, COALESCE(filled_price,0), COALESCE(exchange_order_id,'')
			FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`, cycleID)
		if lErr == nil && lrows != nil {
			defer lrows.Close()
			for lrows.Next() {
				var l levelInfo
				if lrows.Scan(&l.LevelIdx, &l.Side, &l.TargetPrice,
					&l.SizeUSDT, &l.Status, &l.FilledPrice, &l.ExchangeOrderID) == nil {
					levels = append(levels, l)
					if l.Status == "filled" && l.FilledPrice > 0 {
						volumeUSDT += l.SizeUSDT
						totalCost += l.SizeUSDT                 // total cost (USDT)
						totalCoins += l.SizeUSDT / l.FilledPrice // total coins bought
					}
				}
			}
		}
	}
	if levels == nil {
		levels = []levelInfo{}
	}
	var avgEntry float64
	if totalCoins > 0 && !cycleEnded {
		avgEntry = totalCost / totalCoins
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cycle_num":     cycle.CycleNum,
		"start_price":   cycle.StartPrice,
		"tp_order_id":   cycle.TPOrderID,
		"sl_order_id":   cycle.SLOrderID,
		"started_at":    cycle.StartedAt,
		"levels":        levels,
		"volume_usdt":   volumeUSDT,
		"avg_entry":     avgEntry,
		"signal_state":  s.engine.GetSignalState(id),
		"signal_values": s.engine.GetSignalValues(id),
	})
}

func computeTPSLFlag(orderID string, live, inIndex bool) string {
	if orderID == "" {
		return "not_placed"
	}
	if live && inIndex {
		return "ok"
	}
	if !live {
		return "missing"
	}
	return "orphan"
}

// GetCycleAudit returns a real-time audit snapshot of the active cycle:
// DB levels, live exchange orders, in-memory orderIndex, and computed flags.
// GET /strategies/{id}/cycle-audit
func (s *Server) GetCycleAudit(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// 1. Verify ownership + load strategy meta.
	var accountID, symbol, category, direction string
	err := s.pool.QueryRow(r.Context(),
		`SELECT account_id, symbol, category, direction FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&accountID, &symbol, &category, &direction)
	if err != nil {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	// 2. Load the active cycle.
	var cycleID, tpOrderID, slOrderID string
	var cycleNum int
	var startedAt time.Time
	err = s.pool.QueryRow(r.Context(),
		`SELECT id, cycle_num, started_at, COALESCE(tp_order_id,''), COALESCE(sl_order_id,'')
		 FROM strategy_cycles WHERE strategy_id=$1 AND ended_at IS NULL
		 ORDER BY cycle_num DESC LIMIT 1`,
		id,
	).Scan(&cycleID, &cycleNum, &startedAt, &tpOrderID, &slOrderID)
	if err != nil {
		// No active cycle.
		writeJSON(w, http.StatusOK, map[string]any{"no_active_cycle": true})
		return
	}

	// 3. Load DB levels.
	type levelRow struct {
		Idx             int     `json:"idx"`
		Side            string  `json:"side"`
		TargetPrice     float64 `json:"target_price"`
		SizeUSDT        float64 `json:"size_usdt"`
		Qty             string  `json:"qty"`
		DbStatus        string  `json:"db_status"`
		FilledPrice     float64 `json:"filled_price"`
		ExchangeOrderID string  `json:"exchange_order_id"`
		LiveOnExchange  bool    `json:"live_on_exchange"`
		InOrderIndex    bool    `json:"in_order_index"`
		Flag            string  `json:"flag"`
	}
	lrows, err := s.pool.Query(r.Context(),
		`SELECT level_idx, side, target_price, size_usdt, qty, status,
		        COALESCE(filled_price,0), COALESCE(exchange_order_id,'')
		 FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`,
		cycleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	var levels []levelRow
	for lrows.Next() {
		var l levelRow
		if lrows.Scan(&l.Idx, &l.Side, &l.TargetPrice, &l.SizeUSDT, &l.Qty,
			&l.DbStatus, &l.FilledPrice, &l.ExchangeOrderID) == nil {
			levels = append(levels, l)
		}
	}
	lrows.Close()
	if levels == nil {
		levels = []levelRow{}
	}

	// 4. Load exchange credentials.
	creds, err := s.loadCreds(r, accountID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "creds error")
		return
	}

	// 5. Fetch live open orders.
	exchangeOrders, err := trader.FetchOpenOrders(r.Context(), creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "exchange error: "+err.Error())
		return
	}
	liveOrders := make(map[string]bool, len(exchangeOrders))
	for _, o := range exchangeOrders {
		liveOrders[o.OrderId] = true
	}

	// 6. Fetch current position.
	positions, err := trader.FetchPositions(r.Context(), creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "exchange error: "+err.Error())
		return
	}
	wantSide := "Buy"
	if direction == "short" {
		wantSide = "Sell"
	}
	var matchedPos *trader.Position
	for i := range positions {
		p := &positions[i]
		if p.Symbol == symbol && p.Side == wantSide {
			sz, _ := strconv.ParseFloat(p.Size, 64)
			if sz > 0 {
				matchedPos = p
				break
			}
		}
	}

	// 7. Snapshot in-memory orderIndex.
	var orderIndex map[string]bool
	if runner := s.engine.GetAccountRunner(accountID); runner != nil {
		orderIndex = runner.SnapshotOrderIndex()
	} else {
		orderIndex = map[string]bool{}
	}

	// 8. Compute qty discrepancy.
	var posSize float64
	if matchedPos != nil {
		posSize, _ = strconv.ParseFloat(matchedPos.Size, 64)
	}
	var filledQtySum float64
	for _, l := range levels {
		if l.DbStatus == "filled" {
			q, _ := strconv.ParseFloat(l.Qty, 64)
			filledQtySum += q
		}
	}
	qtyDiscrepancy := posSize - filledQtySum

	// 9. Compute per-level flags.
	for i := range levels {
		l := &levels[i]
		if l.ExchangeOrderID != "" {
			l.LiveOnExchange = liveOrders[l.ExchangeOrderID]
			l.InOrderIndex = orderIndex[l.ExchangeOrderID]
		}
		switch l.DbStatus {
		case "filled":
			l.Flag = "ok"
		case "pending":
			l.Flag = "pending"
		case "cancelled":
			l.Flag = "cancelled"
		case "placed":
			if l.LiveOnExchange && l.InOrderIndex {
				l.Flag = "ok"
			} else if l.LiveOnExchange && !l.InOrderIndex {
				l.Flag = "orphan"
			} else { // !l.LiveOnExchange
				if qtyDiscrepancy > 0.000001 {
					l.Flag = "missing_fill"
				} else {
					l.Flag = "cancelled"
				}
			}
		default:
			l.Flag = "ok"
		}
	}

	// 10. Build position output.
	type tpslOut struct {
		OrderID        string `json:"order_id"`
		LiveOnExchange bool   `json:"live_on_exchange"`
		InOrderIndex   bool   `json:"in_order_index"`
		Flag           string `json:"flag"`
	}
	var posOut any
	if matchedPos != nil {
		posOut = map[string]any{
			"size":           matchedPos.Size,
			"avg_entry":      matchedPos.EntryPrice,
			"side":           matchedPos.Side,
			"unrealised_pnl": matchedPos.UnrealisedPnl,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"no_active_cycle":         false,
		"cycle_num":               cycleNum,
		"started_at":              startedAt,
		"position":                posOut,
		"expected_qty_from_fills": fmt.Sprintf("%.6f", filledQtySum),
		"qty_discrepancy":         fmt.Sprintf("%.6f", qtyDiscrepancy),
		"levels":                  levels,
		"tp": tpslOut{
			OrderID:        tpOrderID,
			LiveOnExchange: liveOrders[tpOrderID],
			InOrderIndex:   orderIndex[tpOrderID],
			Flag:           computeTPSLFlag(tpOrderID, liveOrders[tpOrderID], orderIndex[tpOrderID]),
		},
		"sl": tpslOut{
			OrderID:        slOrderID,
			LiveOnExchange: liveOrders[slOrderID],
			InOrderIndex:   orderIndex[slOrderID],
			Flag:           computeTPSLFlag(slOrderID, liveOrders[slOrderID], orderIndex[slOrderID]),
		},
	})
}
