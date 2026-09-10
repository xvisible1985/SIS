//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"sis/pkg/crypto"
	"sis/pkg/trader"
	"sis/pkg/trader/binance"
)

// TestLoadExchange_ResolvesBybitAndBinance seeds a Bybit and a Binance
// exchange_accounts row and confirms loadExchange/loadBotAccountExchange resolve
// each to the right trader.Exchange implementation.
func TestLoadExchange_ResolvesBybitAndBinance(t *testing.T) {
	s := newTestServer(t) // this package's established integration-test Server builder
	s.encKey = testEncKey
	ctx := context.Background()
	ownerID := createWHUser(t, s, "loadexchange-"+t.Name())

	apiKeyEnc, _ := crypto.Encrypt("k", testEncKey)
	secretEnc, _ := crypto.Encrypt("s", testEncKey)

	var bybitID, binanceID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x',$2,$3) RETURNING id`,
		ownerID, apiKeyEnc, secretEnc).Scan(&bybitID); err != nil {
		t.Fatalf("create bybit account: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x',$2,$3) RETURNING id`,
		ownerID, apiKeyEnc, secretEnc).Scan(&binanceID); err != nil {
		t.Fatalf("create binance account: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE owner_id=$1", ownerID) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	exBybit, err := s.loadExchange(req, bybitID, ownerID)
	if err != nil {
		t.Fatalf("loadExchange(bybit): %v", err)
	}
	if _, ok := exBybit.(*trader.BybitExchange); !ok {
		t.Errorf("loadExchange(bybit) = %T, want *trader.BybitExchange", exBybit)
	}

	exBinance, err := s.loadExchange(req, binanceID, ownerID)
	if err != nil {
		t.Fatalf("loadExchange(binance): %v", err)
	}
	if _, ok := exBinance.(*binance.BinanceExchange); !ok {
		t.Errorf("loadExchange(binance) = %T, want *binance.BinanceExchange", exBinance)
	}

	exBotBybit, err := s.loadBotAccountExchange(ctx, bybitID)
	if err != nil {
		t.Fatalf("loadBotAccountExchange(bybit): %v", err)
	}
	if _, ok := exBotBybit.(*trader.BybitExchange); !ok {
		t.Errorf("loadBotAccountExchange(bybit) = %T, want *trader.BybitExchange", exBotBybit)
	}

	exBotBinance, err := s.loadBotAccountExchange(ctx, binanceID)
	if err != nil {
		t.Fatalf("loadBotAccountExchange(binance): %v", err)
	}
	if _, ok := exBotBinance.(*binance.BinanceExchange); !ok {
		t.Errorf("loadBotAccountExchange(binance) = %T, want *binance.BinanceExchange", exBotBinance)
	}
}
