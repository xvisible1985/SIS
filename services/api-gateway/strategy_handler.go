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
	MaxStopActive         int             `json:"max_stop_active"`
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
	MatrixLevels          json.RawMessage `json:"matrix_levels"`
	SafeZonePct           float64         `json:"safe_zone_pct"`
	MatrixEntryLevel      json.RawMessage `json:"matrix_entry_level"`
	ProtectedBuild          bool            `json:"protected_build"`
	MatrixRebuildOnSL       bool            `json:"matrix_rebuild_on_sl"`
	MatrixRebuildFromEntry  bool            `json:"matrix_rebuild_from_entry"`
	SizeAsMain              bool            `json:"size_as_main"`
	TPSignalName            *string         `json:"tp_signal_name"`
	TPSignalDir             *string         `json:"tp_signal_dir"`
	SLSignalName            *string         `json:"sl_signal_name"`
	SLSignalDir             *string         `json:"sl_signal_dir"`
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
			s.grid_levels, s.grid_active, COALESCE(s.max_stop_active,0), s.grid_step_pct, s.grid_size_usdt,
			s.tp_mode, s.tp_pct, s.sl_type, s.sl_pct, s.signal_filter,
			s.leverage, s.margin_type, s.hedge_mode, s.strategy_type, s.entry_order_type,
			s.signal_configs::text, (s.steps::text),
			s.trailing_stop_enabled, s.trailing_activation_pct, s.trailing_callback_pct,
			(s.matrix_levels::text), COALESCE(s.safe_zone_pct,0), (s.matrix_entry_level::text),
			COALESCE(s.protected_build,false), COALESCE(s.matrix_rebuild_on_sl,false),
			COALESCE(s.matrix_rebuild_from_entry,false), COALESCE(s.size_as_main,false),
			s.hedged_strategy_id,
			s.tp_signal_name, s.tp_signal_dir, s.sl_signal_name, s.sl_signal_dir,
			s.created_at, s.updated_at, s.manual_alert,
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
			), 0),
			s.bot_id, b.name
		FROM strategies s
		LEFT JOIN bots b ON b.id = s.bot_id
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
		MaxStopActive         int             `json:"max_stop_active"`
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
		MatrixLevels          json.RawMessage `json:"matrix_levels,omitempty"`
		SafeZonePct           float64         `json:"safe_zone_pct"`
		MatrixEntryLevel      json.RawMessage `json:"matrix_entry_level,omitempty"`
		ProtectedBuild          bool            `json:"protected_build"`
		MatrixRebuildOnSL       bool            `json:"matrix_rebuild_on_sl"`
		MatrixRebuildFromEntry  bool            `json:"matrix_rebuild_from_entry"`
		SizeAsMain              bool            `json:"size_as_main"`
		HedgedStrategyID        *string         `json:"hedged_strategy_id"`
		CreatedAt               time.Time       `json:"created_at"`
		UpdatedAt             time.Time       `json:"updated_at"`
		ManualAlert           *string         `json:"manual_alert"`
		VolumeUSDT            float64         `json:"volume_usdt"`
		ActiveLevels          int             `json:"active_levels"`
		LastPnl               float64         `json:"last_pnl"`
		BotID                 *string         `json:"bot_id"`
		BotName               *string         `json:"bot_name"`
		TPSignalName          *string         `json:"tp_signal_name"`
		TPSignalDir           *string         `json:"tp_signal_dir"`
		SLSignalName          *string         `json:"sl_signal_name"`
		SLSignalDir           *string         `json:"sl_signal_dir"`
	}

	var result []row
	for rows.Next() {
		var r row
		var scStr string
		var stepsStr, matrixStr, entryLevelStr *string
		if err := rows.Scan(
			&r.ID, &r.AccountID, &r.Symbol, &r.Category, &r.Direction, &r.Status,
			&r.GridLevels, &r.GridActive, &r.MaxStopActive, &r.GridStepPct, &r.GridSizeUSDT,
			&r.TPMode, &r.TPPct, &r.SLType, &r.SLPct, &r.SignalFilter,
			&r.Leverage, &r.MarginType, &r.HedgeMode, &r.StrategyType, &r.EntryOrderType,
			&scStr, &stepsStr,
			&r.TrailingStopEnabled, &r.TrailingActivationPct, &r.TrailingCallbackPct,
			&matrixStr, &r.SafeZonePct, &entryLevelStr, &r.ProtectedBuild, &r.MatrixRebuildOnSL,
			&r.MatrixRebuildFromEntry, &r.SizeAsMain,
			&r.HedgedStrategyID,
			&r.TPSignalName, &r.TPSignalDir, &r.SLSignalName, &r.SLSignalDir,
			&r.CreatedAt, &r.UpdatedAt, &r.ManualAlert,
			&r.VolumeUSDT, &r.ActiveLevels, &r.LastPnl,
			&r.BotID, &r.BotName,
		); err == nil {
			r.SignalConfigs = json.RawMessage(scStr)
			if stepsStr != nil {
				r.Steps = json.RawMessage(*stepsStr)
			}
			if matrixStr != nil {
				r.MatrixLevels = json.RawMessage(*matrixStr)
			}
			if entryLevelStr != nil {
				r.MatrixEntryLevel = json.RawMessage(*entryLevelStr)
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

	// Prevent duplicate strategies for same account+symbol+direction (any status).
	var dupCount int
	if err := s.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM strategies
		 WHERE owner_id=$1 AND account_id=$2 AND symbol=$3 AND direction=$4
		   AND status IN ('active','finishing','stopped')`,
		userID, req.AccountID, req.Symbol, req.Direction,
	).Scan(&dupCount); err == nil && dupCount > 0 {
		writeError(w, http.StatusConflict, "стратегия для этой пары уже существует")
		return
	}

	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO strategies
		  (owner_id, account_id, symbol, category, direction,
		   grid_levels, grid_active, max_stop_active, grid_step_pct, grid_size_usdt,
		   tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
		   leverage, margin_type, hedge_mode, strategy_type, entry_order_type,
		   signal_configs, steps,
		   trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct,
		   matrix_levels, safe_zone_pct, matrix_entry_level, protected_build, matrix_rebuild_on_sl,
		   matrix_rebuild_from_entry, size_as_main,
		   tp_signal_name, tp_signal_dir, sl_signal_name, sl_signal_dir)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20,
		        $21::jsonb, ($22::text)::jsonb,
		        $23,$24,$25,
		        ($26::text)::jsonb, $27, ($28::text)::jsonb, $29, $30, $31, $32,
		        $33,$34,$35,$36)
		RETURNING id`,
		userID, req.AccountID, req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.MaxStopActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		req.Leverage, req.MarginType, req.HedgeMode, req.StrategyType, req.EntryOrderType,
		string(req.SignalConfigs), nullableJSONB(req.Steps),
		req.TrailingStopEnabled, req.TrailingActivationPct, req.TrailingCallbackPct,
		nullableJSONB(req.MatrixLevels), req.SafeZonePct, nullableJSONB(req.MatrixEntryLevel),
		req.ProtectedBuild, req.MatrixRebuildOnSL, req.MatrixRebuildFromEntry, req.SizeAsMain,
		req.TPSignalName, req.TPSignalDir, req.SLSignalName, req.SLSignalDir,
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

	// Prevent updating into a duplicate (same account+symbol+direction already exists).
	var curAccID, curSymbol, curDirection string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT account_id, symbol, direction FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&curAccID, &curSymbol, &curDirection)
	newSym := req.Symbol
	if newSym == "" {
		newSym = curSymbol
	}
	newDir := req.Direction
	if newDir == "" {
		newDir = curDirection
	}
	if newSym != curSymbol || newDir != curDirection {
		var dupCount int
		if err := s.pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM strategies
			 WHERE id<>$1 AND owner_id=$2 AND account_id=$3 AND symbol=$4 AND direction=$5
			   AND status IN ('active','finishing','stopped')`,
			id, userID, curAccID, newSym, newDir,
		).Scan(&dupCount); err == nil && dupCount > 0 {
			writeError(w, http.StatusConflict, "стратегия для этой пары уже существует")
			return
		}
	}

	// Read current grid fields so we can detect whether the grid actually changed.
	var oldGridLevels, oldGridActive int
	var oldGridStepPct, oldGridSizeUSDT float64
	var oldDirection, oldEntryOrderType string
	var oldStepsJSON, oldMatrixLevelsJSON, oldMatrixEntryJSON *string
	_ = s.pool.QueryRow(r.Context(),
		`SELECT grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        direction, entry_order_type, steps::text,
		        matrix_levels::text, matrix_entry_level::text
		 FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&oldGridLevels, &oldGridActive, &oldGridStepPct, &oldGridSizeUSDT,
		&oldDirection, &oldEntryOrderType, &oldStepsJSON,
		&oldMatrixLevelsJSON, &oldMatrixEntryJSON)

	tag, err := s.pool.Exec(r.Context(), `
		UPDATE strategies SET
		  symbol=$1, category=$2, direction=$3,
		  grid_levels=$4, grid_active=$5, max_stop_active=$6, grid_step_pct=$7, grid_size_usdt=$8,
		  tp_mode=$9, tp_pct=$10, sl_type=$11, sl_pct=$12, signal_filter=$13,
		  leverage=$14, margin_type=$15, hedge_mode=$16, strategy_type=$17, entry_order_type=$18,
		  signal_configs=$19::jsonb, steps=($20::text)::jsonb,
		  trailing_stop_enabled=$21, trailing_activation_pct=$22, trailing_callback_pct=$23,
		  matrix_levels=($24::text)::jsonb,
		  safe_zone_pct=$25, matrix_entry_level=($26::text)::jsonb,
		  protected_build=$27, matrix_rebuild_on_sl=$28,
		  matrix_rebuild_from_entry=$29, size_as_main=$30,
		  tp_signal_name=$31, tp_signal_dir=$32, sl_signal_name=$33, sl_signal_dir=$34,
		  updated_at=NOW()
		WHERE id=$35 AND owner_id=$36`,
		req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.MaxStopActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		req.Leverage, req.MarginType, req.HedgeMode, req.StrategyType, req.EntryOrderType,
		string(req.SignalConfigs), nullableJSONB(req.Steps),
		req.TrailingStopEnabled, req.TrailingActivationPct, req.TrailingCallbackPct,
		nullableJSONB(req.MatrixLevels),
		req.SafeZonePct, nullableJSONB(req.MatrixEntryLevel),
		req.ProtectedBuild, req.MatrixRebuildOnSL, req.MatrixRebuildFromEntry, req.SizeAsMain,
		req.TPSignalName, req.TPSignalDir, req.SLSignalName, req.SLSignalDir,
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
	newMatrixLevelsJSON := nullableJSONB(req.MatrixLevels)
	newMatrixEntryJSON := nullableJSONB(req.MatrixEntryLevel)
	gridChanged := oldGridLevels != req.GridLevels ||
		oldGridActive != req.GridActive ||
		diffFloat(oldGridStepPct, req.GridStepPct) ||
		diffFloat(oldGridSizeUSDT, req.GridSizeUSDT) ||
		oldDirection != req.Direction ||
		oldEntryOrderType != req.EntryOrderType ||
		!stepsEqual(oldStepsJSON, newStepsJSON) ||
		!stepsEqual(oldMatrixLevelsJSON, newMatrixLevelsJSON) ||
		!stepsEqual(oldMatrixEntryJSON, newMatrixEntryJSON)

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

	// When activating, check for existing active/finishing strategy on the same account+symbol+direction.
	if req.Status == "active" {
		var sym, dir, accID string
		if err := s.pool.QueryRow(r.Context(),
			`SELECT symbol, direction, account_id FROM strategies WHERE id=$1 AND owner_id=$2`,
			id, userID,
		).Scan(&sym, &dir, &accID); err != nil {
			writeError(w, http.StatusNotFound, "strategy not found")
			return
		}
		var dupCount int
		if err := s.pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM strategies
			 WHERE id<>$1 AND owner_id=$2 AND account_id=$3 AND symbol=$4 AND direction=$5
			   AND status IN ('active','finishing')`,
			id, userID, accID, sym, dir,
		).Scan(&dupCount); err == nil && dupCount > 0 {
			writeError(w, http.StatusConflict, "уже есть активная стратегия для этой пары")
			return
		}
	}

	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET status=$1, updated_at=NOW(), manual_alert=NULL WHERE id=$2 AND owner_id=$3`,
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
	// capture for goroutine
	stratID := id
	newStatus := req.Status
	go func() {
		ctx := context.Background()
		s.engine.Notify(ctx, stratID)
		s.engine.LogUserAction(ctx, stratID, logMsg)
		// Telegram notification for status change
		var chatID int64
		var symbol string
		var muteUntil *time.Time
		err := s.pool.QueryRow(ctx, `
			SELECT tc.chat_id, st.symbol, tc.mute_until
			FROM strategies st
			JOIN telegram_connections tc ON tc.user_id = st.owner_id
			WHERE st.id = $1`, stratID,
		).Scan(&chatID, &symbol, &muteUntil)
		if err == nil && (muteUntil == nil || muteUntil.Before(time.Now())) {
			icons := map[string]string{"active": "🟢", "finishing": "🟡", "stopped": "⏸"}
			text := fmt.Sprintf("%s *%s* — статус изменён: %s", icons[newStatus], symbol, statusLabel)
			s.publishTgNotify(ctx, TgNotifyMsg{ChatID: chatID, Text: text})
		}
	}()
}

// DeleteStrategy deletes a strategy (only if stopped and no open position).
// DELETE /strategies/{id}
func (s *Server) DeleteStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// Fetch account_id and check for open filled levels in a single query.
	var accountID string
	var openFills int
	err := s.pool.QueryRow(r.Context(), `
		SELECT st.account_id,
		       COALESCE((
		           SELECT COUNT(*) FROM strategy_levels sl
		           JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		           WHERE sc.strategy_id = st.id
		             AND sc.ended_at IS NULL
		             AND sl.status = 'filled'
		       ), 0)
		FROM strategies st
		WHERE st.id = $1 AND st.owner_id = $2 AND st.status = 'stopped'
	`, id, userID).Scan(&accountID, &openFills)
	if err != nil {
		writeError(w, http.StatusBadRequest, "strategy not found or not stopped")
		return
	}
	if openFills > 0 {
		writeError(w, http.StatusBadRequest, "у стратегии есть открытая позиция — дождитесь её закрытия перед удалением")
		return
	}

	if _, err := s.pool.Exec(r.Context(), `DELETE FROM strategies WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Cancel remaining exchange orders and clean up the in-memory runner.
	s.engine.ForceRemoveStrategy(r.Context(), id, accountID)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetStrategyEvents returns events for a strategy with optional filters, pagination and total count.
// Query params: level (error|warn|info), date (YYYY-MM-DD), limit (max 500), offset.
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

	// parse query params
	levelFilter := r.URL.Query().Get("level")
	dateFilter := r.URL.Query().Get("date")
	limit := 200
	if v, _ := strconv.Atoi(r.URL.Query().Get("limit")); v > 0 && v <= 500 {
		limit = v
	}
	offset := 0
	if v, _ := strconv.Atoi(r.URL.Query().Get("offset")); v > 0 {
		offset = v
	}

	// build WHERE clause
	whereParts := []string{"strategy_id=$1"}
	args := []interface{}{id}
	argIdx := 1

	if levelFilter != "" && levelFilter != "all" {
		argIdx++
		whereParts = append(whereParts, fmt.Sprintf("level=$%d", argIdx))
		args = append(args, levelFilter)
	}
	if dateFilter != "" {
		argIdx++
		whereParts = append(whereParts, fmt.Sprintf("DATE(created_at)=$%d", argIdx))
		args = append(args, dateFilter)
	}

	whereSQL := strings.Join(whereParts, " AND ")

	// total with filters
	var total int
	_ = s.pool.QueryRow(r.Context(),
		fmt.Sprintf("SELECT COUNT(*) FROM strategy_events WHERE %s", whereSQL),
		args...,
	).Scan(&total)

	// fetch events
	queryArgs := append([]interface{}{}, args...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := s.pool.Query(r.Context(),
		fmt.Sprintf(`SELECT message, level, created_at
			FROM strategy_events
			WHERE %s
			ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, whereSQL, argIdx+1, argIdx+2),
		queryArgs...,
	)
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
	var events []eventRow
	for rows.Next() {
		var e eventRow
		if rows.Scan(&e.Message, &e.Level, &e.CreatedAt) == nil {
			events = append(events, e)
		}
	}
	if events == nil {
		events = []eventRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "events": events})
}

// GetStrategyState returns the active cycle's levels plus computed volume and avg entry.
// GET /strategies/{id}/state
func (s *Server) GetStrategyState(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// Verify ownership and read strategy_type in one query.
	var strategyType string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT strategy_type FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&strategyType); err != nil {
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
		Slot            *int16  `json:"slot,omitempty"`
		SLOrderID       string  `json:"sl_order_id,omitempty"`
		SLPrice         float64 `json:"sl_price,omitempty"`
		SLReplaced      bool    `json:"sl_replaced,omitempty"`
		ForceVirtual    bool    `json:"force_virtual,omitempty"`
	}
	var levels []levelInfo
	var volumeUSDT, totalCost, totalCoins float64

	if err == nil {
		lrows, lErr := s.pool.Query(r.Context(), `
			SELECT level_idx, side, target_price, size_usdt, status, COALESCE(filled_price,0), COALESCE(exchange_order_id,''),
			       slot, COALESCE(sl_order_id,''), COALESCE(sl_price,0), COALESCE(sl_replaced,false), COALESCE(force_virtual,false)
			FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`, cycleID)
		if lErr == nil && lrows != nil {
			defer lrows.Close()
			for lrows.Next() {
				var l levelInfo
				if lrows.Scan(&l.LevelIdx, &l.Side, &l.TargetPrice,
					&l.SizeUSDT, &l.Status, &l.FilledPrice, &l.ExchangeOrderID,
					&l.Slot, &l.SLOrderID, &l.SLPrice, &l.SLReplaced, &l.ForceVirtual) == nil {
					levels = append(levels, l)
					if l.Status == "filled" && l.FilledPrice > 0 {
						volumeUSDT += l.SizeUSDT
						totalCost += l.SizeUSDT
						totalCoins += l.SizeUSDT / l.FilledPrice
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

	// Compute safe zone from the engine's in-memory state (matrix only).
	// Reading from memory (not DB) ensures the zone disappears the moment the price
	// exits it — DB sl_closed rows persist forever and would keep the zone visible.
	type safeZoneInfo struct {
		Low  float64 `json:"low"`
		High float64 `json:"high"`
	}
	var safeZone *safeZoneInfo
	if strategyType == "matrix" {
		if sz := s.engine.GetMatrixSafeZone(id); sz != nil {
			safeZone = &safeZoneInfo{Low: sz.Low, High: sz.High}
		}
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
		"safe_zone":     safeZone,
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

// RestartCycle force-restarts the active cycle for a strategy: cancels placed orders,
// closes the current cycle in DB, and starts a fresh one.
// POST /strategies/{id}/cycle-restart
func (s *Server) RestartCycle(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT true FROM strategies WHERE id=$1 AND owner_id=$2`, id, userID,
	).Scan(&exists); err != nil {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	s.pool.Exec(r.Context(), //nolint:errcheck
		`UPDATE strategies SET manual_alert=NULL WHERE id=$1 AND owner_id=$2`, id, userID)
	s.engine.RestartCycle(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DetachFromBot removes bot management from a strategy.
// POST /strategies/{id}/detach
//
// Body (JSON, optional):
//
//	{
//	  "action":       "adopt" | "close" | "leave",  // default: "leave"
//	  "add_blacklist": true | false,
//	  "position": {       // required for "adopt" and "close"
//	    "size":         "35",
//	    "side":         "Buy",
//	    "entry_price":  "0.4647",
//	    "position_idx": 2
//	  }
//	}
//
// Actions:
//
//	adopt — stop old strategy, create new copy with adopt_position_data set;
//	        startMatrixCycle absorbs existing position without placing market L(0)
//	close — place market reduce-only close for hedge position, stop strategy
//	leave — set bot_id=NULL, keep status=active; strategy runs independently,
//	        bot finds slot occupied via resolveHedgeSlotConflict
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
	var symbol, category, accountID string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT bot_id, symbol, category, account_id
		 FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&botID, &symbol, &category, &accountID); err != nil {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}

	// Optional blacklist (applied regardless of action).
	if body.AddBlacklist && botID != nil {
		s.pool.Exec(r.Context(), //nolint:errcheck
			`UPDATE bots SET symbol_blacklist = array_append(symbol_blacklist, $1)
			 WHERE id=$2 AND owner_id=$3 AND NOT ($1 = ANY(symbol_blacklist))`,
			symbol, *botID, userID)
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
			writeError(w, http.StatusNotFound, "strategy not found")
			return
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
			writeError(w, http.StatusNotFound, "strategy not found")
			return
		}
		go s.engine.Notify(context.Background(), id)

		if body.Position != nil && body.Position.Size != "" && body.Position.Size != "0" {
			if creds, credsErr := s.loadCreds(r, accountID, userID); credsErr == nil {
				closeSide := "Sell"
				if body.Position.Side == "Sell" {
					closeSide = "Buy"
				}
				closeReq := trader.OrderRequest{
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
		closeMsg := fmt.Sprintf("Стратегия %s откреплена (close)", symbol)
		if body.Position != nil && body.Position.Size != "" && body.Position.Size != "0" {
			closeMsg = fmt.Sprintf("Стратегия %s откреплена (close) — позиция закрыта", symbol)
		}
		if botID != nil {
			s.logBotEvent(r.Context(), *botID, closeMsg, "info", "user")
		}
		s.pool.Exec(r.Context(), //nolint:errcheck
			`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
			id, "Откреплена от бота (close) — позиция закрыта рыночным ордером")

	default: // "leave"
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
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// botIDOrNull converts *string to interface{} for pgx $N binding (nil → SQL NULL).
func botIDOrNull(botID *string) interface{} {
	if botID == nil {
		return nil
	}
	return *botID
}

// DismissManualAlert clears the manual intervention alert on a strategy.
// POST /strategies/{id}/dismiss-alert
func (s *Server) DismissManualAlert(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET manual_alert=NULL WHERE id=$1 AND owner_id=$2`,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetCycleAudit returns a real-time audit snapshot of the active cycle:
// DB levels, live exchange orders, in-memory orderIndex, and computed flags.
// GET /strategies/{id}/cycle-audit
func (s *Server) GetCycleAudit(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	// 1. Verify ownership + load strategy meta.
	var accountID, symbol, category, direction, strategyType string
	err := s.pool.QueryRow(r.Context(),
		`SELECT account_id, symbol, category, direction, COALESCE(strategy_type,'grid') FROM strategies WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&accountID, &symbol, &category, &direction, &strategyType)
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
		Slot            *int    `json:"slot"`
		Side            string  `json:"side"`
		TargetPrice     float64 `json:"target_price"`
		SizeUSDT        float64 `json:"size_usdt"`
		Qty             string  `json:"qty"`
		DbStatus        string  `json:"db_status"`
		FilledPrice     float64 `json:"filled_price"`
		ExchangeOrderID string  `json:"exchange_order_id"`
		SlOrderID       string  `json:"sl_order_id"`
		SlPrice         float64 `json:"sl_price"`
		SlReplaced      bool    `json:"sl_replaced"`
		ForceVirtual    bool    `json:"force_virtual"`
		LiveOnExchange  bool    `json:"live_on_exchange"`
		SlLiveOnExchange bool   `json:"sl_live_on_exchange"`
		InOrderIndex    bool    `json:"in_order_index"`
		SlInOrderIndex  bool    `json:"sl_in_order_index"`
		Flag            string  `json:"flag"`
	}
	lrows, err := s.pool.Query(r.Context(),
		`SELECT level_idx, slot, side, target_price, size_usdt, qty, status,
		        COALESCE(filled_price,0), COALESCE(exchange_order_id,''),
		        COALESCE(sl_order_id,''), COALESCE(sl_price,0), COALESCE(sl_replaced,false), COALESCE(force_virtual,false)
		 FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx`,
		cycleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	var levels []levelRow
	for lrows.Next() {
		var l levelRow
		if lrows.Scan(&l.Idx, &l.Slot, &l.Side, &l.TargetPrice, &l.SizeUSDT, &l.Qty,
			&l.DbStatus, &l.FilledPrice, &l.ExchangeOrderID,
			&l.SlOrderID, &l.SlPrice, &l.SlReplaced, &l.ForceVirtual) == nil {
			levels = append(levels, l)
		}
	}
	lrows.Close()
	if lrows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
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
	exchangeOrders, err := trader.FetchOpenOrdersForSymbolAll(r.Context(), creds, category, symbol)
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
		// For matrix strategies, sl_closed means the level entered AND was subsequently
		// closed by its per-level SL (net-zero effect on open position). Only "filled"
		// levels still hold an open position contribution. For grid both statuses count.
		if l.DbStatus == "filled" || (l.DbStatus == "sl_closed" && strategyType != "matrix") {
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
		if l.SlOrderID != "" {
			l.SlLiveOnExchange = liveOrders[l.SlOrderID]
			l.SlInOrderIndex = orderIndex[l.SlOrderID]
		}
		switch l.DbStatus {
		case "filled", "sl_closed":
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
		"strategy_type":           strategyType,
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

// GetHedgeSession returns the most recent hedge session for a strategy.
// The strategy ID can be either the main_strategy_id or hedge_strategy_id.
// Cumulative hedge P&L is computed live from trade_history.
// GET /strategies/{id}/hedge-session
func (s *Server) GetHedgeSession(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	stratID := chi.URLParam(r, "id")

	type sessionResp struct {
		ID                 string   `json:"id"`
		BotID              string   `json:"bot_id"`
		MainStrategyID     *string  `json:"main_strategy_id"`
		HedgeStrategyID    string   `json:"hedge_strategy_id"`
		MainEntryAtStart   *float64 `json:"main_entry_at_start"`
		HedgeEntryAtStart  *float64 `json:"hedge_entry_at_start"`
		GapAtStart         *float64 `json:"gap_at_start"`
		StartedAt          string   `json:"started_at"`
		EndedAt            *string  `json:"ended_at"`
		CumulativeHedgePnl float64  `json:"cumulative_hedge_pnl"`
	}

	var resp sessionResp
	err := s.pool.QueryRow(r.Context(), `
		SELECT
			hs.id::text,
			hs.bot_id::text,
			hs.main_strategy_id::text,
			hs.hedge_strategy_id::text,
			hs.main_entry_at_start,
			hs.hedge_entry_at_start,
			hs.gap_at_start,
			hs.started_at::text,
			hs.ended_at::text,
			COALESCE((
				SELECT SUM(th.net_pnl)
				FROM trade_history th
				WHERE th.strategy_id = hs.hedge_strategy_id
				  AND th.closed_at >= hs.started_at
				  AND (hs.ended_at IS NULL OR th.closed_at <= hs.ended_at)
			), 0)
		FROM hedge_sessions hs
		WHERE (hs.main_strategy_id = $1::uuid OR hs.hedge_strategy_id = $1::uuid)
		  AND hs.bot_id IN (SELECT id FROM bots WHERE owner_id = $2::uuid)
		ORDER BY hs.started_at DESC
		LIMIT 1`,
		stratID, userID,
	).Scan(
		&resp.ID, &resp.BotID, &resp.MainStrategyID, &resp.HedgeStrategyID,
		&resp.MainEntryAtStart, &resp.HedgeEntryAtStart, &resp.GapAtStart,
		&resp.StartedAt, &resp.EndedAt, &resp.CumulativeHedgePnl,
	)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

