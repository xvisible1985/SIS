# pkg/strategy Account-Setup Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the 4 remaining `trader.SetLeverage`/`trader.SwitchPositionMode` calls in `pkg/strategy` onto `AccountRunner.Exchange()`. This is sub-plan #4b-2 of the multi-exchange rollout's #4b phase — smaller and lower-risk than #4b-1 (28 read-only sites, already merged) and #4b-3 (order placement/cancellation, not yet started): leverage/position-mode changes are infrequent account-setup operations, not on the hot per-tick trading path.

**Architecture:** Same substitution pattern as #4b-1: `trader.SetLeverage(ctx, sr.runner.creds, req)` → `sr.runner.Exchange().SetLeverage(ctx, req)` and `trader.SwitchPositionMode(ctx, sr.runner.creds, category, symbol, mode)` → `sr.runner.Exchange().SwitchPositionMode(ctx, category, symbol, mode)` — the `Exchange` interface methods (`pkg/trader/exchange.go`) have the identical shape to the free functions minus the `creds` argument, and `BybitExchange.SetLeverage`/`SwitchPositionMode` are one-line delegations to those same free functions (zero behavior change for Bybit accounts). All 4 sites are direct inline calls (no closure-captured `creds` variable to worry about, unlike several #4b-1 sites) — confirmed by reading each site's full context while writing this plan.

**Tech Stack:** Go. No new tests — the underlying `BybitExchange` methods are already tested (Plan #1); this plan only changes who calls them.

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4a (`AccountRunner.Exchange()`, merged), Plan #4b-1 (established the substitution pattern and Pattern-A/B verification discipline, merged).

---

## Before you start

Read `pkg/trader/exchange.go` for the exact interface signatures: `SetLeverage(ctx context.Context, req LeverageRequest) error` and `SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error`. `LeverageRequest` (`pkg/trader/types.go`) already carries `Symbol`/`Category`/`BuyLeverage`/`SellLeverage` as struct fields, so migrating `SetLeverage` calls needs no restructuring of the request literal — only dropping the `sr.runner.creds` argument.

**One thing to preserve exactly**: `pkg/strategy/cycle.go`'s `SwitchPositionMode` call site is followed by `errors.Is(err, trader.ErrPositionModeUnsupported)` — a sentinel-error check for symbols that don't support mode switching (e.g. dated futures). `BybitExchange.SwitchPositionMode` delegates directly to the same free function that produces this sentinel, so the check keeps working unchanged after migration — but do not accidentally wrap or lose this error along the way.

---

### Task 1: Migrate all 4 call sites

**Files:**
- Modify: `pkg/strategy/cycle.go`
- Modify: `pkg/strategy/reconcile.go`

- [ ] **Step 1: `pkg/strategy/cycle.go` — `SwitchPositionMode` (~line 1566)**

Read the surrounding function first to confirm the exact current text, then change:
```go
	if err := trader.SwitchPositionMode(ctx, sr.runner.creds, category, symbol, mode); err != nil {
```
to:
```go
	if err := sr.runner.Exchange().SwitchPositionMode(ctx, category, symbol, mode); err != nil {
```
Leave the following `errors.Is(err, trader.ErrPositionModeUnsupported)` block and everything else in this function untouched.

- [ ] **Step 2: `pkg/strategy/cycle.go` — two `SetLeverage` calls (~lines 1612 and 1623)**

```go
	if lerr := trader.SetLeverage(ctx, sr.runner.creds, trader.LeverageRequest{
		Symbol:       sr.strategy.Symbol,
		Category:     sr.strategy.Category,
		BuyLeverage:  levStr,
		SellLeverage: levStr,
	}); lerr != nil && !strings.Contains(lerr.Error(), "110043") {
```
becomes:
```go
	if lerr := sr.runner.Exchange().SetLeverage(ctx, trader.LeverageRequest{
		Symbol:       sr.strategy.Symbol,
		Category:     sr.strategy.Category,
		BuyLeverage:  levStr,
		SellLeverage: levStr,
	}); lerr != nil && !strings.Contains(lerr.Error(), "110043") {
```
and, a few lines further down (the capped-leverage retry):
```go
				if lerr2 := trader.SetLeverage(ctx, sr.runner.creds, trader.LeverageRequest{
					Symbol:       sr.strategy.Symbol,
					Category:     sr.strategy.Category,
					BuyLeverage:  cappedStr,
					SellLeverage: cappedStr,
				}); lerr2 != nil && !strings.Contains(lerr2.Error(), "110043") {
```
becomes:
```go
				if lerr2 := sr.runner.Exchange().SetLeverage(ctx, trader.LeverageRequest{
					Symbol:       sr.strategy.Symbol,
					Category:     sr.strategy.Category,
					BuyLeverage:  cappedStr,
					SellLeverage: cappedStr,
				}); lerr2 != nil && !strings.Contains(lerr2.Error(), "110043") {
```
Leave the surrounding `trader.GetPublicInstrumentInfo(ctx, sr.strategy.Category, sr.strategy.Symbol)` call untouched — it's a public, unauthenticated endpoint with no `Exchange`-interface equivalent, out of scope for this plan.

- [ ] **Step 3: `pkg/strategy/reconcile.go` — `SetLeverage` (~line 792)**

```go
			if err := trader.SetLeverage(ctx, sr.runner.creds, trader.LeverageRequest{
				Symbol:       symbol,
				Category:     category,
				BuyLeverage:  levStr,
				SellLeverage: levStr,
			}); err != nil && !strings.Contains(err.Error(), "110043") {
```
becomes:
```go
			if err := sr.runner.Exchange().SetLeverage(ctx, trader.LeverageRequest{
				Symbol:       symbol,
				Category:     category,
				BuyLeverage:  levStr,
				SellLeverage: levStr,
			}); err != nil && !strings.Contains(err.Error(), "110043") {
```

- [ ] **Step 4: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: no regressions (93 PASS baseline established by #4b-1).
Run: `go test -tags=integration ./pkg/strategy/...` — expected: same 2 pre-existing, unrelated failures (`TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents`, `TestResolveSignalConfigs_MixedCatalogAndCustom`), no growth.
Run: `grep -rn "trader.SetLeverage(\|trader.SwitchPositionMode(" pkg/strategy/*.go` — expected: no matches (all 4 sites migrated).

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/reconcile.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate SetLeverage/SwitchPositionMode onto AccountRunner.Exchange()

Sub-plan #4b-2 of the multi-exchange rollout — the 4 account-setup calls
(leverage, position mode) still using Bybit-specific free functions
directly, now routed through AccountRunner.Exchange(). Zero behavior change
for Bybit accounts: BybitExchange's methods are one-line delegations to the
exact same free functions, including the ErrPositionModeUnsupported
sentinel error cycle.go's caller depends on.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not touch `trader.GetPublicInstrumentInfo` (public, unauthenticated, no `Exchange`-interface equivalent) or `trader.GetInstrumentInfo` (a different, authenticated instrument-info call not yet in scope) — neither is part of this plan.
- Does not touch order placement/cancellation (`tradeStream.PlaceOrder`/`PlaceOrderBatch`/`CancelOrder`/`CancelOrderBatch`) — sub-plan #4b-3, the highest-stakes remaining chunk of #4b.
- Does not touch `services/api-gateway` — that's #4c.
