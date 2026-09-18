# Мультибот Clone/Catalog Pairing Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix a bug reported by the user right after a prior plan made Мультибот publishing possible (`MultiBotForm.tsx`'s new "Публичный бот" toggle, which sets `is_public=true` on BOTH legs): when another user deploys ("adds to себе") a public Мультибот from the catalog, only the signal leg gets cloned — the hedge leg silently disappears, and the resulting card renders with SignalBot's blue styling instead of Мультибот's magenta. Investigation found **three separate, related gaps**, all stemming from code that predates Мультибот existing as a concept and was never updated for it:

1. **`DeployBot`** (`services/api-gateway/bots_handler.go`) clones exactly one `bots` row and has no awareness of `paired_bot_id` at all.
2. **`ListBots`**'s public catalog query has no filter excluding a Мультибот's hedge leg — since the recent toggle now publishes both legs, the hedge leg independently appears in the catalog as its own deployable entry (which would itself reproduce this same bug if deployed on its own — a "half a Мультибот" with no signal twin).
3. **`toFeaturedBot`** (`frontend/src/pages/BotsPage.tsx`, builds catalog cards) derives `botKind` directly from `strategyConfig.bot_kind`, unlike its sibling `toMyBot` (builds "Мои боты" cards) which correctly derives `botKind: b.pairedBotId ? 'multi' : b.strategyConfig.bot_kind` — so even a Мультибот's signal leg shows the wrong color/label while still sitting in the catalog, before anyone deploys it.

A fourth, closely related gap found during investigation: **`ForkBot`** only flips `is_fork` on one row too — not the immediate cause of the reported bug, but the same class of oversight, and left inconsistent would surface its own bug the first time a user tries to fork (edit independently) a deployed Мультибот subscription. Fixed alongside for the same reason `StartBot`/`StopBot`/`DeleteAccount`(sic, `DeleteBot`) already cascade to the paired leg — this is the established, existing pattern in this exact file, not a new one being introduced.

**Architecture:** No schema changes. Every fix reuses patterns **already established and working** elsewhere in this same file:
- `DeployBot`'s new pair-cloning logic mirrors `CreateMultiBot`'s existing pair-creation logic (`bots_handler.go:335-445`) almost exactly — create the paired clone, link both rows via `paired_bot_id` in both directions, and re-point the hedge clone's `hedge_bot_whitelist` at the new signal clone's id (`CreateMultiBot` does the identical rewiring for a freshly created pair).
- `ForkBot`'s cascade uses the exact SQL idiom `StartBot`/`StopBot` already use: `WHERE id = $1 OR id = (SELECT paired_bot_id FROM bots WHERE id = $1)`.
- `ListBots`'s catalog exclusion mirrors the frontend's own existing `visibleMine` filter condition (`frontend/src/pages/BotsPage.tsx:100-103`): a row is hidden when `bot_kind = 'hedge' AND paired_bot_id IS NOT NULL`.
- `toFeaturedBot`'s fix is a one-line copy of `toMyBot`'s existing, already-correct derivation.

**Tech Stack:** Go (backend), TypeScript/React (frontend, one derivation line).

**Spec:** none — this is a bug fix against existing, already-shipped Мультибот functionality (`docs/superpowers/plans/2026-09-07-bot-strategy-settings-sync.md` and the earlier Мультибот creation work established the `paired_bot_id`/`hedge_bot_whitelist` conventions this plan reuses).
**Depends on:** the already-merged Мультибот "Публичный бот" toggle (this session, `MultiBotForm.tsx`) — that change is what made this bug reachable in practice (both legs can now actually become `is_public=true`); nothing in this plan reverts or alters that toggle.

---

## Before you start

Read:
- `services/api-gateway/bots_handler.go:335-450` — `CreateMultiBot`, the pair-creation pattern this plan's `DeployBot` fix mirrors. Pay particular attention to lines 421-422 (`hedgeCfg := setJSONField(req.HedgeStrategyConfig, "bot_kind", "hedge")` / `hedgeCfg = setJSONField(hedgeCfg, "hedge_bot_whitelist", []string{signalID})`) and lines 424-444 (insert hedge row with `paired_bot_id`, then back-fill the signal row's `paired_bot_id`).
- `services/api-gateway/bots_handler.go:982-1065` — `DeployBot`, the function this plan modifies. Verify the current code still matches what's quoted in Task 1 before editing.
- `services/api-gateway/bots_handler.go:1067-1104` — `ForkBot`, modified in Task 3.
- `services/api-gateway/bots_handler.go:1145-1165` and `:1176-1194` — `StartBot`/`StopBot`, showing the existing `WHERE owner_id = $2 AND (id = $1 OR id = (SELECT paired_bot_id FROM bots WHERE id = $1))` cascade idiom `ForkBot`'s fix reuses (note `ForkBot` itself doesn't currently repeat the owner_id check in its UPDATE, relying on an earlier SELECT instead — Task 3 preserves that existing convention rather than introducing a new one).
- `services/api-gateway/bots_handler.go:186-223` — `ListBots`, specifically the `catalogSQL` query modified in Task 2.
- `services/api-gateway/bots_handler.go:316-327` — `setJSONField`, the existing helper this plan's new `getJSONStringField` (Task 1) mirrors in style.
- `frontend/src/pages/BotsPage.tsx:16-56` (`toMyBot`) and `:58-88` (`toFeaturedBot`) — compare the two `botKind` derivations directly; line 38 is already correct, line 64 is the bug fixed in Task 4.
- `frontend/src/pages/BotsPage.tsx:98-103` — the existing `visibleMine` filter, whose condition Task 2's SQL filter mirrors server-side for the catalog.

**In scope for this plan:**
- `services/api-gateway/bots_handler.go`: `DeployBot`, `ListBots`, `ForkBot`, plus a new small helper `getJSONStringField`.
- `frontend/src/pages/BotsPage.tsx`: `toFeaturedBot`'s `botKind` line.
- Regression tests for the pair-cloning behavior.

**Explicitly OUT of scope for this plan:**
- Any change to `CreateMultiBot` itself — already correct, used as the reference pattern, not modified.
- Any change to what fields `DeployBot` copies for a *single-leg* (non-paired) bot clone — currently a deliberately minimal set (name/description/full_description/triggers/strategy_config/account_id; NOT avatar_url, symbol_whitelist/blacklist, max_* limits, auto_mode, ignore_coin_filter). This plan's new hedge-leg clone uses the exact same minimal field set for consistency with the existing single-leg behavior — widening what gets copied (for either leg) is a separate, deliberate feature decision, not this bug fix's job.
- `deploy_count` bookkeeping for the hedge leg's original template row — only the signal leg's `deploy_count` is incremented (matching current behavior; only the signal leg is ever catalog-visible after Task 2, so only its count is ever displayed via `fire: b.deployCount > 10`).
- Any new UI treatment for Мультибот catalog cards beyond the color/label fix (e.g. a distinct tagline) — `getBotKindMeta('multi')` already provides one, this plan just makes sure it's reached.

---

### Task 1: `DeployBot` — clone the paired leg too

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

- [ ] **Step 1: Add `getJSONStringField`, right after the existing `setJSONField`**

Current code at `bots_handler.go:316-327` (verified against the live file while writing this plan):
```go
func setJSONField(raw json.RawMessage, key string, val interface{}) json.RawMessage {
	m := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m) //nolint:errcheck
	}
	m[key] = val
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
```
Add immediately after it:
```go

// getJSONStringField reads a single string field out of a strategy_config-shaped
// json.RawMessage — the read counterpart to setJSONField, same tolerant style (empty
// string if raw is empty, absent, or not a string).
func getJSONStringField(raw json.RawMessage, key string) string {
	m := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m) //nolint:errcheck
	}
	v, _ := m[key].(string)
	return v
}
```

- [ ] **Step 2: `DeployBot` — read `paired_bot_id`, clone the paired leg, wire it up**

Current code at `bots_handler.go:982-1065` (verified against the live file while writing this plan):
```go
func (s *Server) DeployBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	sourceID := chi.URLParam(r, "id")
	ctx := r.Context()

	var req struct {
		AccountID string `json:"accountId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // optional body — absent/malformed is fine, accountId just stays ""
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	var acctOwner string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM exchange_accounts WHERE id = $1`, req.AccountID).Scan(&acctOwner); err != nil {
		writeError(w, http.StatusBadRequest, "account not found")
		return
	}
	if acctOwner != callerID {
		writeError(w, http.StatusForbidden, "account does not belong to caller")
		return
	}

	var name, desc, fullDesc string
	var triggers, stratCfg []byte
	var isPublic bool
	if err := s.pool.QueryRow(ctx,
		`SELECT name, description, full_description, is_public, triggers, strategy_config FROM bots WHERE id = $1`,
		sourceID,
	).Scan(&name, &desc, &fullDesc, &isPublic, &triggers, &stratCfg); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !isPublic {
		writeError(w, http.StatusForbidden, "bot is not public")
		return
	}

	// Prevent deploying the same template twice (even if the subscription was later edited/forked).
	var existingID string
	dupErr := s.pool.QueryRow(ctx,
		`SELECT id FROM bots WHERE owner_id = $1 AND source_bot_id = $2 LIMIT 1`,
		callerID, sourceID,
	).Scan(&existingID)
	if dupErr == nil {
		writeError(w, http.StatusConflict, "already deployed")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tx error")
		return
	}
	defer tx.Rollback(ctx)

	var newID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, full_description, triggers, strategy_config, account_id)
		VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		callerID, sourceID, name, desc, fullDesc, triggers, stratCfg, req.AccountID,
	).Scan(&newID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bots SET deploy_count = deploy_count + 1 WHERE id = $1`, sourceID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "tx commit error")
		return
	}

	bot, ok := fetchBot(s, r, newID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}
```
becomes:
```go
func (s *Server) DeployBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	sourceID := chi.URLParam(r, "id")
	ctx := r.Context()

	var req struct {
		AccountID string `json:"accountId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // optional body — absent/malformed is fine, accountId just stays ""
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	var acctOwner string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM exchange_accounts WHERE id = $1`, req.AccountID).Scan(&acctOwner); err != nil {
		writeError(w, http.StatusBadRequest, "account not found")
		return
	}
	if acctOwner != callerID {
		writeError(w, http.StatusForbidden, "account does not belong to caller")
		return
	}

	var name, desc, fullDesc string
	var triggers, stratCfg []byte
	var isPublic bool
	var pairedSourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT name, description, full_description, is_public, triggers, strategy_config, paired_bot_id FROM bots WHERE id = $1`,
		sourceID,
	).Scan(&name, &desc, &fullDesc, &isPublic, &triggers, &stratCfg, &pairedSourceID); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !isPublic {
		writeError(w, http.StatusForbidden, "bot is not public")
		return
	}

	// Prevent deploying the same template twice (even if the subscription was later edited/forked).
	var existingID string
	dupErr := s.pool.QueryRow(ctx,
		`SELECT id FROM bots WHERE owner_id = $1 AND source_bot_id = $2 LIMIT 1`,
		callerID, sourceID,
	).Scan(&existingID)
	if dupErr == nil {
		writeError(w, http.StatusConflict, "already deployed")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tx error")
		return
	}
	defer tx.Rollback(ctx)

	var newID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, full_description, triggers, strategy_config, account_id)
		VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		callerID, sourceID, name, desc, fullDesc, triggers, stratCfg, req.AccountID,
	).Scan(&newID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bots SET deploy_count = deploy_count + 1 WHERE id = $1`, sourceID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// The source bot is one leg of a Мультибот (see CreateMultiBot above) — clone its paired
	// leg too and link the two new clones the same way, otherwise a deployed Мультибот would
	// silently lose its other leg (the exact bug this task fixes). Mirrors CreateMultiBot's
	// own pair-creation wiring, including re-pointing the hedge clone's hedge_bot_whitelist
	// at the new signal clone's id — without that, the cloned hedge leg would still watch the
	// ORIGINAL template's signal leg (owned by someone else) instead of its own new twin.
	if pairedSourceID != nil {
		var pName, pDesc, pFullDesc string
		var pTriggers, pStratCfg []byte
		if err := tx.QueryRow(ctx,
			`SELECT name, description, full_description, triggers, strategy_config FROM bots WHERE id = $1`,
			*pairedSourceID,
		).Scan(&pName, &pDesc, &pFullDesc, &pTriggers, &pStratCfg); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		var pairedNewID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO bots (owner_id, source_bot_id, is_fork, name, description, full_description, triggers, strategy_config, account_id)
			VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8)
			RETURNING id`,
			callerID, *pairedSourceID, pName, pDesc, pFullDesc, pTriggers, pStratCfg, req.AccountID,
		).Scan(&pairedNewID); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bots SET paired_bot_id = $1 WHERE id = $2`, pairedNewID, newID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bots SET paired_bot_id = $1 WHERE id = $2`, newID, pairedNewID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		// Whichever of the two new clones is the hedge leg gets its hedge_bot_whitelist
		// re-pointed at the OTHER clone (the signal leg's) new id. Determined by each row's
		// OWN bot_kind, not by which one happened to be sourceID — after Task 2, the catalog
		// only ever exposes the signal leg, but this stays correct even if a hedge leg's id
		// were ever passed directly (e.g. a stale link from before Task 2 shipped).
		newSignalID, newHedgeID, hedgeStratCfg := newID, pairedNewID, pStratCfg
		if getJSONStringField(stratCfg, "bot_kind") == "hedge" {
			newSignalID, newHedgeID, hedgeStratCfg = pairedNewID, newID, stratCfg
		}
		hedgeStratCfg = setJSONField(hedgeStratCfg, "hedge_bot_whitelist", []string{newSignalID})
		if _, err := tx.Exec(ctx,
			`UPDATE bots SET strategy_config = $1 WHERE id = $2`, []byte(hedgeStratCfg), newHedgeID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "tx commit error")
		return
	}

	bot, ok := fetchBot(s, r, newID, callerID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}
```
Note the two `UPDATE bots SET paired_bot_id = ...` calls (one each direction) rather than one, since neither `newID` nor `pairedNewID` exists yet at INSERT time for the other — same two-step back-fill `CreateMultiBot` already uses (there, the hedge INSERT can set its own `paired_bot_id` in one shot since the signal id already exists by then, but the signal row still needs its own back-filling `UPDATE` afterward — this task's situation needs both directions back-filled since both new rows are created independently in either order).

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline.
Run: `go test -tags=integration ./services/api-gateway/... -run 'TestDeployBot|TestForkBot|TestPatchBot_LinkedSubscriptionBlocked'` — these 3 are in the established ~10-failure integration baseline for THIS repo checkout (a pre-existing environment/test-infra gap unrelated to this plan, confirmed consistently across every prior plan this session) — expect them to still be present in the failure output for the same reason as before, not newly caused by this change. Cross-check by running the same command against `HEAD` (before this task's changes) to confirm the failure reason/count is identical before vs. after.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/bots_handler.go
git commit -m "$(cat <<'EOF'
fix(api-gateway): DeployBot now clones a Мультибот's paired leg too

A user deploying ("adding to себе") a public Мультибот from the catalog
only got the signal leg cloned — the hedge leg silently disappeared,
since DeployBot had no awareness of paired_bot_id at all (predates
Мультибот existing as a concept). Now detects the source bot's
paired_bot_id and clones both legs together, linking the two new clones
bidirectionally and re-pointing the cloned hedge leg's
hedge_bot_whitelist at the new signal clone's id — mirrors
CreateMultiBot's own pair-creation wiring exactly.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `ListBots` — exclude a Мультибот's hedge leg from the public catalog

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

- [ ] **Step 1: Add the exclusion filter to `catalogSQL`**

Current code at `bots_handler.go:201-208` (verified against the live file while writing this plan):
```go
	// Hide an original that has a detached catalog copy — the copy represents it
	// in the library (and survives deletion of the original).
	catalogSQL := `SELECT ` + botCols + zeroStatsCols + botFrom + `
		WHERE b.is_public = true
		  AND NOT EXISTS (SELECT 1 FROM bots c WHERE c.published_from_id = b.id)
		  AND ($1 = '' OR b.name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR b.strategy_config->>'direction' = $2)
		ORDER BY ` + orderBy
```
becomes:
```go
	// Hide an original that has a detached catalog copy — the copy represents it
	// in the library (and survives deletion of the original).
	// Also hide a Мультибот's hedge leg — it exists only to be driven by its paired signal
	// leg (hedge_bot_whitelist locks it to that one bot's strategies) and must never be
	// independently browsable/deployable on its own; the signal leg already represents the
	// whole pair in the catalog (see DeployBot, Task 1, which clones both legs together
	// however the pair is reached). Mirrors frontend/src/pages/BotsPage.tsx's existing
	// visibleMine filter, which hides the same row from "Мои боты" for the same reason.
	catalogSQL := `SELECT ` + botCols + zeroStatsCols + botFrom + `
		WHERE b.is_public = true
		  AND NOT EXISTS (SELECT 1 FROM bots c WHERE c.published_from_id = b.id)
		  AND NOT (b.strategy_config->>'bot_kind' = 'hedge' AND b.paired_bot_id IS NOT NULL)
		  AND ($1 = '' OR b.name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR b.strategy_config->>'direction' = $2)
		ORDER BY ` + orderBy
```

- [ ] **Step 2: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline.
Run: `go test -tags=integration ./services/api-gateway/... -run TestListBots` — `TestListBots` (unlike `TestDeployBot`/`TestForkBot`) is NOT in the pre-existing failure baseline — confirm it still passes after this change (it creates only a plain, non-paired public bot, so this filter shouldn't affect it, but confirm rather than assume).

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/bots_handler.go
git commit -m "$(cat <<'EOF'
fix(api-gateway): exclude a Мультибот's hedge leg from the public catalog

Sub-plan for the Мультибот clone/catalog pairing fix, Task 2. Now that
Мультибот legs can both be published (MultiBotForm.tsx's "Публичный бот"
toggle sets is_public on both), the hedge leg was independently
appearing in the catalog as its own deployable entry — deploying it
alone would create an orphaned hedge bot with no signal twin, the same
class of bug Task 1 fixes for DeployBot itself. The signal leg already
represents the whole pair in the catalog; this mirrors the frontend's
existing visibleMine filter (which already hides this same row from
"Мои боты") server-side for the catalog listing.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `ForkBot` — cascade to the paired leg

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

- [ ] **Step 1: Extend the `UPDATE`**

Current code at `bots_handler.go:1091-1096` (verified against the live file while writing this plan):
```go
	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET is_fork = true, updated_at = NOW() WHERE id = $1`, botID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
```
becomes:
```go
	// A Мультибот's two legs fork together — same reasoning and same SQL idiom as
	// StartBot/StopBot's existing paired-leg cascade (see those functions above): a lone
	// hedge leg left un-forked after its signal twin forks would still be treated as a live
	// (non-fork) linked subscription, an inconsistent state the rest of this file doesn't
	// expect. ownerID was already verified above (matches callerID) for botID itself; the
	// paired leg (if any) always shares the same owner_id by construction (see CreateMultiBot/
	// DeployBot), so no separate ownership re-check is needed for it here.
	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET is_fork = true, updated_at = NOW() WHERE id = $1 OR id = (SELECT paired_bot_id FROM bots WHERE id = $1)`, botID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
```

- [ ] **Step 2: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-task baseline.
Run: `go test -tags=integration ./services/api-gateway/... -run TestForkBot` — in the pre-existing failure baseline (same reasoning as Task 1 Step 3) — cross-check the failure is identical before/after this change, not newly caused by it.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/bots_handler.go
git commit -m "$(cat <<'EOF'
fix(api-gateway): ForkBot cascades to a Мультибот's paired leg

Sub-plan for the Мультибот clone/catalog pairing fix, Task 3. Same class
of pre-Мультибот oversight as DeployBot (Task 1) — ForkBot only flipped
is_fork on one bots row. Uses the exact SQL idiom StartBot/StopBot
already use for their own paired-leg cascade.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Frontend — fix `toFeaturedBot`'s `botKind` derivation

**Files:**
- Modify: `frontend/src/pages/BotsPage.tsx`

- [ ] **Step 1: Match `toMyBot`'s existing derivation**

Current code at `frontend/src/pages/BotsPage.tsx:58-64` (verified against the live file while writing this plan):
```tsx
function toFeaturedBot(b: Bot): FeaturedBot {
  return {
    id:        b.id,
    name:      b.name,
    author:    b.isOfficial ? 'NovaBot' : b.ownerName,
    avatarUrl: b.avatarUrl,
    botKind:   (b.strategyConfig?.bot_kind as BotKind) ?? 'signal',
```
becomes:
```tsx
function toFeaturedBot(b: Bot): FeaturedBot {
  return {
    id:        b.id,
    name:      b.name,
    author:    b.isOfficial ? 'NovaBot' : b.ownerName,
    avatarUrl: b.avatarUrl,
    // Matches toMyBot's derivation above — a Мультибот's signal leg (the only leg the
    // catalog ever shows, see the ListBots fix) must render with the 'multi' color/label,
    // not its own raw strategyConfig.bot_kind ('signal').
    botKind:   b.pairedBotId ? 'multi' : ((b.strategyConfig?.bot_kind as BotKind) ?? 'signal'),
```
(the rest of the function, `strategy:` through the closing `};`, is unchanged.)

- [ ] **Step 2: Typecheck and run the frontend test suite**

Run: `npx tsc --noEmit -p .` (from `frontend/`) — expected clean.
Run: `npx vitest run` — expected: identical to the pre-this-plan baseline, 0 new failures.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/BotsPage.tsx
git commit -m "$(cat <<'EOF'
fix(frontend): catalog cards for a Мультибот's signal leg show as multi

Sub-plan for the Мультибот clone/catalog pairing fix, Task 4.
toFeaturedBot (catalog cards) derived botKind from strategyConfig.bot_kind
directly, unlike its sibling toMyBot ("Мои боты" cards) which already
correctly derives 'multi' from pairedBotId — so a public Мультибот's
signal leg showed SignalBot's blue styling in the catalog itself, not
just after being deployed. Now matches toMyBot's existing derivation
exactly.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Regression tests

**Files:**
- Modify: `services/api-gateway/bots_handler_test.go`

- [ ] **Step 1: `TestDeployBot_ClonesMultiBotPair`**

Add to `services/api-gateway/bots_handler_test.go`, following the exact conventions already established by `TestDeployBot` (`bytes`/`httptest`/`withUserID`/`addChiParams`) and `TestCreateMultiBot_CreatesPairedSignalAndHedgeLegs`/`TestStopBot_CascadesToPairedLeg` (creating a pair via `s.CreateMultiBot`, reading `pairedBotId` off the response) — read both of those existing tests in full first to match their exact style (variable naming, cleanup `defer` pattern, JSON body construction) before writing this one:

```go
// TestDeployBot_ClonesMultiBotPair pins this plan's core fix: deploying a public
// Мультибот's signal leg must clone BOTH legs (not just the one that was deployed),
// link the two new clones via paired_bot_id, and re-point the new hedge clone's
// hedge_bot_whitelist at the new signal clone's id — not the original template's.
func TestDeployBot_ClonesMultiBotPair(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_multi_deploy_author@example.com", "pass1234", false)
	deployerID := createAdminTestUser(t, s, "bots_multi_deploy_er@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", deployerID)
	authorAcctID := createTestAccountLabeled(t, s, authorID, "multi-deploy-author-acct")
	deployerAcctID := createTestAccountLabeled(t, s, deployerID, "multi-deploy-deployer-acct")

	// Create a Мультибот (both legs private by default) and publish the signal leg.
	body, _ := json.Marshal(map[string]interface{}{
		"name": "Public Multi", "accountId": authorAcctID,
		"strategyConfig":      map[string]interface{}{"symbol": "BTCUSDT", "direction": "long"},
		"hedgeStrategyConfig": map[string]interface{}{"direction": "both"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, authorID)
	s.CreateMultiBot(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create multi: got %d: %s", rec.Code, rec.Body.String())
	}
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	origSignalID := signalBot["id"].(string)
	origHedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", origSignalID, origHedgeID)

	if _, err := s.pool.Exec(context.Background(), "UPDATE bots SET is_public = true WHERE id=$1", origSignalID); err != nil {
		t.Fatalf("publish signal leg: %v", err)
	}

	// Deploy the signal leg (the only leg the catalog ever exposes, per Task 2).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+origSignalID+"/deploy", bytes.NewBufferString(`{"accountId":"`+deployerAcctID+`"}`))
	req2 = withUserID(req2, deployerID)
	req2 = addChiParams(req2, map[string]string{"id": origSignalID})
	s.DeployBot(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("deploy: got %d: %s", rec2.Code, rec2.Body.String())
	}
	var newSignalBot map[string]interface{}
	json.NewDecoder(rec2.Body).Decode(&newSignalBot)
	newSignalID := newSignalBot["id"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=(SELECT paired_bot_id FROM bots WHERE id=$1)", newSignalID)

	newHedgeIDVal, _ := newSignalBot["pairedBotId"].(string)
	if newHedgeIDVal == "" {
		t.Fatalf("expected pairedBotId set on the new signal clone, got %v", newSignalBot["pairedBotId"])
	}
	if newHedgeIDVal == origHedgeID {
		t.Fatalf("new signal clone's pairedBotId still points at the ORIGINAL hedge leg (%s) — a new hedge clone should have been created", origHedgeID)
	}

	// The new hedge clone must exist, belong to the deployer, and be linked back.
	var hedgeOwner, hedgePairedID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT owner_id, paired_bot_id FROM bots WHERE id=$1`, newHedgeIDVal,
	).Scan(&hedgeOwner, &hedgePairedID); err != nil {
		t.Fatalf("new hedge clone not found: %v", err)
	}
	if hedgeOwner != deployerID {
		t.Errorf("new hedge clone owner = %s, want %s", hedgeOwner, deployerID)
	}
	if hedgePairedID != newSignalID {
		t.Errorf("new hedge clone's paired_bot_id = %s, want %s", hedgePairedID, newSignalID)
	}

	// The new hedge clone's hedge_bot_whitelist must point at the NEW signal clone, not the
	// original template's signal leg — otherwise it would watch a bot the deployer doesn't own.
	var hedgeCfgRaw []byte
	s.pool.QueryRow(context.Background(), `SELECT strategy_config FROM bots WHERE id=$1`, newHedgeIDVal).Scan(&hedgeCfgRaw)
	var hedgeCfg struct {
		HedgeBotWhitelist []string `json:"hedge_bot_whitelist"`
	}
	json.Unmarshal(hedgeCfgRaw, &hedgeCfg)
	if len(hedgeCfg.HedgeBotWhitelist) != 1 || hedgeCfg.HedgeBotWhitelist[0] != newSignalID {
		t.Errorf("new hedge clone's hedge_bot_whitelist = %v, want [%s]", hedgeCfg.HedgeBotWhitelist, newSignalID)
	}
}
```

- [ ] **Step 2: `TestListBots_ExcludesMultiBotHedgeLeg`**

```go
// TestListBots_ExcludesMultiBotHedgeLeg pins Task 2's fix: even if a Мультибот's hedge
// leg somehow has is_public=true (e.g. an old row from before this fix, or the
// "Публичный бот" toggle setting it on both legs), it must never appear in the catalog
// on its own — only the signal leg represents the pair there.
func TestListBots_ExcludesMultiBotHedgeLeg(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_multi_catalog@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	acctID := createTestAccountLabeled(t, s, userID, "multi-catalog-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Catalog Multi", "accountId": acctID,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateMultiBot(rec, req)
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	signalID := signalBot["id"].(string)
	hedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", signalID, hedgeID)

	// Publish BOTH legs, exactly as MultiBotForm.tsx's "Публичный бот" toggle does.
	if _, err := s.pool.Exec(context.Background(),
		"UPDATE bots SET is_public = true WHERE id=$1 OR id=$2", signalID, hedgeID,
	); err != nil {
		t.Fatalf("publish pair: %v", err)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/bots", nil)
	req2 = withUserID(req2, userID)
	s.ListBots(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp struct {
		Catalog []map[string]interface{} `json:"catalog"`
	}
	json.NewDecoder(rec2.Body).Decode(&resp)

	sawSignal, sawHedge := false, false
	for _, b := range resp.Catalog {
		if b["id"] == signalID {
			sawSignal = true
		}
		if b["id"] == hedgeID {
			sawHedge = true
		}
	}
	if !sawSignal {
		t.Error("expected the signal leg in the catalog")
	}
	if sawHedge {
		t.Error("hedge leg must NOT appear in the catalog on its own")
	}
}
```

- [ ] **Step 3: `TestForkBot_CascadesToPairedLeg`**

```go
// TestForkBot_CascadesToPairedLeg pins Task 3's fix — mirrors the existing
// TestStopBot_CascadesToPairedLeg/TestDeleteBot_CascadesToPairedLeg tests' structure.
func TestForkBot_CascadesToPairedLeg(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_multi_fork_author@example.com", "pass1234", false)
	forkerID := createAdminTestUser(t, s, "bots_multi_forker@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", forkerID)
	authorAcctID := createTestAccountLabeled(t, s, authorID, "multi-fork-author-acct")
	forkerAcctID := createTestAccountLabeled(t, s, forkerID, "multi-fork-forker-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Fork Multi", "accountId": authorAcctID,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, authorID)
	s.CreateMultiBot(rec, req)
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	origSignalID := signalBot["id"].(string)
	origHedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", origSignalID, origHedgeID)

	if _, err := s.pool.Exec(context.Background(), "UPDATE bots SET is_public = true WHERE id=$1", origSignalID); err != nil {
		t.Fatalf("publish signal leg: %v", err)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+origSignalID+"/deploy", bytes.NewBufferString(`{"accountId":"`+forkerAcctID+`"}`))
	req2 = withUserID(req2, forkerID)
	req2 = addChiParams(req2, map[string]string{"id": origSignalID})
	s.DeployBot(rec2, req2)
	var depSignalBot map[string]interface{}
	json.NewDecoder(rec2.Body).Decode(&depSignalBot)
	depSignalID := depSignalBot["id"].(string)
	depHedgeID := depSignalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", depSignalID, depHedgeID)

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/bots/"+depSignalID+"/fork", nil)
	req3 = withUserID(req3, forkerID)
	req3 = addChiParams(req3, map[string]string{"id": depSignalID})
	s.ForkBot(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("fork: got %d: %s", rec3.Code, rec3.Body.String())
	}

	var hedgeIsFork bool
	if err := s.pool.QueryRow(context.Background(), "SELECT is_fork FROM bots WHERE id=$1", depHedgeID).Scan(&hedgeIsFork); err != nil {
		t.Fatalf("hedge leg not found: %v", err)
	}
	if !hedgeIsFork {
		t.Error("expected the paired hedge leg's is_fork to also be true after forking the signal leg")
	}
}
```

- [ ] **Step 4: Run the new tests**

Run: `go build ./...` — expected clean.
Run: `go test -tags=integration ./services/api-gateway/... -run 'TestDeployBot_ClonesMultiBotPair|TestListBots_ExcludesMultiBotHedgeLeg|TestForkBot_CascadesToPairedLeg' -v` — expected: all 3 new tests PASS (these are genuinely new coverage, not part of the pre-existing failure baseline — if any of them fail, that's a real problem with this plan's Tasks 1-3, not an environment issue, and must be fixed before proceeding).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/bots_handler_test.go
git commit -m "$(cat <<'EOF'
test(api-gateway): regression coverage for Мультибот clone/fork/catalog pairing

Sub-plan for the Мультибот clone/catalog pairing fix, Task 5. Per this
project's CLAUDE.md conventions (regression tests for anything touching
account/bot mechanics), pins the behavior Tasks 1-3 fixed: DeployBot
clones both legs and re-wires hedge_bot_whitelist onto the new signal
clone (not the original template's), ListBots never exposes a
Мультибот's hedge leg on its own, and ForkBot cascades to the paired leg
— mirroring the existing TestStopBot_CascadesToPairedLeg/
TestDeleteBot_CascadesToPairedLeg tests' structure and conventions.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not widen what fields `DeployBot` copies for either leg (avatar_url, symbol_whitelist/blacklist, max_* limits, auto_mode, ignore_coin_filter stay uncopied, matching the existing single-leg behavior exactly) — a separate, deliberate feature decision if ever wanted, not part of this bug fix.
- Does not increment `deploy_count` on the hedge leg's original template row — only the signal leg's count is ever displayed (`fire: b.deployCount > 10` in the catalog), and only the signal leg is catalog-visible after Task 2.
- Does not add any new UI treatment for Мультибот catalog cards beyond the color/label fix — `getBotKindMeta('multi')` already provides tagline/description/color, Task 4 just makes sure the catalog reaches it.
- Does not touch `CreateMultiBot` itself, already correct and used throughout this plan as the reference pattern.
- Does not retroactively fix already-existing bad data (e.g. a hedge leg that's currently `is_public=true` with no matching public signal leg, if one already exists in the live DB from before this fix) — Task 2's catalog filter makes such a row invisible going forward, which is the practical fix; a one-off data cleanup, if needed, is a separate, human-triggered action outside this plan's scope.
