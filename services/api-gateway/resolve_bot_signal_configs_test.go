//go:build integration

package main

import (
	"context"
	"testing"
)

// TestResolveBotSignalConfigs_CatalogEntryPassesThrough pins that a bare catalog reference
// (no CustomSignalID) is untouched — unchanged from before resolveBotSignalConfigs existed.
func TestResolveBotSignalConfigs_CatalogEntryPassesThrough(t *testing.T) {
	s := newTestServer(t)

	out := s.resolveBotSignalConfigs(context.Background(), []botSignalRef{
		{Name: "rsi-os", Params: map[string]any{"period": 14.0, "threshold": 30.0}},
	})
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].Name != "rsi-os" {
		t.Errorf("Name = %q, want rsi-os", out[0].Name)
	}
}

// TestResolveBotSignalConfigs_ExpandsCustomSignalIntoComponents is the core regression for
// the live incident (2026-10-06/07, "ST_Bot v.1.0"/custom signal "123213"): a bot whose
// activation signal references a saved custom combo must have it expanded into the combo's
// real component configs — before this fix, the raw entry's Name (just the combo's display
// badge, e.g. "123213") was passed straight to signal.Build, which always failed ("unknown
// signal"), silently excluding the bot from every tick with zero error logging. Mirrors
// pkg/strategy's TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents — same table,
// same expansion shape, now reused for bot-level activation/signal configs too.
func TestResolveBotSignalConfigs_ExpandsCustomSignalIntoComponents(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	var ownerID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"rbsc-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })

	var comboID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO custom_signals (owner_id, name, badge) VALUES ($1,'123213','1232') RETURNING id`,
		ownerID).Scan(&comboID); err != nil {
		t.Fatalf("create custom signal: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO custom_signal_components (custom_signal_id, component_signal_id, params, position) VALUES
		 ($1,'st-flip','{"tf":"1h","atr":10,"dir":"лонг","mult":3}',0),
		 ($1,'vol-spike','{"tf":"15m","mult":2.5}',1)`, comboID); err != nil {
		t.Fatalf("create components: %v", err)
	}

	out := s.resolveBotSignalConfigs(ctx, []botSignalRef{
		{Name: "123213", Params: map[string]any{}, CustomSignalID: comboID},
	})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2: %+v", len(out), out)
	}
	if out[0].Name != "st-flip" || out[1].Name != "vol-spike" {
		t.Errorf("names = [%s, %s], want [st-flip, vol-spike] in position order", out[0].Name, out[1].Name)
	}
	if out[0].Params["atr"] != 10.0 {
		t.Errorf("out[0].Params[atr] = %v, want 10", out[0].Params["atr"])
	}
}

// TestResolveBotSignalConfigs_MixedCatalogAndCustom: a bot can have both a bare catalog leg
// (e.g. "leverage") and a combo reference among its activation signals — resolveBotSignalConfigs
// must flatten both into one AND-combined list.
func TestResolveBotSignalConfigs_MixedCatalogAndCustom(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	var ownerID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"rbscmix-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })

	var comboID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO custom_signals (owner_id, name, badge) VALUES ($1,'combo','cmb') RETURNING id`,
		ownerID).Scan(&comboID); err != nil {
		t.Fatalf("create custom signal: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO custom_signal_components (custom_signal_id, component_signal_id, params, position) VALUES
		 ($1,'ema-x','{}',0)`, comboID); err != nil {
		t.Fatalf("create component: %v", err)
	}

	out := s.resolveBotSignalConfigs(ctx, []botSignalRef{
		{Name: "leverage", Params: map[string]any{"min_leverage": 20.0}},
		{Name: "combo", Params: map[string]any{}, CustomSignalID: comboID},
	})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2: %+v", len(out), out)
	}
	if out[0].Name != "leverage" || out[1].Name != "ema-x" {
		t.Errorf("names = [%s, %s], want [leverage, ema-x]", out[0].Name, out[1].Name)
	}
}

// TestResolveBotSignalConfigs_MissingCustomSignal_SkippedNotErrored: a combo that no longer
// exists (deleted) must not block resolution of the bot's other activation legs — it's
// simply dropped, same fail-open posture as pkg/strategy's version.
func TestResolveBotSignalConfigs_MissingCustomSignal_SkippedNotErrored(t *testing.T) {
	s := newTestServer(t)

	out := s.resolveBotSignalConfigs(context.Background(), []botSignalRef{
		{Name: "123213", Params: map[string]any{}, CustomSignalID: "00000000-0000-0000-0000-000000000000"},
		{Name: "leverage", Params: map[string]any{"min_leverage": 20.0}},
	})
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 (missing combo dropped, catalog leg kept): %+v", len(out), out)
	}
	if out[0].Name != "leverage" {
		t.Errorf("Name = %q, want leverage", out[0].Name)
	}
}
