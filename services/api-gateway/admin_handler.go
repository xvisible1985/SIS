package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"sis/pkg/signal"
)

// GetSignalOverride returns the current manual override for a test signal.
// GET /admin/signal-override/{name}
func (s *Server) GetSignalOverride(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	v, ok := signal.GetTestOverride(name)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"active": ok,
		"value":  v,
	})
}

// SetSignalOverride sets or clears the manual override for a test signal.
// PUT /admin/signal-override/{name}  body: {"value":45.5} or {"value":null}
func (s *Server) SetSignalOverride(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body struct {
		Value *float64 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Value == nil {
		signal.ClearTestOverride(name)
	} else {
		signal.SetTestOverride(name, *body.Value)
	}
	s.signalEngine.ForceRecompute(name)
	s.engine.PushSignalOverride(name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetAdminMetrics returns a live snapshot of Signal Engine + Strategy Engine metrics.
func (s *Server) GetAdminMetrics(w http.ResponseWriter, r *http.Request) {
	sigSnap := s.signalEngine.Metrics()

	// Strategy engine counters
	active, cycles := s.engine.ActiveStats()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"signal": sigSnap,
		"strategy": map[string]interface{}{
			"activeStrategies": active,
			"activeCycles":     cycles,
			"ordersToday":      0, // TODO: query DB
			"fillsToday":       0, // TODO: query DB
			"accounts":         []interface{}{},
		},
	})
}
