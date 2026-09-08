# AccountRunner Exchange Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every `AccountRunner` (`pkg/strategy/engine.go`) a resolved `trader.Exchange` — `BybitExchange` or `BinanceExchange` depending on the account's `exchange_accounts.exchange` column — as a struct field, ready for later plans to actually read and call. This is Plan #4a of the multi-exchange rollout's 3-part final phase (#4a infrastructure → #4b migrate `pkg/strategy` call sites → #4c migrate `services/api-gateway` call sites), per `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`.

**Architecture:** `AccountRunner` is constructed once per account by `newAccountRunner` (called from `Engine.getOrCreateRunner`, `pkg/strategy/engine.go:236`), fed by `loadAccountInfo`'s DB read. This plan threads the account's `exchange` string through that same path: `loadAccountInfo` reads one more column, `newAccountRunner` takes one more parameter, and a small pure function (`resolveExchange`) picks which `trader.Exchange` implementation to construct — `BybitExchange` wrapping the account's already-constructed `*trader.TradeStream` (zero new behavior, same object that already exists today), or `BinanceExchange` (REST-only, no WS order client needed, per Plan #2's design). **Nothing in this plan reads the new field** — every one of the 62 existing Bybit-specific call sites across `pkg/strategy`/`services/api-gateway` keeps working exactly as before. This plan is purely additive scaffolding; #4b/#4c are what actually switch call sites over.

**Tech Stack:** Go. One new pure-function unit test (no DB), one new `//go:build integration` test against the real TimescaleDB (matching this package's existing convention, e.g. `pkg/strategy/trade_recorder_fees_test.go`).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #1 (`trader.Exchange`/`BybitExchange`, merged), Plan #2 (`binance.BinanceExchange`, merged).

---

## Before you start

Read these first:
- `pkg/strategy/engine.go:669-847` — `accountInfo`, `loadAccountInfo`, the `AccountRunner` struct, and `newAccountRunner`. This plan edits all four.
- `pkg/strategy/engine.go:228-237` — `Engine.getOrCreateRunner`'s single call site for `newAccountRunner`. The only caller in the whole repo (verified via `grep -rn newAccountRunner`).
- `pkg/trader/exchange.go` — `NewBybitExchange(creds Credentials, ws wsOrderClient) *BybitExchange`. `*trader.TradeStream` already satisfies `wsOrderClient` (asserted at `pkg/trader/trade_ws.go:47`), so the account's existing `tradeStream` field can be passed straight through — no new WS connection, no behavior change to the live Bybit path.
- `pkg/trader/binance/exchange.go` — `NewBinanceExchange(creds trader.Credentials) *BinanceExchange`. Single-argument, REST-only — Binance has no separate low-latency order-placement WS the way Bybit does (see Plan #2's `PlaceOrder`==`PlaceOrderREST` design), so no WS client needs constructing for it.
- `pkg/strategy/engine_test.go:546` — existing tests construct `AccountRunner` via a bare struct literal (`&AccountRunner{...}`), not via `newAccountRunner` — this plan's new tests for `resolveExchange` don't need to touch that pattern, they test the new pure function directly.

**Real fact this plan relies on:** `exchange_accounts.exchange` is `NOT NULL` with a `CHECK (exchange = ANY (ARRAY['bybit','binance']))` constraint (confirmed via `\d exchange_accounts` against the real DB) — every real row has exactly one of these two values, never NULL, never anything else. `resolveExchange` still defaults unrecognized/empty strings to Bybit defensively (matching the same "unknown → default to the original exchange" pattern already used in `frontend/src/pages/AccountsPage.tsx`'s `EXCHANGES[id] ?? EXCHANGES.bybit`), even though the DB constraint means that branch should be unreachable in production.

---

### Task 1: `resolveExchange` + `AccountRunner.exchange` field

**Files:**
- Modify: `pkg/strategy/engine.go`
- Create: `pkg/strategy/exchange_resolution_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/strategy/exchange_resolution_test.go`:

```go
package strategy

import (
	"testing"

	"sis/pkg/trader"
	"sis/pkg/trader/binance"
)

func testCreds() trader.Credentials {
	return trader.Credentials{APIKey: "test-key", SecretKey: "test-secret"}
}

func TestResolveExchange_Bybit_ReturnsBybitExchangeWrappingTheGivenTradeStream(t *testing.T) {
	ts := trader.NewTradeStream(testCreds())
	ex := resolveExchange("bybit", testCreds(), ts)

	be, ok := ex.(*trader.BybitExchange)
	if !ok {
		t.Fatalf("resolveExchange(bybit, ...) returned %T, want *trader.BybitExchange", ex)
	}
	_ = be
}

func TestResolveExchange_Binance_ReturnsBinanceExchange(t *testing.T) {
	ts := trader.NewTradeStream(testCreds())
	ex := resolveExchange("binance", testCreds(), ts)

	if _, ok := ex.(*binance.BinanceExchange); !ok {
		t.Fatalf("resolveExchange(binance, ...) returned %T, want *binance.BinanceExchange", ex)
	}
}

func TestResolveExchange_UnknownDefaultsToBybit(t *testing.T) {
	ts := trader.NewTradeStream(testCreds())
	ex := resolveExchange("", testCreds(), ts)

	if _, ok := ex.(*trader.BybitExchange); !ok {
		t.Fatalf("resolveExchange(\"\", ...) returned %T, want *trader.BybitExchange (defensive default)", ex)
	}
}

func TestNewAccountRunner_SetsExchangeFromExchangeNameParam(t *testing.T) {
	cancel := func() {}
	ar := newAccountRunner("acct-1", "label", "owner", testCreds(), nil, nil, nil, cancel)
	if _, ok := ar.Exchange().(*trader.BybitExchange); !ok {
		t.Fatalf("newAccountRunner with default exchangeName: Exchange() = %T, want *trader.BybitExchange", ar.Exchange())
	}

	arBinance := newAccountRunnerWithExchange("acct-2", "label", "owner", testCreds(), nil, nil, nil, cancel, "binance")
	if _, ok := arBinance.Exchange().(*binance.BinanceExchange); !ok {
		t.Fatalf("newAccountRunnerWithExchange(..., \"binance\"): Exchange() = %T, want *binance.BinanceExchange", arBinance.Exchange())
	}
}
```

Note: the last test calls both `newAccountRunner` (unchanged 8-arg signature, kept for now) and a new `newAccountRunnerWithExchange` (9-arg, the real signature going forward) — Step 3 below explains why both exist transiently and how Step 4 removes the old one. Read Step 3-4 before implementing, don't skip ahead based on the test names alone.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/strategy/... -run 'TestResolveExchange|TestNewAccountRunner_SetsExchange' -v`
Expected: FAIL to compile — `resolveExchange`, `AccountRunner.Exchange()`, and `newAccountRunnerWithExchange` don't exist yet.

- [ ] **Step 3: Add the `resolveExchange` function and `AccountRunner.exchange` field**

In `pkg/strategy/engine.go`, add `"sis/pkg/trader/binance"` to the import block (alongside the existing `"sis/pkg/trader"` import).

Add this function near `newAccountRunner` (e.g. right before it):

```go
// resolveExchange builds the trader.Exchange implementation for one account, based on
// its exchange_accounts.exchange column value. ws backs Bybit's WS-order-placement path
// (BybitExchange wraps it directly, zero new behavior vs. today's tradeStream field);
// Binance has no separate order-placement WS yet (see Plan #2's design — PlaceOrder ==
// PlaceOrderREST for Binance), so BinanceExchange only needs creds. Unrecognized/empty
// exchangeName defaults to Bybit — defensive only, exchange_accounts.exchange is
// NOT NULL with a CHECK constraint limiting it to "bybit"/"binance", so this branch
// should be unreachable against real data.
func resolveExchange(exchangeName string, creds trader.Credentials, ws *trader.TradeStream) trader.Exchange {
	if exchangeName == "binance" {
		return binance.NewBinanceExchange(creds)
	}
	return trader.NewBybitExchange(creds, ws)
}
```

Add an `exchange trader.Exchange` field to the `AccountRunner` struct, with a doc comment, right after the existing `tradeStream *trader.TradeStream` field:

```go
	tradeStream   *trader.TradeStream
	// exchange is the resolved Exchange implementation for this account (Bybit or
	// Binance) — added by Plan #4a. Not yet read anywhere; existing code still calls
	// Bybit-specific free functions and tradeStream directly. Later plans (#4b, #4c)
	// migrate those call sites to use this instead.
	exchange      trader.Exchange
```

Add an accessor method right after `newAccountRunner`'s closing brace:

```go
// Exchange returns the resolved trader.Exchange for this account (Bybit or Binance).
func (ar *AccountRunner) Exchange() trader.Exchange {
	return ar.exchange
}
```

- [ ] **Step 4: Add `newAccountRunnerWithExchange`, keep `newAccountRunner` as a thin Bybit-defaulting wrapper**

This two-function shape exists so `TestNewAccountRunner_SetsExchangeFromExchangeNameParam` can prove both "the old call shape still defaults sanely" and "the new parameter actually changes what gets constructed" without a two-step commit sequence. Replace the existing `newAccountRunner` function with:

```go
func newAccountRunnerWithExchange(accountID, accountLabel, ownerUsername string, creds trader.Credentials, pool *pgxpool.Pool, signalEngine *signal.Engine, eng *Engine, cancel context.CancelFunc, exchangeName string) *AccountRunner {
	tradeStream := trader.NewTradeStream(creds)
	return &AccountRunner{
		accountID:           accountID,
		accountLabel:        accountLabel,
		ownerUsername:       ownerUsername,
		creds:               creds,
		pool:                pool,
		signalEngine:        signalEngine,
		engine:              eng,
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		tradeStream:         tradeStream,
		exchange:            resolveExchange(exchangeName, creds, tradeStream),
		cancel:              cancel,
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
}

// newAccountRunner is a thin wrapper defaulting to Bybit — kept only so this task's
// tests can compare "old call shape" against "new call shape" in one commit. Task 2
// removes it and switches the one real call site directly to
// newAccountRunnerWithExchange.
func newAccountRunner(accountID, accountLabel, ownerUsername string, creds trader.Credentials, pool *pgxpool.Pool, signalEngine *signal.Engine, eng *Engine, cancel context.CancelFunc) *AccountRunner {
	return newAccountRunnerWithExchange(accountID, accountLabel, ownerUsername, creds, pool, signalEngine, eng, cancel, "bybit")
}
```

Note this makes `Engine.getOrCreateRunner`'s existing call site (`pkg/strategy/engine.go:236`, still calling the old 8-arg `newAccountRunner`) continue to compile and behave identically — it just now ALSO gets an `exchange` field set to a `*trader.BybitExchange`, which nothing reads yet. Task 2 switches this call site to `newAccountRunnerWithExchange` with the real `info.exchange` value and deletes the transitional `newAccountRunner` wrapper.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./pkg/strategy/... -run 'TestResolveExchange|TestNewAccountRunner_SetsExchange' -v`
Expected: `PASS` for all 4 tests.

- [ ] **Step 6: Run the full `pkg/strategy` test suite**

Run: `go test ./pkg/strategy/... -v 2>&1 | tail -80`
Expected: everything passes, no regressions — this task only adds a field and two new functions; the one existing call site (`newAccountRunner` at `engine.go:236`) is untouched and behaves identically.

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/exchange_resolution_test.go
git commit -m "$(cat <<'EOF'
feat(strategy): add resolveExchange, give AccountRunner a resolved Exchange field

First step of Plan #4 (migrating pkg/strategy/services/api-gateway off
Bybit-specific free functions onto the generic trader.Exchange interface —
see docs/superpowers/specs/2026-09-03-binance-live-trading-design.md).

Purely additive: AccountRunner.exchange is set but not yet read anywhere.
BybitExchange wraps the account's existing tradeStream (zero new WS
connections, zero behavior change to the live Bybit path); BinanceExchange
is REST-only per Plan #2's design.

newAccountRunner is kept as a thin Bybit-defaulting wrapper around the new
newAccountRunnerWithExchange for this commit only — Task 2 wires the real
per-account exchange value through and removes it.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Thread the real exchange value from the DB through to `AccountRunner`

**Files:**
- Modify: `pkg/strategy/engine.go`
- Create: `pkg/strategy/load_account_info_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/strategy/load_account_info_test.go`:

```go
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
```

This follows `pkg/strategy/trade_recorder_fees_test.go`'s exact established pattern for seeding a user + exchange_accounts row in this package's integration tests (`newTestPool(t)`, `INSERT ... RETURNING id`, `t.Cleanup` for teardown) — verified against that file directly while writing this plan, not guessed. `newTestPool` is already defined once in `pkg/strategy/trade_recorder_insert_test.go` and shared package-wide by Go's same-package test-file linking; do not redefine it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags=integration ./pkg/strategy/... -run TestLoadAccountInfo_ReadsExchangeColumn -v`
Expected: FAIL to compile — `accountInfo.exchange` doesn't exist yet, `loadAccountInfo`'s SQL doesn't select it.

- [ ] **Step 3: Add `exchange` to `accountInfo` and `loadAccountInfo`'s query**

In `pkg/strategy/engine.go`, change:
```go
type accountInfo struct {
	creds         trader.Credentials
	accountLabel  string
	ownerUsername string
}
```
to:
```go
type accountInfo struct {
	creds         trader.Credentials
	exchange      string
	accountLabel  string
	ownerUsername string
}
```

In `loadAccountInfo`, change the query and scan from:
```go
	var apiKeyEnc, secretEnc, label string
	var username *string
	var whitelistedIPs []string
	if err := e.pool.QueryRow(ctx,
		`SELECT ea.api_key_enc, ea.secret_enc, ea.label, ea.whitelisted_ips,
		        NULLIF(COALESCE(u.username, ''), '')
		 FROM exchange_accounts ea
		 JOIN users u ON u.id = ea.owner_id
		 WHERE ea.id = $1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &label, &whitelistedIPs, &username); err != nil {
		return accountInfo{}, err
	}
```
to:
```go
	var apiKeyEnc, secretEnc, exchangeName, label string
	var username *string
	var whitelistedIPs []string
	if err := e.pool.QueryRow(ctx,
		`SELECT ea.api_key_enc, ea.secret_enc, ea.exchange, ea.label, ea.whitelisted_ips,
		        NULLIF(COALESCE(u.username, ''), '')
		 FROM exchange_accounts ea
		 JOIN users u ON u.id = ea.owner_id
		 WHERE ea.id = $1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &exchangeName, &label, &whitelistedIPs, &username); err != nil {
		return accountInfo{}, err
	}
```

A few lines below, `loadAccountInfo`'s final return currently reads exactly:
```go
	return accountInfo{
		creds:         trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs},
		accountLabel:  label,
		ownerUsername: un,
	}, nil
}
```
Change it to:
```go
	return accountInfo{
		creds:         trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs},
		exchange:      exchangeName,
		accountLabel:  label,
		ownerUsername: un,
	}, nil
}
```

- [ ] **Step 4: Switch the one real call site, remove the transitional wrapper**

In `pkg/strategy/engine.go:236` (verify the exact current line — it may have shifted slightly from Task 1's edits), change:
```go
		runner = newAccountRunner(s.AccountID, info.accountLabel, info.ownerUsername, info.creds, e.pool, e.signalEngine, e, cancel)
```
to:
```go
		runner = newAccountRunnerWithExchange(s.AccountID, info.accountLabel, info.ownerUsername, info.creds, e.pool, e.signalEngine, e, cancel, info.exchange)
```

Delete the transitional `newAccountRunner` wrapper function added in Task 1 (the thin one that called `newAccountRunnerWithExchange(..., "bybit")`) — it has no callers left once this call site and this task's test are updated.

- [ ] **Step 5: Update Task 1's test to match**

`TestNewAccountRunner_SetsExchangeFromExchangeNameParam` (in `exchange_resolution_test.go`, from Task 1) calls the now-deleted `newAccountRunner`. Update it to call `newAccountRunnerWithExchange` directly for both cases instead:

```go
func TestNewAccountRunner_SetsExchangeFromExchangeNameParam(t *testing.T) {
	cancel := func() {}
	arBybit := newAccountRunnerWithExchange("acct-1", "label", "owner", testCreds(), nil, nil, nil, cancel, "bybit")
	if _, ok := arBybit.Exchange().(*trader.BybitExchange); !ok {
		t.Fatalf("newAccountRunnerWithExchange(..., \"bybit\"): Exchange() = %T, want *trader.BybitExchange", arBybit.Exchange())
	}

	arBinance := newAccountRunnerWithExchange("acct-2", "label", "owner", testCreds(), nil, nil, nil, cancel, "binance")
	if _, ok := arBinance.Exchange().(*binance.BinanceExchange); !ok {
		t.Fatalf("newAccountRunnerWithExchange(..., \"binance\"): Exchange() = %T, want *binance.BinanceExchange", arBinance.Exchange())
	}
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./pkg/strategy/... -run 'TestResolveExchange|TestNewAccountRunner_SetsExchange' -v`
Expected: `PASS` for all 3 tests (the 2 `resolveExchange` tests from Task 1 are untouched by this task and should still pass; `TestNewAccountRunner_SetsExchangeFromExchangeNameParam` now tests the direct call).

Run: `go test -tags=integration ./pkg/strategy/... -run TestLoadAccountInfo_ReadsExchangeColumn -v`
Expected: `PASS` — needs a real DB connection (this package's established integration-test convention; same as `trade_recorder_fees_test.go`).

- [ ] **Step 7: Run the full `pkg/strategy` and whole-repo test suites**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: every package passes, identical to the pre-Task-1 baseline.
Run: `go test -tags=integration ./pkg/strategy/... ./services/api-gateway/...` — expected: same failure count as this session's already-established baseline for this branch (10 pre-existing unrelated failures in `services/api-gateway`, 2 pre-existing unrelated failures in `pkg/strategy` — both already confirmed environmental/stale, unrelated to any multi-exchange work; do not attempt to fix them here, just confirm the count hasn't grown).

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/exchange_resolution_test.go pkg/strategy/load_account_info_test.go
git commit -m "$(cat <<'EOF'
feat(strategy): thread the account's real exchange into AccountRunner

loadAccountInfo now reads exchange_accounts.exchange; the one real call
site (Engine.getOrCreateRunner) passes it through newAccountRunnerWithExchange
instead of the Task-1 Bybit-hardcoded transitional wrapper, which is removed.

Still purely additive for behavior: every account in production today is
"bybit", so AccountRunner.exchange resolves to the exact same *BybitExchange
wrapping the exact same tradeStream as before — nothing reads this field yet.
Migrating actual call sites is Plan #4b (pkg/strategy) and #4c
(services/api-gateway).

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not migrate a single one of the 62 existing Bybit-specific call sites (`trader.FetchPositions`, `trader.PlaceOrder`, `ar.tradeStream.*`, etc.) across `pkg/strategy`/`services/api-gateway` — every one of them keeps calling exactly what it calls today. `AccountRunner.exchange`/`Exchange()` exist but are unread outside this plan's own tests. That's Plan #4b (`pkg/strategy`) and #4c (`services/api-gateway`).
- Does not give `services/api-gateway`'s HTTP handlers or background engines (which don't hold an `AccountRunner` reference) any way to resolve an account's `Exchange` — they'll need their own resolution path, likely sharing `resolveExchange`'s logic, designed in Plan #4c once the shape of that package's needs is clearer from #4b's experience.
- Does not change `AccountRunner.tradeStream`'s lifecycle, `.Run(ctx)` call site, or any WS connection behavior — `BybitExchange` wraps the exact same `*TradeStream` object that already exists and already gets `.Run()` called on it elsewhere; this plan adds a reference to it, not a new instance.
- Does not add Binance-specific account setup (hedge-mode/leverage on connect) — that's Plan #5 per the original spec's phasing, and depends on #4b/#4c actually routing order placement through `Exchange` first.
