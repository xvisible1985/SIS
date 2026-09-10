# services/api-gateway Leverage/Position-Mode Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sub-plan #4c-2 — migrate `services/api-gateway`'s 2 leverage/position-mode call sites (`trader_handler.go`'s `TraderSetLeverage` and `TraderSwitchPositionMode` handlers) off the Bybit-specific `trader.SetLeverage(ctx, creds, ...)`/`trader.SwitchPositionMode(ctx, creds, ...)` free functions onto the generic `trader.Exchange` interface, using the `loadExchange` helper added by Plan #4c-1. This is the second of the three remaining #4c sub-plans (#4c-1 read-only, done; #4c-2 this plan; #4c-3 order placement/cancellation, the highest-stakes remaining piece).

**Architecture:** Both sites already load raw `trader.Credentials` via `s.loadCreds(r, req.AccountID, userID)` immediately before the migrated call, and — verified while writing this plan by reading each handler in full — `creds` is used *only* for that one call in each function; nothing downstream needs it. This is the same "Pattern A, simple rename" shape as most of #4c-1's Task 2 sites: swap `s.loadCreds` for the already-existing `s.loadExchange` helper (added in Plan #4c-1, unchanged by this plan), and swap `trader.X(ctx, creds, ...)` for `ex.X(ctx, ...)`.

Both `BybitExchange.SetLeverage`/`SwitchPositionMode` and `BinanceExchange.SetLeverage`/`SwitchPositionMode` already exist on the `trader.Exchange` interface (confirmed at `pkg/trader/exchange.go:35-36`, `pkg/trader/exchange.go:103-108`, `pkg/trader/binance/exchange.go:439/453`) and are already tested for both exchanges (`pkg/trader/exchange_test.go:242-273`, `pkg/trader/binance/exchange_test.go:500-557`) — this plan adds no new tests to `pkg/trader`/`pkg/trader/binance`, only migrates the 2 call sites.

**Tech Stack:** Go. No new tests beyond a small addition to the existing `services/api-gateway/exchange_resolution_test.go` (added by Plan #4c-1) is NOT needed here — that file tests `loadExchange`/`loadBotAccountExchange` themselves, which are unchanged by this plan. This plan's own correctness is covered by the existing handler-level tests plus the already-tested `Exchange` interface methods.

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4c-1 (`loadExchange` helper, merged as commit range `21c8ddf`..`3665703` into `fix/trade-attribution-orderlinkid`).

---

## Before you start

Read:
- `services/api-gateway/trader_handler.go:16-62` — `loadCreds` and `loadExchange` (both already exist; this plan uses `loadExchange` as-is, does not modify it).
- `services/api-gateway/trader_handler.go:213-285` — the two handlers this plan touches, `TraderSetLeverage` (starts ~213) and `TraderSwitchPositionMode` (starts ~256).

**In scope for this plan** (2 call sites, both verified against the live file while writing this plan — `creds` used only once, immediately, no downstream reuse in either function):
- `TraderSetLeverage` (line ~242): `trader.SetLeverage(r.Context(), creds, trader.LeverageRequest{...})`.
- `TraderSwitchPositionMode` (line ~280): `trader.SwitchPositionMode(r.Context(), creds, req.Category, req.Symbol, req.Mode)`.

**Explicitly OUT of scope for this plan:**
- Any `PlaceOrder`/`CancelOrder` call site, including `trader_handler.go`'s own `GetTradeStream`-based dual-path elsewhere in this same file — sub-plan #4c-3.
- The 4 `trader.GetPublicInstrumentInfo` call sites — no `Exchange`-interface equivalent, never migrate these.
- `loadCreds` itself, and every other one of its call sites in this file and others — left completely untouched; still legitimately used by the order-write paths deferred to #4c-3.

---

### Task 1: Migrate both call sites

**Files:**
- Modify: `services/api-gateway/trader_handler.go`

- [ ] **Step 1: `TraderSetLeverage` (line ~213-252)**

Current code (verified against the live file while writing this plan):
```go
	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if err := trader.SetLeverage(r.Context(), creds, trader.LeverageRequest{
		Symbol:       req.Symbol,
		Category:     req.Category,
		BuyLeverage:  req.Leverage,
		SellLeverage: req.Leverage,
	}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
```
becomes:
```go
	ex, err := s.loadExchange(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if err := ex.SetLeverage(r.Context(), trader.LeverageRequest{
		Symbol:       req.Symbol,
		Category:     req.Category,
		BuyLeverage:  req.Leverage,
		SellLeverage: req.Leverage,
	}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
```
Read the lines between the `creds`/`ex` declaration and the migrated call to reconfirm nothing else in the function uses `creds` — this plan's research found nothing, but re-verify against the live file since it may have drifted.

- [ ] **Step 2: `TraderSwitchPositionMode` (line ~256-285)**

Current code (verified against the live file while writing this plan):
```go
	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := trader.SwitchPositionMode(r.Context(), creds, req.Category, req.Symbol, req.Mode); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
```
becomes:
```go
	ex, err := s.loadExchange(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := ex.SwitchPositionMode(r.Context(), req.Category, req.Symbol, req.Mode); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
```
Note this handler's error-status convention is already different from `TraderSetLeverage`'s (`http.StatusForbidden` + `err.Error()` here, vs `http.StatusNotFound` + a fixed string there) — this is a **pre-existing difference between the two handlers**, not something this migration should normalize. Preserve each handler's existing status code and error message exactly as-is; only the credential-loading/call mechanism changes. `loadExchange` mirrors `loadCreds`'s error behavior exactly (same `"account not found"` / `"decrypt: %w"` error contents), so this preserves both handlers' current behavior unchanged.

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-plan baseline (0 new failures).
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth.
Run: `grep -n "trader\.SetLeverage(r\.Context(), creds\|trader\.SwitchPositionMode(r\.Context(), creds" services/api-gateway/*.go` — expected: **no matches** (confirms both sites migrated).
Run: `grep -n "trader\.SetLeverage(\|trader\.SwitchPositionMode(" services/api-gateway/*.go` — expected: no matches at all in `services/api-gateway` (these were the only 2 call sites of these free functions in the whole service — this plan's own research found no others; if this grep finds something not touched by this plan, investigate before considering the task done).

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/trader_handler.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate leverage/position-mode onto loadExchange

Sub-plan #4c-2 — the 2 leverage/position-mode call sites in
trader_handler.go (TraderSetLeverage, TraderSwitchPositionMode), migrated
from the Bybit-specific trader.SetLeverage/SwitchPositionMode(ctx, creds,
...) free functions onto trader.Exchange, using the loadExchange helper
added by Plan #4c-1. Both sites' creds variable was used only for the one
migrated call, so this is a simple rename (loadCreds -> loadExchange,
creds -> ex) at both. Each handler's own pre-existing error-status
convention (404 vs 403) is preserved exactly as before.

trader.Exchange's SetLeverage/SwitchPositionMode are already implemented
and tested for both BybitExchange and BinanceExchange (Plan #1/#4a), so
this migration itself adds no new tests. Zero behavior change for Bybit
accounts: BybitExchange.SetLeverage/SwitchPositionMode are one-line
delegations to the exact same free functions these call sites used
directly before.

This closes out sub-plan #4c-2. Only #4c-3 (order placement/cancellation)
remains before Plan #4c (and the whole Binance live-trading rollout) is
complete.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not touch any `PlaceOrder`/`CancelOrder` call site, including `trader_handler.go`'s own `GetTradeStream`-based dual-path used elsewhere in this same file for order placement — sub-plan #4c-3, which also needs a new `Engine.GetExchange()` accessor mirroring the existing `Engine.GetTradeStream()`.
- Does not touch `loadCreds` itself or any of its other call sites — still legitimately needed until #4c-3 migrates the order-write paths.
- Does not normalize the two handlers' differing error-status conventions (404 vs 403) — a pre-existing inconsistency, out of scope for a pure migration task.
