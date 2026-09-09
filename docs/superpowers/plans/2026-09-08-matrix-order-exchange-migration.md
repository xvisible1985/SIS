# matrix.go Order Placement/Cancellation Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate every `sr.runner.tradeStream.PlaceOrder/CancelOrder` call in `pkg/strategy/matrix.go` onto `sr.runner.Exchange()`. This is sub-plan #4b-3b — the second of three file-scoped order-write migrations (after `cycle.go`, #4b-3a, merged; before the small remaining files, #4b-3c). Same mechanical substitution as #4b-3a, applied to the matrix-strategy engine instead of the grid engine.

**Architecture:** Identical rationale to #4b-3a: `wsOrderClient` (what `*trader.TradeStream` implements) and `trader.Exchange`'s order-write methods have identical signatures; `BybitExchange.PlaceOrder`/`CancelOrder` are one-line delegations to the exact same `*TradeStream` instance already stored as `AccountRunner.tradeStream`. Every substitution in this plan is a pure receiver-expression swap — `sr.runner.tradeStream.X(` → `sr.runner.Exchange().X(` — zero argument changes, zero behavior change for Bybit accounts.

**Tech Stack:** Go. No new tests (same rationale as #4b-3a — the underlying `BybitExchange` methods are already tested).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plans #4a, #4b-1, #4b-2, #4b-3a (all merged).

---

## Before you start

**17 sites total** in `pkg/strategy/matrix.go`, all direct `sr.runner.tradeStream.X(...)` calls (verified while writing this plan: no closure-captured alias variable exists in this file, unlike `cycle.go`'s one `ts := sr.runner.tradeStream` case that #4b-3a already handled — matrix.go has no equivalent, confirmed via `grep ":= sr\.runner\.tradeStream\b"` returning only the same 17 direct-call sites, not a separate alias declaration).

Three sites (lines ~895, ~1553, ~1752) have a `//nolint:errcheck` comment on the same line — a lint suppression for an intentionally-ignored error return. **Preserve this comment exactly** when migrating those three lines; it's not related to the substitution and must not be dropped.

---

### Task 1: Migrate all 17 sites

**Files:**
- Modify: `pkg/strategy/matrix.go`

- [ ] **Step 1: Apply the substitution**

Replace the substring `sr.runner.tradeStream.` with `sr.runner.Exchange().` at each of these lines (verified against the live file while writing this plan — search for the quoted call if a line number has drifted):

```
line ~626:  result, err := sr.runner.tradeStream.PlaceOrder(ctx, req)
line ~666:  result, err = sr.runner.tradeStream.PlaceOrder(ctx, req)
line ~895:  sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
line ~922:  result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1085: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1443: result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1553: sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
line ~1576: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~1629: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~1654: result, err = sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1674: result, err = sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
line ~1752: sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
line ~1858: cancelErr := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~1865: sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
line ~1973: if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~2516: cancelErr := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
line ~2523: if err2 := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
```

For each: change only the receiver (`sr.runner.tradeStream` → `sr.runner.Exchange()`), keep everything else on that line — including any trailing `//nolint:errcheck` comment — and the following request-body lines byte-for-byte identical.

- [ ] **Step 2: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: identical to the established 93 PASS baseline.
Run: `go test -tags=integration ./pkg/strategy/...` — expected: same 2 pre-existing, unrelated failures (`TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents`, `TestResolveSignalConfigs_MixedCatalogAndCustom`), no growth.
Run: `grep -n "sr\.runner\.tradeStream\b" pkg/strategy/matrix.go` — expected: **no matches**.

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/matrix.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate matrix.go's order placement/cancellation onto AccountRunner.Exchange()

Sub-plan #4b-3b of the multi-exchange rollout — 17 order placement/
cancellation call sites in the matrix-strategy engine, now routed through
AccountRunner.Exchange() instead of tradeStream directly. Same rationale
as #4b-3a (cycle.go, already merged): BybitExchange.PlaceOrder/CancelOrder
are one-line delegations to the exact same *trader.TradeStream instance
already stored on AccountRunner — pure receiver-expression swap, zero
behavior change for Bybit accounts.

The remaining small files (hedge_support.go, matrix_relative_engine.go,
reconcile.go — 4 sites total) are sub-plan #4b-3c.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not touch `pkg/strategy/hedge_support.go`, `pkg/strategy/matrix_relative_engine.go`, or `pkg/strategy/reconcile.go` (4 order-write sites combined) — sub-plan #4b-3c.
- Does not touch `services/api-gateway` — that's #4c.
- Does not add new tests.
