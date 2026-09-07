//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/crypto"
)

// testEncKey matches the literal already used across this codebase's integration/unit
// tests for AES-256 encrypt/decrypt round-trips (see pkg/crypto/aes_test.go,
// services/api-gateway/accounts_handler_test.go) — reusing the same value is
// intentional, not required for correctness, just consistent with established practice.
const testEncKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// TestLoadAccountInfo_ReadsExchangeColumn seeds one Bybit and one Binance
// exchange_accounts row and confirms loadAccountInfo reports each one's real exchange,
// not a hardcoded default — the concrete regression this task exists to prevent.
func TestLoadAccountInfo_ReadsExchangeColumn(t *testing.T) {
	pool := newTestPool(t) // already defined package-wide in trade_recorder_insert_test.go
	ctx := context.Background()

	var ownerID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"loadinfo-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })

	apiKeyEnc, err := crypto.Encrypt("k", testEncKey)
	if err != nil {
		t.Fatalf("encrypt api key: %v", err)
	}
	secretEnc, err := crypto.Encrypt("s", testEncKey)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}

	var bybitID, binanceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','bybit-acct',$2,$3) RETURNING id`,
		ownerID, apiKeyEnc, secretEnc).Scan(&bybitID); err != nil {
		t.Fatalf("create bybit account: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','binance-acct',$2,$3) RETURNING id`,
		ownerID, apiKeyEnc, secretEnc).Scan(&binanceID); err != nil {
		t.Fatalf("create binance account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE owner_id=$1", ownerID) })

	e := &Engine{pool: pool, encKey: testEncKey}

	bybitInfo, err := e.loadAccountInfo(ctx, bybitID)
	if err != nil {
		t.Fatalf("loadAccountInfo(bybit): %v", err)
	}
	if bybitInfo.exchange != "bybit" {
		t.Errorf("bybitInfo.exchange = %q, want bybit", bybitInfo.exchange)
	}

	binanceInfo, err := e.loadAccountInfo(ctx, binanceID)
	if err != nil {
		t.Fatalf("loadAccountInfo(binance): %v", err)
	}
	if binanceInfo.exchange != "binance" {
		t.Errorf("binanceInfo.exchange = %q, want binance", binanceInfo.exchange)
	}
}
