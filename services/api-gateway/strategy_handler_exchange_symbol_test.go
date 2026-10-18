//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sis/pkg/trader"
)

// TestCreateStrategy_RejectsSymbolNotOnAccountExchange locks in the PLAYSOUTUSDT fix
// (see strategy_handler.go's CreateStrategy comment): a symbol that doesn't exist on
// the account's actual exchange must be rejected with 400 before anything is persisted,
// not silently saved and left to fail forever once the strategy engine tries to trade it.
func TestCreateStrategy_RejectsSymbolNotOnAccountExchange(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "symreject")
	accID := createTestAccountExchange(t, s, userID, "binance")

	origBinance := binanceGetPublicInstrumentInfo
	t.Cleanup(func() { binanceGetPublicInstrumentInfo = origBinance })
	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{}, errors.New("binance: no exchangeInfo entry for symbol PLAYSOUTUSDT")
	}

	body, _ := json.Marshal(map[string]any{
		"account_id": accID,
		"symbol":     "PLAYSOUTUSDT",
		"direction":  "long",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/strategies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateStrategy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CreateStrategy: got %d %s, want 400", rec.Code, rec.Body.String())
	}

	var count int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM strategies WHERE account_id=$1 AND symbol='PLAYSOUTUSDT'`, accID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("strategy was persisted despite the symbol lookup failing — count=%d, want 0", count)
	}
}

// TestCreateStrategy_AllowsSymbolThatExistsOnAccountExchange is the positive counterpart —
// a symbol the lookup confirms exists on the account's exchange must still be accepted.
func TestCreateStrategy_AllowsSymbolThatExistsOnAccountExchange(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "symallow")
	accID := createTestAccountExchange(t, s, userID, "binance")

	origBinance := binanceGetPublicInstrumentInfo
	t.Cleanup(func() { binanceGetPublicInstrumentInfo = origBinance })
	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001, MinNotionalValue: 20}, nil
	}

	body, _ := json.Marshal(map[string]any{
		"account_id": accID,
		"symbol":     "PLAYUSDT",
		"direction":  "long",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/strategies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateStrategy(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateStrategy: got %d %s, want 201", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(rec.Body).Decode(&created)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", created.ID) })
}

// TestUpdateStrategy_RejectsNewSymbolNotOnAccountExchange guards the same defense-in-depth
// check added to UpdateStrategy (see its comment) — changing a strategy's symbol to one
// that doesn't exist on its account's exchange must be rejected, same as creation.
func TestUpdateStrategy_RejectsNewSymbolNotOnAccountExchange(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "symupdreject")
	accID := createTestAccountExchange(t, s, userID, "binance")

	origBinance := binanceGetPublicInstrumentInfo
	t.Cleanup(func() { binanceGetPublicInstrumentInfo = origBinance })
	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001, MinNotionalValue: 20}, nil
	}

	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, category, direction)
		 VALUES ($1,$2,'PLAYUSDT','linear','long') RETURNING id`,
		userID, accID,
	).Scan(&id); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })

	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{}, errors.New("binance: no exchangeInfo entry for symbol PLAYSOUTUSDT")
	}

	body, _ := json.Marshal(map[string]any{
		"symbol":    "PLAYSOUTUSDT",
		"direction": "long",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": id})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("UpdateStrategy: got %d %s, want 400", rec.Code, rec.Body.String())
	}

	var gotSymbol string
	if err := s.pool.QueryRow(ctx, `SELECT symbol FROM strategies WHERE id=$1`, id).Scan(&gotSymbol); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if gotSymbol != "PLAYUSDT" {
		t.Errorf("symbol = %q, want unchanged %q (update must be rejected, not partially applied)", gotSymbol, "PLAYUSDT")
	}
}
