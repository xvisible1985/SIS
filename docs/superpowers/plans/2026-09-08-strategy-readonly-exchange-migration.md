# pkg/strategy Read-Only Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate every **read-only** Bybit-specific free-function call in `pkg/strategy` (`trader.FetchPositions`, `trader.FetchOpenOrdersForSymbolAll`, `trader.FetchMarkPrice`) onto the account's resolved `trader.Exchange` (via `AccountRunner.Exchange()`, added in Plan #4a). This is sub-plan #4b-1 of the multi-exchange rollout's largest phase — #4b was split into #4b-1 (read-only, this plan), #4b-2 (account setup: `SetLeverage`/`SwitchPositionMode`), #4b-3 (order placement/cancellation — the highest-stakes chunk, real money, split further by file) after discovering the true scope (82 call sites across 8 files, not the originally estimated 36) made one giant plan too risky for the only exchange actually in live production use.

**Architecture:** No new types or interfaces — `trader.Exchange` (Plan #1) and `AccountRunner.Exchange()` (Plan #4a) already exist and already resolve to the exact right implementation (`BybitExchange` today, `BinanceExchange` once an account is ever created with `exchange='binance'`). This plan is pure call-site substitution: every `trader.FetchXxx(ctx, someCreds, ...)` becomes `someExchange.Xxx(ctx, ...)` — dropping the now-unneeded `creds`/`category` duplication (an `Exchange` already has its own credentials baked in at construction) and, for mark price specifically, renaming `FetchMarkPrice` → `GetMarkPrice` (the interface method's actual name — see `pkg/trader/exchange.go`). **Zero behavior change for Bybit accounts**: `BybitExchange.FetchPositions`/`FetchOpenOrdersForSymbolAll`/`GetMarkPrice` (see `pkg/trader/exchange.go:83-113`) are direct one-line delegations to the exact same free functions this plan removes the direct calls to.

**Tech Stack:** Go. No new tests needed for the substitution itself (the underlying `BybitExchange` methods are already tested — Plan #1's `pkg/trader/exchange_test.go` — and this plan doesn't change their behavior, only who calls them). Existing `pkg/strategy` tests (unit + `//go:build integration`) are the regression net; this plan's job is to not break any of them.

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4a (`AccountRunner.Exchange()`, merged).

---

## Before you start

Read these first:
- `pkg/trader/exchange.go` — the `Exchange` interface. Note the exact method signatures this plan's call sites must match: `FetchPositions(ctx context.Context) ([]Position, error)` (no `creds`, no `category` — Bybit's REST endpoint for positions doesn't take a category param either, it's baked into `BybitExchange`'s own free-function delegation), `FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]Order, error)`, `GetMarkPrice(ctx context.Context, category, symbol string) (float64, error)` — **the interface method is named `GetMarkPrice`, not `FetchMarkPrice`** (the free function you're replacing calls is `trader.FetchMarkPrice`; do not write `.FetchMarkPrice(...)` on an `Exchange` value, it doesn't exist and won't compile).
- `pkg/strategy/engine.go` — `AccountRunner.Exchange() trader.Exchange` (the accessor added in Plan #4a) and `AccountRunner.exchange trader.Exchange` (the field itself, usable directly since `engine.go` is in the same package as every file this plan touches).
- `pkg/strategy/cycle.go:21-26` — `StrategyRunner.runner *AccountRunner`, so every `sr.*` method has `sr.runner.Exchange()` available.

**Explicitly OUT of scope for this plan** (do not touch these, even though they match the same free-function names):
- `pkg/strategy/trade_recorder.go:54` (inside `RecordMatrixTPProfit`) and `:395` (inside `RecordStrategyTrade`) — both call `trader.FetchClosedPnlForSymbol(ctx, creds, ...)`. **Deliberately deferred**: both functions are exported and called from `services/api-gateway` too (`RecordStrategyTrade` from `services/api-gateway/closed_pnl_syncer.go:497` and `pkg/strategy/startup_reconcile.go:193`), so migrating their signature to take `trader.Exchange` instead of `trader.Credentials` reaches across the #4b/#4c package boundary — that crossover belongs with #4c (`services/api-gateway` migration), not this pkg/strategy-internal plan. Do not "helpfully" migrate these while you're in the file.
- All `tradeStream.PlaceOrder`/`PlaceOrderBatch`/`CancelOrder`/`CancelOrderBatch` calls, and all `trader.SetLeverage`/`trader.SwitchPositionMode` calls — different sub-plans (#4b-3 and #4b-2 respectively), much higher risk (real order placement/cancellation), not touched here.

---

### Task 1: `pkg/strategy/engine.go` and `pkg/strategy/cycle.go`

**Files:**
- Modify: `pkg/strategy/engine.go`
- Modify: `pkg/strategy/cycle.go`

This task covers 1 site in `engine.go` and 20 in `cycle.go`. Every site falls into one of three patterns:

**Pattern A (direct, inline)** — the call already reads `sr.runner.creds` (or `ar.creds`) directly as an argument, with no intermediate variable. Just replace the whole call expression.

**Pattern B (closure-captured)** — an earlier line in the same function does `creds := sr.runner.creds` (to snapshot it before a `go func(){...}()` closure or before releasing a mutex), and the read-only call inside that closure uses the captured `creds` var. **Before editing a Pattern B site**, read the ENTIRE enclosing function (not just the matched line) to check whether that same captured `creds` variable is ALSO used anywhere else in that function for something besides this read-only call (in particular, for a `tradeStream`/order-write call, which is out of scope for this plan). Two cases:
  - If `creds` is used ONLY for the read-only call this task is migrating: change the capture line itself (`creds := sr.runner.creds` → `ex := sr.runner.Exchange()`) and update the call site to use `ex`.
  - If `creds` is ALSO used for something else in the same function (a write call, out of scope): **do not touch or remove the existing `creds := sr.runner.creds` line** — add a new, separate line right after it capturing `ex := sr.runner.Exchange()`, and use `ex` only at the read-only call site this task is migrating. Leave every other use of `creds` untouched.

**Pattern C (AccountRunner-internal)** — `engine.go:1410`'s call is inside an `AccountRunner` method itself (not a `StrategyRunner`), so it accesses `ar.exchange` directly (the field, not through an accessor — same struct).

For every site below, the **exact current line text** is quoted (verified against the live file while writing this plan) so you can locate it precisely even if line numbers have drifted slightly from other work landing on `main` since this plan was written — search for the quoted text, not just the line number.

- [ ] **Step 1: `pkg/strategy/engine.go` — Pattern C**

Line ~1410:
```go
	positions, err := trader.FetchPositions(ctx, ar.creds)
```
becomes:
```go
	positions, err := ar.exchange.FetchPositions(ctx)
```

- [ ] **Step 2: `pkg/strategy/cycle.go` — Pattern A sites (17 of the 20)**

Each of these is a direct inline call — read the few lines around each to confirm it's Pattern A (no earlier `creds := sr.runner.creds` capture feeding it) before editing, then apply the substitution rule. Exact current lines:

```go
// line ~362
resumePrice, _ := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
resumePrice, _ := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~672
positions, err := trader.FetchPositions(ctx, sr.runner.creds)
// becomes:
positions, err := sr.runner.Exchange().FetchPositions(ctx)

// line ~762 — identical shape to ~672, same substitution
positions, err := trader.FetchPositions(ctx, sr.runner.creds)
// becomes:
positions, err := sr.runner.Exchange().FetchPositions(ctx)

// line ~825 — identical shape to ~672/~762, same substitution
positions, err := trader.FetchPositions(ctx, sr.runner.creds)
// becomes:
positions, err := sr.runner.Exchange().FetchPositions(ctx)

// line ~906
p, e := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
p, e := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~910 — note: 4 lines below ~906, likely same function; if both ~906 and ~910
// are in the same function you may call sr.runner.Exchange() twice (once per line) or
// hoist it into a local var used by both — either is fine, prefer whichever reads
// cleaner in context, this is a style choice not a correctness one.
o, e := trader.FetchOpenOrdersForSymbolAll(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
o, e := sr.runner.Exchange().FetchOpenOrdersForSymbolAll(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~1180
openOrders, err := trader.FetchOpenOrdersForSymbolAll(ctx, sr.runner.creds, category, symbol)
// becomes:
openOrders, err := sr.runner.Exchange().FetchOpenOrdersForSymbolAll(ctx, category, symbol)

// line ~1302 — identical shape to ~1180, same substitution
openOrders, err := trader.FetchOpenOrdersForSymbolAll(ctx, sr.runner.creds, category, symbol)
// becomes:
openOrders, err := sr.runner.Exchange().FetchOpenOrdersForSymbolAll(ctx, category, symbol)

// line ~1708
price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~2403 — identical shape to ~672/~762/~825, same substitution
positions, err := trader.FetchPositions(ctx, sr.runner.creds)
// becomes:
positions, err := sr.runner.Exchange().FetchPositions(ctx)

// line ~3783 — identical shape to ~1708
price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~4034
price, ferr := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, ferr := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~4473 — identical shape, same substitution
positions, err := trader.FetchPositions(ctx, sr.runner.creds)
// becomes:
positions, err := sr.runner.Exchange().FetchPositions(ctx)

// line ~4820 — identical shape, same substitution
positions, err := trader.FetchPositions(ctx, sr.runner.creds)
// becomes:
positions, err := sr.runner.Exchange().FetchPositions(ctx)

// line ~4874
currentPrice, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
currentPrice, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~5507
price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~5750
markPrice, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
markPrice, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)
```

- [ ] **Step 3: `pkg/strategy/cycle.go` — Pattern B sites (3 of the 20)**

**Site 1 — around line 2992-2997.** The enclosing block currently reads (inside `closeCycle`'s grid safety-sweep, verified against the live file):
```go
			prefix := "SIS_STR-" + stratID8 + "-"
			symbol := sr.strategy.Symbol
			category := sr.strategy.Category
			creds := sr.runner.creds
			ts := sr.runner.tradeStream
			go func() {
				sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				orders, err := trader.FetchOpenOrdersForSymbolAll(sweepCtx, creds, category, symbol)
```
`ts` (the tradeStream) is used later in this same closure for a `CancelOrder` call — **out of scope for this plan, do not touch `ts` or its later use**. `creds` is used ONLY for the `FetchOpenOrdersForSymbolAll` call being migrated here — confirm this yourself by reading the rest of the closure (through its closing `}()`) before editing, then change:
```go
			creds := sr.runner.creds
			ts := sr.runner.tradeStream
			go func() {
				sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				orders, err := trader.FetchOpenOrdersForSymbolAll(sweepCtx, creds, category, symbol)
```
to:
```go
			ex := sr.runner.Exchange()
			ts := sr.runner.tradeStream
			go func() {
				sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				orders, err := ex.FetchOpenOrdersForSymbolAll(sweepCtx, category, symbol)
```
(i.e. rename the captured var from `creds` to `ex` and change what it holds, since nothing else in this closure uses `creds` — confirmed by your own read. If your read finds `creds` IS used elsewhere in that closure too, instead follow the general Pattern B fallback: keep `creds := sr.runner.creds` as-is and add `ex := sr.runner.Exchange()` as a new adjacent line, using `ex` only at the `FetchOpenOrdersForSymbolAll` call.)

**Site 2 — around line 5555-5573.** Currently:
```go
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel
	symbol := sr.strategy.Symbol
	category := sr.strategy.Category
	creds := sr.runner.creds
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	// Immediate startup check: execute any levels whose trigger condition is already met
	// (handles server restart where price arrived at target while we were down).
	go func() {
		if price, err := trader.FetchMarkPrice(ctx, creds, category, symbol); err == nil {
```
Read forward from here to the end of this function (through the `if se == nil { go func() {...}() }` block that follows) to confirm `creds` isn't reused past this point (the `se == nil` polling-fallback branch has its OWN separate `creds := sr.runner.creds` capture — see Site 3 below — so this outer `creds` should only feed the one immediate-startup-check goroutine). If confirmed, change:
```go
	creds := sr.runner.creds
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	// Immediate startup check: execute any levels whose trigger condition is already met
	// (handles server restart where price arrived at target while we were down).
	go func() {
		if price, err := trader.FetchMarkPrice(ctx, creds, category, symbol); err == nil {
```
to:
```go
	ex := sr.runner.Exchange()
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	// Immediate startup check: execute any levels whose trigger condition is already met
	// (handles server restart where price arrived at target while we were down).
	go func() {
		if price, err := ex.GetMarkPrice(ctx, category, symbol); err == nil {
```

**Site 3 — around line 5585-5593** (inside the `se == nil` polling-fallback goroutine that follows Site 2, its own separate per-tick capture):
```go
				case <-ticker.C:
					sr.mu.Lock()
					hasCycle := sr.cycle != nil
					sr.mu.Unlock()
					if !hasCycle {
						return
					}
					creds := sr.runner.creds
					category := sr.strategy.Category
					price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
```
becomes:
```go
				case <-ticker.C:
					sr.mu.Lock()
					hasCycle := sr.cycle != nil
					sr.mu.Unlock()
					if !hasCycle {
						return
					}
					ex := sr.runner.Exchange()
					category := sr.strategy.Category
					price, err := ex.GetMarkPrice(ctx, category, symbol)
```

- [ ] **Step 4: Run the full `pkg/strategy` test suite**

Run: `go build ./...`
Expected: clean.

Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100`
Expected: every test passes, identical to the pre-this-task baseline (this task changes zero externally-observable behavior for Bybit accounts — see the plan's Architecture section).

Run: `go test -tags=integration ./pkg/strategy/...`
Expected: same 2 pre-existing, unrelated failures this session has already established as baseline (`TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents`, `TestResolveSignalConfigs_MixedCatalogAndCustom` — a `custom_signals_badge_len` check-constraint issue, nothing to do with this plan). No new failures.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/cycle.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate cycle.go's read-only calls onto AccountRunner.Exchange()

First of 3 sub-plans migrating pkg/strategy off Bybit-specific free
functions onto the generic trader.Exchange interface (see
docs/superpowers/specs/2026-09-03-binance-live-trading-design.md) — split
from the original single "#4b" plan after discovering the true scope (82
call sites across 8 files) made one giant plan too risky for the only
exchange actually in live production use. This sub-plan covers only
read-only calls (FetchPositions, FetchOpenOrdersForSymbolAll, FetchMarkPrice
→ GetMarkPrice); order placement/cancellation and account-setup calls are
separate, higher-stakes sub-plans.

Zero behavior change for Bybit accounts: BybitExchange's methods are
one-line delegations to the exact same free functions this commit stops
calling directly.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `pkg/strategy/matrix.go`

**Files:**
- Modify: `pkg/strategy/matrix.go`

5 sites: 4 Pattern A, 1 Pattern B (line ~849, fed by a capture that this plan's own research traced to `runMatrixPriceMonitorPolling` — confirm no other use of `creds` in that function before editing, same verification discipline as Task 1's Pattern B sites).

- [ ] **Step 1: Pattern A sites**

```go
// line ~329
price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~654
priceForBump, _ = trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes (note: assignment `=`, not declaration `:=` — priceForBump is declared earlier; keep the `=`):
priceForBump, _ = sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~722
price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)

// line ~1263
price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
// becomes:
price, err := sr.runner.Exchange().GetMarkPrice(ctx, sr.strategy.Category, sr.strategy.Symbol)
```

- [ ] **Step 2: Pattern B site — around line 829-849**

Currently (function `runMatrixPriceMonitorPolling`):
```go
func (sr *StrategyRunner) runMatrixPriceMonitorPolling(ctx context.Context, cancel context.CancelFunc, stratID, symbol string) {
	defer cancel()
	sr.mu.Lock()
	creds := sr.runner.creds
	category := sr.strategy.Category
	sr.mu.Unlock()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sr.mu.Lock()
			hasCycle := sr.cycle != nil
			sr.mu.Unlock()
			if !hasCycle {
				return
			}
			price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
```
Read the rest of this function (through its closing brace) to confirm `creds` isn't used anywhere else besides this one `FetchMarkPrice` call. If confirmed, change:
```go
	sr.mu.Lock()
	creds := sr.runner.creds
	category := sr.strategy.Category
	sr.mu.Unlock()
```
to:
```go
	sr.mu.Lock()
	ex := sr.runner.Exchange()
	category := sr.strategy.Category
	sr.mu.Unlock()
```
and:
```go
			price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
```
to:
```go
			price, err := ex.GetMarkPrice(ctx, category, symbol)
```

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: identical to Task 1's post-commit baseline, no regressions.
Run: `go test -tags=integration ./pkg/strategy/...` — expected: same 2 pre-existing failures, no growth.

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/matrix.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate matrix.go's read-only calls onto AccountRunner.Exchange()

Second of 3 sub-plans for the read-only slice of pkg/strategy's Bybit-free-
function migration (see cycle.go's commit for full context). Zero behavior
change for Bybit accounts.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `pkg/strategy/startup_reconcile.go`

**Files:**
- Modify: `pkg/strategy/startup_reconcile.go`

This file is structurally different from Tasks 1-2: it runs once at `Engine.Start()`, before any `AccountRunner` exists for the accounts it processes — it queries `exchange_accounts` directly and decrypts credentials itself, caching them in a function-local map. There are **two separate functions**, each with its own local credential cache, each needing the same treatment: add `ea.exchange` to the SQL query, add an `exchange` field to the row-scanning struct, and cache a resolved `trader.Exchange` (via `resolveExchange`, already defined package-wide in `engine.go` since Plan #4a — same package, no import needed) instead of/alongside the raw credentials.

- [ ] **Step 1: First function — `reconcileStoppedCycles`**

The current query (verified against the live file) is:
```go
	rows, err := e.pool.Query(ctx, `
		SELECT sc.id, sc.cycle_num, sc.started_at,
		       COALESCE(sc.tp_order_id, ''), COALESCE(sc.sl_order_id, ''),
		       s.id, s.account_id, s.owner_id, s.symbol, s.category,
		       s.direction, COALESCE(s.hedge_mode, true),
		       s.bot_id,
		       ea.api_key_enc, ea.secret_enc, ea.whitelisted_ips
		FROM strategy_cycles sc
		JOIN strategies       s  ON s.id  = sc.strategy_id
		JOIN exchange_accounts ea ON ea.id = s.account_id
		WHERE sc.ended_at IS NULL
		  AND s.status NOT IN ('active', 'finishing')
		  AND ea.is_active = true`,
	)
```
Add `ea.exchange` to the SELECT list (right after `s.bot_id,` and before `ea.api_key_enc`, to keep it near the other `ea.*` columns):
```go
	rows, err := e.pool.Query(ctx, `
		SELECT sc.id, sc.cycle_num, sc.started_at,
		       COALESCE(sc.tp_order_id, ''), COALESCE(sc.sl_order_id, ''),
		       s.id, s.account_id, s.owner_id, s.symbol, s.category,
		       s.direction, COALESCE(s.hedge_mode, true),
		       s.bot_id,
		       ea.exchange, ea.api_key_enc, ea.secret_enc, ea.whitelisted_ips
		FROM strategy_cycles sc
		JOIN strategies       s  ON s.id  = sc.strategy_id
		JOIN exchange_accounts ea ON ea.id = s.account_id
		WHERE sc.ended_at IS NULL
		  AND s.status NOT IN ('active', 'finishing')
		  AND ea.is_active = true`,
	)
```

The `cycleRow` struct currently ends with:
```go
		// credentials (encrypted)
		apiKeyEnc      string
		secretEnc      string
		whitelistedIPs []string
	}
```
Add an `exchangeName` field:
```go
		// credentials (encrypted)
		exchangeName   string
		apiKeyEnc      string
		secretEnc      string
		whitelistedIPs []string
	}
```

The `Scan` call currently reads:
```go
		if err := rows.Scan(
			&r.cycleID, &r.cycleNum, &r.startedAt,
			&r.tpOrderID, &r.slOrderID,
			&r.stratID, &r.accountID, &r.ownerID, &r.symbol, &r.category,
			&r.direction, &r.hedgeMode,
			&r.botID,
			&r.apiKeyEnc, &r.secretEnc, &r.whitelistedIPs,
		); err != nil {
```
becomes:
```go
		if err := rows.Scan(
			&r.cycleID, &r.cycleNum, &r.startedAt,
			&r.tpOrderID, &r.slOrderID,
			&r.stratID, &r.accountID, &r.ownerID, &r.symbol, &r.category,
			&r.direction, &r.hedgeMode,
			&r.botID,
			&r.exchangeName, &r.apiKeyEnc, &r.secretEnc, &r.whitelistedIPs,
		); err != nil {
```

The local `creds`/cache section currently reads:
```go
	// Cache decrypted credentials and fetched positions per account.
	type creds struct {
		apiKey, secret string
		whitelistedIPs []string
	}
	credCache := make(map[string]*creds)
	posCache  := make(map[string][]trader.Position)

	for _, c := range cycles {
		// ── decrypt credentials (once per account) ────────────────────────────
		if _, seen := credCache[c.accountID]; !seen {
			apiKey, err := crypto.Decrypt(c.apiKeyEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				credCache[c.accountID] = nil
				continue
			}
			secret, err := crypto.Decrypt(c.secretEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				credCache[c.accountID] = nil
				continue
			}
			credCache[c.accountID] = &creds{apiKey: apiKey, secret: secret, whitelistedIPs: c.whitelistedIPs}
		}
		cr := credCache[c.accountID]
		if cr == nil {
			continue
		}

		// ── fetch all positions for this account (once per account) ───────────
		if _, seen := posCache[c.accountID]; !seen {
			positions, err := trader.FetchPositions(ctx, trader.Credentials{
				APIKey: cr.apiKey, SecretKey: cr.secret, AccountID: c.accountID, WhitelistedIPs: cr.whitelistedIPs,
			})
			if err != nil {
				log.Printf("startup reconcile: fetch positions account=%s: %v", c.accountID, err)
				posCache[c.accountID] = nil
				continue
			}
			posCache[c.accountID] = positions
		}
```
Replace the `credCache`/decrypt block to build and cache a resolved `trader.Exchange` instead of a raw `*creds`, and change the position fetch to use it:
```go
	// Cache resolved Exchange (per account) and fetched positions per account.
	exCache := make(map[string]trader.Exchange)
	posCache := make(map[string][]trader.Position)

	for _, c := range cycles {
		// ── decrypt credentials + resolve Exchange (once per account) ─────────
		if _, seen := exCache[c.accountID]; !seen {
			apiKey, err := crypto.Decrypt(c.apiKeyEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				exCache[c.accountID] = nil
				continue
			}
			secret, err := crypto.Decrypt(c.secretEnc, e.encKey)
			if err != nil {
				log.Printf("startup reconcile: decrypt account=%s: %v", c.accountID, err)
				exCache[c.accountID] = nil
				continue
			}
			creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: c.accountID, WhitelistedIPs: c.whitelistedIPs}
			exCache[c.accountID] = resolveExchange(c.exchangeName, creds, trader.NewTradeStream(creds))
		}
		ex := exCache[c.accountID]
		if ex == nil {
			continue
		}

		// ── fetch all positions for this account (once per account) ───────────
		if _, seen := posCache[c.accountID]; !seen {
			positions, err := ex.FetchPositions(ctx)
			if err != nil {
				log.Printf("startup reconcile: fetch positions account=%s: %v", c.accountID, err)
				posCache[c.accountID] = nil
				continue
			}
			posCache[c.accountID] = positions
		}
```

Read the rest of this function past this point (it continues using `positions := posCache[c.accountID]` and other `c.*` fields, none of which this step touches) to confirm nothing else in the function referenced the now-removed `cr`/`creds`-typed local (the type named `creds` was function-local and only used in the block just replaced — a repo-wide grep for the OLD local type won't help since it's shadowed; just read the rest of this one function).

- [ ] **Step 2: Second function — the one with `accountInfo`/`apiKeyEnc` around line 235-254**

Read the full second function in this file (containing the `type stratInfo`/local `accountInfo`-shaped struct and the `rows.Scan(&s.stratID, &accountID, &s.symbol, &s.direction, &s.hedgeMode, &apiKeyEnc, &secretEnc, &whitelistedIPs)` call around line 239-240) in its entirety before editing — this plan's research did not capture its full body, only a fragment, because it follows the exact same shape as Step 1's function (query missing `ea.exchange`, local struct/cache needing an `exchange` field, a `trader.FetchPositions(ctx, trader.Credentials{...})` call needing to become `ex.FetchPositions(ctx)`). Apply the identical treatment:
1. Add `ea.exchange` to that function's SQL query (find the `SELECT ... FROM ... JOIN exchange_accounts ea ...` in this function specifically — it's a separate query from Step 1's, likely with a different column list; add `ea.exchange` alongside wherever `ea.api_key_enc`/`ea.secret_enc` are selected).
2. Add an `exchange` (or `exchangeName`) field to whatever local struct holds `apiKeyEnc`/`secretEnc` for this function (the plan's research saw this as a struct literal `&accountInfo{apiKeyEnc: ..., secretEnc: ..., whitelistedIPs: ...}` at line ~244 — confirm the actual field/type name yourself, it may not be literally called `accountInfo`, just described that way informally in this plan's earlier research pass).
3. Update its `Scan` call to also read the new column.
4. Find where this function calls `trader.FetchPositions(ctx, trader.Credentials{...})` (around former line 265, may have shifted after Step 1's edits) and replace it with a `resolveExchange`-built `Exchange`'s `.FetchPositions(ctx)`, following the exact same pattern as Step 1.

If you find this second function's shape differs meaningfully from what's described above once you actually read it in full, treat this plan's description as a **hypothesis to verify, not ground truth** — implement the equivalent transformation (query gains `ea.exchange`, cache resolves and stores a `trader.Exchange`, the `FetchPositions` call site uses it) matching what you actually find, and report the discrepancy clearly in your task summary the way earlier tasks in this multi-plan effort have done successfully when their plan's research had gaps.

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: no regressions vs. Task 2's post-commit baseline.
Run: `go test -tags=integration ./pkg/strategy/...` — expected: same 2 pre-existing failures, no growth. If this file has its own dedicated test coverage (check for a `startup_reconcile_test.go` or similar before assuming none exists), run it specifically too and confirm it passes.

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/startup_reconcile.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate startup_reconcile.go onto resolveExchange

Last of 3 sub-plans for the read-only slice of pkg/strategy's Bybit-free-
function migration. This file runs before any AccountRunner exists for the
accounts it processes, so it resolves its own trader.Exchange per account
(via resolveExchange, from Plan #4a) instead of going through
AccountRunner.Exchange() — same underlying resolution logic, just invoked
directly since there's no AccountRunner here yet.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not migrate `trade_recorder.go`'s 2 `FetchClosedPnlForSymbol` call sites (`RecordMatrixTPProfit`, `RecordStrategyTrade`) — both cross into `services/api-gateway`, deferred to align with #4c.
- Does not touch any order-placement or order-cancellation call (`tradeStream.PlaceOrder`/`PlaceOrderBatch`/`CancelOrder`/`CancelOrderBatch`) — a separate, higher-stakes sub-plan (#4b-3), since these move real money and deserve their own careful review cycle, likely split further by file given `cycle.go` alone has ~15 such calls.
- Does not touch `trader.SetLeverage`/`trader.SwitchPositionMode` calls — sub-plan #4b-2, infrequent account-setup calls, lower urgency than order placement but still separate from this purely-read-only plan.
- Does not touch `pkg/strategy/reconcile.go`, `pkg/strategy/hedge_support.go`, or `pkg/strategy/matrix_relative_engine.go` — verified during this plan's research that none of their Bybit-specific calls are read-only (`FetchPositions`/`FetchOpenOrdersForSymbolAll`/`FetchMarkPrice`); whatever they do call belongs to #4b-2 or #4b-3.
- Does not touch `services/api-gateway` at all — that's #4c, a separate later plan.
