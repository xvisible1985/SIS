//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestScanStrategy_ReadsAlsFields is the regression test for wiring the new ALS columns
// into scanStrategy — without it, every ALS field silently stays at its Go zero value
// regardless of what's actually stored in the DB.
func TestScanStrategy_ReadsAlsFields(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-scan-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	thresholdsJSON := `[{"trigger_type":1,"trigger_pct":-10,"target_hedge_pct":30},{"trigger_type":1,"trigger_pct":-30,"target_hedge_pct":50}]`
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type,
		  als_thresholds, unsafe_hedge_min_pct, unsafe_hedge_max_pct, stop_market_size_k,
		  tp_control_enabled, initial_stop_be_pct)
		VALUES ($1,$2,'ALSUSDT','long','als',$3::jsonb,5,40,1.2,true,2.5)
		RETURNING id`,
		ownerID, accID, thresholdsJSON,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var s Strategy
	row := pool.QueryRow(ctx, `
		SELECT id, owner_id, account_id, symbol, category, direction, status,
		       grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
		       tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
		       leverage, COALESCE(margin_type,'isolated'),
		       entry_order_type, COALESCE(steps::text,'[]'),
		       COALESCE(signal_configs::text,'[]'),
		       cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,''),
		       COALESCE(protected_build,false),
		       COALESCE(matrix_rebuild_on_sl,false),
		       COALESCE(matrix_rebuild_from_entry,false),
		       COALESCE(relative_slots,false),
		       COALESCE(strategy_type,'grid'),
		       COALESCE(size_as_main,false),
		       COALESCE(hedge_tp_suppressed,false),
		       COALESCE(hedge_sl_suppressed,false),
		       hedge_stopped_by::text,
		       COALESCE(adopt_position_data::text,''),
		       tp_signal_dir,
		       sl_signal_dir,
		       tp_signal_configs::text,
		       sl_signal_configs::text,
		       COALESCE(als_thresholds::text,'[]'),
		       COALESCE(unsafe_hedge_min_pct,0),
		       COALESCE(unsafe_hedge_max_pct,0),
		       COALESCE(stop_market_size_k,0),
		       COALESCE(tp_control_enabled,false),
		       initial_stop_be_pct,
		       COALESCE(als_stop_cond_pct,0),
		       COALESCE(als_stop_replace_pct,0)
		FROM strategies WHERE id=$1`, stratID)
	if err := scanStrategyRow(row, &s); err != nil {
		t.Fatalf("scanStrategyRow: %v", err)
	}

	if len(s.AlsThresholds) != 2 {
		t.Fatalf("AlsThresholds = %d rows, want 2", len(s.AlsThresholds))
	}
	if s.AlsThresholds[0].TriggerType != 1 || s.AlsThresholds[0].TriggerPct != -10 || s.AlsThresholds[0].TargetHedgePct != 30 {
		t.Errorf("AlsThresholds[0] = %+v, want {1 -10 30}", s.AlsThresholds[0])
	}
	if s.UnsafeHedgeMinPct != 5 || s.UnsafeHedgeMaxPct != 40 {
		t.Errorf("UnsafeHedgeMinPct/MaxPct = %v/%v, want 5/40", s.UnsafeHedgeMinPct, s.UnsafeHedgeMaxPct)
	}
	if s.StopMarketSizeK != 1.2 {
		t.Errorf("StopMarketSizeK = %v, want 1.2", s.StopMarketSizeK)
	}
	if !s.TPControlEnabled {
		t.Error("TPControlEnabled = false, want true")
	}
	if s.InitialStopBEPct == nil || *s.InitialStopBEPct != 2.5 {
		t.Errorf("InitialStopBEPct = %v, want 2.5", s.InitialStopBEPct)
	}
}
