// services/api-gateway/bots_handler.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

// в"Ђв"Ђ Types в"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђ

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
	IgnoreCoinFilter      bool            `json:"ignoreCoinFilter"`
	ActiveSecondsAcc      int64           `json:"activeSecondsAcc"`
	ActiveSince           *time.Time      `json:"activeSince"`
	ApprovalStatus        *string         `json:"approvalStatus"`
	Price                 float64         `json:"price"`
	Spark                 []float64       `json:"spark"`
	ActiveUsersCount      int             `json:"activeUsersCount"`
	TradesTotal           int             `json:"tradesTotal"`
	TradesWin             int             `json:"tradesWin"`
	NetPnlTotal           float64         `json:"netPnlTotal"`
	SourceAuthor          string          `json:"sourceAuthor"`
	PairedBotID           *string         `json:"pairedBotId"`
}

type patchBotWithWarningsResp struct {
	botResp
	Warnings []string `json:"warnings,omitempty"`
}

type listBotsResp struct {
	Catalog []botResp `json:"catalog"`
	Mine    []botResp `json:"mine"`
}

type scanHit struct {
	Symbol          string  `json:"symbol"`
	SignalState     string  `json:"signal_state"`
	Direction       string  `json:"direction"`
	AlreadyOpen     bool    `json:"already_open"`
	DirBlocked      bool    `json:"dir_blocked"`
	SignalValue     float64 `json:"signal_value"`      // raw indicator value (e.g. RSI=14.2)
	Strength        float64 `json:"strength"`          // sort key: higher = stronger signal
	TTLRemainingSec float64 `json:"ttl_remaining_sec"` // -1 = no TTL; ≥0 = seconds left
}

// в"Ђв"Ђ Helpers в"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђ

const botCols = `b.id, b.name, b.description, b.full_description, b.avatar_url, b.owner_id,
	COALESCE(
		(SELECT COALESCE(ua.username, ua.email) FROM users ua WHERE ua.id = b.original_author_id),
		u.username, u.email),
	b.is_public, b.is_official, b.status, b.source_bot_id, b.is_fork,
	b.symbol_whitelist, b.symbol_blacklist,
	b.triggers, b.strategy_config, b.deploy_count, b.created_at,
	b.max_strategies, b.max_margin_usdt,
	(SELECT COUNT(*) FROM strategies s WHERE s.bot_id = b.id AND s.status = 'active') AS active_strategies_count,
	b.account_id, b.auto_mode, b.max_long_strategies, b.max_short_strategies, b.max_sym_consecutive_runs,
	b.ignore_coin_filter,
	b.active_seconds_acc, b.active_since, b.approval_status,
	b.price_usd_month, b.paired_bot_id,
	ARRAY[]::float8[] AS spark,
	(SELECT COUNT(DISTINCT b2.owner_id)
	 FROM bots b2
	 WHERE b2.source_bot_id = b.id
	   AND b2.status = 'active') AS active_users_count`

const botFrom = ` FROM bots b JOIN users u ON u.id = b.owner_id `

// catalogOwnerID is the system "Catalog" account that owns detached public
// library copies (migration 078). Publishing creates a copy owned by this
// account so deleting the creator's own bot never removes the library entry.
const catalogOwnerID = "00000000-0000-0000-0000-0000000000ca"

// mineStatsCols — реальная статистика сделок владельца по единому источнику PnL
// (botPnlUnionSQL: trade_history + strategy_levels.realized_pnl + matrix_tp_profits),
// иначе боты, чья прибыль идёт через matrix-TP re-arm, показывали бы нули.
const mineStatsCols = `,
	(SELECT COUNT(*) FROM ` + botPnlUnionSQL + ` bp WHERE bp.bot_id = b.id)::int AS trades_total,
	(SELECT COUNT(*) FROM ` + botPnlUnionSQL + ` bp WHERE bp.bot_id = b.id AND bp.net_pnl > 0)::int AS trades_win,
	COALESCE((SELECT SUM(bp.net_pnl) FROM ` + botPnlUnionSQL + ` bp WHERE bp.bot_id = b.id), 0)::float8 AS net_pnl_total,
	COALESCE(
		(SELECT CASE WHEN b2.is_official THEN 'NovaBot'
		             ELSE COALESCE(
		                  (SELECT COALESCE(uo.username, uo.email) FROM users uo WHERE uo.id = b2.original_author_id),
		                  u2.username, u2.email) END
		 FROM bots b2 JOIN users u2 ON u2.id = b2.owner_id
		 WHERE b2.id = b.source_bot_id),
		''
	) AS source_author`

// zeroStatsCols — нули/пустые значения для запросов без контекста пользователя
const zeroStatsCols = `, 0::int AS trades_total, 0::int AS trades_win, 0::float8 AS net_pnl_total, '' AS source_author`

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
			&b.IgnoreCoinFilter,
			&b.ActiveSecondsAcc, &b.ActiveSince, &b.ApprovalStatus,
			&b.Price, &b.PairedBotID,
			&b.Spark, &b.ActiveUsersCount,
			&b.TradesTotal, &b.TradesWin, &b.NetPnlTotal, &b.SourceAuthor,
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
	rows, err := s.pool.Query(r.Context(), `SELECT `+botCols+zeroStatsCols+botFrom+`WHERE b.id = $1`, botID)
	if err != nil {
		return botResp{}, false
	}
	bots, err := collectBots(rows, callerID)
	if err != nil || len(bots) == 0 {
		return botResp{}, false
	}
	return bots[0], true
}

// в"Ђв"Ђ Handlers в"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђ

// GET /bots
func (s *Server) ListBots(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	ctx := r.Context()
	q := r.URL.Query().Get("q")
	direction := r.URL.Query().Get("direction")
	accountID := r.URL.Query().Get("accountId")

	orderBy := "b.created_at DESC"
	if r.URL.Query().Get("sort") == "popular" {
		orderBy = "b.deploy_count DESC"
	}

	// Hide an original that has a detached catalog copy — the copy represents it
	// in the library (and survives deletion of the original).
	catalogSQL := `SELECT ` + botCols + zeroStatsCols + botFrom + `
		WHERE b.is_public = true
		  AND NOT EXISTS (SELECT 1 FROM bots c WHERE c.published_from_id = b.id)
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
		`SELECT `+botCols+mineStatsCols+botFrom+`WHERE b.owner_id = $1 AND ($2 = '' OR b.account_id::text = $2) ORDER BY b.created_at DESC`,
		callerID, accountID)
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
		Name                  string          `json:"name"`
		Description           string          `json:"description"`
		FullDescription       string          `json:"fullDescription"`
		AvatarURL             string          `json:"avatarUrl"`
		IsPublic              bool            `json:"isPublic"`
		AccountID             *string         `json:"accountId"`
		SymbolWhitelist       []string        `json:"symbolWhitelist"`
		SymbolBlacklist       []string        `json:"symbolBlacklist"`
		Triggers              json.RawMessage `json:"triggers"`
		StrategyConfig        json.RawMessage `json:"strategyConfig"`
		MaxStrategies         int             `json:"maxStrategies"`
		MaxLongStrategies     int             `json:"maxLongStrategies"`
		MaxShortStrategies    int             `json:"maxShortStrategies"`
		MaxMarginUsdt         float64         `json:"maxMarginUsdt"`
		MaxSymConsecutiveRuns int             `json:"maxSymConsecutiveRuns"`
		AutoMode              bool            `json:"autoMode"`
		IgnoreCoinFilter      bool            `json:"ignoreCoinFilter"`
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
		                  max_strategies, max_long_strategies, max_short_strategies, max_margin_usdt, max_sym_consecutive_runs, auto_mode, ignore_coin_filter)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id`,
		callerID, req.Name, req.Description, req.FullDescription, req.AvatarURL, req.IsPublic,
		req.AccountID, req.SymbolWhitelist, req.SymbolBlacklist,
		[]byte(req.Triggers), []byte(req.StrategyConfig),
		req.MaxStrategies, req.MaxLongStrategies, req.MaxShortStrategies, req.MaxMarginUsdt, req.MaxSymConsecutiveRuns, req.AutoMode, req.IgnoreCoinFilter,
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

// setJSONField overwrites (or adds) a single key in a JSON object, tolerating an empty/nil
// input (treated as `{}`). Used to inject fields the client shouldn't be trusted to set
// itself (bot_kind, hedge_bot_whitelist) into an otherwise client-authored strategy_config blob.
func setJSONField(raw json.RawMessage, key string, val interface{}) json.RawMessage {
	m := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m) //nolint:errcheck
	}
	m[key] = val
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// getJSONStringField reads a single string field out of a strategy_config-shaped
// json.RawMessage — the read counterpart to setJSONField, same tolerant style (empty
// string if raw is empty, absent, or not a string).
func getJSONStringField(raw json.RawMessage, key string) string {
	m := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m) //nolint:errcheck
	}
	v, _ := m[key].(string)
	return v
}

// POST /bots/multi вЂ" creates a "МультиБот": a signal leg and a hedge leg created and
// managed together as one entity in the UI. Internally these are two ordinary bots rows —
// the hedge leg is a normal hedge bot whose hedge_bot_whitelist is locked to the signal
// leg's id (so it only ever reacts to that leg's own strategies), linked back to it via
// paired_bot_id. No change to pkg/strategy or hedge_engine.go: both legs run through the
// exact same code paths as any standalone signal/hedge bot pair already does today.
func (s *Server) CreateMultiBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	ctx := r.Context()

	var req struct {
		Name                string          `json:"name"`
		Description         string          `json:"description"`
		FullDescription     string          `json:"fullDescription"`
		AvatarURL           string          `json:"avatarUrl"`
		AccountID           string          `json:"accountId"`
		SymbolWhitelist     []string        `json:"symbolWhitelist"`
		SymbolBlacklist     []string        `json:"symbolBlacklist"`
		Triggers            json.RawMessage `json:"triggers"`
		StrategyConfig      json.RawMessage `json:"strategyConfig"`      // signal leg
		HedgeStrategyConfig json.RawMessage `json:"hedgeStrategyConfig"` // hedge leg

		MaxStrategies         int     `json:"maxStrategies"`
		MaxLongStrategies     int     `json:"maxLongStrategies"`
		MaxShortStrategies    int     `json:"maxShortStrategies"`
		MaxMarginUsdt         float64 `json:"maxMarginUsdt"`
		MaxSymConsecutiveRuns int     `json:"maxSymConsecutiveRuns"`
		AutoMode              bool    `json:"autoMode"`
		IgnoreCoinFilter      bool    `json:"ignoreCoinFilter"`

		HedgeMaxStrategies         int     `json:"hedgeMaxStrategies"`
		HedgeMaxLongStrategies     int     `json:"hedgeMaxLongStrategies"`
		HedgeMaxShortStrategies    int     `json:"hedgeMaxShortStrategies"`
		HedgeMaxMarginUsdt         float64 `json:"hedgeMaxMarginUsdt"`
		HedgeMaxSymConsecutiveRuns int     `json:"hedgeMaxSymConsecutiveRuns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	var acctOwner string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM exchange_accounts WHERE id = $1`, req.AccountID).Scan(&acctOwner); err != nil {
		writeError(w, http.StatusBadRequest, "account not found")
		return
	}
	if acctOwner != callerID {
		writeError(w, http.StatusForbidden, "account does not belong to caller")
		return
	}
	if len(req.Triggers) == 0 {
		req.Triggers = json.RawMessage("[]")
	}
	if req.SymbolWhitelist == nil {
		req.SymbolWhitelist = []string{}
	}
	if req.SymbolBlacklist == nil {
		req.SymbolBlacklist = []string{}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tx error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	signalCfg := setJSONField(req.StrategyConfig, "bot_kind", "signal")

	var signalID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO bots (owner_id, name, description, full_description, avatar_url, is_public,
		                  account_id, symbol_whitelist, symbol_blacklist, triggers, strategy_config,
		                  max_strategies, max_long_strategies, max_short_strategies, max_margin_usdt, max_sym_consecutive_runs, auto_mode, ignore_coin_filter)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id`,
		callerID, req.Name, req.Description, req.FullDescription, req.AvatarURL,
		req.AccountID, req.SymbolWhitelist, req.SymbolBlacklist,
		[]byte(req.Triggers), []byte(signalCfg),
		req.MaxStrategies, req.MaxLongStrategies, req.MaxShortStrategies, req.MaxMarginUsdt, req.MaxSymConsecutiveRuns, req.AutoMode, req.IgnoreCoinFilter,
	).Scan(&signalID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	hedgeCfg := setJSONField(req.HedgeStrategyConfig, "bot_kind", "hedge")
	hedgeCfg = setJSONField(hedgeCfg, "hedge_bot_whitelist", []string{signalID})

	var hedgeID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO bots (owner_id, name, description, full_description, avatar_url, is_public,
		                  account_id, symbol_whitelist, symbol_blacklist, triggers, strategy_config,
		                  max_strategies, max_long_strategies, max_short_strategies, max_margin_usdt, max_sym_consecutive_runs, auto_mode, ignore_coin_filter,
		                  paired_bot_id)
		VALUES ($1, $2, $3, $4, $5, false, $6, '{}', '{}', $7, $8, $9, $10, $11, $12, $13, false, false, $14)
		RETURNING id`,
		callerID, req.Name, req.Description, req.FullDescription, req.AvatarURL,
		req.AccountID,
		[]byte(req.Triggers), []byte(hedgeCfg),
		req.HedgeMaxStrategies, req.HedgeMaxLongStrategies, req.HedgeMaxShortStrategies, req.HedgeMaxMarginUsdt, req.HedgeMaxSymConsecutiveRuns,
		signalID,
	).Scan(&hedgeID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE bots SET paired_bot_id = $1 WHERE id = $2`, hedgeID, signalID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "tx commit error")
		return
	}

	bot, ok := fetchBot(s, r, signalID, callerID)
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

	var ownerID, botStatus string
	var isFork, isOfficial bool
	var sourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, is_fork, source_bot_id, status, is_official FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &isFork, &sourceID, &botStatus, &isOfficial); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if ownerID != callerID && !s.isAdmin(ctx, callerID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	// Auto-fork on first edit: transparently detach linked subscription so user can edit freely.
	if sourceID != nil && !isFork {
		if _, err := s.pool.Exec(ctx,
			`UPDATE bots SET is_fork = true, updated_at = NOW() WHERE id = $1`, botID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

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
	addBool("ignoreCoinFilter", "ignore_coin_filter")
	// Validate account ownership before allowing account_id change.
	if v, ok := body["accountId"]; ok {
		var newAccountID string
		json.Unmarshal(v, &newAccountID) //nolint:errcheck
		if newAccountID != "" {
			var owned bool
			if err := s.pool.QueryRow(ctx,
				`SELECT true FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
				newAccountID, ownerID).Scan(&owned); err != nil || !owned {
				writeError(w, http.StatusForbidden, "account not found")
				return
			}
		}
	}
	addNullableStr("accountId", "account_id")
	addSlice("symbolWhitelist", "symbol_whitelist")
	addSlice("symbolBlacklist", "symbol_blacklist")
	addRaw("triggers", "triggers")
	addRaw("strategyConfig", "strategy_config")
	addInt("maxStrategies", "max_strategies")
	addInt("maxLongStrategies", "max_long_strategies")
	addInt("maxShortStrategies", "max_short_strategies")
	addFloat("maxMarginUsdt", "max_margin_usdt")
	addInt("maxSymConsecutiveRuns", "max_sym_consecutive_runs")

	// Reset approval timer + stats when strategy_config changes on a non-official user bot.
	changingStrategy := false
	if _, ok := body["strategyConfig"]; ok && !isOfficial {
		sets = append(sets, "active_seconds_acc = 0", "active_since = NULL")
		changingStrategy = true
	}

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
	s.logBotEvent(ctx, botID, "Настройки бота изменены пользователем", "info", "user")

	// Unpublish: making the bot private removes its detached library copy.
	if v, ok := body["isPublic"]; ok {
		var pub bool
		if json.Unmarshal(v, &pub) == nil && !pub {
			s.pool.Exec(ctx, `DELETE FROM bots WHERE published_from_id = $1`, botID) //nolint:errcheck
		}
	}

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

	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Check for symbol whitelist conflicts with other active bots.
	if _, changingWL := body["symbolWhitelist"]; changingWL {
		if warnings := s.checkWhitelistConflicts(ctx, botID, ownerID, bot.SymbolWhitelist); len(warnings) > 0 {
			writeJSON(w, http.StatusOK, patchBotWithWarningsResp{botResp: bot, Warnings: warnings})
			return
		}
	}
	writeJSON(w, http.StatusOK, bot)
}

// checkWhitelistConflicts returns warning strings for each other active bot
// whose symbol_whitelist overlaps with the given whitelist.
func (s *Server) checkWhitelistConflicts(ctx context.Context, botID, ownerID string, whitelist []string) []string {
	if len(whitelist) == 0 {
		return nil
	}
	// Build set of concrete (non-pattern) symbols to check
	wlSet := make(map[string]bool, len(whitelist))
	for _, sym := range whitelist {
		if !strings.Contains(sym, "*") {
			wlSet[sym] = true
		}
	}
	if len(wlSet) == 0 {
		return nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT name, COALESCE(strategy_config->>'bot_kind', 'bot'), symbol_whitelist
		FROM bots
		WHERE owner_id=$1 AND id != $2 AND status='active'
		  AND cardinality(symbol_whitelist) > 0
	`, ownerID, botID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var warnings []string
	for rows.Next() {
		var otherName, otherKind string
		var otherWL []string
		if rows.Scan(&otherName, &otherKind, &otherWL) != nil {
			continue
		}
		var conflicts []string
		for _, sym := range otherWL {
			if wlSet[sym] {
				conflicts = append(conflicts, sym)
			}
		}
		if len(conflicts) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s пересекается с ботом «%s» (%s)", strings.Join(conflicts, ", "), otherName, otherKind))
		}
	}
	return warnings
}

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
// Absent (nil/empty) and explicit JSON null are treated as equivalent — mirrors
// strategy_handler.go's normSteps, which exists for the identical reason: a config created
// with a NULL column and later round-tripped through a PATCH body that serializes it as
// literal "null" must not look like a structural change.
func jsonRawEqual(a, b json.RawMessage) bool {
	normA, normB := normalizeJSONForCompare(a), normalizeJSONForCompare(b)
	if normA == "" && normB == "" {
		return true
	}
	if normA == "" || normB == "" {
		return false
	}
	var x, y interface{}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return normA == normB
	}
	return reflect.DeepEqual(x, y)
}

// normalizeJSONForCompare returns "" for both an absent/empty json.RawMessage and one
// containing only the literal JSON null (after trimming whitespace), so callers can treat
// "field never set" and "field explicitly set to null" as the same value.
func normalizeJSONForCompare(a json.RawMessage) string {
	s := strings.TrimSpace(string(a))
	if s == "" || s == "null" {
		return ""
	}
	return s
}

// syncBotStrategies reads the bot's current strategy_config and applies it to all
// active/finishing strategies created by this bot, then notifies the engine.
func (s *Server) syncBotStrategies(ctx context.Context, botID string, structural bool) {
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
	tpPct := 2.0 // default
	if cfg.TPPct != nil {
		tpPct = *cfg.TPPct
	}
	slPct := -5.0 // default
	if cfg.SLPct != nil {
		slPct = *cfg.SLPct
		if slPct > 0 {
			slPct = -slPct
		}
		if slPct <= -100 {
			slPct = 0
		}
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

	// Matrix params (nil = keep existing when not a matrix bot)
	matrixLevelsParam := nullableJSONB(cfg.MatrixLevels)
	matrixEntryParam := nullableJSONB(cfg.MatrixEntryLevel)

	// Hedge-bot strategies must never block cycle-start on a signal:
	// signal_configs is a pool for per-level use_signal buttons only.
	syncSignalFilter := cfg.SignalFilter
	if cfg.BotKind == "hedge" {
		syncSignalFilter = false
	}

	// Update all active/finishing strategies belonging to this bot
	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET
		   grid_levels = $1, grid_active = $2, grid_step_pct = $3, grid_size_usdt = $4,
		   tp_mode = $5, tp_pct = $6, sl_type = $7, sl_pct = $8, signal_filter = $9,
		   leverage = $10, margin_type = $11, hedge_mode = $12,
		   signal_configs = $13::jsonb, steps = ($14::text)::jsonb,
		   trailing_stop_enabled = $15, trailing_activation_pct = $16, trailing_callback_pct = $17,
		   matrix_levels      = COALESCE(($18::text)::jsonb, matrix_levels),
		   matrix_entry_level = COALESCE(($19::text)::jsonb, matrix_entry_level),
		   safe_zone_pct      = CASE WHEN $20::float8 > 0 THEN $20 ELSE safe_zone_pct END,
		   protected_build    = $21,
		   matrix_rebuild_on_sl      = $22,
		   matrix_rebuild_from_entry = $23,
		   relative_slots            = $24
		 WHERE bot_id = $25 AND status IN ('active','finishing')
		 RETURNING id`,
		gridLevels, gridActive, gridStep, gridSize,
		tpMode, tpPct, slType, slPct, syncSignalFilter,
		leverage, marginType, cfg.HedgeMode,
		string(scJSON), stepsParam,
		cfg.TrailingEnabled, trailingActPct, trailingCallPct,
		matrixLevelsParam, matrixEntryParam, cfg.SafeZonePct,
		cfg.ProtectedBuild, cfg.MatrixRebuildOnSL, cfg.MatrixRebuildFromEntry, cfg.RelativeSlots,
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
		if structural {
			s.engine.RestartCycle(ctx, id)
		} else {
			s.engine.UpdateTPSL(ctx, id)
		}
	}
}

// DELETE /bots/{id}
func (s *Server) DeleteBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")

	// A Мультибот's two legs are deleted together — a lone hedge leg left behind would
	// have nothing to hedge (its whitelist points at the signal leg being deleted), and a
	// lone signal leg would silently stop being hedged with no way to tell from the UI.
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM bots WHERE owner_id = $2 AND (id = $1 OR id = (SELECT paired_bot_id FROM bots WHERE id = $1))`,
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

// POST /bots/{id}/deploy вЂ" creates a subscription (linked copy) for the caller.
func (s *Server) DeployBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	sourceID := chi.URLParam(r, "id")
	ctx := r.Context()

	var req struct {
		AccountID string `json:"accountId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // optional body — absent/malformed is fine, accountId just stays ""
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	var acctOwner string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM exchange_accounts WHERE id = $1`, req.AccountID).Scan(&acctOwner); err != nil {
		writeError(w, http.StatusBadRequest, "account not found")
		return
	}
	if acctOwner != callerID {
		writeError(w, http.StatusForbidden, "account does not belong to caller")
		return
	}

	var name, desc, fullDesc string
	var triggers, stratCfg []byte
	var isPublic bool
	var pairedSourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT name, description, full_description, is_public, triggers, strategy_config, paired_bot_id FROM bots WHERE id = $1`,
		sourceID,
	).Scan(&name, &desc, &fullDesc, &isPublic, &triggers, &stratCfg, &pairedSourceID); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !isPublic {
		writeError(w, http.StatusForbidden, "bot is not public")
		return
	}

	// Prevent deploying the same template twice (even if the subscription was later edited/forked).
	var existingID string
	dupErr := s.pool.QueryRow(ctx,
		`SELECT id FROM bots WHERE owner_id = $1 AND source_bot_id = $2 LIMIT 1`,
		callerID, sourceID,
	).Scan(&existingID)
	if dupErr == nil {
		writeError(w, http.StatusConflict, "already deployed")
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
		INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, full_description, triggers, strategy_config, account_id)
		VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		callerID, sourceID, name, desc, fullDesc, triggers, stratCfg, req.AccountID,
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

	// The source bot is one leg of a Мультибот (see CreateMultiBot above) — clone its paired
	// leg too and link the two new clones the same way, otherwise a deployed Мультибот would
	// silently lose its other leg (the exact bug this task fixes). Mirrors CreateMultiBot's
	// own pair-creation wiring, including re-pointing the hedge clone's hedge_bot_whitelist
	// at the new signal clone's id — without that, the cloned hedge leg would still watch the
	// ORIGINAL template's signal leg (owned by someone else) instead of its own new twin.
	if pairedSourceID != nil {
		var pName, pDesc, pFullDesc string
		var pTriggers, pStratCfg []byte
		if err := tx.QueryRow(ctx,
			`SELECT name, description, full_description, triggers, strategy_config FROM bots WHERE id = $1`,
			*pairedSourceID,
		).Scan(&pName, &pDesc, &pFullDesc, &pTriggers, &pStratCfg); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		var pairedNewID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, full_description, triggers, strategy_config, account_id)
			VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8)
			RETURNING id`,
			callerID, *pairedSourceID, pName, pDesc, pFullDesc, pTriggers, pStratCfg, req.AccountID,
		).Scan(&pairedNewID); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bots SET paired_bot_id = $1 WHERE id = $2`, pairedNewID, newID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bots SET paired_bot_id = $1 WHERE id = $2`, newID, pairedNewID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		// Whichever of the two new clones is the hedge leg gets its hedge_bot_whitelist
		// re-pointed at the OTHER clone (the signal leg's) new id. Determined by each row's
		// OWN bot_kind, not by which one happened to be sourceID — after Task 2, the catalog
		// only ever exposes the signal leg, but this stays correct even if a hedge leg's id
		// were ever passed directly (e.g. a stale link from before Task 2 shipped).
		newSignalID, newHedgeID, hedgeStratCfg := newID, pairedNewID, pStratCfg
		if getJSONStringField(stratCfg, "bot_kind") == "hedge" {
			newSignalID, newHedgeID, hedgeStratCfg = pairedNewID, newID, stratCfg
		}
		hedgeStratCfg = setJSONField(hedgeStratCfg, "hedge_bot_whitelist", []string{newSignalID})
		if _, err := tx.Exec(ctx,
			`UPDATE bots SET strategy_config = $1 WHERE id = $2`, []byte(hedgeStratCfg), newHedgeID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
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

// POST /bots/{id}/fork вЂ" unlinks a subscription so it can be edited independently.
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
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	// Fetch bot kind, symbol, and override flag to check coin filter.
	var botKind, symbol string
	var ignoreCoinFilter bool
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(strategy_config->>'bot_kind', ''),
		        COALESCE(strategy_config->>'symbol', ''),
		        COALESCE(ignore_coin_filter, false)
		 FROM bots WHERE id = $1 AND owner_id = $2`,
		botID, callerID,
	).Scan(&botKind, &symbol, &ignoreCoinFilter)
	if err != nil {
		// bot not found — let setBotStatus return 404
		s.setBotStatus(w, r, "active")
		return
	}

	// For signal bots without the override, check the global blacklist.
	if botKind == "signal" && !ignoreCoinFilter && symbol != "" {
		var blacklist []string
		if dbErr := s.pool.QueryRow(ctx,
			`SELECT blacklist FROM coin_filter_settings WHERE id = 1`,
		).Scan(&blacklist); dbErr == nil {
			for _, b := range blacklist {
				if b == symbol {
					writeError(w, http.StatusUnprocessableEntity,
						"Монета «"+symbol+"» находится в чёрном списке фильтра монет. "+
							"Включите «Игнорировать фильтр монет» в настройках бота, чтобы продолжить.")
					return
				}
			}
		}
	}

	// Atomically start the timer and set status = active.
	// COALESCE keeps the existing active_since if the bot was already running (double-click idempotency).
	// A Мультибот's two legs (signal + hedge, linked via paired_bot_id) start together —
	// the hedge leg has nothing to watch until its signal twin is running.
	tag, err := s.pool.Exec(ctx,
		`UPDATE bots
		 SET active_since   = COALESCE(active_since, NOW()),
		     status         = 'active',
		     updated_at     = NOW()
		 WHERE owner_id = $2 AND (id = $1 OR id = (SELECT paired_bot_id FROM bots WHERE id = $1))`,
		botID, callerID,
	)
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

// POST /bots/{id}/stop
func (s *Server) StopBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	// Atomically accumulate active seconds and set status = stopped.
	// CASE guard ensures we only add elapsed time when the timer was actually running.
	// Stops the Мультибот's paired leg too (see StartBot) — each side ticks its own
	// active_seconds_acc independently, so this stays correct for either leg.
	tag, err := s.pool.Exec(ctx,
		`UPDATE bots
		 SET active_seconds_acc = active_seconds_acc
		       + CASE WHEN active_since IS NOT NULL
		              THEN GREATEST(0, EXTRACT(EPOCH FROM NOW() - active_since)::BIGINT)
		              ELSE 0
		         END,
		     active_since  = NULL,
		     status        = 'stopped',
		     updated_at    = NOW()
		 WHERE owner_id = $2 AND (id = $1 OR id = (SELECT paired_bot_id FROM bots WHERE id = $1))`,
		botID, callerID,
	)
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
	msg := "Бот остановлен пользователем"
	if status == "active" {
		msg = "Бот запущен пользователем"
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
			fmt.Sprintf("Остановлено %d стратегий без открытой позиции", len(ids)), "info", "strategy")
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

	// before: load events older than this timestamp (for "load more" pagination).
	// Defaults to a slight future offset so the first call returns the latest events.
	before := time.Now().Add(time.Second)
	if bs := r.URL.Query().Get("before"); bs != "" {
		if t, err := time.Parse(time.RFC3339Nano, bs); err == nil {
			before = t
		}
	}

	limit := 100
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	var queryErr error
	var rows pgx.Rows
	if category != "" {
		rows, queryErr = s.pool.Query(r.Context(),
			`SELECT message, level, category, created_at FROM bot_events
			 WHERE bot_id=$1 AND created_at < $2 AND category=$3
			 ORDER BY created_at DESC LIMIT $4`, botID, before, category, limit)
	} else {
		rows, queryErr = s.pool.Query(r.Context(),
			`SELECT message, level, category, created_at FROM bot_events
			 WHERE bot_id=$1 AND created_at < $2
			 ORDER BY created_at DESC LIMIT $3`, botID, before, limit)
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
	ctx := r.Context()

	// Non-official bots require admin approval before publishing. Also fetch the
	// display/config fields to snapshot into the independent catalog copy.
	var isOfficial bool
	var approvalStatus *string
	var name, desc, fullDesc, avatar string
	var whitelist, blacklist []string
	var triggers, stratCfg []byte
	var price float64
	if err := s.pool.QueryRow(ctx,
		`SELECT is_official, approval_status, name, description, full_description, avatar_url,
		        symbol_whitelist, symbol_blacklist, triggers, strategy_config, price_usd_month
		 FROM bots WHERE id = $1 AND owner_id = $2`,
		botID, callerID,
	).Scan(&isOfficial, &approvalStatus, &name, &desc, &fullDesc, &avatar,
		&whitelist, &blacklist, &triggers, &stratCfg, &price); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !isOfficial {
		if approvalStatus == nil || *approvalStatus != "approved" {
			writeError(w, http.StatusUnprocessableEntity,
				"Бот не прошёл согласование. Отправьте заявку и дождитесь одобрения администратора.")
			return
		}
	}

	// Mark the creator's own bot as published (UI state marker). The catalog list
	// hides this original in favour of its detached copy (see ListBots dedup).
	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET is_public = true, updated_at = NOW() WHERE id = $1 AND owner_id = $2`,
		botID, callerID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Upsert the independent catalog copy owned by the system Catalog account.
	// It survives deletion of the creator's bot (published_from_id ON DELETE SET NULL),
	// so accidentally deleting your own bot no longer removes it from the library.
	var copyID string
	if err := s.pool.QueryRow(ctx,
		`SELECT id FROM bots WHERE published_from_id = $1`, botID).Scan(&copyID); err == nil {
		// Re-publish: refresh the existing copy (snapshot update).
		if _, err := s.pool.Exec(ctx,
			`UPDATE bots SET name=$1, description=$2, full_description=$3, avatar_url=$4,
			        symbol_whitelist=$5, symbol_blacklist=$6, triggers=$7, strategy_config=$8,
			        price_usd_month=$9, is_official=$10, is_public=true, original_author_id=$11, updated_at=NOW()
			 WHERE id=$12`,
			name, desc, fullDesc, avatar, whitelist, blacklist, triggers, stratCfg,
			price, isOfficial, callerID, copyID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// First publish: create the detached copy.
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO bots (owner_id, name, description, full_description, avatar_url, is_public, is_official,
			                   symbol_whitelist, symbol_blacklist, triggers, strategy_config, price_usd_month,
			                   published_from_id, original_author_id)
			 VALUES ($1,$2,$3,$4,$5,true,$6,$7,$8,$9,$10,$11,$12,$13)`,
			catalogOwnerID, name, desc, fullDesc, avatar, isOfficial,
			whitelist, blacklist, triggers, stratCfg, price, botID, callerID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /bots/signal-scan — scan available symbols against provided signal configs.
// Applies direction filtering (like ScanBot) so counts match across the UI.
func (s *Server) ScanSignals(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SignalConfigs []signal.Config `json:"signal_configs"`
		Whitelist     []string        `json:"whitelist"`
		Blacklist     []string        `json:"blacklist"`
		Interval      string          `json:"interval"`
		Direction     string          `json:"direction"` // "long", "short", "both" (empty = both)
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
			// ComputeMultiTFState evaluates each signal on its own TF snapshot,
			// so mixed-TF configs (e.g. ST 5m + ADX 1h) work correctly.
			st := s.signalEngine.ComputeMultiTFState(sym, req.SignalConfigs)
			if st == signal.Neutral {
				return
			}
			// Apply direction filter: match logic used in ScanBot so counts align.
			var stateStr string
			switch req.Direction {
			case "long":
				if st != signal.Buy {
					return // sell signal doesn't match a long-only bot
				}
				stateStr = string(st)
			case "short":
				if st != signal.Sell {
					return // buy signal doesn't match a short-only bot
				}
				stateStr = string(st)
			default: // "both" or unset
				stateStr = string(st)
			}
			mu.Lock()
			matches = append(matches, scanResult{Symbol: sym, State: stateStr})
			mu.Unlock()
		}()
	}

	wg.Wait()

	if matches == nil {
		matches = []scanResult{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": matches})
}

// ── Hedge bot scan ────────────────────────────────────────────────────────────

// hedgeScanPos represents a single monitored/hedged position returned by scanHedgeBot.
type hedgeScanPos struct {
	Symbol        string  `json:"symbol"`
	MainDir       string  `json:"main_dir"`
	HedgeDir      string  `json:"hedge_dir"`
	Size          float64 `json:"size"`
	EntryPrice    float64 `json:"entry_price"`
	MarkPrice     float64 `json:"mark_price"`
	UnrealisedPnl float64 `json:"unrealised_pnl"`
	MetricLabel   string  `json:"metric_label"`
	MetricValue   float64 `json:"metric_value"`
	Threshold     float64 `json:"threshold"`
	MeetsCriteria bool    `json:"meets_criteria"`
	// status: "hedged" | "ready" | "monitoring"
	Status       string  `json:"status"`
	HedgeStratID string  `json:"hedge_strat_id,omitempty"`
	HedgePnl     float64 `json:"hedge_pnl,omitempty"`
	HedgeSize    float64 `json:"hedge_size,omitempty"`
}

// calcHedgeMetricValue returns the human-readable label, current metric value, and
// activation threshold for a position, matching meetsActivationCriteria() semantics.
func calcHedgeMetricValue(pos hedgePosInfo, cfg botCfgJSON) (label string, current, threshold float64) {
	threshold = math.Abs(cfg.HedgeActValue)
	switch cfg.HedgeActType {
	case 0, 1: // last_order% or drawdown%
		label = "Просадка"
		current = hedgeDrawdown(pos)
	case 2: // pnl$ — show loss as positive number
		label = "Убыток $"
		current = -pos.UnrealisedPnl
	case 3: // roi% — show loss as positive number
		label = "ROI убыток %"
		current = -hedgeROI(pos)
	default:
		label = "Просадка"
		current = hedgeDrawdown(pos)
	}
	return
}

// scanHedgeBot fetches open exchange positions and returns which ones the hedge bot
// is currently monitoring and which are already being hedged.
func (s *Server) scanHedgeBot(w http.ResponseWriter, ctx context.Context, botID, accountID string, whitelist, blacklist []string, cfg botCfgJSON) {
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "бот не привязан к торговому аккаунту")
		return
	}

	ex, err := s.loadBotAccountExchange(ctx, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось загрузить ключи аккаунта")
		return
	}

	rawPositions, err := ex.FetchPositions(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("ошибка получения позиций: %v", err))
		return
	}

	posMap, _ := buildHedgePosMap(rawPositions)
	delistSymbols := s.GetDelistingSymbols()

	var positions []hedgeScanPos

	for _, bySymbol := range posMap {
		for side, pos := range bySymbol {
			if !symbolPassesHedgeFilter(pos.Symbol, whitelist, blacklist, delistSymbols) {
				continue
			}

			mainDir := hedgeSideToDir(side)

			if !s.positionPassesBotFilter(ctx, accountID, pos.Symbol, mainDir,
				cfg.HedgeBotWhitelist, cfg.HedgeBotBlacklist, "") {
				continue
			}

			// Direction filter
			switch cfg.Direction {
			case "long":
				if mainDir != "long" {
					continue
				}
			case "short":
				if mainDir != "short" {
					continue
				}
			}

			hedgeDir := oppositeHedgeDir(mainDir)

			// Check if already hedged by this bot
			var hedgeStratID string
			_ = s.pool.QueryRow(ctx,
				`SELECT id FROM strategies
				 WHERE bot_id=$1 AND symbol=$2 AND direction=$3
				   AND status IN ('active','finishing')
				 LIMIT 1`,
				botID, pos.Symbol, hedgeDir,
			).Scan(&hedgeStratID)

			// Get hedge position metrics if hedged
			var hedgePnl, hedgeSize float64
			if hedgeStratID != "" {
				hedgeSide := hedgeDirToSide(hedgeDir)
				if hPos, ok := posMap[pos.Symbol][hedgeSide]; ok {
					hedgePnl = hPos.UnrealisedPnl
					hedgeSize = hPos.Size
				}
			}

			label, metricVal, threshold := calcHedgeMetricValue(pos, cfg)
			meetsCriteria := meetsActivationCriteria(pos, cfg)

			status := "monitoring"
			if hedgeStratID != "" {
				status = "hedged"
			} else if meetsCriteria {
				status = "ready"
			}

			positions = append(positions, hedgeScanPos{
				Symbol:        pos.Symbol,
				MainDir:       mainDir,
				HedgeDir:      hedgeDir,
				Size:          pos.Size,
				EntryPrice:    pos.EntryPrice,
				MarkPrice:     pos.MarkPrice,
				UnrealisedPnl: pos.UnrealisedPnl,
				MetricLabel:   label,
				MetricValue:   metricVal,
				Threshold:     threshold,
				MeetsCriteria: meetsCriteria,
				Status:        status,
				HedgeStratID:  hedgeStratID,
				HedgePnl:      hedgePnl,
				HedgeSize:     hedgeSize,
			})
		}
	}

	// Sort: hedged first, then ready (criteria met), then monitoring
	statusOrder := map[string]int{"hedged": 0, "ready": 1, "monitoring": 2}
	sort.Slice(positions, func(i, j int) bool {
		oi, oj := statusOrder[positions[i].Status], statusOrder[positions[j].Status]
		if oi != oj {
			return oi < oj
		}
		return positions[i].Symbol < positions[j].Symbol
	})

	if positions == nil {
		positions = []hedgeScanPos{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":      "hedge",
		"positions": positions,
	})
}

// GET /bots/{id}/scan вЂ" run signal scan for this bot and return matching symbols.
// For hedge bots, returns positions being monitored/hedged instead of signal hits.
func (s *Server) ScanBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Fetch bot (including account_id needed for hedge bots)
	var ownerID string
	var accountIDPtr *string
	var whitelist, blacklist []string
	var stratCfgBytes []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, account_id, symbol_whitelist, symbol_blacklist, strategy_config
		 FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &accountIDPtr, &whitelist, &blacklist, &stratCfgBytes); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if ownerID != callerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var cfg botCfgJSON
	if err := json.Unmarshal(stratCfgBytes, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid bot config")
		return
	}

	// Hedge and matrix bots don't scan for signals — they manage positions/pairs
	// directly. Show the monitored/open positions instead of erroring on missing
	// activation signals (a matrix bot legitimately has none).
	if cfg.BotKind == "hedge" || cfg.BotKind == "matrix" {
		accountID := ""
		if accountIDPtr != nil {
			accountID = *accountIDPtr
		}
		s.scanHedgeBot(w, ctx, botID, accountID, whitelist, blacklist, cfg)
		return
	}

	if len(cfg.ActivationSignals) == 0 {
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
			st := s.signalEngine.ComputeMultiTFState(sym, sigCfgs)
			if st == signal.Neutral {
				return
			}

			// Get raw signal value for the priority signal (or first available).
			vals := s.signalEngine.QueryValues(sym, interval, sigCfgs)
			var sigVal float64
			if vals != nil {
				if cfg.PrioritySignal != "" {
					sigVal = vals[cfg.PrioritySignal]
				} else {
					for _, v := range vals {
						sigVal = v
						break
					}
				}
			}

			ttlRem := s.signalEngine.QueryTTLRemaining(sym, interval, sigCfgs)

			// Strength = sort key (higher = preferred).
			// priority_signal=st-flip  → TTL remaining (more = more recent signal)
			// priority_signal=<other>  → signal value (higher = stronger)
			// no priority_signal       → buy: negate value (lower RSI = stronger); sell: value
			var strength float64
			switch {
			case cfg.PrioritySignal == "st-flip":
				if ttlRem >= 0 {
					strength = ttlRem
				}
			case cfg.PrioritySignal != "":
				strength = sigVal
			default:
				if st == signal.Buy {
					strength = -sigVal
				} else {
					strength = sigVal
				}
			}

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
		"results":            hits,
		"scanned":            len(symbols),
		"activation_signals": sigNames,
		"preview":            cfg,
	})
}

// POST /bots/{id}/trigger вЂ" manually trigger strategy creation for a specific symbol.
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
			writeError(w, http.StatusConflict, fmt.Sprintf("лимит стратегий бота достигнут (%d/%d)", activeCount, b.maxStrat))
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
			writeError(w, http.StatusConflict, fmt.Sprintf("лимит %s стратегий бота достигнут (%d/%d)", req.Direction, dirCount, dirLimit))
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
		writeError(w, http.StatusConflict, "стратегия для этой пары уже открыта")
		return
	}

	if _, err := s.createBotStrategy(ctx, b, cfg, req.Symbol, req.Direction, 0, "", nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Запущена стратегия вручную: %s %s", req.Symbol, req.Direction), "info", "user")
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// sortHits orders scan hits:
//  1. actionable (not blocked, not open) вЂ" sorted by signal strength desc
//  2. already open вЂ" sorted by signal strength desc
//  3. direction blocked вЂ" sorted by signal strength desc
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

// в"Ђв"Ђ Admin handlers в"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђв"Ђ

// AddBotBlacklist adds a symbol to the bot's symbol_blacklist.
// POST /bots/{id}/blacklist-add  Body: {"symbol":"BTCUSDT"}
func (s *Server) AddBotBlacklist(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")

	var req struct {
		Symbol string `json:"symbol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}

	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots
		 SET symbol_blacklist = array_append(symbol_blacklist, $1)
		 WHERE id = $2 AND owner_id = $3
		   AND NOT ($1 = ANY(symbol_blacklist))`,
		req.Symbol, botID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	added := tag.RowsAffected() > 0
	if added {
		s.logBotEvent(r.Context(), botID,
			fmt.Sprintf("Символ %s добавлен в блэклист пользователем", req.Symbol), "info", "user")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": added})
}

// GET /admin/bots вЂ" list all bots (admin only)
func (s *Server) ListAdminBots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.pool.Query(ctx,
		`SELECT `+botCols+zeroStatsCols+botFrom+`ORDER BY b.is_official DESC, b.created_at DESC`)
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

// POST /admin/bots/{id}/publish-to-catalog (RequireAdmin)
// Publishes any bot instance to the public library.
// Body: { name, isOfficial, price }
// Checks for duplicate names among public bots (case-insensitive).
func (s *Server) PublishBotToCatalog(w http.ResponseWriter, r *http.Request) {
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	var req struct {
		Name       string  `json:"name"`
		IsOfficial bool    `json:"isOfficial"`
		Price      float64 `json:"price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if req.Price < 0 {
		req.Price = 0
	}

	// Duplicate name check among public bots (excluding this bot itself)
	var dupCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bots WHERE LOWER(name) = LOWER($1) AND is_public = true AND id != $2`,
		req.Name, botID,
	).Scan(&dupCount); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if dupCount > 0 {
		writeError(w, http.StatusConflict, "Бот с таким именем уже существует в библиотеке")
		return
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE bots SET name = $1, is_official = $2, price_usd_month = $3, is_public = true, updated_at = NOW()
		 WHERE id = $4`,
		req.Name, req.IsOfficial, req.Price, botID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}

	callerID := UserIDFromCtx(ctx)
	bot, ok := fetchBot(s, r, botID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, bot)
}

// DELETE /admin/bots/{id} (RequireAdmin)
// Admin-only: delete any bot regardless of ownership.
func (s *Server) DeleteAdminBot(w http.ResponseWriter, r *http.Request) {
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM bots WHERE id = $1`, botID)
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

// POST /admin/bots вЂ" create an official NovaBot (admin only)
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
