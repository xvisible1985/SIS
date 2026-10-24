//go:build integration

package main

import (
	"context"
	"encoding/json"
	"testing"

	"sis/pkg/trader"
)

// TestAlsFieldsRoundTrip covers all four SQL call sites that turn a hedge bot's
// strategy_config into strategies-table columns (see computeBotStrategyCols's doc
// comment): createBotStrategy's fresh-INSERT and reuse-stopped-row-UPDATE paths,
// applyBotConfigToStrategy (the bind endpoint), and syncBotStrategies (apply-to-active).
// Each must carry the nine ALS columns through unchanged (eight original + the
// 2026-10-23 als_exchange_orders addition).
func TestAlsFieldsRoundTrip(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "als_persist")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "als_persist")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	origBybit := bybitGetPublicInstrumentInfo
	t.Cleanup(func() { bybitGetPublicInstrumentInfo = origBybit })
	bybitGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{TickSize: 0.1, QtyStep: 0.001, MinQty: 0.001, MinNotionalValue: 5}, nil
	}

	initialStop := 2.5
	cfg := botCfgJSON{
		StrategyType:      "als",
		GridSizeUSDT:      20,
		HedgeMode:         true,
		AlsThresholds:     json.RawMessage(`[{"trigger_type":1,"trigger_pct":-10,"target_hedge_pct":30}]`),
		UnsafeHedgeMinPct: 5,
		UnsafeHedgeMaxPct: 80,
		StopMarketSizeK:   1.2,
		TPControlEnabled:  true,
		InitialStopBEPct:  &initialStop,
		AlsStopCondPct:    3,
		AlsStopReplacePct: 1,
		AlsExchangeOrders: true,
		SizeAsMain:        true,
	}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}

	type alsRow struct {
		thresholds        *string
		minPct, maxPct    float64
		stopK             float64
		tpControl         bool
		initialStop       *float64
		stopCond, stopRep float64
		exchangeOrders    bool
	}
	readAls := func(id string) alsRow {
		var r alsRow
		s.pool.QueryRow(ctx, `
			SELECT als_thresholds::text, unsafe_hedge_min_pct, unsafe_hedge_max_pct,
			       stop_market_size_k, tp_control_enabled, initial_stop_be_pct,
			       als_stop_cond_pct, als_stop_replace_pct, als_exchange_orders
			FROM strategies WHERE id=$1`, id,
		).Scan(&r.thresholds, &r.minPct, &r.maxPct, &r.stopK, &r.tpControl, &r.initialStop, &r.stopCond, &r.stopRep, &r.exchangeOrders)
		return r
	}
	assertRow := func(t *testing.T, r alsRow, label string) {
		t.Helper()
		if r.thresholds == nil || *r.thresholds == "" {
			t.Errorf("%s: als_thresholds is NULL, want the config jsonb", label)
		}
		if r.minPct != 5 || r.maxPct != 80 {
			t.Errorf("%s: unsafe_hedge_min/max_pct = %v/%v, want 5/80", label, r.minPct, r.maxPct)
		}
		if r.stopK != 1.2 {
			t.Errorf("%s: stop_market_size_k = %v, want 1.2", label, r.stopK)
		}
		if !r.tpControl {
			t.Errorf("%s: tp_control_enabled = false, want true", label)
		}
		if r.initialStop == nil || *r.initialStop != 2.5 {
			t.Errorf("%s: initial_stop_be_pct = %v, want 2.5", label, r.initialStop)
		}
		if r.stopCond != 3 || r.stopRep != 1 {
			t.Errorf("%s: als_stop_cond/replace_pct = %v/%v, want 3/1", label, r.stopCond, r.stopRep)
		}
		if !r.exchangeOrders {
			t.Errorf("%s: als_exchange_orders = false, want true", label)
		}
	}

	t.Run("createBotStrategy INSERT path", func(t *testing.T) {
		id, err := s.createBotStrategy(ctx, b, cfg, "ALSINSUSDT", "long", 0, "", nil)
		if err != nil {
			t.Fatalf("createBotStrategy: %v", err)
		}
		assertRow(t, readAls(id), "INSERT")
	})

	t.Run("createBotStrategy reuse-stopped-row UPDATE path", func(t *testing.T) {
		var stoppedID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, created_at)
			 VALUES ($1,$2,$3,'ALSREUUSDT','linear','long','als','stopped',NOW()-INTERVAL '1 hour') RETURNING id`,
			userID, accID, botID,
		).Scan(&stoppedID); err != nil {
			t.Fatalf("seed stopped: %v", err)
		}
		id, err := s.createBotStrategy(ctx, b, cfg, "ALSREUUSDT", "long", 0, "", nil)
		if err != nil {
			t.Fatalf("createBotStrategy reuse: %v", err)
		}
		if id != stoppedID {
			t.Fatalf("expected reuse of %s, got %s", stoppedID, id)
		}
		assertRow(t, readAls(id), "reuse-UPDATE")
	})

	t.Run("applyBotConfigToStrategy (bind endpoint)", func(t *testing.T) {
		var stratID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, symbol, category, direction, status)
			 VALUES ($1,$2,'ALSBINDUSDT','linear','long','stopped') RETURNING id`,
			userID, accID,
		).Scan(&stratID); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cols, err := s.computeBotStrategyCols(ctx, cfg, "ALSBINDUSDT", accID, 0)
		if err != nil {
			t.Fatalf("computeBotStrategyCols: %v", err)
		}
		if err := s.applyBotConfigToStrategy(ctx, s.pool, stratID, botID, cols, "", nil); err != nil {
			t.Fatalf("applyBotConfigToStrategy: %v", err)
		}
		assertRow(t, readAls(stratID), "bind-UPDATE")
	})

	t.Run("syncBotStrategies (apply to active)", func(t *testing.T) {
		var stratID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status)
			 VALUES ($1,$2,$3,'ALSSYNCUSDT','linear','long','als','active') RETURNING id`,
			userID, accID, botID,
		).Scan(&stratID); err != nil {
			t.Fatalf("seed active: %v", err)
		}
		cfgBytes, _ := json.Marshal(cfg)
		if _, err := s.pool.Exec(ctx, `UPDATE bots SET strategy_config=$1 WHERE id=$2`, cfgBytes, botID); err != nil {
			t.Fatalf("seed bot config: %v", err)
		}
		s.syncBotStrategies(ctx, botID, false)
		assertRow(t, readAls(stratID), "sync-UPDATE")
		// Switching a hedge bot's size to "as main" must reach an already-running strategy —
		// it used to be dropped from this UPDATE, so the live leg kept sizing from the fixed
		// grid_size_usdt (live DNAUSDT, 2026-10-24).
		var sizeAsMain bool
		s.pool.QueryRow(ctx, `SELECT size_as_main FROM strategies WHERE id=$1`, stratID).Scan(&sizeAsMain) //nolint:errcheck
		if !sizeAsMain {
			t.Errorf("sync-UPDATE: size_as_main = false, want true")
		}
	})
}
