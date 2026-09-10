# services/api-gateway Read-Only Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Begin Plan #4c (migrating `services/api-gateway` off Bybit-specific `trader.X(ctx, creds, ...)` free functions onto the generic `trader.Exchange` interface) with its read-only slice: `FetchPositions`, `GetWalletBalance`, `FetchOpenOrdersForSymbolAll`, `FetchClosedPnlForSymbol`/`FetchRecentClosedPnl`. This mirrors `pkg/strategy`'s own phasing (#4b-1 read-only → #4b-2 account-setup → #4b-3 order-write, all merged) — #4c follows the same read-first sequencing: #4c-1 (this plan, read-only) → #4c-2 (leverage/position-mode) → #4c-3 (order placement/cancellation, highest stakes, likely split further).

**Architecture:** Unlike `pkg/strategy`, `services/api-gateway`'s handlers and background engines don't have a live `AccountRunner` to call `.Exchange()` on — most of them independently load an account's credentials from the DB per-call (via two existing shared helpers, `loadCreds` and `loadBotAccountCreds`, both in `services/api-gateway`) exactly the way `pkg/strategy/startup_reconcile.go` did before Plan #4a existed. Per the confirmed design: **every call site resolves its own `trader.Exchange` independently — no attempt to reuse a live `AccountRunner` from `pkg/strategy`'s `Engine`, except where the code already does so today** (the one exception, `trader_handler.go`'s `GetTradeStream`-based dual-path, is explicitly out of scope for this plan — it's an order-write site, deferred to #4c-3).

This plan:
1. Exports `pkg/strategy`'s already-existing `resolveExchange` as `ResolveExchange` (a trivial capitalization — the function itself is unchanged) so `services/api-gateway` can call it. `services/api-gateway` already imports `sis/pkg/strategy` in several files (`server.go`, `strategy_handler.go`, `closed_pnl_syncer.go`), so this is not a new dependency.
2. Adds two new sibling helper functions — `loadExchange` (mirrors `loadCreds`) and `loadBotAccountExchange` (mirrors `loadBotAccountCreds`) — that each do their own DB lookup (now also reading the `exchange` column) and return a resolved `trader.Exchange` instead of raw `trader.Credentials`. **The existing `loadCreds`/`loadBotAccountCreds` are left completely untouched** — several call sites in later plans (#4c-2/#4c-3) may still need raw credentials for something the `Exchange` interface doesn't cover yet, and this plan's own research found that every read-only call site's `creds` variable is used *only* for the one call being migrated, so introducing a second, parallel helper (accepting one small duplicate-query cost) is safer than threading a second return value through the existing helpers' 10+ other call sites, none of which this plan touches.

**Tech Stack:** Go. No new tests for the new helpers beyond what's needed to prove they resolve the right `Exchange` type — the underlying `BybitExchange` methods are already tested (Plan #1).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4a (`resolveExchange`, `trader.Exchange`), Plan #4b (all of `pkg/strategy` already migrated).

---

## Before you start

Read:
- `pkg/strategy/engine.go`'s `resolveExchange` function (added in Plan #4a) — this plan exports it, nothing else about it changes.
- `services/api-gateway/trader_handler.go:15-34` — `loadCreds`, the function this plan's `loadExchange` mirrors.
- `services/api-gateway/bot_engine.go:1437-1454` — `loadBotAccountCreds`, the function this plan's `loadBotAccountExchange` mirrors.
- `exchange_accounts.exchange` is `NOT NULL` with a `CHECK` constraint limiting it to `'bybit'`/`'binance'` (confirmed against the real DB schema during Plan #4a's research) — every real row has a valid value.

**In scope for this plan** (11 call sites total — 8 simple renames below, plus 3 Pattern-B sites in the next block — all verified against the live files while writing this plan):
- `services/api-gateway/accounts_handler.go` — 2 sites (`GetAccountBalance`'s `GetWalletBalance` call, `GetAccountPositions`'s `FetchPositions` call), both currently ad-hoc DB queries duplicating `loadCreds`'s shape rather than calling it — this plan replaces those inline blocks with calls to the new `loadExchange` helper, a simplification as a side effect.
- `services/api-gateway/bots_handler.go:1579` — `FetchPositions`, fed by `loadBotAccountCreds` at line 1573. Verified: `creds` used nowhere else in `scanHedgeBot`.
- `services/api-gateway/bot_engine.go:220` and `:1524` — `FetchPositions`, each fed by its own `loadBotAccountCreds` call immediately before. Verified: at `:220`, `creds` is scoped to a single `if creds, err := ...; err == nil { ... }` block and used nowhere else; at `:1524`, `creds` used nowhere else in `cleanupStoppedBotStrategies`'s remaining ~60 lines.
- `services/api-gateway/strategy_handler.go` — **3 sites sharing ONE capture**, not 2 as an earlier pass of this plan's research first found (a real gap in the original research, caught during self-review before dispatch — see Task 2 Step 6 below for why both `:1431` and `:1442` must be migrated together): `GetCycleAudit` calls `s.loadCreds` once (~line 1424) and then uses that one `creds` for BOTH `FetchOpenOrdersForSymbolAll` (~line 1431) AND `FetchPositions` (~line 1442) — verified `creds` is used nowhere else in the function beyond these two calls. Separately, `:1904` (`FetchPositions`, inside an `if/else if/else` chain in a different function, fed by `loadBotAccountCreds` scoped to that chain) is independent of the other two and verified unused elsewhere.

**Sites requiring the "Pattern B" dual-capture treatment (`creds` also feeds an out-of-scope order-write call later in the same function) — do NOT blindly rename these, see Task 2 Step 5:**
- `services/api-gateway/matrix_engine.go:55` (`processMatrixBot`) — `creds` (from `loadBotAccountCreds` at line 49) is ALSO passed into `s.checkMatrixPairedClose(...)` and `s.ensureMatrixStrategies(...)` later in the same function, both of which reach `PlaceOrder`/`CancelOrder` calls deferred to #4c-3.
- `services/api-gateway/hedge_engine.go:142` (`processHedgeBot`) — `creds` (from `loadBotAccountCreds` at line 135) is ALSO passed into `s.checkHedgeActivation(...)` later in the same function, which reaches order-write calls deferred to #4c-3.
- `services/api-gateway/hedge_engine.go:672` (`verifyAndClosePairedBot`) — `creds` (from `loadBotAccountCreds` at line 668) is ALSO passed into `s.checkMatrixPairedClose(...)` in the `"matrix"` branch of a `switch` later in the same function (the `"hedge"` branch calls `checkHedgeDeactivation` without `creds`, but the shared capture still can't be removed since the matrix branch needs it).

This was found by tracing each site's FULL enclosing function body (not just the ~10 lines immediately around the call), not just the immediate vicinity — a discipline this plan's Task 2 instructions now bake in explicitly. All 3 of these were originally going to be treated as simple renames in an earlier draft of this plan; that draft was wrong and has been corrected before any task was dispatched.

**Explicitly OUT of scope for this plan** (handled in a later task within #4c-1, or a separate sub-plan):
- `services/api-gateway/closed_pnl_syncer.go`'s 2 sites (`FetchRecentClosedPnl`, `FetchClosedPnlForSymbol`) — structurally different: `creds` is threaded as a function PARAMETER across `runAccount(ctx, a, creds)` → `syncAccount(ctx, a, creds)`, and this plan's research could not fully rule out `creds` (or the `closedPnlAccount` struct `a`) being used elsewhere in `syncAccount` for something out of scope (e.g. the still-deferred `RecordStrategyTrade`/`FeesAndFundingInRange` machinery from Plan #4b-1). This needs its own careful, dedicated task — see **Task 3** below, which handles it with the same "add a new parameter alongside the existing one, don't remove anything" discipline Plan #4b-1 used for `startup_reconcile.go`'s dual-cache.
- The 4 `trader.GetPublicInstrumentInfo` call sites (`bot_engine.go:1163`, `instrument_handler.go:25`, `leverage_cache.go:49`, `rescue_engine.go:309`) — a genuinely public, unauthenticated endpoint with no `Exchange`-interface equivalent (same reasoning already established and confirmed correct in Plan #4b-3b's review for `GetInstrumentInfo`/`GetPublicInstrumentInfo`). Never migrate these — there's nothing to migrate them to.
- `trader_handler.go`'s `SetLeverage`/`SwitchPositionMode` (2 sites) — sub-plan #4c-2.
- All `PlaceOrder`/`CancelOrder` call sites (`hedge_engine.go` ×5, `matrix_engine.go` ×1, `rescue_engine.go` ×1, `strategy_handler.go` ×1, `trader_handler.go` ×2 via the `GetTradeStream` dual-path) — sub-plan #4c-3, the highest-stakes remaining chunk of the whole migration.

---

### Task 1: Export `ResolveExchange`, add the two new helpers

**Files:**
- Modify: `pkg/strategy/engine.go`
- Modify: `services/api-gateway/trader_handler.go`
- Modify: `services/api-gateway/bot_engine.go`
- Create: `services/api-gateway/exchange_resolution_test.go`

- [x] **Step 1: Write the failing tests**

Create `services/api-gateway/exchange_resolution_test.go`:

```go
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
}
```

This matches the package's real, established integration-test convention exactly — verified against `services/api-gateway/accounts_handler_test.go` (`newTestServer(t)` + `s.encKey = testEncKey` + the `//go:build integration` tag) and `services/api-gateway/webhooks_handler_test.go` (`createWHUser(t, s, label)`, already defined package-wide and reusable here) while writing this plan, not guessed. `newTestServer` itself `t.Skip`s if TimescaleDB/Redis aren't reachable — no extra handling needed for that.

- [x] **Step 2: Run the test to verify it fails**

Run: `go test -tags=integration ./services/api-gateway/... -run TestLoadExchange_ResolvesBybitAndBinance -v`
Expected: FAIL to compile — `strategy.ResolveExchange`, `s.loadExchange`, `s.loadBotAccountExchange` don't exist yet.

- [x] **Step 3: Export `resolveExchange` as `ResolveExchange` in `pkg/strategy`**

In `pkg/strategy/engine.go`, rename the function (capitalize only — no other change):
```go
func resolveExchange(exchangeName string, creds trader.Credentials, ws *trader.TradeStream) trader.Exchange {
```
to:
```go
// ResolveExchange builds the trader.Exchange implementation for one account, based on
// its exchange_accounts.exchange column value. Exported so services/api-gateway (which
// has no live AccountRunner for most of its handlers) can resolve an Exchange the same
// way AccountRunner itself does, without duplicating this logic.
func ResolveExchange(exchangeName string, creds trader.Credentials, ws *trader.TradeStream) trader.Exchange {
```
Update every in-package call site (`resolveExchange(...)` → `ResolveExchange(...)`) — there should be exactly the ones already established in Plan #4a/#4b-1 (`newAccountRunnerWithExchange`, `startup_reconcile.go`'s two functions, and this function's own test file `exchange_resolution_test.go` in `pkg/strategy`). Search with `grep -rn "resolveExchange(" pkg/strategy/*.go` and rename every match.

- [x] **Step 4: Add `loadExchange` to `services/api-gateway/trader_handler.go`**

Add `"sis/pkg/strategy"` to the import block if not already present (check first — `strategy_handler.go` and `closed_pnl_syncer.go` in this same package already import it, but `trader_handler.go` itself may not).

Add this function right after `loadCreds`:

```go
// loadExchange looks up an exchange account by id (must be owned by userID), decrypts
// keys, and resolves the account's trader.Exchange (Bybit or Binance) — the Exchange-
// returning counterpart to loadCreds, added for Plan #4c's migration off Bybit-specific
// free functions. loadCreds itself is left unchanged; callers that still need raw
// Credentials for something the Exchange interface doesn't cover keep using it.
func (s *Server) loadExchange(r *http.Request, accountID, userID string) (trader.Exchange, error) {
	var apiKeyEnc, secretEnc, exchangeName string
	var whitelistedIPs []string
	err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, exchange, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		accountID, userID,
	).Scan(&apiKeyEnc, &secretEnc, &exchangeName, &whitelistedIPs)
	if err != nil {
		return nil, fmt.Errorf("account not found")
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs}
	return strategy.ResolveExchange(exchangeName, creds, trader.NewTradeStream(creds)), nil
}
```

- [x] **Step 5: Add `loadBotAccountExchange` to `services/api-gateway/bot_engine.go`**

Add `"sis/pkg/strategy"` to the import block if not already present.

Add this function right after `loadBotAccountCreds`:

```go
// loadBotAccountExchange resolves an exchange account's trader.Exchange (Bybit or
// Binance) — the Exchange-returning counterpart to loadBotAccountCreds, added for
// Plan #4c's migration. loadBotAccountCreds itself is left unchanged.
func (s *Server) loadBotAccountExchange(ctx context.Context, accountID string) (trader.Exchange, error) {
	var apiKeyEnc, secretEnc, exchangeName string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(ctx,
		`SELECT api_key_enc, secret_enc, exchange, whitelisted_ips FROM exchange_accounts WHERE id=$1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &exchangeName, &whitelistedIPs); err != nil {
		return nil, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		return nil, err
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		return nil, err
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs}
	return strategy.ResolveExchange(exchangeName, creds, trader.NewTradeStream(creds)), nil
}
```

- [x] **Step 6: Run the tests to verify they pass**

Run the same command as Step 2.
Expected: `PASS`.

- [x] **Step 7: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-plan baseline.
Run: `go test -tags=integration ./pkg/strategy/... ./services/api-gateway/...` — expected: same pre-existing failure counts already established this session (2 in `pkg/strategy`, 10 in `services/api-gateway`), plus your one new passing test.

- [x] **Step 8: Commit**

```bash
git add pkg/strategy/engine.go services/api-gateway/trader_handler.go services/api-gateway/bot_engine.go services/api-gateway/exchange_resolution_test.go
git commit -m "$(cat <<'EOF'
feat(api-gateway): export ResolveExchange, add loadExchange/loadBotAccountExchange

First step of Plan #4c (migrating services/api-gateway off Bybit-specific
free functions onto trader.Exchange — see
docs/superpowers/specs/2026-09-03-binance-live-trading-design.md). Exports
pkg/strategy's resolveExchange (added in Plan #4a) as ResolveExchange, and
adds two new sibling helpers alongside the existing loadCreds/
loadBotAccountCreds that resolve a trader.Exchange instead of raw
Credentials — the existing helpers are untouched, several later call sites
in #4c-2/#4c-3 still need raw creds for things Exchange doesn't cover yet.

Purely additive: nothing calls the new helpers outside this commit's own
test yet.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Migrate the 11 read-only call sites (8 simple renames + 3 Pattern-B)

**Files:**
- Modify: `services/api-gateway/accounts_handler.go`
- Modify: `services/api-gateway/bots_handler.go`
- Modify: `services/api-gateway/bot_engine.go`
- Modify: `services/api-gateway/hedge_engine.go`
- Modify: `services/api-gateway/matrix_engine.go`
- Modify: `services/api-gateway/strategy_handler.go`

- [x] **Step 1: `accounts_handler.go` — 2 sites, replace ad-hoc blocks with `loadExchange`**

`GetAccountBalance` currently reads (verified against the live file while writing this plan):
```go
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id, WhitelistedIPs: whitelistedIPs}
	equity, available, err := trader.GetWalletBalance(r.Context(), creds)
```
becomes:
```go
	ex, err := s.loadExchange(r, id, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	equity, available, err := ex.GetWalletBalance(r.Context())
```

`GetAccountPositions` (a separate handler further down the file, verified against the live file while writing this plan) currently reads:
```go
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id, WhitelistedIPs: whitelistedIPs}
	positions, err := trader.FetchPositions(r.Context(), creds)
```
becomes:
```go
	ex, err := s.loadExchange(r, id, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	positions, err := ex.FetchPositions(r.Context())
```
The rest of the handler (the `if err != nil { writeJSON(...); return }` / final `writeJSON(...)` success response) is unchanged.

- [x] **Step 2: `bots_handler.go:1579`**

```go
	rawPositions, err := trader.FetchPositions(ctx, creds)
```
Read the ~10 lines above this to find the `creds, err := s.loadBotAccountCreds(ctx, accountID)` call feeding it (confirmed at line 1573 during this plan's research — verify against the live file). Change that line to `ex, err := s.loadBotAccountExchange(ctx, accountID)`, and change the migrated call to:
```go
	rawPositions, err := ex.FetchPositions(ctx)
```

- [x] **Step 3: `bot_engine.go:220` and `:1524`**

Line ~219-220:
```go
	if creds, err := s.loadBotAccountCreds(ctx, b.accountID); err == nil {
		if positions, err := trader.FetchPositions(ctx, creds); err == nil {
```
becomes:
```go
	if ex, err := s.loadBotAccountExchange(ctx, b.accountID); err == nil {
		if positions, err := ex.FetchPositions(ctx); err == nil {
```

Line ~1518-1524 (a different function): read the ~6 lines above line 1524 to confirm the `creds, err := s.loadBotAccountCreds(ctx, b.accountID)` call feeding it, then apply the same rename (`loadBotAccountCreds`→`loadBotAccountExchange`, `creds`→`ex`, `trader.FetchPositions(ctx, creds)`→`ex.FetchPositions(ctx)`).

- [x] **Step 4: `hedge_engine.go:142` and `:672` — Pattern B (add `ex` alongside the existing `creds`, do NOT rename/remove `creds`)**

**Do NOT apply the simple rename here.** Both sites' `creds` variable is also passed downstream into a function that isn't in scope for this plan (order-write logic deferred to #4c-3). The fix is to add a second, independent `ex` capture via the new helper, leaving `creds` and every one of its existing uses completely untouched.

`processHedgeBot` (lines 134-147, verified against the live file):
```go
func (s *Server) processHedgeBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, watches map[string]hedgeWatchEntry, pairedWatches map[string]pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка получения позиций: %v", err), "error", "system")
		return
	}
```
becomes:
```go
func (s *Server) processHedgeBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, watches map[string]hedgeWatchEntry, pairedWatches map[string]pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}
	ex, err := s.loadBotAccountExchange(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := ex.FetchPositions(ctx)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка получения позиций: %v", err), "error", "system")
		return
	}
```
Line 159 (`s.checkHedgeActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap, watches)`) is unchanged — it keeps using `creds`, not `ex`. This does mean `loadBotAccountCreds`/`loadBotAccountExchange` both run per tick (two decrypts instead of one) — acceptable per this plan's explicit non-goal of touching `checkHedgeActivation`'s signature (that belongs to #4c-3, which will remove this duplication when it migrates the order-write path).

`verifyAndClosePairedBot` (lines 667-696, verified against the live file):
```go
func (s *Server) verifyAndClosePairedBot(ctx context.Context, entry pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, entry.accountID)
	if err != nil {
		return
	}
	rawPositions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		return
	}
	posMap, _ := buildHedgePosMap(rawPositions)
```
becomes:
```go
func (s *Server) verifyAndClosePairedBot(ctx context.Context, entry pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, entry.accountID)
	if err != nil {
		return
	}
	ex, err := s.loadBotAccountExchange(ctx, entry.accountID)
	if err != nil {
		return
	}
	rawPositions, err := ex.FetchPositions(ctx)
	if err != nil {
		return
	}
	posMap, _ := buildHedgePosMap(rawPositions)
```
Line 694 (`s.checkMatrixPairedClose(ctx, entry.botID, entry.accountID, cfg, creds, posMap)`, inside the `case "matrix":` branch) is unchanged — still uses `creds`.

- [x] **Step 5: `matrix_engine.go:55` — Pattern B (same treatment as Step 4)**

`processMatrixBot`'s `creds` (lines 48-69, verified against the live file) is ALSO used at line 66 (`checkMatrixPairedClose`) and line 68 (`ensureMatrixStrategies`), both out of scope for this plan. Same fix: add `ex` alongside, don't touch `creds`.

```go
func (s *Server) processMatrixBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, pairedWatches map[string]pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка получения позиций: %v", err), "error", "system")
		return
	}
```
becomes:
```go
func (s *Server) processMatrixBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, pairedWatches map[string]pairedCloseWatchEntry) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}
	ex, err := s.loadBotAccountExchange(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := ex.FetchPositions(ctx)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка получения позиций: %v", err), "error", "system")
		return
	}
```
Lines 66 and 68 (`checkMatrixPairedClose`/`ensureMatrixStrategies`, both still taking `creds`) are unchanged.

- [x] **Step 6: `strategy_handler.go` — `GetCycleAudit`'s TWO sites (`:1431` and `:1442`), migrated together, plus `:1904` separately**

`GetCycleAudit` loads `creds` once and uses it for exactly two calls, both in scope for this plan (confirmed via a full-function scan: `creds` appears only at the declaration, these two calls, and once inside an unrelated error-message string — no other use). Migrate both under a single rename.

Line ~1424-1442 (verified against the live file):
```go
	creds, err := s.loadCreds(r, accountID, userID)
	...
	// 5. Fetch live open orders.
	exchangeOrders, err := trader.FetchOpenOrdersForSymbolAll(r.Context(), creds, category, symbol)
	...
	positions, err := trader.FetchPositions(r.Context(), creds)
```
becomes:
```go
	ex, err := s.loadExchange(r, accountID, userID)
	...
	// 5. Fetch live open orders.
	exchangeOrders, err := ex.FetchOpenOrdersForSymbolAll(r.Context(), category, symbol)
	...
	positions, err := ex.FetchPositions(r.Context())
```
Read the full function body (lines 1343-1469) to confirm no other `creds` reference exists between these edits before committing to the rename — this plan's research already did this full-function scan and found none, but re-verify against the live file since line numbers may have drifted.

Line ~1902-1904 (a separate function, independent `creds` capture):
```go
	if creds, cErr := s.loadBotAccountCreds(ctx, a.accountID); cErr != nil {
		s.logBotEvent(ctx, req.BotID, fmt.Sprintf("%s — привязка: не удалось получить ключи для позиций (роли по направлению, без adopt): %v", a.symbol, cErr), "warn", "user")
	} else if positions, pErr := trader.FetchPositions(ctx, creds); pErr != nil {
```
becomes:
```go
	if ex, cErr := s.loadBotAccountExchange(ctx, a.accountID); cErr != nil {
		s.logBotEvent(ctx, req.BotID, fmt.Sprintf("%s — привязка: не удалось получить ключи для позиций (роли по направлению, без adopt): %v", a.symbol, cErr), "warn", "user")
	} else if positions, pErr := ex.FetchPositions(ctx); pErr != nil {
```

- [x] **Step 7: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-plan baseline.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same 10 pre-existing failures, no growth.
Run: `grep -rn "trader\.FetchPositions(ctx, creds)\|trader\.FetchPositions(r\.Context(), creds)\|trader\.FetchOpenOrdersForSymbolAll(r\.Context(), creds\|trader\.GetWalletBalance(r\.Context(), creds" services/api-gateway/*.go` — expected: **no matches** (confirms every targeted read-only call, across all 11 sites, migrated onto `ex`).
Run: `grep -n "creds" services/api-gateway/hedge_engine.go services/api-gateway/matrix_engine.go` and manually confirm `creds` is still present and unchanged in `processHedgeBot`/`verifyAndClosePairedBot`/`processMatrixBot` (feeding `checkHedgeActivation`/`checkMatrixPairedClose`/`ensureMatrixStrategies`) — these 4 downstream call sites deliberately still use `creds`, not `ex`; that's correct per Steps 4-5, not a leftover to clean up.
The first grep from the original draft of this step (`loadBotAccountCreds`/`loadCreds` usage) is deliberately dropped here — after Steps 4-5's Pattern-B fix, `loadBotAccountCreds` legitimately still appears in `hedge_engine.go`/`matrix_engine.go` (feeding the untouched downstream calls) as well as in the correctly-deferred leverage/position-mode/order-write sites, so a bare grep for it is no longer a useful signal; the two greps above are the precise checks that matter.

- [x] **Step 8: Commit**

```bash
git add services/api-gateway/accounts_handler.go services/api-gateway/bots_handler.go services/api-gateway/bot_engine.go services/api-gateway/hedge_engine.go services/api-gateway/matrix_engine.go services/api-gateway/strategy_handler.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate 11 read-only exchange call sites onto loadExchange/loadBotAccountExchange

Sub-plan #4c-1, Task 2. 8 sites (accounts_handler.go x2, bots_handler.go,
bot_engine.go x2, strategy_handler.go's GetCycleAudit x2 + one more
elsewhere) were simple renames: creds was used only for the migrated call
in scope, so loadCreds/loadBotAccountCreds -> loadExchange/
loadBotAccountExchange and creds -> ex directly. accounts_handler.go's 2
sites previously duplicated loadCreds' query inline; both now call the new
loadExchange helper instead, a simplification alongside the migration.

3 sites (hedge_engine.go's processHedgeBot and verifyAndClosePairedBot,
matrix_engine.go's processMatrixBot) needed a different treatment: their
creds variable is also threaded into checkHedgeActivation/
checkMatrixPairedClose/ensureMatrixStrategies, which reach order-write
calls deliberately deferred to #4c-3. For these, ex was added as a new,
independent capture alongside the existing, untouched creds and its
downstream uses -- not a rename. This means loadBotAccountCreds and
loadBotAccountExchange both run per tick at these 3 sites (an extra
decrypt) until #4c-3 migrates the downstream calls and removes creds
entirely.

Zero behavior change for Bybit accounts: BybitExchange.FetchPositions/
GetWalletBalance/FetchOpenOrdersForSymbolAll are one-line delegations to
the exact same free functions these call sites used directly before.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `closed_pnl_syncer.go` — the dual-parameter case

**Files:**
- Modify: `services/api-gateway/closed_pnl_syncer.go`

This file's `creds` is threaded as a function parameter (`runAccount(ctx, a, creds)` → `syncAccount(ctx, a, creds)`), not a simple per-call local. Follow the same discipline Plan #4b-1 used for `pkg/strategy/startup_reconcile.go`'s dual-cache: **add the resolved `Exchange` as a new, additional parameter alongside `creds` — do not remove or restructure `creds` itself**, since this plan's research could not fully rule out `creds` (or the `closedPnlAccount` struct) being used elsewhere in `syncAccount` for something out of scope (in particular, the still-deferred `RecordStrategyTrade`/`RecordMatrixTPProfit`/`FeesAndFundingInRange` machinery from Plan #4b-1, which may well be called somewhere later in this same function).

- [x] **Step 1: Read the whole file first**

Read `services/api-gateway/closed_pnl_syncer.go` in full before making any edit. Specifically determine:
1. Does the `closedPnlAccount` struct (used for `a` in `launch`/`runAccount`/`syncAccount`) already have an `exchange` field? (Almost certainly not — find wherever `closedPnlAccount` rows are loaded from the DB, likely a `SELECT ... FROM exchange_accounts` query building a `[]closedPnlAccount`, and confirm it doesn't currently select `exchange`.)
2. Is `creds` (the parameter threaded through `runAccount`/`syncAccount`) used anywhere in `syncAccount` OTHER than the 2 call sites this task migrates (`trader.FetchRecentClosedPnl` at ~line 145, `trader.FetchClosedPnlForSymbol` at ~line 248)? Read the entire function body — it's long (has multiple numbered "PATH"/step sections per this session's own earlier work on this file, e.g. the `reconcileMissingTradeHistory` watchdog). If `creds` is used elsewhere (e.g. passed into `RecordStrategyTrade`, `writeGapTradeHistory`, or similar), those other uses are OUT OF SCOPE and must be left completely alone — do not migrate them, do not remove `creds`.

- [x] **Step 2: Add `exchange` to the `closedPnlAccount` struct and its populating query**

Find the struct definition and the query that populates `[]closedPnlAccount` (read the file to find the exact current shape — this plan's research did not capture it in full). Add an `exchange string` (or `exchangeName string`, matching this file's existing naming style — check whether it uses `exchange` or `exchangeName` elsewhere first) field to the struct, and add `ea.exchange` (or the correct column reference, matching however the query currently aliases the `exchange_accounts` table) to the SELECT list and the corresponding `Scan` call.

- [x] **Step 3: Thread a resolved `Exchange` alongside `creds`**

In `runAccount` (~line 101-116), right after the existing `creds := trader.Credentials{...}` line, add:
```go
	ex := strategy.ResolveExchange(a.exchange, creds, trader.NewTradeStream(creds))
```
(adjust `a.exchange` to whatever field name Step 2 actually used). Add `"sis/pkg/strategy"` to this file's imports if not already present (check first — `closed_pnl_syncer.go` may already import it, per this plan's "Before you start" section noting it does for `RecordStrategyTrade`).

Change `runAccount`'s calls to `syncAccount` (both the initial call after the 30s offset sleep, and the one inside the ticker loop) from `s.syncAccount(ctx, a, creds)` to `s.syncAccount(ctx, a, creds, ex)`.

Change `syncAccount`'s signature from:
```go
func (s *ClosedPnlSyncer) syncAccount(ctx context.Context, a closedPnlAccount, creds trader.Credentials) {
```
to:
```go
func (s *ClosedPnlSyncer) syncAccount(ctx context.Context, a closedPnlAccount, creds trader.Credentials, ex trader.Exchange) {
```

- [x] **Step 4: Migrate the 2 call sites**

```go
			pnls, err := trader.FetchRecentClosedPnl(ctx, creds, category, since)
```
becomes:
```go
			pnls, err := ex.FetchRecentClosedPnl(ctx, category, since)
```

```go
		pnls, err := trader.FetchClosedPnlForSymbol(ctx, creds, symGaps[0].category, symbol, 50)
```
becomes:
```go
		pnls, err := ex.FetchClosedPnlForSymbol(ctx, symGaps[0].category, symbol, 50)
```

Leave every other use of `creds` in this file (if any were found in Step 1) completely untouched.

- [x] **Step 5: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same 10 pre-existing failures, no growth. If this file has any dedicated integration test coverage (check `grep -l closed_pnl_syncer services/api-gateway/*_test.go`), run it specifically and confirm it passes.
Run: `grep -n "trader\.FetchRecentClosedPnl(\|trader\.FetchClosedPnlForSymbol(" services/api-gateway/closed_pnl_syncer.go` — expected: no matches.

- [x] **Step 6: Commit**

```bash
git add services/api-gateway/closed_pnl_syncer.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate closed_pnl_syncer.go's closed-pnl fetches onto Exchange

Sub-plan #4c-1, Task 3 — the one structurally different read-only site:
creds is threaded as a function parameter across runAccount/syncAccount,
used for more than just the 2 migrated calls (RecordStrategyTrade and
friends, deferred to a later plan since they also touch pkg/strategy).
Adds a resolved trader.Exchange as a new parameter alongside creds rather
than replacing it, following the same dual-parameter discipline Plan #4b-1
used for pkg/strategy/startup_reconcile.go.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not touch `trader_handler.go`'s `SetLeverage`/`SwitchPositionMode` — sub-plan #4c-2.
- Does not touch any `PlaceOrder`/`CancelOrder` call site, including `trader_handler.go`'s `GetTradeStream`-based dual-path (which needs a new `Engine.GetExchange` accessor mirroring the existing `GetTradeStream`) — sub-plan #4c-3, the highest-stakes remaining chunk.
- Does not migrate `RecordStrategyTrade`/`RecordMatrixTPProfit` (still take raw `trader.Credentials`, called from both `pkg/strategy` and `services/api-gateway`) — deferred since Plan #4b-1, still deferred here; a future plan can revisit once both call sites are ready to change together.
- Does not touch any `trader.GetPublicInstrumentInfo`/`trader.GetInstrumentInfo` call — no `Exchange`-interface equivalent exists, and none is being added by this plan.
