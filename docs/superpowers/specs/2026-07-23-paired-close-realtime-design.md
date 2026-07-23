# Paired-Close Real-Time Trigger + WS Push Implementation Plan

**Goal:** Hedge/matrix paired-close (both bot kinds) triggers immediately when the combined-PnL+накопление threshold is crossed, instead of waiting up to 30s for the next engine tick — and the frontend's paired-close progress bar (and chart target-price line) update from the same live, backend-computed values via WebSocket instead of a 30s REST poll.

**Architecture:** Extend the existing `hedgeWatches`/`hedgeTriggerCh` reactive-wake mechanism (`services/api-gateway/hedge_engine.go`) with a parallel per-pair watcher subscribed to `signalEngine.PriceHub()`. On a price tick past the pair's precomputed threshold price, it wakes the existing 30s tick loop early (via `hedgeTriggerCh`) rather than closing the pair directly — the actual close decision stays with the existing, already-correct `checkHedgeDeactivation`/`checkMatrixPairedClose`, which re-reads live positions and `accumulated_pnl` fresh from the DB. The same watcher also computes ready-to-display `current`/`pct`/`target_price` and pushes them to all of that account's open WS connections via a new generic broadcast passthrough added to `pkg/trader.RunPositionStream`.

**Tech Stack:** Go (services/api-gateway, pkg/trader, pkg/signal), TypeScript/React (frontend), existing Postgres schema (no new tables/columns).

---

## Context: why this is safe on a live-money system

This session already found and fixed two real accuracy bugs in the CLIENT-SIDE version of this same math (ROI% margin missing `/leverage`; matrix накопление double-counted because both legs of a pair resolve to the same `hedge_sessions` row). Both caused the progress bar to show a misleading number without the bot actually misbehaving — the AKEUSDT case investigated live in this session is a direct example: the bar showed "100% reached" while the true value was ~43% of the threshold, and the bot was correctly *not* closing.

The design below eliminates this class of bug by construction: the backend computes the authoritative number once, in one place (the same formula `meetsPairedCloseCriteria` already uses), and both the immediate-trigger decision and the frontend display consume that single result. The frontend's own JS reimplementation of the formula is removed entirely rather than kept as a fallback — a stale/wrong client-computed number is worse than a brief empty state while the first WS message arrives (reasoning: a wrong number can mislead a trader into either false alarm or false confidence about a live position; an honest "no data yet" cannot).

## Affected mechanics (per project convention — list before implementing)

- **`pkg/signal`** (`PriceHub`/TickerHub): gains new subscribers (one per watched pair's symbol), no changes to the hub itself — same `Subscribe(symbol, callback)` pattern `hedgeWatches` already uses.
- **`services/api-gateway/hedge_engine.go`**: extends the existing watch/trigger mechanism. `hedgeEngineTick` already computes `newWatches` for activation; it will also compute a parallel `pairedCloseWatches` map. `hedgeTriggerCh`'s existing 2s debounce is reused as-is — no new debounce logic needed.
- **`services/api-gateway/matrix_engine.go`**: `checkMatrixPairedClose` is unchanged — it's the thing being woken early, its own logic (already fixed in Tasks 6/7 this session) is the authority.
- **`pkg/trader/ws.go`** (`RunPositionStream`): gains one new generic `select` case relaying an inbound broadcast channel to the connection — additive, does not touch the existing Bybit-relay/ping/reconnect logic.
- **`services/api-gateway/trader_ws_handler.go`**: creates and registers the per-connection broadcast channel before calling `RunPositionStream`, unregisters via `defer`.
- **`pkg/strategy` накопление write path (`AccumulateHedgeSessionPnl`, Tasks 2/5A/6/7 this session)**: **not touched.** Deliberately avoided threading a new cross-package notification through this already-validated code — see the periodic-reread note below.
- **`frontend/src/types.ts`**: new `WsMsg` variant.
- **`frontend/src/components/strategies/HedgePairCard.tsx`**: `pairedCloseCurrent` and `pairedCloseTarget` (client-side formulas, including today's two bug fixes) are deleted; both the progress bar and the existing chart-target-price-line feature (`onPairTargetUpdate` → `hedgePairTarget` → `Chart.tsx`'s price line) switch to reading the new WS-pushed values.
- **`frontend` WS consumption** (wherever `WsMsg` is dispatched, e.g. `usePositionsWs`): needs to surface `paired_close` messages to `HedgePairCard`.

## Components

### 1. Go: price-threshold formula (port of existing JS math)

A pure function, mirroring `pairedCloseTarget`'s already-fixed JS logic (all 3 close-type modes, including the breakeven fee-adjustment term), taking both legs' entry/size/direction/leverage + close_type + threshold + накопление, returning the price at which `current == threshold`. Pure, easily unit-testable, no DB/network access.

### 2. Go: `pairedCloseWatchEntry` + `pairedCloseWatches` map

Computed once per 30s tick alongside the existing `newWatches` (activation), for every active hedge/matrix pair (both bot kinds). Holds both legs' strategy IDs/account ID/entry/size/direction/leverage, close_type, threshold, and the precomputed target price. Subscribed to `PriceHub` per symbol, same lifecycle (`applyHedgeWatches`-style subscribe/unsubscribe) as the existing activation watches — coexists as a separate map/subscription set, does not replace it.

### 3. Go: two independent recompute triggers → wake, not close

The same recompute-and-push routine (item 5) runs from **two independent triggers**, so накопление changes are visible even when price is quiet, and price crossings are visible even between накопление refreshes:

- **Price tick** for a watched pair's symbol (throttled to ~1 recompute/sec per pair to avoid redoing work on every single tick in a fast market), using the cached накопление value.
- **A periodic накопление refresh timer** (~2s per pair), independent of price ticks — re-reads `hedge_sessions.accumulated_pnl` from the DB and updates the cache. This is the refinement noted above: no push-notification hook into the trade-recording code, just a cheap periodic DB read, since накопление only changes on discrete trade-close events and a ~2s worst-case staleness is an acceptable trade for not touching already-validated code.

Either trigger recomputes `current`/`pct` from whatever's currently cached (latest price × latest накопление) and pushes the WS message (item 6). If the recomputed value has crossed the target, either trigger also sends (non-blocking, already-pending-skip) to `hedgeTriggerCh` — same channel activation watching already uses, same existing 2s debounce on the consumer side.

### 4. Go: per-account WS broadcast registry

New fields on `Server` (mirroring `hedgeWatchMu`/`hedgeWatches`): a mutex-protected `map[accountID][]chan any`. `Subscribe(accountID) (ch chan any, unsub func())` / used by `trader_ws_handler.go` around its `RunPositionStream` call. Broadcast is non-blocking per-subscriber (`select { case ch <- msg: default: }`), matching the existing non-blocking philosophy already used for the Bybit-read goroutine in the same file.

### 5. Go: `RunPositionStream` passthrough case

One new `select` case in the existing loop:
```go
case m := <-broadcastCh:
    safeSend(conn, m)
```
Generic — doesn't know about "paired close" semantics, just relays whatever's pushed. Reusable for future broadcast needs.

### 6. WS message shape

```json
{ "type": "paired_close", "main_strategy_id": "...", "hedge_strategy_id": "...",
  "current": 1.98, "threshold": 2.00, "close_type": 2, "pct": 99,
  "target_price": 0.002418 }
```
Pushed for all of an account's active pairs whenever any one of them recomputes (not gated per-pair per-connection — frontend filters by strategy IDs of whatever's currently rendered).

### 7. Frontend

New `WsMsg` variant in `types.ts`. `HedgePairCard.tsx`: delete `pairedCloseCurrent`/`pairedCloseTarget` useMemos entirely; `PairedCloseProgress` and the chart-line `onPairTargetUpdate` call both read from a small local state keyed by this pair's `main_strategy_id`/`hedge_strategy_id`, updated by a WS message listener. No fallback value — renders nothing until the first message arrives (matches how накопление already renders "—" while loading elsewhere in this card, so this isn't a new UX pattern for the app).

## Testing

- Go unit tests: price-threshold formula, all 3 close-type modes + edge cases (perfectly-hedged equal sizes → no finite price; threshold ≤ 0 for modes where that's reachable) — parity-checked against the already-fixed JS formula's known-good outputs from this session's manual verification.
- Go unit tests: WS broadcast registry (subscribe/broadcast/unsubscribe/non-blocking-drop-when-full) using plain channels, no real network.
- Go integration test: confirms a price tick crossing the precomputed target sends to `hedgeTriggerCh` (reusing the existing `hedgePriceCallback` test pattern if one exists, or the same nil-runner/panic-site style established this session where a real DB is needed).
- Regression: full `pkg/strategy` + `services/api-gateway` unit+integration suites must stay green, with particular attention to the **existing** activation-watch tests (`hedgeWatches`/`hedgeTriggerCh` for entry-price activation) — this feature shares that channel and must not change its existing debounce/trigger behavior for activation.
- Frontend: `npx tsc --noEmit` clean; manual visual check in browser (project convention — no frontend unit-test framework currently in use) confirming the progress bar and chart target line both populate from WS and update live, for one hedge pair and one matrix pair.

## Deploy note

Same as every backend change this session: needs a full rebuild + `api-gateway` restart before it's live. This one also touches the position WS relay (`pkg/trader/ws.go`) — worth a quick sanity check after restart that ordinary position/order/execution/wallet streaming still works for a plain (non-paired) strategy too, since that code path is shared.
