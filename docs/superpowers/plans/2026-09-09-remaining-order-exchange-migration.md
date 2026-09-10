# Remaining pkg/strategy Order Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the last 4 order-write call sites in `pkg/strategy` — split across `hedge_support.go` (2), `matrix_relative_engine.go` (1), and `reconcile.go` (1) — onto `AccountRunner.Exchange()`. This is sub-plan #4b-3c, the final piece of #4b-3 (order placement/cancellation migration) and, once merged, **closes out all of #4b** (`pkg/strategy`'s entire migration off Bybit-specific free functions/`tradeStream` direct calls). Only `services/api-gateway` (#4c) will remain.

**Architecture:** Same mechanical receiver-swap as #4b-3a (`cycle.go`, merged) and #4b-3b (`matrix.go`, merged): `sr.runner.tradeStream.X(` → `sr.runner.Exchange().X(`. One site (`reconcile.go:749`) is inside an `AccountRunner` method itself (receiver `ar`, not `sr`), matching the "Pattern C" direct-field-access style already used for `engine.go:1410`/`1396` in Plan #4b-1: `ar.tradeStream.X(` → `ar.exchange.X(`.

**Tech Stack:** Go. No new tests (same rationale as #4b-3a/#4b-3b).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plans #4a, #4b-1, #4b-2, #4b-3a, #4b-3b (all merged).

---

## Before you start

All 4 sites are direct calls with no closure-captured alias variable (confirmed while writing this plan by reading each site's full context) — the simplest category, same as most of #4b-3b's sites.

---

### Task 1: Migrate all 4 sites

**Files:**
- Modify: `pkg/strategy/hedge_support.go`
- Modify: `pkg/strategy/matrix_relative_engine.go`
- Modify: `pkg/strategy/reconcile.go`

- [ ] **Step 1: `pkg/strategy/hedge_support.go` — 2 sites**

Line ~31 (`cancelTPForHedge`):
```go
	if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
```
becomes:
```go
	if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
```

Line ~57 (`cancelSLForHedge`):
```go
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
```
becomes:
```go
		if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
```

- [ ] **Step 2: `pkg/strategy/matrix_relative_engine.go` — 1 site**

Line ~228:
```go
	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
```
becomes:
```go
	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
```

- [ ] **Step 3: `pkg/strategy/reconcile.go` — 1 site (Pattern C, `AccountRunner` method)**

Line ~749, inside an `(ar *AccountRunner)` method (not a `StrategyRunner` one — confirm the enclosing function's receiver is `ar *AccountRunner` before editing, per this plan's own research):
```go
		if err := ar.tradeStream.CancelOrder(ctx, trader.CancelRequest{
```
becomes:
```go
		if err := ar.exchange.CancelOrder(ctx, trader.CancelRequest{
```

- [ ] **Step 4: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: identical to the established 93 PASS baseline.
Run: `go test -tags=integration ./pkg/strategy/...` — expected: same 2 pre-existing, unrelated failures, no growth.
Run: `grep -rn "sr\.runner\.tradeStream\b\|ar\.tradeStream\b" pkg/strategy/*.go` — expected: **no matches anywhere in the package** — this is the final check closing out all of #4b's order-write migration; if this returns anything, an order-write site was missed somewhere across the ENTIRE package, not just these 3 files, and must be found and migrated before this task is done.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/hedge_support.go pkg/strategy/matrix_relative_engine.go pkg/strategy/reconcile.go
git commit -m "$(cat <<'EOF'
refactor(strategy): migrate the last order placement/cancellation call sites onto Exchange()

Sub-plan #4b-3c — the final 4 order-write call sites in pkg/strategy
(hedge_support.go x2, matrix_relative_engine.go x1, reconcile.go x1, the
last one a direct AccountRunner-method access: ar.tradeStream → ar.exchange).
Same rationale as #4b-3a/#4b-3b (both merged): pure receiver-expression
swap, zero behavior change for Bybit accounts.

This closes out all of Plan #4b (pkg/strategy's full migration off
Bybit-specific free functions/tradeStream direct calls onto the generic
trader.Exchange interface). Only services/api-gateway (Plan #4c) remains
before Binance accounts can actually trade through this system.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not touch `services/api-gateway` — that's #4c, the last remaining phase before Binance accounts can trade (plus Plan #5's account-setup-on-connect and frontend flag flip).
