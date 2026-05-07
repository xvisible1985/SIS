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
	var volumeUSDT, totalCost, totalCoins float64

	if err == nil {
		lrows, lErr := s.pool.Query(r.Context(), `
			SELECT level_idx, side, target_price, size_usdt, status, COALESCE(filled_price,0)
			FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`, cycleID)
		if lErr == nil && lrows != nil {
			defer lrows.Close()
			for lrows.Next() {
				var l levelInfo
				if lrows.Scan(&l.LevelIdx, &l.Side, &l.TargetPrice,
					&l.SizeUSDT, &l.Status, &l.FilledPrice) == nil {
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
	if totalCoins > 0 {
		avgEntry = totalCost / totalCoins
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
