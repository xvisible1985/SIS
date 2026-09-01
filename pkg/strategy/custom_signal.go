// pkg/strategy/custom_signal.go
package strategy

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"sis/pkg/signal"
)

// resolveSignalConfigs expands a strategy's []SignalConfig into the []signal.Config the
// signal engine actually evaluates. A bare catalog reference (Name set) maps 1:1. A saved
// combo reference (CustomSignalID set — see migrations/091_custom_signals.sql) expands into
// its saved component legs, each with its own saved params. This is safe to splice flat
// into the returned slice: every one of this package's evaluation paths (Subscribe,
// ComputeStateForce, ComputeMultiTFState, QueryState, QueryValues) already AND-combines the
// whole slice, so a combo used as one entry among several behaves identically to writing
// out its legs by hand — same semantics as a user manually adding N catalog signals.
//
// A custom signal that fails to resolve (deleted, DB hiccup) is dropped rather than erroring
// the whole call — the same fail-open-per-entry posture callers already have for an empty
// configs list; an entry search silently missing one leg is preferable to every strategy
// referencing that combo going permanently unable to evaluate its signal filter at all.
//
// No ctx parameter deliberately: every call site here is either mid-lock (can't await an
// outer ctx anyway) or an internal polling loop (engine.go's periodic signal-state refresh),
// so this owns a short-lived background context with its own timeout rather than asking 8
// call sites to thread one through just for this.
func (ar *AccountRunner) resolveSignalConfigs(configs []SignalConfig) []signal.Config {
	out := make([]signal.Config, 0, len(configs))
	for _, sc := range configs {
		if sc.CustomSignalID == "" {
			out = append(out, signal.Config{Name: sc.Name, Params: sc.Params})
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rows, err := ar.pool.Query(ctx,
			`SELECT component_signal_id, params FROM custom_signal_components
			 WHERE custom_signal_id=$1 ORDER BY position`, sc.CustomSignalID)
		if err != nil {
			cancel()
			log.Printf("strategy: resolveSignalConfigs: load custom signal %s: %v", sc.CustomSignalID, err)
			continue
		}
		for rows.Next() {
			var id string
			var raw []byte
			if rows.Scan(&id, &raw) != nil {
				continue
			}
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			out = append(out, signal.Config{Name: id, Params: p})
		}
		rows.Close()
		cancel()
	}
	return out
}

// signalConfigLabel returns a short, human-readable log label for one SignalConfig leg —
// the catalog id, or a truncated combo marker for a custom reference (matches this
// codebase's own convention of truncating UUIDs to 8 chars in logs, e.g. strategy.ID[:8]).
// Deliberately doesn't resolve the combo's real name — this is a log line, not a place
// worth spending a DB round-trip on.
func signalConfigLabel(sc SignalConfig) string {
	if sc.CustomSignalID == "" {
		return sc.Name
	}
	if len(sc.CustomSignalID) > 8 {
		return "combo:" + sc.CustomSignalID[:8]
	}
	return "combo:" + sc.CustomSignalID
}
