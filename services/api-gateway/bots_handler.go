// services/api-gateway/bots_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ── Types ────────────────────────────────────────────────────────────────────

type botResp struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	OwnerID         string          `json:"ownerId"`
	OwnerName       string          `json:"ownerName"`
	IsOwn           bool            `json:"isOwn"`
	IsPublic        bool            `json:"isPublic"`
	Status          string          `json:"status"`
	SourceBotID     *string         `json:"sourceBotId"`
	IsFork          bool            `json:"isFork"`
	SymbolWhitelist []string        `json:"symbolWhitelist"`
	SymbolBlacklist []string        `json:"symbolBlacklist"`
	Triggers        json.RawMessage `json:"triggers"`
	StrategyConfig  json.RawMessage `json:"strategyConfig"`
	DeployCount     int             `json:"deployCount"`
	CreatedAt       time.Time       `json:"createdAt"`
}

type listBotsResp struct {
	Catalog []botResp `json:"catalog"`
	Mine    []botResp `json:"mine"`
}

// ── Helpers ──────────────────────────────────────────────────────────────────

const botCols = `b.id, b.name, b.description, b.owner_id, u.email,
	b.is_public, b.status, b.source_bot_id, b.is_fork,
	b.symbol_whitelist, b.symbol_blacklist,
	b.triggers, b.strategy_config, b.deploy_count, b.created_at`

const botFrom = ` FROM bots b JOIN users u ON u.id = b.owner_id `

// collectBots scans all rows into []botResp and closes rows.
func collectBots(rows pgx.Rows, callerID string) ([]botResp, error) {
	defer rows.Close()
	var result []botResp
	for rows.Next() {
		var b botResp
		var triggers, stratCfg []byte
		if err := rows.Scan(
			&b.ID, &b.Name, &b.Description, &b.OwnerID, &b.OwnerName,
			&b.IsPublic, &b.Status, &b.SourceBotID, &b.IsFork,
			&b.SymbolWhitelist, &b.SymbolBlacklist,
			&triggers, &stratCfg, &b.DeployCount, &b.CreatedAt,
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

// ── Handlers ─────────────────────────────────────────────────────────────────

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
		Name            string          `json:"name"`
		Description     string          `json:"description"`
		IsPublic        bool            `json:"isPublic"`
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
		INSERT INTO bots (owner_id, name, description, is_public,
		                  symbol_whitelist, symbol_blacklist, triggers, strategy_config)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		callerID, req.Name, req.Description, req.IsPublic,
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

	addStr("name", "name")
	addStr("description", "description")
	addBool("isPublic", "is_public")
	addSlice("symbolWhitelist", "symbol_whitelist")
	addSlice("symbolBlacklist", "symbol_blacklist")
	addRaw("triggers", "triggers")
	addRaw("strategyConfig", "strategy_config")

	if len(sets) == 0 {
		bot, _ := fetchBot(s, r, botID, callerID)
		writeJSON(w, http.StatusOK, bot)
		return
	}

	sets = append(sets, "updated_at = NOW()")
	sql := "UPDATE bots SET " + strings.Join(sets, ", ") + " WHERE id = $1"
	if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
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

// POST /bots/{id}/deploy — creates a subscription (linked copy) for the caller.
func (s *Server) DeployBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	sourceID := chi.URLParam(r, "id")
	ctx := r.Context()

	var name, desc string
	var triggers, stratCfg []byte
	var isPublic bool
	if err := s.pool.QueryRow(ctx,
		`SELECT name, description, is_public, triggers, strategy_config FROM bots WHERE id = $1`,
		sourceID,
	).Scan(&name, &desc, &isPublic, &triggers, &stratCfg); err != nil {
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
		INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, triggers, strategy_config)
		VALUES ($1, $2, false, $3, $4, $5, $6)
		RETURNING id`,
		callerID, sourceID, name, desc, triggers, stratCfg,
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

// POST /bots/{id}/fork — unlinks a subscription so it can be edited independently.
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
	w.WriteHeader(http.StatusNoContent)
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
