//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestResolveSignalConfigs_CatalogEntryPassesThrough pins that a bare catalog reference
// (no CustomSignalID) is untouched — the vast majority case, and the one every existing
// strategy row already relies on.
func TestResolveSignalConfigs_CatalogEntryPassesThrough(t *testing.T) {
	pool := newTestPool(t)
	ar := &AccountRunner{pool: pool}

	out := ar.resolveSignalConfigs([]SignalConfig{
		{Name: "rsi-os", Params: map[string]any{"period": 14.0, "threshold": 30.0}},
	})
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].Name != "rsi-os" {
		t.Errorf("Name = %q, want rsi-os", out[0].Name)
	}
	if out[0].Params["period"] != 14.0 {
		t.Errorf("Params[period] = %v, want 14", out[0].Params["period"])
	}
}

// TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents is the core regression: a
// SignalConfig with CustomSignalID set must expand into that combo's saved component legs
// (each with its own saved params), in position order — the same shape
// resolveSignalConfigs in services/api-gateway/custom_signals_handler.go already produces
// for webhooks, now reused for strategies.
func TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	ar := &AccountRunner{pool: pool}

	var ownerID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"rsc-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })

	var comboID string
	if err := pool.QueryRow(ctx, `INSERT INTO custom_signals (owner_id, name) VALUES ($1,'RSI+MACD') RETURNING id`,
		ownerID).Scan(&comboID); err != nil {
		t.Fatalf("create custom signal: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO custom_signal_components (custom_signal_id, component_signal_id, params, position) VALUES
		 ($1,'rsi-os','{"period":14,"threshold":30}',0),
		 ($1,'macd-x','{"fast":12,"slow":26}',1)`, comboID); err != nil {
		t.Fatalf("create components: %v", err)
	}

	out := ar.resolveSignalConfigs([]SignalConfig{{CustomSignalID: comboID}})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2: %+v", len(out), out)
	}
	if out[0].Name != "rsi-os" || out[1].Name != "macd-x" {
		t.Errorf("names = [%s, %s], want [rsi-os, macd-x] in position order", out[0].Name, out[1].Name)
	}
	if out[0].Params["period"] != 14.0 {
		t.Errorf("out[0].Params[period] = %v, want 14", out[0].Params["period"])
	}
}

// TestResolveSignalConfigs_MixedCatalogAndCustom: a strategy can have both a bare catalog
// leg and a combo reference in the SAME SignalConfigs slot — resolveSignalConfigs must
// flatten both into one list (the engine's AND-combine treats the result identically to a
// strategy that had all N legs typed out by hand).
func TestResolveSignalConfigs_MixedCatalogAndCustom(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	ar := &AccountRunner{pool: pool}

	var ownerID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"rscmix-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })

	var comboID string
	if err := pool.QueryRow(ctx, `INSERT INTO custom_signals (owner_id, name) VALUES ($1,'combo') RETURNING id`,
		ownerID).Scan(&comboID); err != nil {
		t.Fatalf("create custom signal: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO custom_signal_components (custom_signal_id, component_signal_id, params, position) VALUES
		 ($1,'ema-x','{}',0)`, comboID); err != nil {
		t.Fatalf("create component: %v", err)
	}

	out := ar.resolveSignalConfigs([]SignalConfig{
		{Name: "vol-spike", Params: map[string]any{}},
		{CustomSignalID: comboID},
	})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2: %+v", len(out), out)
	}
	if out[0].Name != "vol-spike" || out[1].Name != "ema-x" {
		t.Errorf("names = [%s, %s], want [vol-spike, ema-x]", out[0].Name, out[1].Name)
	}
}

// TestResolveSignalConfigs_MissingCustomSignal_SkippedNotErrored: a combo that no longer
// exists (deleted) must not panic or block resolution of the strategy's other legs — it's
// simply dropped, same fail-open posture as an empty configs list elsewhere in this package.
func TestResolveSignalConfigs_MissingCustomSignal_SkippedNotErrored(t *testing.T) {
	pool := newTestPool(t)
	ar := &AccountRunner{pool: pool}

	out := ar.resolveSignalConfigs([]SignalConfig{
		{CustomSignalID: "00000000-0000-0000-0000-000000000000"},
		{Name: "rsi-os", Params: map[string]any{}},
	})
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 (missing combo dropped, catalog leg kept): %+v", len(out), out)
	}
	if out[0].Name != "rsi-os" {
		t.Errorf("Name = %q, want rsi-os", out[0].Name)
	}
}
