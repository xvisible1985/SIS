# cycle.go Order Placement/Cancellation Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate every `sr.runner.tradeStream.PlaceOrder/PlaceOrderBatch/CancelOrder/CancelOrderBatch` call in `pkg/strategy/cycle.go` onto `sr.runner.Exchange()`. This is sub-plan #4b-3a — the highest-stakes chunk of the whole multi-exchange rollout so far (real order placement/cancellation, real money, on the live grid-strategy engine), split out of `pkg/strategy` order-write migration by file given `cycle.go` alone has 26 sites (more than the entirety of #4b-1's 28 read-only sites combined). `matrix.go` (17 sites) and the remaining small files (`hedge_support.go`, `matrix_relative_engine.go`, `reconcile.go` — 4 sites total) are separate follow-up plans (#4b-3b, #4b-3c), not covered here.

**Architecture:** This is the simplest substitution in the whole migration series, mechanically: `wsOrderClient` (the interface `*trader.TradeStream` satisfies, `pkg/trader/exchange.go`) and `trader.Exchange`'s order-write methods have **identical signatures** — `PlaceOrder(ctx, req) (OrderResult, error)`, `PlaceOrderBatch(ctx, req) ([]BatchPlaceResult, error)`, `CancelOrder(ctx, req) error`, `CancelOrderBatch(ctx, req) error`. `BybitExchange.PlaceOrder`/etc. (`pkg/trader/exchange.go`) are one-line delegations straight to `e.ws.PlaceOrder(ctx, req)` — and `e.ws` is the exact same `*trader.TradeStream` instance stored as `AccountRunner.tradeStream`, since `resolveExchange` (Plan #4a) constructs `BybitExchange` by wrapping that same object. **This means every substitution in this plan is a pure receiver-expression swap — `sr.runner.tradeStream.X(` → `sr.runner.Exchange().X(` — with zero argument restructuring, unlike the read-only migration (#4b-1) which had to drop `creds`/rename `FetchMarkPrice`→`GetMarkPrice`.** Zero behavior change for Bybit accounts.

**Tech Stack:** Go. No new tests — `BybitExchange`'s order methods are already tested (Plan #1); this plan only changes who calls them.

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4a (`AccountRunner.Exchange()`), Plan #4b-1 (established substitution/verification conventions), Plan #4b-2 (all merged).

---

## Before you start

Read `pkg/trader/exchange.go` — confirm `Exchange`'s `PlaceOrder`/`PlaceOrderBatch`/`CancelOrder`/`CancelOrderBatch` signatures match `wsOrderClient`'s exactly (they do — `BybitExchange` is defined to satisfy both by simple delegation).

**26 sites total** in `pkg/strategy/cycle.go`: 25 use `sr.runner.tradeStream.X(...)` directly; 1 (line ~2993, inside `closeCycle`'s grid safety-sweep) captures `ts := sr.runner.tradeStream` before a `go func(){...}()` closure and calls `ts.CancelOrder(...)` inside it at line ~3010 — this is the exact same closure Plan #4b-1 already touched (renaming its `creds` capture to `ex := sr.runner.Exchange()` for a `FetchOpenOrdersForSymbolAll` call at line ~2997, while deliberately leaving `ts := sr.runner.tradeStream` untouched since order-write calls were out of #4b-1's scope). **This plan now migrates that same `ts` capture** — since `#4b-1` already added an `ex := sr.runner.Exchange()` line right above where `ts` is declared, in this closure `ex` is already available; delete the now-redundant `ts := sr.runner.tradeStream` line entirely and use the existing `ex` for the `CancelOrder` call too.

**The substitution rule for the other 25 sites**: literally replace the substring `sr.runner.tradeStream.` with `sr.runner.Exchange().` at each site. Nothing else on the line changes — same method name, same arguments, same everything.

---

### Task 1: Migrate all 26 sites

**Files:**
- Modify: `pkg/strategy/cycle.go`

- [ ] **Step 1: The 25 direct sites**

Apply the substring substitution `sr.runner.tradeStream.` → `sr.runner.Exchange().` at each of these lines (verified against the live file while writing this plan — search for the quoted call if a line number has drifted):

```
line ~721:  _, closeErr := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~852:  _, closeErr := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1130: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1212: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~1244: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~1317: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~1347: _, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1392: _, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~2004: results, err := sr.runner.tradeStream.PlaceOrderBatch(ctx, trader.BatchPlaceRequest{
line ~2128: result, err := sr.runner.tradeStream.PlaceOrder(ctx, req)
line ~2514: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~2536: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~2711: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~2730: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~2777: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~2964: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~2973: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~3113: if err := sr.runner.tradeStream.CancelOrderBatch(ctx, trader.BatchCancelRequest{
line ~3125: if err := sr.runner.tradeStream.CancelOrderBatch(ctx, trader.BatchCancelRequest{
line ~3816: result, placeErr := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~4762: if err := sr.runner.tradeStream.CancelOrderBatch(ctx, trader.BatchCancelRequest{
line ~5029: if e := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~5048: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~5213: if _, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~5680: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
```

For each: change only the receiver (`sr.runner.tradeStream` → `sr.runner.Exchange()`), keep everything else on that line and the following lines (the `trader.OrderRequest{...}`/`trader.CancelRequest{...}`/etc. body) byte-for-byte identical — this is not a refactor of the request construction, only of who it's sent to.

- [ ] **Step 2: The 1 aliased site (~lines 2989-3021, inside `closeCycle`'s grid safety-sweep)**

Read this closure in full first (it was already touched by Plan #4b-1, which added an `ex := sr.runner.Exchange()` line here for a different, read-only call). The current shape should be:
```go
			prefix := "SIS_STR-" + stratID8 + "-"
			symbol := sr.strategy.Symbol
			category := sr.strategy.Category
			ex := sr.runner.Exchange()
			ts := sr.runner.tradeStream
			go func() {
				sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				orders, err := ex.FetchOpenOrdersForSymbolAll(sweepCtx, category, symbol)
				if err != nil {
					log.Printf("strategy %s: closeCycle sweep: %v", stratID8, err)
					return
				}
				for _, o := range orders {
					if !strings.HasPrefix(o.OrderLinkId, prefix) {
						continue
```
and further down, inside the same closure (around line 3010):
```go
				if err := ts.CancelOrder(sweepCtx, trader.CancelRequest{
```
Change to: delete the now-unused `ts := sr.runner.tradeStream` line entirely, and change the `CancelOrder` call site from `ts.CancelOrder(...)` to `ex.CancelOrder(...)` (reusing the `ex` already captured above for the `FetchOpenOrdersForSymbolAll` call):
```go
			prefix := "SIS_STR-" + stratID8 + "-"
			symbol := sr.strategy.Symbol
			category := sr.strategy.Category
			ex := sr.runner.Exchange()
			go func() {
				sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				orders, err := ex.FetchOpenOrdersForSymbolAll(sweepCtx, category, symbol)
```
```go
				if err := ex.CancelOrder(sweepCtx, trader.CancelRequest{
```
If your read of the live file finds this closure's current shape differs from what's quoted above (e.g. if a later, unrelated change touched this same block since #4b-1 merged), treat this as a hypothesis to verify against the real file, not ground truth — apply the equivalent transformation (delete the redundant `ts` capture, route its one use through the already-captured `ex`) to whatever you actually find, and note any discrepancy in your report.

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: identical to the established 93 PASS baseline (this task changes zero externally-observable behavior for Bybit accounts).
Run: `go test -tags=integration ./pkg/strategy/...` — expected: same 2 pre-existing, unrelated failures (`TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents`, `TestResolveSignalConfigs_MixedCatalogAndCustom`), no growth.
Run: `grep -n "sr\.runner\.tradeStream\b" pkg/strategy/cycle.go` — expected: **no matches** (all direct uses migrated, and the one alias deleted).

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/cycle.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate cycle.go's order placement/cancellation onto AccountRunner.Exchange()

Sub-plan #4b-3a of the multi-exchange rollout — the highest-stakes chunk so
far: 26 order placement/cancellation call sites in the live grid-strategy
engine, now routed through AccountRunner.Exchange() instead of
tradeStream directly. Zero behavior change for Bybit accounts:
BybitExchange.PlaceOrder/PlaceOrderBatch/CancelOrder/CancelOrderBatch are
one-line delegations straight to the exact same *trader.TradeStream
instance already stored as AccountRunner.tradeStream (see Plan #4a's
resolveExchange, which constructs BybitExchange by wrapping that same
object) — this is a pure receiver-expression swap, no argument or behavior
change anywhere.

matrix.go (17 sites) and the remaining small files (hedge_support.go,
matrix_relative_engine.go, reconcile.go — 4 sites) are separate follow-up
plans (#4b-3b, #4b-3c).

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not touch `pkg/strategy/matrix.go` (17 order-write sites) — sub-plan #4b-3b.
- Does not touch `pkg/strategy/hedge_support.go`, `pkg/strategy/matrix_relative_engine.go`, or `pkg/strategy/reconcile.go` (4 order-write sites combined) — sub-plan #4b-3c.
- Does not touch `services/api-gateway` — that's #4c.
- Does not add new tests — the underlying `BybitExchange` order methods are already tested; this plan only changes the call site, not the tested behavior.
