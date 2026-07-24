# Paired-Close Real-Time Trigger + WS Push Implementation Plan

**Goal:** Hedge/matrix paired-close (both bot kinds) triggers immediately when the combined-PnL+накопление threshold is crossed, instead of waiting up to 30s for the next engine tick — and the frontend's paired-close progress bar (and chart target-price line) update from the same live, backend-computed values via WebSocket instead of a 30s REST poll.

**Architecture:** A new, dedicated per-pair watcher (separate from the existing `hedgeWatches`/`hedgeTriggerCh` activation mechanism, which is untouched) subscribes to `signalEngine.PriceHub()` for each active hedge/matrix pair's symbol. On a price tick past the pair's precomputed threshold price — or on an event-driven накопление change notification — it does a **targeted** re-verify (fresh position + fresh накопление for just that pair, not a full account scan) and, if the condition still holds, closes the pair immediately. This is deliberately NOT routed through the existing 30s tick loop or its 2s activation debounce (that debounce exists to protect a full account-wide scan from thrashing, which doesn't apply to a single targeted pair check) — latency is bounded only by real Bybit/DB round-trip time (~50-300ms), gated by a per-pair in-flight flag (not a fixed delay) so overlapping triggers collapse into one in-progress check rather than queuing. The same watcher also computes ready-to-display `current`/`pct`/`target_price` and pushes them to all of that account's open WS connections via a new generic broadcast passthrough added to `pkg/trader.RunPositionStream`.

**Tech Stack:** Go (services/api-gateway, pkg/trader, pkg/signal), TypeScript/React (frontend), existing Postgres schema (no new tables/columns).

---

## Context: why this is safe on a live-money system

This session already found and fixed two real accuracy bugs in the CLIENT-SIDE version of this same math (ROI% margin missing `/leverage`; matrix накопление double-counted because both legs of a pair resolve to the same `hedge_sessions` row). Both caused the progress bar to show a misleading number without the bot actually misbehaving — the AKEUSDT case investigated live in this session is a direct example: the bar showed "100% reached" while the true value was ~43% of the threshold, and the bot was correctly *not* closing.

The design below eliminates this class of bug by construction: the backend computes the authoritative number once, in one place (the same formula `meetsPairedCloseCriteria` already uses), and both the immediate-trigger decision and the frontend display consume that single result. The frontend's own JS reimplementation of the formula is removed entirely rather than kept as a fallback — a stale/wrong client-computed number is worse than a brief empty state while the first WS message arrives (reasoning: a wrong number can mislead a trader into either false alarm or false confidence about a live position; an honest "no data yet" cannot).

## Affected mechanics (per project convention — list before implementing)

- **`pkg/signal`** (`PriceHub`/TickerHub): gains new subscribers (one per watched pair's symbol), no changes to the hub itself — same `Subscribe(symbol, callback)` pattern `hedgeWatches` already uses.
- **`services/api-gateway/hedge_engine.go`**: `hedgeEngineTick` already computes `newWatches` for activation (unchanged); it will also compute a parallel `pairedCloseWatches` map, on its own separate subscription set. The existing `hedgeWatches`/`hedgeTriggerCh`/2s-debounce activation mechanism is **not modified** — the new paired-close watcher is a sibling, not an extension of it.
- **`services/api-gateway/matrix_engine.go`**: `checkMatrixPairedClose`'s existing logic (already fixed in Tasks 6/7 this session) is reused as the actual verification+close call the fast path invokes — not re-derived, just called directly with the specific pair instead of iterated over the whole account.
- **`pkg/trader/ws.go`** (`RunPositionStream`): gains one new generic `select` case relaying an inbound broadcast channel to the connection — additive, does not touch the existing Bybit-relay/ping/reconnect logic.
- **`services/api-gateway/trader_ws_handler.go`**: creates and registers the per-connection broadcast channel before calling `RunPositionStream`, unregisters via `defer`.
- **`pkg/strategy` накопление write path (`AccumulateHedgeSessionPnl`, Tasks 2/5A/6/7 this session)**: gains one new line at the very end — an optional notification hook (package-level function variable, nil by default, registered once by `services/api-gateway` at startup). Does not change `AccumulateHedgeSessionPnl`'s existing logic, return value, or behavior when the hook is unset — see component 3 below for why this replaced an earlier periodic-poll design.
- **`frontend/src/types.ts`**: new `WsMsg` variant.
- **`frontend/src/components/strategies/HedgePairCard.tsx`**: `pairedCloseCurrent` and `pairedCloseTarget` (client-side formulas, including today's two bug fixes) are deleted; both the progress bar and the existing chart-target-price-line feature (`onPairTargetUpdate` → `hedgePairTarget` → `Chart.tsx`'s price line) switch to reading the new WS-pushed values.
- **`frontend` WS consumption** (wherever `WsMsg` is dispatched, e.g. `usePositionsWs`): needs to surface `paired_close` messages to `HedgePairCard`.

## Components

### 1. Go: price-threshold formula (port of existing JS math)

A pure function, mirroring `pairedCloseTarget`'s already-fixed JS logic (all 3 close-type modes, including the breakeven fee-adjustment term), taking both legs' entry/size/direction/leverage + close_type + threshold + накопление, returning the price at which `current == threshold`. Pure, easily unit-testable, no DB/network access.

### 2. Go: `pairedCloseWatchEntry` + `pairedCloseWatches` map

Computed once per 30s tick alongside the existing `newWatches` (activation), for every active hedge/matrix pair (both bot kinds). Holds both legs' strategy IDs/account ID/entry/size/direction/leverage, close_type, threshold, and the precomputed target price. Subscribed to `PriceHub` per symbol, same lifecycle (`applyHedgeWatches`-style subscribe/unsubscribe) as the existing activation watches — a separate map/subscription set, independent of it.

### 3. Go: two independent recompute triggers, scoped to one pair each

The same recompute-and-maybe-close routine runs from **two independent triggers**, each scoped to the ONE pair it concerns (not a full account scan), so накопление changes are visible even when price is quiet, and price crossings are visible independent of when накопление last changed:

- **Price tick** for a watched pair's symbol (throttled to ~1 recompute/sec per pair, using the cached накопление value, to avoid redoing work on every single tick in a fast market).
- **An event-driven накопление-change notification.** `AccumulateHedgeSessionPnl` (`pkg/strategy`) calls an optional package-level hook after it finishes writing — `services/api-gateway` registers this hook once at startup to update the matching pair's cached накопление and trigger a recompute for just that pair. Chosen over an earlier periodic-poll design specifically because of expected multi-tenant scale: a fixed-interval poll costs grow linearly with total active pairs platform-wide regardless of whether anything changed, while an event hook costs scale with actual trade-close events (inherently rare per pair) — better fit once there are many clients, not just one account. The hook is a single `if hook != nil { hook(stratID, netPnl) }` appended after `AccumulateHedgeSessionPnl`'s existing logic — doesn't alter its existing behavior, return value, or tests when unset (nil by default).

Either trigger recomputes `current`/`pct` from whatever's currently cached (latest price × latest накопление) and pushes the WS message (item 6) for that one pair. If the recomputed value has crossed the target price: a **targeted** verify-and-close runs — refetch fresh position data for just these two legs plus a fresh single-row накопление read, re-run `checkHedgeDeactivation`/`checkMatrixPairedClose`'s existing criteria logic, and if still true, close via the existing close call for that pair. Gated by a per-pair in-flight flag (an overlapping trigger while a check is already running is simply dropped — the in-flight one already has the freshest available data) rather than a fixed delay, and by a **per-account** bounded semaphore matching `matrixBatchCheckActivation`'s existing `sem := make(chan struct{}, 20)` pattern — scoped per account rather than platform-wide because Bybit's rate limits are per-API-key, so one account's burst of closes shouldn't throttle an unrelated account's.

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
- Go unit test: `AccumulateHedgeSessionPnl`'s existing tests must all still pass unmodified with the hook unset (default nil) — proves the new hook line is genuinely behavior-preserving by default. A new, separate test registers a hook and asserts it fires with the right strategy ID/netPnl after a successful accumulate call.
- Go unit test: the per-pair in-flight flag — a second trigger arriving while one verify-and-close is already running for that pair must be dropped (no overlapping/duplicate close attempts), proven with a controllable/blockable fake close call.
- Go unit test: the per-account semaphore caps concurrent verify-and-close operations at the configured limit; a burst of triggers for the same account queues rather than running unbounded, while a different account's trigger is unaffected.
- Go integration test: confirms a price tick crossing the precomputed target actually invokes the targeted verify-and-close path (not `hedgeTriggerCh` — that channel is no longer involved in this feature) and that the existing `checkHedgeDeactivation`/`checkMatrixPairedClose` logic is what makes the final call.
- Regression: full `pkg/strategy` + `services/api-gateway` unit+integration suites must stay green, with particular attention to the **existing, untouched** activation-watch tests (`hedgeWatches`/`hedgeTriggerCh`) — confirms the new paired-close watcher, being a separate sibling mechanism, has zero effect on activation's existing behavior.
- Frontend: `npx tsc --noEmit` clean; manual visual check in browser (project convention — no frontend unit-test framework currently in use) confirming the progress bar and chart target line both populate from WS and update live, for one hedge pair and one matrix pair.

## Deploy note

Same as every backend change this session: needs a full rebuild + `api-gateway` restart before it's live. This one also touches the position WS relay (`pkg/trader/ws.go`) — worth a quick sanity check after restart that ordinary position/order/execution/wallet streaming still works for a plain (non-paired) strategy too, since that code path is shared.
