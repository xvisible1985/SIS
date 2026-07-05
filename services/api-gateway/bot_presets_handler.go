package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type presetBotResp struct {
	EntryID     string `json:"entry_id"`
	BotID       string `json:"bot_id"`
	Role        string `json:"role"`
	SortOrder   int    `json:"sort_order"`
	Name        string `json:"name"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
}

type presetResp struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Aggressiveness string          `json:"aggressiveness"`
	CoinType       string          `json:"coin_type"`
	TradingStyle   string          `json:"trading_style"`
	Tags           []string        `json:"tags"`
	IsActive       bool            `json:"is_active"`
	SortOrder      int             `json:"sort_order"`
	Bots           []presetBotResp `json:"bots"`
}

func (s *Server) loadPresetBots(ctx context.Context, presetID string) ([]presetBotResp, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pb.id, pb.bot_id, pb.role, pb.sort_order,
		       b.name, b.description, COALESCE(b.avatar_url, '')
		FROM bot_preset_bots pb
		JOIN bots b ON b.id = pb.bot_id
		WHERE pb.preset_id = $1
		ORDER BY pb.sort_order, pb.id`, presetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bots []presetBotResp
	for rows.Next() {
		var b presetBotResp
		if err := rows.Scan(&b.EntryID, &b.BotID, &b.Role, &b.SortOrder,
			&b.Name, &b.Description, &b.AvatarURL); err != nil {
			return nil, err
		}
		bots = append(bots, b)
	}
	if bots == nil {
		bots = []presetBotResp{}
	}
	return bots, nil
}

func scanPreset(row interface {
	Scan(...any) error
}, p *presetResp) error {
	return row.Scan(&p.ID, &p.Name, &p.Description, &p.Aggressiveness,
		&p.CoinType, &p.TradingStyle, &p.Tags, &p.IsActive, &p.SortOrder)
}

const presetCols = `id, name, description, aggressiveness, coin_type, trading_style,
	tags, is_active, sort_order`

// GET /admin/bot-presets
func (s *Server) ListAdminBotPresets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.pool.Query(ctx,
		`SELECT `+presetCols+` FROM bot_presets ORDER BY sort_order, created_at`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	var presets []presetResp
	for rows.Next() {
		var p presetResp
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Aggressiveness,
			&p.CoinType, &p.TradingStyle, &p.Tags, &p.IsActive, &p.SortOrder); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		presets = append(presets, p)
	}
	rows.Close()

	for i := range presets {
		bots, err := s.loadPresetBots(ctx, presets[i].ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		presets[i].Bots = bots
	}
	if presets == nil {
		presets = []presetResp{}
	}
	writeJSON(w, http.StatusOK, presets)
}

// POST /admin/bot-presets
func (s *Server) CreateBotPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string   `json:"name"`
		Description    string   `json:"description"`
		Aggressiveness string   `json:"aggressiveness"`
		CoinType       string   `json:"coin_type"`
		TradingStyle   string   `json:"trading_style"`
		Tags           []string `json:"tags"`
		SortOrder      int      `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if body.Aggressiveness == "" {
		body.Aggressiveness = "moderate"
	}
	if body.CoinType == "" {
		body.CoinType = "both"
	}
	if body.TradingStyle == "" {
		body.TradingStyle = "both"
	}
	if body.Tags == nil {
		body.Tags = []string{}
	}

	var p presetResp
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO bot_presets (name, description, aggressiveness, coin_type, trading_style, tags, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+presetCols,
		body.Name, body.Description, body.Aggressiveness, body.CoinType, body.TradingStyle,
		body.Tags, body.SortOrder,
	).Scan(&p.ID, &p.Name, &p.Description, &p.Aggressiveness, &p.CoinType, &p.TradingStyle,
		&p.Tags, &p.IsActive, &p.SortOrder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	p.Bots = []presetBotResp{}
	writeJSON(w, http.StatusCreated, p)
}

// PATCH /admin/bot-presets/{id}
func (s *Server) PatchBotPreset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx := r.Context()
	var args []any
	var sets []string

	addStr := func(key, col string) {
		if v, ok := raw[key]; ok {
			var s string
			json.Unmarshal(v, &s)
			args = append(args, s)
			sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
		}
	}
	addBool := func(key, col string) {
		if v, ok := raw[key]; ok {
			var b bool
			json.Unmarshal(v, &b)
			args = append(args, b)
			sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
		}
	}
	addInt := func(key, col string) {
		if v, ok := raw[key]; ok {
			var n int
			json.Unmarshal(v, &n)
			args = append(args, n)
			sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
		}
	}
	addStrSlice := func(key, col string) {
		if v, ok := raw[key]; ok {
			var ss []string
			json.Unmarshal(v, &ss)
			args = append(args, ss)
			sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
		}
	}

	addStr("name", "name")
	addStr("description", "description")
	addStr("aggressiveness", "aggressiveness")
	addStr("coin_type", "coin_type")
	addStr("trading_style", "trading_style")
	addStrSlice("tags", "tags")
	addBool("is_active", "is_active")
	addInt("sort_order", "sort_order")

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	sets = append(sets, "updated_at=NOW()")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE bot_presets SET %s WHERE id=$%d",
		joinStr(sets, ","), len(args))

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}

	var p presetResp
	s.pool.QueryRow(ctx, `SELECT `+presetCols+` FROM bot_presets WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Aggressiveness, &p.CoinType, &p.TradingStyle,
			&p.Tags, &p.IsActive, &p.SortOrder)

	bots, _ := s.loadPresetBots(ctx, id)
	p.Bots = bots
	writeJSON(w, http.StatusOK, p)
}

// DELETE /admin/bot-presets/{id}
func (s *Server) DeleteBotPreset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM bot_presets WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/bot-presets/{id}/bots
func (s *Server) AddBotToPreset(w http.ResponseWriter, r *http.Request) {
	presetID := chi.URLParam(r, "id")
	var body struct {
		BotID     string `json:"bot_id"`
		Role      string `json:"role"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.BotID == "" {
		writeError(w, http.StatusBadRequest, "bot_id required")
		return
	}
	if body.Role == "" {
		body.Role = "trading"
	}

	var entry presetBotResp
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO bot_preset_bots (preset_id, bot_id, role, sort_order)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (preset_id, bot_id) DO UPDATE SET role=EXCLUDED.role, sort_order=EXCLUDED.sort_order
		RETURNING id, bot_id, role, sort_order`,
		presetID, body.BotID, body.Role, body.SortOrder,
	).Scan(&entry.EntryID, &entry.BotID, &entry.Role, &entry.SortOrder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	s.pool.QueryRow(r.Context(),
		`SELECT name, description, COALESCE(avatar_url,'') FROM bots WHERE id=$1`, entry.BotID,
	).Scan(&entry.Name, &entry.Description, &entry.AvatarURL)

	writeJSON(w, http.StatusCreated, entry)
}

// DELETE /admin/bot-presets/{id}/bots/{entryId}
func (s *Server) RemoveBotFromPreset(w http.ResponseWriter, r *http.Request) {
	presetID := chi.URLParam(r, "id")
	entryID := chi.URLParam(r, "entryId")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM bot_preset_bots WHERE id=$1 AND preset_id=$2`, entryID, presetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "entry not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /bot-presets  (RequireAuth — для Quick Start)
// Query params: aggressiveness, coin_type, trading_style, tags
func (s *Server) ListBotPresets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	var args []any
	conditions := []string{"is_active = true"}

	if v := q.Get("aggressiveness"); v != "" {
		args = append(args, v)
		conditions = append(conditions, fmt.Sprintf("(aggressiveness=$%d OR aggressiveness='moderate')", len(args)))
	}
	if v := q.Get("coin_type"); v != "" {
		args = append(args, v)
		conditions = append(conditions, fmt.Sprintf("(coin_type=$%d OR coin_type='both')", len(args)))
	}
	if v := q.Get("trading_style"); v != "" {
		args = append(args, v)
		conditions = append(conditions, fmt.Sprintf("(trading_style=$%d OR trading_style='both')", len(args)))
	}
	if v := q.Get("tags"); v != "" {
		args = append(args, v)
		conditions = append(conditions, fmt.Sprintf("$%d=ANY(tags)", len(args)))
	}

	query := fmt.Sprintf(`SELECT %s FROM bot_presets WHERE %s ORDER BY sort_order, created_at`,
		presetCols, joinStr(conditions, " AND "))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	var presets []presetResp
	for rows.Next() {
		var p presetResp
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Aggressiveness,
			&p.CoinType, &p.TradingStyle, &p.Tags, &p.IsActive, &p.SortOrder); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		presets = append(presets, p)
	}
	rows.Close()

	for i := range presets {
		bots, _ := s.loadPresetBots(ctx, presets[i].ID)
		presets[i].Bots = bots
	}
	if presets == nil {
		presets = []presetResp{}
	}
	writeJSON(w, http.StatusOK, presets)
}

func joinStr(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
