// services/api-gateway/bots_handler.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

// в”Ђв”Ђ Types в”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђ

type botResp struct {
	ID                    string          `json:"id"`
	Name                  string          `json:"name"`
	Description           string          `json:"description"`
	FullDescription       string          `json:"fullDescription"`
	AvatarURL             string          `json:"avatarUrl"`
	OwnerID               string          `json:"ownerId"`
	OwnerName             string          `json:"ownerName"`
	IsOwn                 bool            `json:"isOwn"`
	IsPublic              bool            `json:"isPublic"`
	IsOfficial            bool            `json:"isOfficial"`
	Status                string          `json:"status"`
	SourceBotID           *string         `json:"sourceBotId"`
	IsFork                bool            `json:"isFork"`
	SymbolWhitelist       []string        `json:"symbolWhitelist"`
	SymbolBlacklist       []string        `json:"symbolBlacklist"`
	Triggers              json.RawMessage `json:"triggers"`
	StrategyConfig        json.RawMessage `json:"strategyConfig"`
	DeployCount           int             `json:"deployCount"`
	CreatedAt             time.Time       `json:"createdAt"`
	MaxStrategies         int             `json:"maxStrategies"`
	MaxLongStrategies     int             `json:"maxLongStrategies"`
	MaxShortStrategies    int             `json:"maxShortStrategies"`
	MaxMarginUsdt         float64         `json:"maxMarginUsdt"`
	MaxSymConsecutiveRuns int             `json:"maxSymConsecutiveRuns"`
	ActiveStrategiesCount int             `json:"activeStrategiesCount"`
	AccountID             *string         `json:"accountId"`
	AutoMode              bool            `json:"autoMode"`
}

type listBotsResp struct {
	Catalog []botResp `json:"catalog"`
	Mine    []botResp `json:"mine"`
}

type scanHit struct {
	Symbol         string  `json:"symbol"`
	SignalState    string  `json:"signal_state"`
	Direction      string  `json:"direction"`
	AlreadyOpen    bool    `json:"already_open"`
	DirBlocked     bool    `json:"dir_blocked"`
	SignalValue    float64 `json:"signal_value"`     // raw indicator value (e.g. RSI=14.2)
	Strength       float64 `json:"strength"`         // sort key: higher = stronger signal
	TTLRemainingSec float64 `json:"ttl_remaining_sec"` // -1 = no TTL; в‰Ґ0 = seconds left
}

// в”Ђв”Ђ Helpers в”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђ

const botCols = `b.id, b.name, b.description, b.full_description, b.avatar_url, b.owner_id, u.email,
	b.is_public, b.is_official, b.status, b.source_bot_id, b.is_fork,
	b.symbol_whitelist, b.symbol_blacklist,
	b.triggers, b.strategy_config, b.deploy_count, b.created_at,
	b.max_strategies, b.max_margin_usdt,
	(SELECT COUNT(*) FROM strategies s WHERE s.bot_id = b.id AND s.status = 'active') AS active_strategies_count,
	b.account_id, b.auto_mode, b.max_long_strategies, b.max_short_strategies, b.max_sym_consecutive_runs`

const botFrom = ` FROM bots b JOIN users u ON u.id = b.owner_id `

// collectBots scans all rows into []botResp and closes rows.
func collectBots(rows pgx.Rows, callerID string) ([]botResp, error) {
	defer rows.Close()
	var result []botResp
	for rows.Next() {
		var b botResp
		var triggers, stratCfg []byte
		if err := rows.Scan(
			&b.ID, &b.Name, &b.Description, &b.FullDescription, &b.AvatarURL, &b.OwnerID, &b.OwnerName,
			&b.IsPublic, &b.IsOfficial, &b.Status, &b.SourceBotID, &b.IsFork,
			&b.SymbolWhitelist, &b.SymbolBlacklist,
			&triggers, &stratCfg, &b.DeployCount, &b.CreatedAt,
			&b.MaxStrategies, &b.MaxMarginUsdt, &b.ActiveStrategiesCount,
			&b.AccountID, &b.AutoMode, &b.MaxLongStrategies, &b.MaxShortStrategies, &b.MaxSymConsecutiveRuns,
		); err != nil {
			return nil, err
		}
		b.Triggers = json.RawMessage(triggers)
		b.StrategyConfig = json.RawMessage(stratCfg)
		b.IsOwn = b.OwnerID == callerID
		if b.SymbolWhitelist == nil {
			b.SymbolWhitelist = []string{}
		}
		if b.SymbolBlacklist == nil {
			b.SymbolBlacklist = []string{}
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// fetchBot fetches a single bot by id.
func fetchBot(s *Server, r *http.Request, botID, callerID string) (botResp, bool) {
	rows, err := s.pool.Query(r.Context(), `SELECT `+botCols+botFrom+`WHERE b.id = $1`, botID)
	if err != nil {
		return botResp{}, false
	}
	bots, err := collectBots(rows, callerID)
	if err != nil || len(bots) == 0 {
		return botResp{}, false
	}
	return bots[0], true
}

// в”Ђв”Ђ Handlers в”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђ

// GET /bots
func (s *Server) ListBots(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	ctx := r.Context()
	q := r.URL.Query().Get("q")
	direction := r.URL.Query().Get("direction")

	orderBy := "b.created_at DESC"
	if r.URL.Query().Get("sort") == "popular" {
		orderBy = "b.deploy_count DESC"
	}

	catalogSQL := `SELECT ` + botCols + botFrom + `
		WHERE b.is_public = true
		  AND ($1 = '' OR b.name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR b.strategy_config->>'direction' = $2)
		ORDER BY ` + orderBy

	catalogRows, err := s.pool.Query(ctx, catalogSQL, q, direction)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	catalog, err := collectBots(catalogRows, callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan error")
		return
	}
	if catalog == nil {
		catalog = []botResp{}
	}

	mineRows, err := s.pool.Query(ctx,
		`SELECT `+botCols+botFrom+`WHERE b.owner_id = $1 ORDER BY b.created_at DESC`,
		callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	mine, err := collectBots(mineRows, callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan error")
		return
	}
	if mine == nil {
		mine = []botResp{}
	}

	writeJSON(w, http.StatusOK, listBotsResp{Catalog: catalog, Mine: mine})
}

// POST /bots
func (s *Server) CreateBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	ctx := r.Context()

	var req struct {
		Name               string          `json:"name"`
		Description        string          `json:"description"`
		FullDescription    string          `json:"fullDescription"`
		AvatarURL          string          `json:"avatarUrl"`
		IsPublic           bool            `json:"isPublic"`
		AccountID          *string         `json:"accountId"`
		SymbolWhitelist    []string        `json:"symbolWhitelist"`
		SymbolBlacklist    []string        `json:"symbolBlacklist"`
		Triggers           json.RawMessage `json:"triggers"`
		StrategyConfig     json.RawMessage `json:"strategyConfig"`
		MaxStrategies        int             `json:"maxStrategies"`
		MaxLongStrategies    int             `json:"maxLongStrategies"`
		MaxShortStrategies   int             `json:"maxShortStrategies"`
		MaxMarginUsdt        float64         `json:"maxMarginUsdt"`
		MaxSymConsecutiveRuns int             `json:"maxSymConsecutiveRuns"`
		AutoMode             bool            `json:"autoMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if len(req.Triggers) == 0 {
		req.Triggers = json.RawMessage("[]")
	}
	if len(req.StrategyConfig) == 0 {
		req.StrategyConfig = json.RawMessage("{}")
	}
	if req.SymbolWhitelist == nil {
		req.SymbolWhitelist = []string{}
	}
	if req.SymbolBlacklist == nil {
		req.SymbolBlacklist = []string{}
	}

	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO bots (owner_id, name, description, full_description, avatar_url, is_public,
		                  account_id, symbol_whitelist, symbol_blacklist, triggers, strategy_config,
		                  max_strategies, max_long_strategies, max_short_strategies, max_margin_usdt, max_sym_consecutive_runs, auto_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id`,
		callerID, req.Name, req.Description, req.FullDescription, req.AvatarURL, req.IsPublic,
		req.AccountID, req.SymbolWhitelist, req.SymbolBlacklist,
		[]byte(req.Triggers), []byte(req.StrategyConfig),
		req.MaxStrategies, req.MaxLongStrategies, req.MaxShortStrategies, req.MaxMarginUsdt, req.MaxSymConsecutiveRuns, req.AutoMode,
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	bot, ok := fetchBot(s, r, id, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}

// GET /bots/{id}
func (s *Server) GetBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")

	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !bot.IsPublic && !bot.IsOwn {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	writeJSON(w, http.StatusOK, bot)
}

// PATCH /bots/{id}
func (s *Server) PatchBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	var ownerID string
	var isFork bool
	var sourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, is_fork, source_bot_id FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &isFork, &sourceID); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if ownerID != callerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if sourceID != nil && !isFork {
		writeError(w, http.StatusForbidden, "fork first")
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	args := []interface{}{botID}
	sets := []string{}

	addStr := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			var s string
			json.Unmarshal(v, &s) //nolint:errcheck
			args = append(args, s)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}
	addBool := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			var b bool
			json.Unmarshal(v, &b) //nolint:errcheck
			args = append(args, b)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}
	addSlice := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			var sl []string
			json.Unmarshal(v, &sl) //nolint:errcheck
			if sl == nil {
				sl = []string{}
			}
			args = append(args, sl)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}
	addRaw := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			args = append(args, []byte(v))
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}

	addInt := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			var i int
			json.Unmarshal(v, &i) //nolint:errcheck
			args = append(args, i)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}
	addFloat := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			var f float64
			json.Unmarshal(v, &f) //nolint:errcheck
			args = append(args, f)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}

	addNullableStr := func(jsonKey, col string) {
		if v, ok := body[jsonKey]; ok {
			var sv *string
			var tmp string
			if json.Unmarshal(v, &tmp) == nil && tmp != "" {
				sv = &tmp
			}
			args = append(args, sv)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}

	addStr("name", "name")
	addStr("description", "description")
	addStr("fullDescription", "full_description")
	addStr("avatarUrl", "avatar_url")
	addBool("isPublic", "is_public")
	addBool("autoMode", "auto_mode")
	addNullableStr("accountId", "account_id")
	addSlice("symbolWhitelist", "symbol_whitelist")
	addSlice("symbolBlacklist", "symbol_blacklist")
	addRaw("triggers", "triggers")
	addRaw("strategyConfig", "strategy_config")
	addInt("maxStrategies", "max_strategies")
	addInt("maxLongStrategies", "max_long_strategies")
	addInt("maxShortStrategies", "max_short_strategies")
	addFloat("maxMarginUsdt", "max_margin_usdt")
	addInt("MaxSymConsecutiveRuns", "max_sym_consecutive_runs")

	if len(sets) == 0 {
		bot, ok := fetchBot(s, r, botID, callerID)
		if !ok {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		writeJSON(w, http.StatusOK, bot)
		return
	}

	sets = append(sets, "updated_at = NOW()")
	sql := "UPDATE bots SET " + strings.Join(sets, ", ") + " WHERE id = $1"
	if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	s.logBotEvent(ctx, botID, "РќР°СЃС‚СЂРѕР№РєРё Р±РѕС‚Р° РёР·РјРµРЅРµРЅС‹ РїРѕР»СЊР·РѕРІР°С‚РµР»РµРј", "info", "user")

	// If strategyConfig changed, sync params to all active bot strategies and notify the engine.
	if _, cfgChanged := body["strategyConfig"]; cfgChanged {
		go s.syncBotStrategies(context.Background(), botID)
	}

	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, bot)
}

// syncBotStrategies reads the bot's current strategy_config and applies it to all
// active/finishing strategies created by this bot, then notifies the engine.
func (s *Server) syncBotStrategies(ctx context.Context, botID string) {
	var stratCfgBytes []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT strategy_config FROM bots WHERE id = $1`, botID,
	).Scan(&stratCfgBytes); err != nil {
		return
	}

	var cfg botCfgJSON
	if err := json.Unmarshal(stratCfgBytes, &cfg); err != nil {
		return
	}

	// Apply defaults (same as createBotStrategy)
	tpMode := cfg.TPMode
	if tpMode == "" {
		tpMode = "total"
	}
	slType := cfg.SLType
	if slType == "" {
		slType = "conditional"
	}
	leverage := cfg.Leverage
	if leverage == 0 {
		leverage = 1
	}
	marginType := cfg.MarginType
	if marginType == "" {
		marginType = "isolated"
	}
	gridLevels := cfg.GridLevels
	if gridLevels == 0 {
		gridLevels = 5
	}
	gridActive := cfg.GridActive
	if gridActive == 0 {
		gridActive = 3
	}
	gridStep := cfg.GridStepPct
	if gridStep == 0 {
		gridStep = 1.0
	}
	gridSize := cfg.GridSizeUSDT
	if gridSize == 0 {
		gridSize = 100
	}
	tpPct := cfg.TPPct
	if tpPct == 0 {
		tpPct = 2.0
	}
	slPct := cfg.SLPct
	if slPct == 0 {
		slPct = 5.0
	}

	scJSON, _ := json.Marshal(cfg.SignalConfigs)
	if scJSON == nil {
		scJSON = []byte("[]")
	}
	var stepsParam *string
	if len(cfg.Steps) > 0 {
		sb, err := json.Marshal(cfg.Steps)
		if err == nil {
			sv := string(sb)
			stepsParam = &sv
		}
	}
	var trailingActPct *float64
	var trailingCallPct *float64
	if cfg.TrailingEnabled {
		if cfg.TrailingActPct > 0 {
			v := cfg.TrailingActPct
			trailingActPct = &v
		}
		if cfg.TrailingCallPct > 0 {
			v := cfg.TrailingCallPct
			trailingCallPct = &v
		}
	}

	// Update all active/finishing strategies belonging to this bot
	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET
		   grid_levels = $1, grid_active = $2, grid_step_pct = $3, grid_size_usdt = $4,
		   tp_mode = $5, tp_pct = $6, sl_type = $7, sl_pct = $8, signal_filter = $9,
		   leverage = $10, margin_type = $11, hedge_mode = $12,
		   signal_configs = $13::jsonb, steps = ($14::text)::jsonb,
		   trailing_stop_enabled = $15, trailing_activation_pct = $16, trailing_callback_pct = $17,
		   after_stop_mode = $18
		 WHERE bot_id = $19 AND status IN ('active','finishing')
		 RETURNING id`,
		gridLevels, gridActive, gridStep, gridSize,
		tpMode, tpPct, slType, slPct, cfg.SignalFilter,
		leverage, marginType, cfg.HedgeMode,
		string(scJSON), stepsParam,
		cfg.TrailingEnabled, trailingActPct, trailingCallPct, cfg.AfterStopMode,
		botID,
	)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		s.engine.Notify(ctx, id)
	}
}

// DELETE /bots/{id}
func (s *Server) DeleteBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")

	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM bots WHERE id = $1 AND owner_id = $2`, botID, callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /bots/{id}/deploy вЂ” creates a subscription (linked copy) for the caller.
func (s *Server) DeployBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	sourceID := chi.URLParam(r, "id")
	ctx := r.Context()

	var name, desc, fullDesc string
	var triggers, stratCfg []byte
	var isPublic bool
	if err := s.pool.QueryRow(ctx,
		`SELECT name, description, full_description, is_public, triggers, strategy_config FROM bots WHERE id = $1`,
		sourceID,
	).Scan(&name, &desc, &fullDesc, &isPublic, &triggers, &stratCfg); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !isPublic {
		writeError(w, http.StatusForbidden, "bot is not public")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tx error")
		return
	}
	defer tx.Rollback(ctx)

	var newID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, full_description, triggers, strategy_config)
		VALUES ($1, $2, false, $3, $4, $5, $6, $7)
		RETURNING id`,
		callerID, sourceID, name, desc, fullDesc, triggers, stratCfg,
	).Scan(&newID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bots SET deploy_count = deploy_count + 1 WHERE id = $1`, sourceID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "tx commit error")
		return
	}

	bot, ok := fetchBot(s, r, newID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}

// POST /bots/{id}/fork вЂ” unlinks a subscription so it can be edited independently.
func (s *Server) ForkBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	var ownerID string
	var isFork bool
	var sourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, is_fork, source_bot_id FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &isFork, &sourceID); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if ownerID != callerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if sourceID == nil || isFork {
		writeError(w, http.StatusBadRequest, "bot is not a linked subscription")
		return
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET is_fork = true, updated_at = NOW() WHERE id = $1`, botID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, bot)
}

// POST /bots/{id}/start
func (s *Server) StartBot(w http.ResponseWriter, r *http.Request) {
	s.setBotStatus(w, r, "active")
}

// POST /bots/{id}/stop
func (s *Server) StopBot(w http.ResponseWriter, r *http.Request) {
	s.setBotStatus(w, r, "stopped")
}

func (s *Server) setBotStatus(w http.ResponseWriter, r *http.Request, status string) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET status = $1, updated_at = NOW() WHERE id = $2 AND owner_id = $3`,
		status, botID, callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	msg := "Р‘РѕС‚ РѕСЃС‚Р°РЅРѕРІР»РµРЅ РїРѕР»СЊР·РѕРІР°С‚РµР»РµРј"
	if status == "active" {
		msg = "Р‘РѕС‚ Р·Р°РїСѓС‰РµРЅ РїРѕР»СЊР·РѕРІР°С‚РµР»РµРј"
	}
	s.logBotEvent(r.Context(), botID, msg, "info", "user")
	if status == "stopped" {
		go s.stopBotStrategiesWithoutPosition(context.Background(), botID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// stopBotStrategiesWithoutPosition stops all bot strategies that have no open
// position (no filled levels in the current cycle). Strategies with a position
// are left running so they can close naturally.
func (s *Server) stopBotStrategiesWithoutPosition(ctx context.Context, botID string) {
	rows, err := s.pool.Query(ctx, `
		UPDATE strategies SET status = 'stopped'
		WHERE bot_id = $1
		  AND status IN ('active', 'finishing')
		  AND NOT EXISTS (
		    SELECT 1 FROM strategy_levels sl
		    JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		    WHERE sc.strategy_id = strategies.id
		      AND sc.ended_at IS NULL
		      AND sl.status = 'filled'
		  )
		RETURNING id`, botID)
	if err != nil {
		log.Printf("stopBotStrategiesWithoutPosition %s: %v", botID, err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		s.engine.Notify(ctx, id)
	}
	if len(ids) > 0 {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("РћСЃС‚Р°РЅРѕРІР»РµРЅРѕ %d СЃС‚СЂР°С‚РµРіРёР№ Р±РµР· РѕС‚РєСЂС‹С‚РѕР№ РїРѕР·РёС†РёРё", len(ids)), "info", "strategy")
	}
}

// GET /bots/{id}/events
func (s *Server) GetBotEvents(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")

	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT true FROM bots WHERE id=$1 AND owner_id=$2`, botID, callerID,
	).Scan(&exists); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}

	category := r.URL.Query().Get("category")
	var queryErr error
	var rows pgx.Rows
	if category != "" {
		rows, queryErr = s.pool.Query(r.Context(),
			`SELECT message, level, category, created_at FROM bot_events
			 WHERE bot_id=$1 AND category=$2 ORDER BY created_at DESC LIMIT 200`, botID, category)
	} else {
		rows, queryErr = s.pool.Query(r.Context(),
			`SELECT message, level, category, created_at FROM bot_events
			 WHERE bot_id=$1 ORDER BY created_at DESC LIMIT 200`, botID)
	}
	if queryErr != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type eventRow struct {
		Message   string    `json:"message"`
		Level     string    `json:"level"`
		Category  string    `json:"category"`
		CreatedAt time.Time `json:"created_at"`
	}
	var events []eventRow
	for rows.Next() {
		var e eventRow
		if rows.Scan(&e.Message, &e.Level, &e.Category, &e.CreatedAt) == nil {
			events = append(events, e)
		}
	}
	if events == nil {
		events = []eventRow{}
	}
	writeJSON(w, http.StatusOK, events)
}

// POST /bots/{id}/publish
func (s *Server) PublishBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET is_public = true, updated_at = NOW() WHERE id = $1 AND owner_id = $2`,
		botID, callerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /bots/signal-scan вЂ” scan available symbols against provided signal configs.
// Returns all symbols where the combined signal state is non-neutral (buy or sell).
func (s *Server) ScanSignals(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SignalConfigs []signal.Config `json:"signal_configs"`
		Whitelist     []string        `json:"whitelist"`
		Blacklist     []string        `json:"blacklist"`
		Interval      string          `json:"interval"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.SignalConfigs) == 0 {
		writeError(w, http.StatusBadRequest, "signal_configs required")
		return
	}
	if req.Interval == "" {
		req.Interval = "15"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var symbols []string
	if len(req.Whitelist) > 0 {
		symbols = req.Whitelist
	} else {
		var err error
		symbols, err = trader.FetchAllLinearSymbols(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch symbols")
			return
		}
	}

	delistSymbols := s.GetDelistingSymbols()
	blackSet := make(map[string]bool, len(req.Blacklist)+len(delistSymbols))
	for _, sym := range req.Blacklist {
		blackSet[sym] = true
	}
	for _, sym := range delistSymbols {
		blackSet[sym] = true
	}

	type scanResult struct {
		Symbol string `json:"symbol"`
		State  string `json:"state"`
	}

	sem := make(chan struct{}, 20)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var matches []scanResult

	for _, sym := range symbols {
		if blackSet[sym] {
			continue
		}
		wg.Add(1)
		sym := sym
		go func() {
			defer wg.Done()
			// Bail immediately if context expired before acquiring semaphore slot.
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			// Re-check after acquiring slot (context may have expired while waiting).
			select {
			case <-ctx.Done():
				return
			default:
			}
			st, _ := s.signalEngine.QueryState(sym, req.Interval, req.SignalConfigs)
			if st != signal.Neutral {
				mu.Lock()
				matches = append(matches, scanResult{Symbol: sym, State: string(st)})
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if matches == nil {
		matches = []scanResult{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": matches})
}

// GET /bots/{id}/scan вЂ” run signal scan for this bot and return matching symbols.
func (s *Server) ScanBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Fetch bot
	var ownerID string
	var whitelist, blacklist []string
	var stratCfgBytes []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, symbol_whitelist, symbol_blacklist, strategy_config
		 FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &whitelist, &blacklist, &stratCfgBytes); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if ownerID != callerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var cfg botCfgJSON
	if err := json.Unmarshal(stratCfgBytes, &cfg); err != nil || len(cfg.ActivationSignals) == 0 {
		writeError(w, http.StatusBadRequest, "bot has no activation signals configured")
		return
	}

	interval := "15"
	for _, a := range cfg.ActivationSignals {
		if v, ok := a.Params["tf"].(string); ok && v != "" {
			interval = v
			break
		}
	}
	sigCfgs := make([]signal.Config, len(cfg.ActivationSignals))
	for i, a := range cfg.ActivationSignals {
		sigCfgs[i] = signal.Config{Name: a.Name, Params: a.Params}
	}

	allSymbols, _ := trader.FetchAllLinearSymbols(ctx)
	delistSymbols := s.GetDelistingSymbols()
	symbols := resolveSymbolList(whitelist, blacklist, delistSymbols, allSymbols)
	if len(symbols) == 0 {
		symbols = allSymbols
	}

	// Load already-open strategies for this bot
	type openKey struct{ sym, dir string }
	openRows, _ := s.pool.Query(ctx,
		`SELECT symbol, direction FROM strategies
		 WHERE bot_id = $1 AND status IN ('active','finishing')`, botID)
	opened := make(map[openKey]bool)
	if openRows != nil {
		for openRows.Next() {
			var sym, dir string
			if openRows.Scan(&sym, &dir) == nil {
				opened[openKey{sym, dir}] = true
			}
		}
		openRows.Close()
	}

	// Scan symbols concurrently
	sem := make(chan struct{}, botEngineSymbolSem)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var hits []scanHit

	for _, sym := range symbols {
		sym := sym
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			select {
			case <-ctx.Done():
				return
			default:
			}
			st := s.signalEngine.ComputeStateForce(sym, interval, sigCfgs)
			if st == signal.Neutral {
				return
			}

			// Get raw signal value and compute strength for sorting
			var sigVal float64
			if vals := s.signalEngine.QueryValues(sym, interval, sigCfgs); vals != nil {
				for _, v := range vals {
					sigVal = v
					break
				}
			}
			// Strength: for buy вЂ” lower value is stronger; for sell вЂ” higher value is stronger.
			// Negate buy so that both cases sort descending by strength.
			var strength float64
			if st == signal.Buy {
				strength = -sigVal
			} else {
				strength = sigVal
			}

			ttlRem := s.signalEngine.QueryTTLRemaining(sym, interval, sigCfgs)

			var dir string
			switch cfg.Direction {
			case "long":
				if st == signal.Buy {
					dir = "long"
				}
			case "short":
				if st == signal.Sell {
					dir = "short"
				}
			default:
				if st == signal.Buy {
					dir = "long"
				} else {
					dir = "short"
				}
			}
			mu.Lock()
			if dir == "" {
				signalDir := "long"
				if st == signal.Sell {
					signalDir = "short"
				}
				hits = append(hits, scanHit{
					Symbol:          sym,
					SignalState:     string(st),
					Direction:       signalDir,
					AlreadyOpen:     false,
					DirBlocked:      true,
					SignalValue:     sigVal,
					Strength:        strength,
					TTLRemainingSec: ttlRem,
				})
			} else {
				hits = append(hits, scanHit{
					Symbol:          sym,
					SignalState:     string(st),
					Direction:       dir,
					AlreadyOpen:     opened[openKey{sym, dir}],
					DirBlocked:      false,
					SignalValue:     sigVal,
					Strength:        strength,
					TTLRemainingSec: ttlRem,
				})
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if hits == nil {
		hits = []scanHit{}
	}

	// Sort: actionable first, then already-open, then direction-blocked
	sortHits(hits)

	sigNames := make([]string, len(cfg.ActivationSignals))
	for i, a := range cfg.ActivationSignals {
		sigNames[i] = a.Name
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results":           hits,
		"scanned":           len(symbols),
		"activation_signals": sigNames,
		"preview":           cfg,
	})
}

// POST /bots/{id}/trigger вЂ” manually trigger strategy creation for a specific symbol.
// Body: { "symbol": "BTCUSDT", "direction": "long" }
func (s *Server) TriggerBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	var req struct {
		Symbol    string `json:"symbol"`
		Direction string `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Symbol == "" || req.Direction == "" {
		writeError(w, http.StatusBadRequest, "symbol and direction required")
		return
	}

	var b botEngineRow
	var accountIDPtr *string
	var stratCfgBytes []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT id, owner_id, account_id, symbol_whitelist, symbol_blacklist,
		        strategy_config, max_strategies, max_margin_usdt, max_long_strategies, max_short_strategies
		 FROM bots WHERE id = $1 AND owner_id = $2`, botID, callerID,
	).Scan(&b.id, &b.ownerID, &accountIDPtr, &b.whitelist, &b.blacklist,
		&stratCfgBytes, &b.maxStrat, &b.maxMargin, &b.maxLong, &b.maxShort); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if accountIDPtr == nil || *accountIDPtr == "" {
		writeError(w, http.StatusBadRequest, "bot has no account configured")
		return
	}
	b.accountID = *accountIDPtr

	var cfg botCfgJSON
	if err := json.Unmarshal(stratCfgBytes, &cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid bot config")
		return
	}

	// Check max_strategies limit.
	if b.maxStrat > 0 {
		var activeCount int
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM strategies WHERE bot_id = $1 AND status IN ('active', 'finishing')`,
			botID,
		).Scan(&activeCount); err == nil && activeCount >= b.maxStrat {
			writeError(w, http.StatusConflict, fmt.Sprintf("Р»РёРјРёС‚ СЃС‚СЂР°С‚РµРіРёР№ Р±РѕС‚Р° РґРѕСЃС‚РёРіРЅСѓС‚ (%d/%d)", activeCount, b.maxStrat))
			return
		}
	}
	// Check per-direction limits.
	dirLimit := b.maxLong
	if req.Direction == "short" {
		dirLimit = b.maxShort
	}
	if dirLimit > 0 {
		var dirCount int
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM strategies WHERE bot_id = $1 AND direction = $2 AND status IN ('active', 'finishing')`,
			botID, req.Direction,
		).Scan(&dirCount); err == nil && dirCount >= dirLimit {
			writeError(w, http.StatusConflict, fmt.Sprintf("Р»РёРјРёС‚ %s СЃС‚СЂР°С‚РµРіРёР№ Р±РѕС‚Р° РґРѕСЃС‚РёРіРЅСѓС‚ (%d/%d)", req.Direction, dirCount, dirLimit))
			return
		}
	}

	// Check not already open (including detached strategies)
	var existing int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM strategies
		 WHERE symbol = $1 AND direction = $2 AND status IN ('active','finishing')
		   AND (bot_id = $3 OR (bot_id IS NULL AND owner_id = $4 AND account_id = $5))`,
		req.Symbol, req.Direction, botID, b.ownerID, b.accountID,
	).Scan(&existing); err == nil && existing > 0 {
		writeError(w, http.StatusConflict, "СЃС‚СЂР°С‚РµРіРёСЏ РґР»СЏ СЌС‚РѕР№ РїР°СЂС‹ СѓР¶Рµ РѕС‚РєСЂС‹С‚Р°")
		return
	}

	if err := s.createBotStrategy(ctx, b, cfg, req.Symbol, req.Direction, 0); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Р—Р°РїСѓС‰РµРЅР° СЃС‚СЂР°С‚РµРіРёСЏ РІСЂСѓС‡РЅСѓСЋ: %s %s", req.Symbol, req.Direction), "info", "user")
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// sortHits orders scan hits:
//  1. actionable (not blocked, not open) вЂ” sorted by signal strength desc
//  2. already open вЂ” sorted by signal strength desc
//  3. direction blocked вЂ” sorted by signal strength desc
func sortHits(hits []scanHit) {
	rank := func(h scanHit) int {
		if h.DirBlocked {
			return 2
		}
		if h.AlreadyOpen {
			return 1
		}
		return 0
	}
	sort.SliceStable(hits, func(i, j int) bool {
		ri, rj := rank(hits[i]), rank(hits[j])
		if ri != rj {
			return ri < rj
		}
		// Within same rank: stronger signal first (higher Strength = stronger)
		return hits[i].Strength > hits[j].Strength
	})
}

// в”Ђв”Ђ Admin handlers в”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђв”Ђ

// GET /admin/bots вЂ” list all bots (admin only)
func (s *Server) ListAdminBots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.pool.Query(ctx,
		`SELECT `+botCols+botFrom+`ORDER BY b.is_official DESC, b.created_at DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	bots, err := collectBots(rows, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan error")
		return
	}
	writeJSON(w, http.StatusOK, bots)
}

// POST /admin/bots вЂ” create an official NovaBot (admin only)
func (s *Server) CreateOfficialBot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	callerID := UserIDFromCtx(ctx)

	var req struct {
		Name            string          `json:"name"`
		Description     string          `json:"description"`
		FullDescription string          `json:"fullDescription"`
		SymbolWhitelist []string        `json:"symbolWhitelist"`
		SymbolBlacklist []string        `json:"symbolBlacklist"`
		Triggers        json.RawMessage `json:"triggers"`
		StrategyConfig  json.RawMessage `json:"strategyConfig"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if len(req.Triggers) == 0 {
		req.Triggers = json.RawMessage("[]")
	}
	if len(req.StrategyConfig) == 0 {
		req.StrategyConfig = json.RawMessage("{}")
	}
	if req.SymbolWhitelist == nil {
		req.SymbolWhitelist = []string{}
	}
	if req.SymbolBlacklist == nil {
		req.SymbolBlacklist = []string{}
	}

	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO bots (owner_id, name, description, full_description, is_public, is_official,
		                  symbol_whitelist, symbol_blacklist, triggers, strategy_config)
		VALUES ($1, $2, $3, $4, true, true, $5, $6, $7, $8)
		RETURNING id`,
		callerID, req.Name, req.Description, req.FullDescription,
		req.SymbolWhitelist, req.SymbolBlacklist,
		[]byte(req.Triggers), []byte(req.StrategyConfig),
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	bot, ok := fetchBot(s, r, id, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}

