package main

import (
	"encoding/json"
	"testing"
)

// TestJsonRawEqual covers jsonRawEqual's structural (not byte-for-byte) comparison, including
// the absent-vs-explicit-null normalization: a bot's matrix_levels/matrix_entry_level can be
// nil on one side (never set / column NULL) and the literal JSON "null" on the other (a PATCH
// body that explicitly serialized an empty field) without that being a real config change.
func TestJsonRawEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b json.RawMessage
		want bool
	}{
		{"nil vs literal null", nil, json.RawMessage("null"), true},
		{"empty vs nil", json.RawMessage(""), nil, true},
		{"empty vs literal null", json.RawMessage(""), json.RawMessage("null"), true},
		{"whitespace-only null vs nil", json.RawMessage("  null  "), nil, true},
		{"different matrix_levels", json.RawMessage(`[{"price_pct":1}]`), json.RawMessage(`[{"price_pct":2}]`), false},
		{"null vs non-null", nil, json.RawMessage(`[{"price_pct":1}]`), false},
		{"same object, different key order", json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{"b":2,"a":1}`), true},
		{"same object, whitespace differs", json.RawMessage(`{"a":1}`), json.RawMessage(`{  "a"  :  1  }`), true},
		{"identical arrays", json.RawMessage(`[1,2,3]`), json.RawMessage(`[1,2,3]`), true},
		{"array order differs (genuinely different)", json.RawMessage(`[1,2,3]`), json.RawMessage(`[3,2,1]`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jsonRawEqual(c.a, c.b); got != c.want {
				t.Errorf("jsonRawEqual(%q, %q) = %v, want %v", string(c.a), string(c.b), got, c.want)
			}
			// Symmetry check: comparison shouldn't depend on argument order.
			if got := jsonRawEqual(c.b, c.a); got != c.want {
				t.Errorf("jsonRawEqual(%q, %q) [swapped] = %v, want %v", string(c.b), string(c.a), got, c.want)
			}
		})
	}
}

// TestBotConfigStructuralFieldsChanged covers the higher-level decision that gates
// Engine.RestartCycle vs Engine.UpdateTPSL in syncBotStrategies.
func TestBotConfigStructuralFieldsChanged(t *testing.T) {
	t.Run("false when only non-structural fields differ (tp_pct, and matrix fields absent-vs-null)", func(t *testing.T) {
		tp1, tp2 := 2.0, 7.7
		oldCfg := botCfgJSON{
			GridLevels:       5,
			GridActive:       3,
			GridStepPct:      1.0,
			GridSizeUSDT:     100,
			Direction:        "long",
			EntryOrderType:   "limit",
			Leverage:         5,
			TPPct:            &tp1,
			MatrixLevels:     nil,
			MatrixEntryLevel: json.RawMessage("null"),
		}
		newCfg := oldCfg
		newCfg.TPPct = &tp2
		newCfg.MatrixLevels = json.RawMessage("null")
		newCfg.MatrixEntryLevel = nil

		if botConfigStructuralFieldsChanged(oldCfg, newCfg) {
			t.Errorf("expected false: only tp_pct changed and matrix fields are absent-vs-null equivalents")
		}
	})

	t.Run("true when GridLevels differs", func(t *testing.T) {
		oldCfg := botCfgJSON{GridLevels: 5, Direction: "long"}
		newCfg := botCfgJSON{GridLevels: 6, Direction: "long"}

		if !botConfigStructuralFieldsChanged(oldCfg, newCfg) {
			t.Errorf("expected true: GridLevels differs (5 vs 6)")
		}
	})

	t.Run("true when matrix_levels genuinely differs", func(t *testing.T) {
		oldCfg := botCfgJSON{MatrixLevels: json.RawMessage(`[{"price_pct":1}]`)}
		newCfg := botCfgJSON{MatrixLevels: json.RawMessage(`[{"price_pct":2}]`)}

		if !botConfigStructuralFieldsChanged(oldCfg, newCfg) {
			t.Errorf("expected true: matrix_levels content differs")
		}
	})

	t.Run("false when everything equal", func(t *testing.T) {
		cfg := botCfgJSON{
			GridLevels:     5,
			GridActive:     3,
			GridStepPct:    1.0,
			GridSizeUSDT:   100,
			Direction:      "long",
			EntryOrderType: "limit",
			Leverage:       5,
		}
		if botConfigStructuralFieldsChanged(cfg, cfg) {
			t.Errorf("expected false: identical configs")
		}
	})
}
