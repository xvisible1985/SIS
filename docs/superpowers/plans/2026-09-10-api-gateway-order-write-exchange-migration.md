# services/api-gateway Order Write (PlaceOrder/CancelOrder) Exchange Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sub-plan #4c-3 — the last and highest-stakes remaining piece of Plan #4c. Migrates all 10 remaining `trader.PlaceOrder(ctx, creds, ...)`/`trader.CancelOrder(ctx, creds, ...)` call sites in `services/api-gateway` onto the generic `trader.Exchange` interface, so order placement/cancellation works for Binance accounts, not just Bybit. This closes out Plan #4c entirely — after this plan merges, only Plan #5 (Binance account setup on connect + frontend flag flip) remains before Binance accounts can actually trade through this system.

**Architecture — 3 distinct shapes, discovered by tracing every call site's full call graph before writing this plan (not just the immediate ~10 lines around each call):**

1. **Simple leaf sites** (`strategy_handler.go`'s `DetachFromBot`): `creds` loaded and used once, nowhere else — a plain rename, same as most of #4c-1/#4c-2.

2. **Deep call-graph threading** (`hedge_engine.go`, `matrix_engine.go`, `rescue_engine.go`): the order-write call sites sit several function calls deep below where `creds`/`ex` is resolved. `processHedgeBot`, `processMatrixBot`, and `verifyAndClosePairedBot` (the three "root" per-tick functions) already resolve a local `ex trader.Exchange` (added by Plan #4c-1's Pattern-B treatment) that today is used ONLY for `FetchPositions` and dropped — never threaded further. This plan threads `ex` the rest of the way down each call chain to the actual write call, and — since after this migration `creds` becomes genuinely unused along the ENTIRE remaining chain in each case (confirmed function-by-function below, including two functions that already don't use their `creds` parameter at all: `checkHedgeForceStandaloneActivation`, `ensureMatrixStrategies`) — removes `creds` and the now-redundant `loadBotAccountCreds` call entirely, rather than leaving it as dead weight. This was a deliberate choice confirmed with the user: full cleanup over minimal diff, since it eliminates a real per-tick DB round-trip + decrypt that Plan #4c-1's own commit messages already flagged as temporary ("until #4c-3 migrates the downstream calls and removes creds entirely").

3. **The `trader_handler.go` dual-path** (`TraderPlaceOrder`, `TraderCancelOrder`): today these check `s.engine.GetTradeStream(accountID)` — if a live `AccountRunner` exists for the account (i.e. it has an active strategy running), use its already-connected WS trade stream (`ts.PlaceOrder`/`ts.CancelOrder`) for speed; otherwise fall back to the raw REST free function directly (`trader.PlaceOrder(ctx, creds, req)`). This plan adds a new `Engine.GetExchange(accountID) trader.Exchange` accessor (mirroring the existing `Engine.GetTradeStream`) that returns the live runner's already-resolved `Exchange` (`runner.exchange`, always non-nil once a runner exists — same invariant as `runner.tradeStream`, confirmed at `pkg/strategy/engine.go:868`). When no live runner exists, this plan falls back to `s.loadExchange(...)` and calls `.PlaceOrderREST(...)`/`.CancelOrderREST(...)` — the REST-only methods, matching the old fallback's REST-only behavior exactly (see "Why PlaceOrder vs PlaceOrderREST" below). **Ownership verification must still happen unconditionally before EITHER branch** — the old code always ran `s.loadCreds(...)` first (which checks `owner_id`) even when the live-runner path was taken, specifically so an authenticated user can't place/cancel orders on an account they don't own just because a live runner happens to exist for it. This plan preserves that ordering exactly; see Task 5.

**Why `PlaceOrder`/`CancelOrder` (WS-preferred) for the live-runner path but `PlaceOrderREST`/`CancelOrderREST` for everything else:** `pkg/trader/exchange.go`'s `Exchange` interface documents this split explicitly (`exchange.go:14-26`): `PlaceOrder`/`CancelOrder` are "WS-backed... the live strategy engine's own path," while `PlaceOrderREST`/`CancelOrderREST` are "REST-only... used by user-facing/admin handlers... that don't hold a live trade-WS connection for the account." For `BybitExchange`, `PlaceOrder` delegates to `e.ws.PlaceOrder` (`*TradeStream`, which internally falls back to REST on its own if not connected — see `pkg/trader/trade_ws.go:219-266`), while `PlaceOrderREST` calls the REST free function directly with no WS attempt at all. For `BinanceExchange`, `PlaceOrder` and `PlaceOrderREST` are currently IDENTICAL (`pkg/trader/binance/exchange.go:265-271`, both call the same private `placeOrder`, REST-only — no WS order placement implemented for Binance yet). So for every site EXCEPT `trader_handler.go`'s two, using `.PlaceOrderREST()`/`.CancelOrderREST()` is both correct (matches old behavior exactly — these sites never attempted WS before) and slightly cheaper (skips a pointless WS-send-attempt-then-fallback dance on an unconnected `TradeStream` for freshly-resolved Bybit exchanges). For `trader_handler.go`'s dual-path specifically, using `.PlaceOrder()`/`.CancelOrder()` on the LIVE runner's `ex` preserves the original performance-motivated behavior (this user's stated priority: prefer WS over REST, minimize round-trips) of using the already-open WS connection when one exists.

**Tech Stack:** Go. No new tests for `pkg/trader`/`pkg/trader/binance` — `PlaceOrderREST`/`CancelOrderREST`/`PlaceOrder`/`CancelOrder` are already implemented and tested for both exchanges (Plans #1/#4a). 3 existing test call sites in `services/api-gateway/matrix_strategy_limits_test.go` need a small signature-following update (Task 3).

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4c-1 (`loadExchange`, `loadBotAccountExchange`, merged), Plan #4c-2 (leverage/position-mode, merged), Plan #4a (`AccountRunner.Exchange()`, `pkg/strategy.ResolveExchange`).

---

## Before you start

Read:
- `pkg/trader/exchange.go` in full — the `Exchange` interface, especially the doc comments on lines 14-26 explaining the WS-vs-REST method split, and `BybitExchange`'s implementations (lines 67-81 for `PlaceOrder`/`CancelOrder`, and further down for `PlaceOrderREST`/`CancelOrderREST` — read the whole file to find them).
- `pkg/trader/trade_ws.go:219-266` — `TradeStream.PlaceOrder`'s internal WS-then-REST-fallback logic, to understand why `BybitExchange.PlaceOrder` is safe to call even on a freshly-resolved (never-connected) exchange.
- `pkg/strategy/engine.go:356-365` — the existing `Engine.GetTradeStream`, which Task 1's `GetExchange` mirrors exactly.
- `pkg/strategy/engine.go:877-880` — `AccountRunner.Exchange()`, already existing.
- `pkg/strategy/engine.go:868` — confirms `runner.exchange` is set unconditionally during `AccountRunner` construction (same invariant as `runner.tradeStream`).

**All 10 in-scope call sites** (verified against the live files while writing this plan, full call-graph traced for each — see per-task sections below for exact line numbers and code):
- `hedge_engine.go`: 5x `trader.CancelOrder`, all inside `cancelStrategyOrders` (Task 2).
- `matrix_engine.go`: 1x `trader.PlaceOrder`, inside `stopMatrixPair` (Task 3).
- `rescue_engine.go`: 1x `trader.PlaceOrder`, inside `checkRescuePartialClose` (Task 2).
- `strategy_handler.go`: 1x `trader.PlaceOrder`, inside `DetachFromBot` (Task 4).
- `trader_handler.go`: 2x, the `GetTradeStream`-based dual-path in `TraderPlaceOrder`/`TraderCancelOrder` (Task 5).

Confirmed via research (grep across all of `services/api-gateway/*.go`): **zero** call sites use `trader.PlaceOrderBatch`, `trader.CancelOrderBatch`, or `trader.CancelAllOrders` — nothing else to migrate beyond these 10.

**Functions confirmed to have a `creds` parameter that is already, independently of this migration, unused in their body** (found while tracing the call graphs — not caused by this migration, but this plan's cleanup naturally removes them since it's already touching these exact functions' signatures):
- `checkHedgeForceStandaloneActivation` (`hedge_engine.go`) — Task 2.
- `ensureMatrixStrategies` (`matrix_engine.go`) — Task 3.

**Explicitly OUT of scope for this plan:**
- The 4 `trader.GetPublicInstrumentInfo` call sites (including the one inside `checkRescuePartialClose` itself, at `rescue_engine.go:309`) — no `Exchange`-interface equivalent, never migrate these (established and confirmed correct throughout this whole migration series).
- Any behavior change to WS-vs-REST selection logic beyond the direct method-name mapping described above — this plan does not add WS order placement for Binance, does not change Bybit's existing WS-fallback logic in `TradeStream`, and does not change which accounts get a live `AccountRunner`.
- General code cleanup unrelated to `creds`/`ex` threading (e.g. `matrixLegCloseRequest`'s builder logic, `rescuePartialCloseRequest`'s builder logic) — untouched, out of scope.

---

### Task 1: `pkg/strategy` — add `Engine.GetExchange` accessor

**Files:**
- Modify: `pkg/strategy/engine.go`

- [ ] **Step 1: Add `GetExchange` right after the existing `GetTradeStream`**

Current code at `pkg/strategy/engine.go:356-365` (verified against the live file while writing this plan):
```go
// GetTradeStream returns the trade WS stream for an account, or nil if no runner exists.
func (e *Engine) GetTradeStream(accountID string) *trader.TradeStream {
	e.mu.RLock()
	runner := e.runners[accountID]
	e.mu.RUnlock()
	if runner == nil {
		return nil
	}
	return runner.tradeStream
}
```
Add immediately after it:
```go

// GetExchange returns the resolved trader.Exchange for an account's live runner, or nil
// if no runner exists. Mirrors GetTradeStream — runner.exchange is set unconditionally
// during AccountRunner construction (same invariant as runner.tradeStream), so this never
// returns a non-nil runner with a nil exchange.
func (e *Engine) GetExchange(accountID string) trader.Exchange {
	e.mu.RLock()
	runner := e.runners[accountID]
	e.mu.RUnlock()
	if runner == nil {
		return nil
	}
	return runner.exchange
}
```

- [ ] **Step 2: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/strategy/... -v 2>&1 | tail -100` — expected: identical to the established baseline (no regressions; this is a pure addition, no existing code touched).

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/engine.go
git commit -m "$(cat <<'EOF'
feat(strategy): add Engine.GetExchange accessor

Sub-plan #4c-3, Task 1 — mirrors the existing Engine.GetTradeStream,
returning a live AccountRunner's already-resolved trader.Exchange instead
of its raw *TradeStream. Needed so trader_handler.go's PlaceOrder/
CancelOrder dual-path (Task 5) can prefer a live runner's connection the
same way it already does today via GetTradeStream, but through the
Exchange interface so it works for Binance accounts too.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `hedge_engine.go`'s order-write chain + `rescue_engine.go`'s `PlaceOrder` site

**Files:**
- Modify: `services/api-gateway/hedge_engine.go`
- Modify: `services/api-gateway/rescue_engine.go`

**Why these two files are one task:** `checkRescuePartialClose` (rescue_engine.go) is called from `processHedgeBot` (hedge_engine.go) at the same call site this task is already editing (`processHedgeBot`'s body, to remove its now-redundant `loadBotAccountCreds` call). Splitting these into separate tasks would leave `processHedgeBot` in a broken intermediate state between commits. Both files change together, one commit.

**Call chain being migrated** (traced in full while writing this plan):
```
processHedgeBot (hedge_engine.go:134)
  ├─→ checkHedgeActivation (hedge_engine.go:927)
  │     ├─→ resolveHedgeSlotConflict (hedge_engine.go:1522)
  │     │     └─→ cancelStrategyOrders (hedge_engine.go:1610)   ← 5x trader.CancelOrder
  │     └─→ checkHedgeForceStandaloneActivation (hedge_engine.go:1245)   ← creds param UNUSED in body, remove
  └─→ checkRescuePartialClose (rescue_engine.go:218)   ← 1x trader.PlaceOrder
```

- [ ] **Step 1: `cancelStrategyOrders` — replace `creds` with `ex`, migrate all 5 calls**

Current signature and all 5 call sites at `hedge_engine.go:1610-1715` (verified against the live file while writing this plan; `creds` is used ONLY for these 5 calls in this function — confirm this still holds by re-reading the function in full before editing, since it's long):
```go
func (s *Server) cancelStrategyOrders(ctx context.Context, botID, stratID, symbol, category string, creds trader.Credentials) (cancelled, errors int) {
```
becomes:
```go
func (s *Server) cancelStrategyOrders(ctx context.Context, botID, stratID, symbol, category string, ex trader.Exchange) (cancelled, errors int) {
```
Then, each of the 5 calls:
```go
		if err := trader.CancelOrder(ctx, creds, trader.CancelRequest{
```
(at line ~1625) becomes:
```go
		if err := ex.CancelOrderREST(ctx, trader.CancelRequest{
```
```go
		err1 := trader.CancelOrder(ctx, creds, trader.CancelRequest{
```
(at line ~1642) becomes:
```go
		err1 := ex.CancelOrderREST(ctx, trader.CancelRequest{
```
```go
			err2 := trader.CancelOrder(ctx, creds, trader.CancelRequest{
```
(at line ~1646) becomes:
```go
			err2 := ex.CancelOrderREST(ctx, trader.CancelRequest{
```
```go
		if err := trader.CancelOrder(ctx, creds, trader.CancelRequest{
```
(at line ~1691) becomes:
```go
		if err := ex.CancelOrderREST(ctx, trader.CancelRequest{
```
```go
			trader.CancelOrder(ctx, creds, trader.CancelRequest{ //nolint:errcheck
```
(at line ~1703) becomes:
```go
			ex.CancelOrderREST(ctx, trader.CancelRequest{ //nolint:errcheck
```
(the request-body fields inside each `trader.CancelRequest{...}` literal are unchanged — only the receiver expression and method name change, per all 5 blocks above.)

- [ ] **Step 2: `resolveHedgeSlotConflict` — replace `creds` with `ex`, thread into `cancelStrategyOrders`**

Current signature at `hedge_engine.go:1522` and the single call site at `hedge_engine.go:1579` (verified against the live file):
```go
func (s *Server) resolveHedgeSlotConflict(ctx context.Context, botID, accountID, symbol, hedgeDir string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) (string, bool) {
```
becomes:
```go
func (s *Server) resolveHedgeSlotConflict(ctx context.Context, botID, accountID, symbol, hedgeDir string, cfg botCfgJSON, ex trader.Exchange, posMap map[string]map[string]hedgePosInfo) (string, bool) {
```
```go
		cancelled, cancelErrors := s.cancelStrategyOrders(ctx, botID, conflictID, symbol, category, creds)
```
becomes:
```go
		cancelled, cancelErrors := s.cancelStrategyOrders(ctx, botID, conflictID, symbol, category, ex)
```
Re-read the whole function (`hedge_engine.go:1522-1604`) before editing to reconfirm `creds` has no other use besides this one call — this plan's research found none (the `case 0`/`case 1` branches never touch it).

- [ ] **Step 3: `checkHedgeActivation` — replace `creds` with `ex`, thread into `resolveHedgeSlotConflict`; drop `creds` from the `checkHedgeForceStandaloneActivation` call**

Current signature at `hedge_engine.go:927` (verified against the live file):
```go
func (s *Server) checkHedgeActivation(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo, watches map[string]hedgeWatchEntry) {
```
becomes:
```go
func (s *Server) checkHedgeActivation(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, ex trader.Exchange, posMap map[string]map[string]hedgePosInfo, watches map[string]hedgeWatchEntry) {
```
The call at `hedge_engine.go:1160`:
```go
			suspendedSlotID, ok := s.resolveHedgeSlotConflict(ctx, botID, accountID, pos.Symbol, hedgeDir, cfg, creds, posMap)
```
becomes:
```go
			suspendedSlotID, ok := s.resolveHedgeSlotConflict(ctx, botID, accountID, pos.Symbol, hedgeDir, cfg, ex, posMap)
```
The call at `hedge_engine.go:1237` (this one DROPS the argument entirely, since `checkHedgeForceStandaloneActivation`'s own `creds` param is removed in Step 4 below):
```go
		s.checkHedgeForceStandaloneActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, delistSymbols, cfg, creds)
```
becomes:
```go
		s.checkHedgeForceStandaloneActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, delistSymbols, cfg)
```

- [ ] **Step 4: `checkHedgeForceStandaloneActivation` — drop the unused `creds` parameter entirely**

Current signature at `hedge_engine.go:1245` (verified: `creds` does not appear anywhere else in this function's body, lines 1245-1306 — it is a pre-existing unused parameter, not caused by this migration, but this plan is already touching this function's only call site so removes it now rather than leaving dead weight):
```go
func (s *Server) checkHedgeForceStandaloneActivation(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist, delistSymbols []string, cfg botCfgJSON, creds trader.Credentials) {
```
becomes:
```go
func (s *Server) checkHedgeForceStandaloneActivation(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist, delistSymbols []string, cfg botCfgJSON) {
```
No other change needed in this function's body. Confirmed via this plan's research: no test file calls this function directly.

- [ ] **Step 5: `processHedgeBot` — drop `loadBotAccountCreds`, thread the existing local `ex` into both downstream calls**

Current code at `hedge_engine.go:134-170` (verified against the live file while writing this plan):
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

	posMap, badPositions := buildHedgePosMap(rawPositions)

	for _, p := range badPositions {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: позиция %s %s (size=%s) отфильтрована — невалидный avgPrice=%q или markPrice=%q",
				p.Symbol, p.Side, p.Size, p.EntryPrice, p.MarkPrice),
			"warn", "system")
	}

	s.checkHedgeDeactivation(ctx, botID, accountID, cfg, posMap)
	s.checkHedgeActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap, watches)
	s.buildPairedCloseWatches(ctx, botID, accountID, "hedge", cfg, posMap, pairedWatches)
	if cfg.RescuePartialCloseEnabled {
		s.checkRescuePartialClose(ctx, botID, accountID, cfg, posMap)
	}
}
```
becomes:
```go
func (s *Server) processHedgeBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, watches map[string]hedgeWatchEntry, pairedWatches map[string]pairedCloseWatchEntry) {
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

	posMap, badPositions := buildHedgePosMap(rawPositions)

	for _, p := range badPositions {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: позиция %s %s (size=%s) отфильтрована — невалидный avgPrice=%q или markPrice=%q",
				p.Symbol, p.Side, p.Size, p.EntryPrice, p.MarkPrice),
			"warn", "system")
	}

	s.checkHedgeDeactivation(ctx, botID, accountID, cfg, posMap)
	s.checkHedgeActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, ex, posMap, watches)
	s.buildPairedCloseWatches(ctx, botID, accountID, "hedge", cfg, posMap, pairedWatches)
	if cfg.RescuePartialCloseEnabled {
		s.checkRescuePartialClose(ctx, botID, accountID, cfg, ex, posMap)
	}
}
```
Note: the entire `creds, err := s.loadBotAccountCreds(ctx, accountID)` block (including its error-handling `if`) is deleted — not kept alongside `ex`. This is safe because, after Steps 1-4 and Step 6 below, nothing downstream of `processHedgeBot` uses `creds` anymore.

- [ ] **Step 6: `checkRescuePartialClose` — replace its own `loadBotAccountCreds` with the `ex` parameter from `processHedgeBot`**

Current code at `rescue_engine.go:218-258` (verified against the live file while writing this plan; showing the signature, the point where sessions are queried, and where `creds` is loaded — the middle of the function, between the DB query and the loop, is unchanged and omitted here for brevity, but re-read the whole function before editing):
```go
func (s *Server) checkRescuePartialClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, posMap map[string]map[string]hedgePosInfo) {
	type sessionRow struct {
		hedgeStratID    string
		accumulatedPnl  float64
		mainReducedUsdt float64
		lastCloseAt     *time.Time
		symbol          string
		mainDir         string
	}

	rows, err := s.pool.Query(ctx, `
		SELECT hs.hedge_strategy_id, hs.accumulated_pnl, hs.main_reduced_usdt,
		       hs.last_partial_close_at, ms.symbol, ms.direction
		FROM hedge_sessions hs
		JOIN strategies ms ON ms.id = hs.main_strategy_id
		JOIN strategies hst ON hst.id = hs.hedge_strategy_id
		WHERE hs.bot_id = $1 AND hs.ended_at IS NULL
		  AND ms.status IN ('active','finishing') AND hst.status IN ('active','finishing')`,
		botID)
	if err != nil {
		log.Printf("checkRescuePartialClose [bot %s]: query: %v", botID, err)
		return
	}
	defer rows.Close()

	var sessions []sessionRow
	for rows.Next() {
		var sr sessionRow
		if err := rows.Scan(&sr.hedgeStratID, &sr.accumulatedPnl, &sr.mainReducedUsdt,
			&sr.lastCloseAt, &sr.symbol, &sr.mainDir); err != nil {
			continue
		}
		sessions = append(sessions, sr)
	}
	rows.Close()

	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		log.Printf("checkRescuePartialClose [bot %s]: creds: %v", botID, err)
		return
	}

	for _, sr := range sessions {
```
becomes (only the signature and the deleted `creds`-loading block change — everything else, including the query above it and the loop below it, is unchanged):
```go
func (s *Server) checkRescuePartialClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, ex trader.Exchange, posMap map[string]map[string]hedgePosInfo) {
	type sessionRow struct {
		hedgeStratID    string
		accumulatedPnl  float64
		mainReducedUsdt float64
		lastCloseAt     *time.Time
		symbol          string
		mainDir         string
	}

	rows, err := s.pool.Query(ctx, `
		SELECT hs.hedge_strategy_id, hs.accumulated_pnl, hs.main_reduced_usdt,
		       hs.last_partial_close_at, ms.symbol, ms.direction
		FROM hedge_sessions hs
		JOIN strategies ms ON ms.id = hs.main_strategy_id
		JOIN strategies hst ON hst.id = hs.hedge_strategy_id
		WHERE hs.bot_id = $1 AND hs.ended_at IS NULL
		  AND ms.status IN ('active','finishing') AND hst.status IN ('active','finishing')`,
		botID)
	if err != nil {
		log.Printf("checkRescuePartialClose [bot %s]: query: %v", botID, err)
		return
	}
	defer rows.Close()

	var sessions []sessionRow
	for rows.Next() {
		var sr sessionRow
		if err := rows.Scan(&sr.hedgeStratID, &sr.accumulatedPnl, &sr.mainReducedUsdt,
			&sr.lastCloseAt, &sr.symbol, &sr.mainDir); err != nil {
			continue
		}
		sessions = append(sessions, sr)
	}
	rows.Close()

	for _, sr := range sessions {
```
Note this also removes the `if len(sessions) == 0 { return early }`-style short-circuit that loading creds AFTER the query provided (the old code deliberately loaded creds only if there might be sessions to act on) — but since resolving `ex` no longer costs a DB round-trip/decrypt here (it's now just a parameter, resolved once by the caller), there's no equivalent laziness to preserve. This is fine: `ex` costs nothing to have on hand even when `sessions` turns out empty.

Then, the migrated call further down the function, at `rescue_engine.go:352` (verified against the live file):
```go
		result, err := trader.PlaceOrder(ctx, creds, orderReq)
```
becomes:
```go
		result, err := ex.PlaceOrderREST(ctx, orderReq)
```

Finally, update `processHedgeBot`'s call site — already shown in Step 5's after-code above: `s.checkRescuePartialClose(ctx, botID, accountID, cfg, ex, posMap)`.

- [ ] **Step 7: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline, 0 new failures.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth.
Run: `grep -n "trader\.CancelOrder(ctx, creds\|trader\.PlaceOrder(ctx, creds" services/api-gateway/hedge_engine.go services/api-gateway/rescue_engine.go` — expected: **no matches** (confirms all 6 sites in these 2 files migrated).
Run: `grep -n "\bcreds\b" services/api-gateway/hedge_engine.go` — manually inspect every remaining hit. Expected: `creds` should no longer appear in `processHedgeBot`, `checkHedgeActivation`, `checkHedgeForceStandaloneActivation`, `resolveHedgeSlotConflict`, `cancelStrategyOrders`, or `checkRescuePartialClose`'s signature. It is EXPECTED to still appear in `verifyAndClosePairedBot` (Task 3 handles that one) and in unrelated functions elsewhere in the file (e.g. `processMatrixBot` doesn't live here, but other unrelated hedge-engine functions with their own independent `creds` may exist — check that any remaining hits are genuinely unrelated to this task's chain, not a missed site).

- [ ] **Step 8: Commit**

```bash
git add services/api-gateway/hedge_engine.go services/api-gateway/rescue_engine.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate hedge order-cancel chain and rescue PlaceOrder onto Exchange

Sub-plan #4c-3, Task 2 — migrates the 6 order-write call sites reachable
from processHedgeBot: 5x trader.CancelOrder inside cancelStrategyOrders
(hedge_engine.go), reached via resolveHedgeSlotConflict and
checkHedgeActivation, and 1x trader.PlaceOrder inside
checkRescuePartialClose (rescue_engine.go).

processHedgeBot already resolved a local ex trader.Exchange (added by Plan
#4c-1 for FetchPositions) that was never threaded further. This plan
threads it the rest of the way down both call chains and removes the
processHedgeBot's now-redundant loadBotAccountCreds call and every
downstream creds parameter entirely, since after this migration nothing
in either chain needs it anymore -- including checkHedgeForceStandaloneActivation,
whose creds parameter was already unused in its body before this change
(a pre-existing dead parameter, cleaned up here since its only call site
was already being touched). This eliminates a real per-tick DB round-trip
+ decrypt that Plan #4c-1's own commit explicitly flagged as temporary.

Zero behavior change for Bybit accounts: BybitExchange.CancelOrderREST/
PlaceOrderREST are one-line delegations to the exact same free functions
these call sites used directly before.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `matrix_engine.go`'s order-write chain (+ `verifyAndClosePairedBot` in `hedge_engine.go`)

**Files:**
- Modify: `services/api-gateway/matrix_engine.go`
- Modify: `services/api-gateway/hedge_engine.go`
- Modify: `services/api-gateway/matrix_strategy_limits_test.go`

**Why `hedge_engine.go` is touched again in this task:** `verifyAndClosePairedBot` (in `hedge_engine.go`) is a SECOND, independent root that also calls `checkMatrixPairedClose` (a price-callback-triggered fast path, separate from `processMatrixBot`'s per-tick path) — both converge on the same `checkMatrixPairedClose`/`stopMatrixPair` functions being migrated here. It must be updated in the same commit as `checkMatrixPairedClose`'s signature change, or the build breaks.

**Call chain being migrated** (traced in full while writing this plan):
```
processMatrixBot (matrix_engine.go:48)              ─┐
verifyAndClosePairedBot (hedge_engine.go:673)        ─┼─→ checkMatrixPairedClose (matrix_engine.go:226)
                                                       │     └─→ stopMatrixPair (matrix_engine.go:324)   ← 1x trader.PlaceOrder
processMatrixBot also calls:
  └─→ ensureMatrixStrategies (matrix_engine.go:481)   ← creds param UNUSED in body, remove (+ 3 test call sites)
```

- [ ] **Step 1: `stopMatrixPair` — replace `creds` with `ex`, migrate the 1 call**

Current signature and the call at `matrix_engine.go:324-362` (verified against the live file while writing this plan; showing the relevant lines only — the surrounding loop/comment structure is unchanged):
```go
func (s *Server) stopMatrixPair(ctx context.Context, botID, accountID, symbol, longID, shortID string, creds trader.Credentials, category string, longPos, shortPos hedgePosInfo) {
```
becomes:
```go
func (s *Server) stopMatrixPair(ctx context.Context, botID, accountID, symbol, longID, shortID string, ex trader.Exchange, category string, longPos, shortPos hedgePosInfo) {
```
```go
		if _, err := trader.PlaceOrder(ctx, creds, req); err != nil {
```
(inside the `for _, leg := range [...]` loop, ~line 362) becomes:
```go
		if _, err := ex.PlaceOrderREST(ctx, req); err != nil {
```

- [ ] **Step 2: `checkMatrixPairedClose` — replace `creds` with `ex`, thread into `stopMatrixPair`**

Current signature at `matrix_engine.go:226` and the single call site at `matrix_engine.go:290` (verified against the live file):
```go
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) map[string]bool {
```
becomes:
```go
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, ex trader.Exchange, posMap map[string]map[string]hedgePosInfo) map[string]bool {
```
```go
			s.stopMatrixPair(ctx, botID, accountID, sym, p.longID, p.shortID, creds, category, longPos, shortPos)
```
becomes:
```go
			s.stopMatrixPair(ctx, botID, accountID, sym, p.longID, p.shortID, ex, category, longPos, shortPos)
```

- [ ] **Step 3: `ensureMatrixStrategies` — drop the unused `creds` parameter entirely**

Current signature at `matrix_engine.go:481` (verified: grepping the full function body for `creds` returns only the parameter declaration itself — it is unused, a pre-existing situation not caused by this migration, but this plan is already touching this function's only production call site so removes it now):
```go
func (s *Server) ensureMatrixStrategies(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo, skipSymbols map[string]bool) {
```
becomes:
```go
func (s *Server) ensureMatrixStrategies(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, posMap map[string]map[string]hedgePosInfo, skipSymbols map[string]bool) {
```
No other change needed in this function's body.

This function has 3 test call sites in `services/api-gateway/matrix_strategy_limits_test.go`, all passing `trader.Credentials{}` (confirming the param really is unused — a real value was never needed even for these tests). Update all 3:
```go
	s.ensureMatrixStrategies(ctx, botID, userID, accID, whitelist, nil, cfg, trader.Credentials{}, map[string]map[string]hedgePosInfo{}, map[string]bool{})
```
(appears at lines ~38, ~175, ~221, each with a different `whitelist`/`nil` argument before it — keep those unchanged, just drop the `trader.Credentials{}` argument) becomes, respectively:
```go
	s.ensureMatrixStrategies(ctx, botID, userID, accID, whitelist, nil, cfg, map[string]map[string]hedgePosInfo{}, map[string]bool{})
```
(line ~38, `whitelist` unchanged from that call's original)
```go
	s.ensureMatrixStrategies(ctx, botID, userID, accID, []string{}, nil, cfg, map[string]map[string]hedgePosInfo{}, map[string]bool{})
```
(line ~175, `[]string{}` unchanged from that call's original)
```go
	s.ensureMatrixStrategies(ctx, botID, userID, accID, whitelist, nil, cfg, map[string]map[string]hedgePosInfo{}, map[string]bool{})
```
(line ~221, `whitelist` unchanged from that call's original)
Check whether `trader` is still imported/used elsewhere in `matrix_strategy_limits_test.go` after removing these 3 `trader.Credentials{}` literals — if that was the only use of the `trader` package in this test file, remove the now-unused import; if `trader` is used elsewhere in the file (check before assuming), leave the import alone.

- [ ] **Step 4: `processMatrixBot` — drop `loadBotAccountCreds`, thread the existing local `ex` into `checkMatrixPairedClose`, drop the argument to `ensureMatrixStrategies`**

Current code at `matrix_engine.go:48-75` (verified against the live file while writing this plan):
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

	posMap, _ := buildHedgePosMap(rawPositions)

	// Symbols whose pair was just closed this tick must NOT be re-opened by
	// ensureMatrixStrategies using the now-stale posMap (it would re-adopt the closing
	// position and re-fire the trigger). They reopen fresh on the next tick from flat.
	closed := s.checkMatrixPairedClose(ctx, botID, accountID, cfg, creds, posMap)
	s.checkMatrixZombieStrategies(ctx, botID, posMap)
	s.ensureMatrixStrategies(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap, closed)
	s.buildPairedCloseWatches(ctx, botID, accountID, "matrix", cfg, posMap, pairedWatches)
```
becomes:
```go
func (s *Server) processMatrixBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, pairedWatches map[string]pairedCloseWatchEntry) {
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

	posMap, _ := buildHedgePosMap(rawPositions)

	// Symbols whose pair was just closed this tick must NOT be re-opened by
	// ensureMatrixStrategies using the now-stale posMap (it would re-adopt the closing
	// position and re-fire the trigger). They reopen fresh on the next tick from flat.
	closed := s.checkMatrixPairedClose(ctx, botID, accountID, cfg, ex, posMap)
	s.checkMatrixZombieStrategies(ctx, botID, posMap)
	s.ensureMatrixStrategies(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, posMap, closed)
	s.buildPairedCloseWatches(ctx, botID, accountID, "matrix", cfg, posMap, pairedWatches)
```
(the function's closing `}` and anything after it, if any, is unchanged — this shows through the last touched line only.)

- [ ] **Step 5: `verifyAndClosePairedBot` (in `hedge_engine.go`) — drop `loadBotAccountCreds`, thread the existing local `ex` into `checkMatrixPairedClose`**

Current code at `hedge_engine.go:673-706` (verified against the live file while writing this plan):
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

	// entry.cfg is a snapshot up to ~30s stale (refreshed once per hedge-engine tick — see
	// buildPairedCloseWatches). A user editing the bot's close threshold live (via
	// bots_handler.go's update endpoint) between ticks must take effect immediately for this
	// fast path to serve its purpose, so re-read the CURRENT config here rather than trusting
	// the cached one. Fall back to entry.cfg on error (transient DB hiccup): acting on
	// slightly-stale data beats silently skipping this close check altogether — the next 30s
	// tick self-heals regardless.
	cfg, err := s.loadFreshBotCfg(ctx, entry.botID)
	if err != nil {
		cfg = entry.cfg
	}

	switch entry.botKind {
	case "hedge":
		s.checkHedgeDeactivation(ctx, entry.botID, entry.accountID, cfg, posMap)
	case "matrix":
		s.checkMatrixPairedClose(ctx, entry.botID, entry.accountID, cfg, creds, posMap)
	}
}
```
becomes:
```go
func (s *Server) verifyAndClosePairedBot(ctx context.Context, entry pairedCloseWatchEntry) {
	ex, err := s.loadBotAccountExchange(ctx, entry.accountID)
	if err != nil {
		return
	}
	rawPositions, err := ex.FetchPositions(ctx)
	if err != nil {
		return
	}
	posMap, _ := buildHedgePosMap(rawPositions)

	// entry.cfg is a snapshot up to ~30s stale (refreshed once per hedge-engine tick — see
	// buildPairedCloseWatches). A user editing the bot's close threshold live (via
	// bots_handler.go's update endpoint) between ticks must take effect immediately for this
	// fast path to serve its purpose, so re-read the CURRENT config here rather than trusting
	// the cached one. Fall back to entry.cfg on error (transient DB hiccup): acting on
	// slightly-stale data beats silently skipping this close check altogether — the next 30s
	// tick self-heals regardless.
	cfg, err := s.loadFreshBotCfg(ctx, entry.botID)
	if err != nil {
		cfg = entry.cfg
	}

	switch entry.botKind {
	case "hedge":
		s.checkHedgeDeactivation(ctx, entry.botID, entry.accountID, cfg, posMap)
	case "matrix":
		s.checkMatrixPairedClose(ctx, entry.botID, entry.accountID, cfg, ex, posMap)
	}
}
```

- [ ] **Step 6: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline, 0 new failures. Pay particular attention to `TestMatrixRepairCandidates_FindsOneSidedSymbols` if it's non-integration (check — the ~10 pre-existing baseline failures list includes it as an INTEGRATION failure; confirm this task doesn't turn it into a non-integration failure too) and to whatever tests exist in `matrix_strategy_limits_test.go`.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth.
Run: `grep -n "trader\.PlaceOrder(ctx, creds" services/api-gateway/matrix_engine.go` — expected: no matches.
Run: `grep -n "\bcreds\b" services/api-gateway/matrix_engine.go` — manually inspect every remaining hit; expected none in `processMatrixBot`, `checkMatrixPairedClose`, `stopMatrixPair`, `ensureMatrixStrategies`.
Run: `grep -n "\bcreds\b" services/api-gateway/hedge_engine.go` — expected: no remaining hits in `verifyAndClosePairedBot` (this task) NOR in any of the functions Task 2 already migrated (`processHedgeBot`, `checkHedgeActivation`, `checkHedgeForceStandaloneActivation`, `resolveHedgeSlotConflict`, `cancelStrategyOrders`) — if Task 2 hasn't been done yet when this task runs, skip this cross-check for now and re-verify once both tasks are complete.

- [ ] **Step 7: Commit**

```bash
git add services/api-gateway/matrix_engine.go services/api-gateway/hedge_engine.go services/api-gateway/matrix_strategy_limits_test.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate matrix paired-close order placement onto Exchange

Sub-plan #4c-3, Task 3 — migrates matrix_engine.go's 1 trader.PlaceOrder
call site (inside stopMatrixPair, reached via checkMatrixPairedClose) onto
trader.Exchange. checkMatrixPairedClose has TWO independent callers —
processMatrixBot's regular per-tick path, and verifyAndClosePairedBot's
price-callback fast path (hedge_engine.go) — both already had a local ex
trader.Exchange (added by Plan #4c-1 for FetchPositions, never threaded
further) that this task finally threads all the way down to
stopMatrixPair, removing both callers' now-redundant loadBotAccountCreds
calls entirely.

Also removes ensureMatrixStrategies' creds parameter, which was already
unused in its body before this change (a pre-existing dead parameter,
cleaned up here since its only production call site — processMatrixBot —
was already being touched); updated its 3 test call sites in
matrix_strategy_limits_test.go accordingly.

Zero behavior change for Bybit accounts: BybitExchange.PlaceOrderREST is a
one-line delegation to the exact same free function this call site used
directly before.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `strategy_handler.go`'s `DetachFromBot` — simple rename

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`

- [ ] **Step 1: Migrate the single call site**

Current code at `strategy_handler.go:1254-1281` (verified against the live file while writing this plan; showing only the changed lines — the surrounding `if body.Position != nil...` block structure, the `closeReq` literal's other fields, and the error-logging below are unchanged):
```go
			if creds, credsErr := s.loadCreds(r, accountID, userID); credsErr == nil {
```
becomes:
```go
			if ex, credsErr := s.loadExchange(r, accountID, userID); credsErr == nil {
```
```go
				if _, placeErr := trader.PlaceOrder(r.Context(), creds, closeReq); placeErr != nil && botID != nil {
```
becomes:
```go
				if _, placeErr := ex.PlaceOrderREST(r.Context(), closeReq); placeErr != nil && botID != nil {
```
Re-read the whole `if creds, credsErr := ...; credsErr == nil { ... }` block (lines ~1255-1281) before editing to reconfirm `creds` has no other use inside it besides this one call — this plan's research found none.

- [ ] **Step 2: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth.
Run: `grep -n "trader\.PlaceOrder(r\.Context(), creds" services/api-gateway/strategy_handler.go` — expected: no matches.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/strategy_handler.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate DetachFromBot's position-close order onto Exchange

Sub-plan #4c-3, Task 4 — the one order-write call site in
strategy_handler.go, migrated from trader.PlaceOrder(ctx, creds, ...) onto
loadExchange + ex.PlaceOrderREST(...). creds was used only for this one
call, so a simple rename (loadCreds -> loadExchange, creds -> ex).

Zero behavior change for Bybit accounts: BybitExchange.PlaceOrderREST is a
one-line delegation to the exact same free function this call site used
directly before.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: `trader_handler.go`'s `TraderPlaceOrder`/`TraderCancelOrder` dual-path

**Files:**
- Modify: `services/api-gateway/trader_handler.go`

**Depends on:** Task 1 (`Engine.GetExchange`).

**Critical constraint — ownership must be verified before EITHER branch of the dual-path.** The old code always ran `s.loadCreds(r, req.AccountID, userID)` (checks `owner_id`) FIRST, unconditionally, before checking whether a live `TradeStream` exists — so a user can never place/cancel an order on an account they don't own, even via the live-runner fast path. This plan preserves that exact ordering.

- [ ] **Step 1: `TraderPlaceOrder` — also fixes the hardcoded `'bybit'` in the `trader_orders` INSERT**

Current code at `trader_handler.go:90-164` (verified against the live file while writing this plan; showing the full handler body from the `creds` line onward — everything above, i.e. the JSON decode + field validation at the top of the handler, is unchanged):
```go
	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	orderLinkID := s.makeOrderLinkID(r.Context())
	orderReq := trader.OrderRequest{
		Symbol:           req.Symbol,
		Category:         req.Category,
		Side:             req.Side,
		OrderType:        req.OrderType,
		Qty:              req.Qty,
		Price:            req.Price,
		TriggerPrice:     req.TriggerPrice,
		TriggerBy:        req.TriggerBy,
		TriggerDirection: req.TriggerDirection,
		TimeInForce:      req.TimeInForce,
		OrderFilter:      req.OrderFilter,
		ReduceOnly:       req.ReduceOnly,
		PositionIdx:      req.PositionIdx,
		OrderLinkId:      orderLinkID,
	}

	var result trader.OrderResult
	if ts := s.engine.GetTradeStream(req.AccountID); ts != nil {
		result, err = ts.PlaceOrder(r.Context(), orderReq)
	} else {
		result, err = trader.PlaceOrder(r.Context(), creds, orderReq)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}

	_, _ = s.pool.Exec(r.Context(),
		`INSERT INTO trader_orders
		 (owner_id, account_id, order_link_id, order_id, exchange, symbol, category, side, order_type, qty, price, trigger_price)
		 VALUES ($1,$2,$3,$4,'bybit',$5,$6,$7,$8,$9,$10,$11)`,
		userID, req.AccountID, orderLinkID, result.OrderId,
		req.Symbol, req.Category, req.Side, req.OrderType,
		nullNum(req.Qty), nullNum(req.Price), nullNum(req.TriggerPrice),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"order_id":      result.OrderId,
		"order_link_id": orderLinkID,
	})
}
```
becomes:
```go
	var exchangeName string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT exchange FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		req.AccountID, userID,
	).Scan(&exchangeName); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	var ex trader.Exchange
	if e := s.engine.GetExchange(req.AccountID); e != nil {
		ex = e
	} else {
		var err error
		ex, err = s.loadExchange(r, req.AccountID, userID)
		if err != nil {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
	}

	orderLinkID := s.makeOrderLinkID(r.Context())
	orderReq := trader.OrderRequest{
		Symbol:           req.Symbol,
		Category:         req.Category,
		Side:             req.Side,
		OrderType:        req.OrderType,
		Qty:              req.Qty,
		Price:            req.Price,
		TriggerPrice:     req.TriggerPrice,
		TriggerBy:        req.TriggerBy,
		TriggerDirection: req.TriggerDirection,
		TimeInForce:      req.TimeInForce,
		OrderFilter:      req.OrderFilter,
		ReduceOnly:       req.ReduceOnly,
		PositionIdx:      req.PositionIdx,
		OrderLinkId:      orderLinkID,
	}

	result, err := ex.PlaceOrder(r.Context(), orderReq)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}

	_, _ = s.pool.Exec(r.Context(),
		`INSERT INTO trader_orders
		 (owner_id, account_id, order_link_id, order_id, exchange, symbol, category, side, order_type, qty, price, trigger_price)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		userID, req.AccountID, orderLinkID, result.OrderId, exchangeName,
		req.Symbol, req.Category, req.Side, req.OrderType,
		nullNum(req.Qty), nullNum(req.Price), nullNum(req.TriggerPrice),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"order_id":      result.OrderId,
		"order_link_id": orderLinkID,
	})
}
```
Notes on this change:
- The ownership check now happens via a small standalone query at the top (needed anyway to capture `exchangeName` for the INSERT fix) instead of via `loadCreds` — same `WHERE id=$1 AND owner_id=$2` condition, same `404 "account not found"` error on failure, so behavior for an unauthorized/missing account is unchanged.
- `ex.PlaceOrder(...)` (not `PlaceOrderREST`) is used deliberately here — see this plan's "Why PlaceOrder vs PlaceOrderREST" section. When `s.engine.GetExchange` returns a live runner's exchange, this preserves the old WS-preferred fast path exactly (`BybitExchange.PlaceOrder` delegates to the live, already-connected `*TradeStream`). When it falls back to `s.loadExchange`, the resulting exchange's `PlaceOrder` still behaves correctly (Bybit: attempts WS on an unconnected stream, which internally falls back to REST per `TradeStream.PlaceOrder`'s own logic — functionally equivalent to the old REST-only fallback, just with a harmless local no-op WS-attempt first; Binance: `PlaceOrder` and `PlaceOrderREST` are identical anyway).
- The INSERT's `exchange` column now uses the real `exchangeName` fetched above (`'bybit'` or `'binance'`) instead of the hardcoded string `'bybit'` — fixes order-history mislabeling for Binance accounts, confirmed with the user as in-scope for this task.

- [ ] **Step 2: `TraderCancelOrder`**

Current code at `trader_handler.go:168-214` (verified against the live file while writing this plan; showing from the `creds` line onward — the JSON decode + field validation above it is unchanged):
```go
	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	cancelReq := trader.CancelRequest{
		Symbol:      req.Symbol,
		Category:    req.Category,
		OrderId:     req.OrderID,
		OrderFilter: req.OrderFilter,
	}
	var cancelErr error
	if ts := s.engine.GetTradeStream(req.AccountID); ts != nil {
		cancelErr = ts.CancelOrder(r.Context(), cancelReq)
	} else {
		cancelErr = trader.CancelOrder(r.Context(), creds, cancelReq)
	}
	if cancelErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": cancelErr.Error()})
		return
	}
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE trader_orders SET status='Cancelled', updated_at=NOW() WHERE order_id=$1 AND owner_id=$2`,
		req.OrderID, userID,
	)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
```
becomes:
```go
	if _, err := s.loadCreds(r, req.AccountID, userID); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	cancelReq := trader.CancelRequest{
		Symbol:      req.Symbol,
		Category:    req.Category,
		OrderId:     req.OrderID,
		OrderFilter: req.OrderFilter,
	}
	var ex trader.Exchange
	if e := s.engine.GetExchange(req.AccountID); e != nil {
		ex = e
	} else {
		var err error
		ex, err = s.loadExchange(r, req.AccountID, userID)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	cancelErr := ex.CancelOrder(r.Context(), cancelReq)
	if cancelErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": cancelErr.Error()})
		return
	}
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE trader_orders SET status='Cancelled', updated_at=NOW() WHERE order_id=$1 AND owner_id=$2`,
		req.OrderID, userID,
	)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
```
Notes: `s.loadCreds(...)`'s result is intentionally discarded (`_`) here — it's kept only for its ownership-check side effect and `403`-with-`err.Error()` error convention (matching this handler's own pre-existing style, deliberately different from `TraderPlaceOrder`'s `404`, per Plan #4c-2's already-established "don't normalize differing per-handler status codes" precedent). `ex.CancelOrder(...)` (not `CancelOrderREST`) is used for the same WS-preferred-when-live-runner-exists reason as Step 1.

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth. If any integration test specifically covers `TraderPlaceOrder`'s `trader_orders` INSERT (check `grep -l "trader_orders" services/api-gateway/*_test.go`), inspect it for any hardcoded `'bybit'` expectation that might need updating to match the new dynamic `exchangeName` — this plan's research did not find one, but verify against the live test files.
Run: `grep -n "s\.engine\.GetTradeStream\|trader\.PlaceOrder(r\.Context(), creds\|trader\.CancelOrder(r\.Context(), creds" services/api-gateway/trader_handler.go` — expected: no matches (confirms the old dual-path and both direct free-function fallback calls are gone).

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/trader_handler.go
git commit -m "$(cat <<'EOF'
refactor(api-gateway): migrate trader_handler's order dual-path onto Exchange

Sub-plan #4c-3, Task 5 — the last 2 order-write call sites in
services/api-gateway. TraderPlaceOrder/TraderCancelOrder's existing
GetTradeStream-based dual-path (prefer a live AccountRunner's connected WS
stream, fall back to REST) now goes through the new Engine.GetExchange
accessor (Task 1) instead, using ex.PlaceOrder/CancelOrder (WS-preferred,
matching the old ts.PlaceOrder/CancelOrder path exactly) when a live
runner exists, and loadExchange + ex.PlaceOrder/CancelOrder (still
WS-preferred, but on a freshly-resolved, unconnected exchange -- which for
Bybit safely falls back to REST internally, and for Binance is identical
to the REST-only path anyway) otherwise. Ownership verification still
happens unconditionally before either branch, exactly as before.

Also fixes a pre-existing bug found while touching this handler:
TraderPlaceOrder's trader_orders INSERT hardcoded exchange='bybit'
regardless of the account's actual exchange, which would have mislabeled
every order placed by a Binance account in the order-history table. Now
uses the account's real exchange name, fetched alongside the ownership
check this handler already needed.

This closes out sub-plan #4c-3, and with it, all of Plan #4c --
services/api-gateway no longer calls any Bybit-specific trader.X(ctx,
creds, ...) free function for reads, leverage/position-mode, or order
placement/cancellation. Only Plan #5 (Binance account setup on connect +
frontend flag flip) remains before Binance accounts can actually trade
through this system.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Final full-plan verification

**Files:** none modified — verification only.

- [ ] **Step 1: Confirm zero remaining raw free-function order-write calls in `services/api-gateway`**

Run:
```bash
grep -rn "trader\.PlaceOrder(\|trader\.CancelOrder(\|trader\.PlaceOrderBatch(\|trader\.CancelOrderBatch(\|trader\.CancelAllOrders(" services/api-gateway/*.go
```
Expected: **zero matches** anywhere in the package (excluding, if grep's pattern happens to also match inside `pkg/trader` itself — it shouldn't, since this grep is scoped to `services/api-gateway/*.go` only). This is the final check closing out all of Plan #4c's order-write migration — if this finds anything, a site was missed somewhere and must be found and migrated before this plan is done.

- [ ] **Step 2: Full build + test suite**

Run: `go build ./...` — expected clean.
Run: `go vet ./...` — expected clean.
Run: `go test ./...` — expected: identical to the pre-plan baseline, 0 new failures.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth.
Run: `gofmt -l $(git diff --name-only b98f78f..HEAD -- '*.go')` (adjust the base ref to this plan's actual starting commit) piped through the established Windows-CRLF-artifact check (`git show HEAD:<file> | gofmt -l -` vs the raw working-tree file) for any file it flags — confirm no genuine formatting issues were introduced, only pre-existing CRLF noise if any.

- [ ] **Step 3: No commit for this task** — it's verification-only. If Step 1 or Step 2 finds a problem, fix it as part of whichever Task's commit is responsible (or a small follow-up commit if the issue spans multiple tasks), then re-run this task's checks.

---

## What this plan deliberately does not do

- Does not add WS-based order placement for Binance accounts — `BinanceExchange.PlaceOrder`/`PlaceOrderREST` remain identical (both REST-only), matching the current state of `pkg/trader/binance`. Adding Binance WS trading (if ever wanted) is a separate, much larger effort outside Plan #4c's scope entirely.
- Does not change which accounts get a live `AccountRunner` (`pkg/strategy.Engine`'s own account-selection logic is untouched).
- Does not touch `RecordStrategyTrade`/`RecordMatrixTPProfit` (still take raw `trader.Credentials`, called from both `pkg/strategy` and `services/api-gateway`) — deferred since Plan #4b-1, still deferred here; explicitly out of scope for every #4c sub-plan so far, revisit in a future plan once both call sites are ready to change together.
- Does not touch `trader.GetPublicInstrumentInfo`/`trader.GetInstrumentInfo` — no `Exchange`-interface equivalent exists, none is added by this plan.
- Does not address Plan #5 (Binance account setup on connect — hedge-mode/leverage defaults — plus flipping the frontend's `EXCHANGES.binance.supported` flag) — the next and final piece of the whole Binance live-trading rollout after this plan merges.
