// services/api-gateway/custom_signals_handler.go
//
// Custom signals let a user combine several catalog signals (pkg/signal registry) into one
// named, reusable AND-combo, private to them (migrations/091_custom_signals.sql). Once
// saved, a custom signal can be referenced by a webhook exactly like a built-in catalog
// signal — resolveSignalConfigs is the single place that turns either kind of reference
// into the []signal.Config that both the live signal engine (Engine.Subscribe already
// AND-combines multiple configs — see computeUnit.compute in pkg/signal/engine.go) and this
// file's own historical preview walk consume.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"sis/pkg/signal"
)

// resolveSignalConfigs returns the []signal.Config for either a single catalog signal
// (catalogSignalID set, params is that signal's own params) or a saved custom combo
// (customSignalID set — params is ignored, each leg uses its own saved params). Exactly one
// of the two must be non-empty; callers get there via the webhooks_signal_source_xor DB
// constraint or their own request validation.
func (s *Server) resolveSignalConfigs(ctx context.Context, catalogSignalID, customSignalID string, params map[string]any) ([]signal.Config, error) {
	if catalogSignalID != "" {
		return []signal.Config{{Name: catalogSignalID, Params: params}}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT component_signal_id, params FROM custom_signal_components
		 WHERE custom_signal_id=$1 ORDER BY position`, customSignalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []signal.Config
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		configs = append(configs, signal.Config{Name: id, Params: p})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("custom signal %s has no components", customSignalID)
	}
	return configs, nil
}

// ── Combo preview (historical, read-only) ───────────────────────────────────

type comboPreviewComponent struct {
	SignalID string         `json:"signal_id"`
	Params   map[string]any `json:"params"`
}

// SignalComboPreview walks candle history AND-combining several signals at once — the
// historical counterpart to SignalChartHistory, for previewing a combo before saving it.
// Each leg is evaluated with the plain Compute(candles) — deliberately NOT the
// SymbolComputer/ComputeWithSymbol dispatch computeSignalState uses for the live engine:
// whale/leverage only expose a real state via the LIVE cache, so applying that here would
// paint every past bar with today's live reading. Compute() alone correctly (and honestly)
// reads Neutral for those the whole way back, same as previewing them singly already does.
// POST /signals/combo-preview
func (s *Server) SignalComboPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol     string                   `json:"symbol"`
		Interval   string                   `json:"interval"`
		Limit      int                      `json:"limit"`
		Components []comboPreviewComponent  `json:"components"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Symbol == "" || req.Interval == "" {
		writeError(w, http.StatusBadRequest, "symbol and interval required")
		return
	}
	if len(req.Components) < 2 {
		writeError(w, http.StatusBadRequest, "at least 2 components required")
		return
	}
	limit := 500
	if req.Limit > 0 && req.Limit <= 1000 {
		limit = req.Limit
	}

	sigs := make([]signal.Signal, 0, len(req.Components))
	for _, c := range req.Components {
		if c.SignalID == "" {
			writeError(w, http.StatusBadRequest, "component signal_id required")
			return
		}
		sig, err := signal.Build(signal.Config{Name: c.SignalID, Params: c.Params})
		if err != nil {
			writeError(w, http.StatusBadRequest, "signal \""+c.SignalID+"\": "+err.Error())
			return
		}
		sigs = append(sigs, sig)
	}

	bybitIv := bybitChartTF(req.Interval)
	candles, err := signal.FetchKlineHistory(req.Symbol, bybitIv, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch klines: "+err.Error())
		return
	}

	var events []chartEvent
	prev := signal.Neutral
	for i := 2; i < len(candles); i++ {
		slice := candles[:i+1]
		combined := sigs[0].Compute(slice)
		for _, sig := range sigs[1:] {
			if combined == signal.Neutral {
				break
			}
			if st := sig.Compute(slice); st != combined {
				combined = signal.Neutral
			}
		}
		if combined != prev {
			events = append(events, chartEvent{
				Time:  candles[i].Time,
				State: string(combined),
				Price: candles[i].Close,
			})
			prev = combined
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

// ── Custom signals CRUD ──────────────────────────────────────────────────────

type customSignalComponentRow struct {
	SignalID   string         `json:"signal_id"`
	SignalName string         `json:"signal_name"`
	Params     map[string]any `json:"params"`
}

type customSignalRow struct {
	ID         string                      `json:"id"`
	Name       string                      `json:"name"`
	Badge      string                      `json:"badge"`
	CreatedAt  string                      `json:"created_at"`
	Components []customSignalComponentRow  `json:"components"`
}

// ListCustomSignals returns the caller's own saved combos, each with its resolved
// components (used to render the "Мои сигналы" catalog section and to prefill the combo
// builder for editing/duplicating).
// GET /custom-signals
func (s *Server) ListCustomSignals(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT cs.id::text, cs.name, cs.badge, cs.created_at::text, csc.component_signal_id, COALESCE(st.name, csc.component_signal_id), csc.params
		 FROM custom_signals cs
		 JOIN custom_signal_components csc ON csc.custom_signal_id = cs.id
		 LEFT JOIN signal_types st ON st.id = csc.component_signal_id
		 WHERE cs.owner_id = $1
		 ORDER BY cs.created_at DESC, csc.position ASC`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	order := make([]string, 0)
	byID := make(map[string]*customSignalRow)
	for rows.Next() {
		var id, name, badge, createdAt, compID, compName string
		var rawParams []byte
		if err := rows.Scan(&id, &name, &badge, &createdAt, &compID, &compName, &rawParams); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		cs, ok := byID[id]
		if !ok {
			cs = &customSignalRow{ID: id, Name: name, Badge: badge, CreatedAt: createdAt}
			byID[id] = cs
			order = append(order, id)
		}
		var params map[string]any
		_ = json.Unmarshal(rawParams, &params)
		cs.Components = append(cs.Components, customSignalComponentRow{SignalID: compID, SignalName: compName, Params: params})
	}

	result := make([]customSignalRow, 0, len(order))
	for _, id := range order {
		result = append(result, *byID[id])
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateCustomSignal saves a new combo. Every component must independently build via
// signal.Build (same computability check CreateWebhook already does for a single signal) —
// catching a bad signal_id/params here, not later when something tries to subscribe to it.
// POST /custom-signals
func (s *Server) CreateCustomSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Name       string                  `json:"name"`
		Badge      string                  `json:"badge"`
		Components []comboPreviewComponent `json:"components"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	req.Badge = strings.ToUpper(strings.TrimSpace(req.Badge))
	if req.Badge == "" || len([]rune(req.Badge)) > 4 {
		writeError(w, http.StatusBadRequest, "badge is required and must be at most 4 characters")
		return
	}
	if len(req.Components) < 2 {
		writeError(w, http.StatusBadRequest, "at least 2 components required")
		return
	}
	for _, c := range req.Components {
		if c.SignalID == "" {
			writeError(w, http.StatusBadRequest, "component signal_id required")
			return
		}
		if _, err := signal.Build(signal.Config{Name: c.SignalID, Params: c.Params}); err != nil {
			writeError(w, http.StatusBadRequest, "signal \""+c.SignalID+"\" cannot be used in a combo: "+err.Error())
			return
		}
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO custom_signals (owner_id, name, badge) VALUES ($1, $2, $3) RETURNING id`,
		userID, req.Name, req.Badge,
	).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	for i, c := range req.Components {
		paramsJSON, _ := json.Marshal(c.Params)
		if _, err := tx.Exec(ctx,
			`INSERT INTO custom_signal_components (custom_signal_id, component_signal_id, params, position)
			 VALUES ($1, $2, $3, $4)`,
			id, c.SignalID, paramsJSON, i,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// DeleteCustomSignal removes a combo owned by the caller. Any webhook alerts built on it
// cascade-delete at the DB level (custom_signal_id ON DELETE CASCADE) — unsubscribed from
// the live signal engine first so no orphaned callback keeps referencing a deleted alert.
// DELETE /custom-signals/{id}
func (s *Server) DeleteCustomSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	rows, err := s.pool.Query(r.Context(),
		`SELECT id FROM webhooks WHERE custom_signal_id=$1 AND owner_id=$2`, id, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	var webhookIDs []string
	for rows.Next() {
		var whID string
		if rows.Scan(&whID) == nil {
			webhookIDs = append(webhookIDs, whID)
		}
	}
	rows.Close()
	for _, whID := range webhookIDs {
		s.signalEngine.Unsubscribe(whID)
	}

	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM custom_signals WHERE id=$1 AND owner_id=$2`, id, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "custom signal not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
