# Binance Account Verification (Plan #5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close out the Binance live-trading rollout (last of the originally-planned 5 phases). Everything ORDER-related already works generically for Binance since Plan #4c merged (`trader.Exchange` interface, migrated throughout `pkg/strategy` and `services/api-gateway`). Hedge-mode and leverage are ALSO already handled generically — both are set lazily per-symbol on first strategy start (`pkg/strategy/cycle.go`'s `ensurePositionMode` and the `SetLeverage` call in the same file, both already going through `sr.runner.Exchange()`), so **no changes are needed there**. The one remaining genuine gap: `VerifyAccount` (the "Проверить" test button shown when adding a new API key) calls `trader.QueryAPI` directly — a Bybit-specific REST call (`/v5/user/query-api`) — which would fail outright for a Binance key. This plan adds a Binance equivalent and flips the frontend's `EXCHANGES.binance.supported` flag, the last blocker preventing a user from adding a Binance account through the UI at all.

**Architecture:** `pkg/trader/binance` gets a new function, `QueryAPIRestrictions`, hitting Binance's `/sapi/v1/account/apiRestrictions` endpoint (a different host than the futures API `pkg/trader/binance` normally talks to — `https://api.binance.com`, not `https://fapi.binance.com`). `services/api-gateway/accounts_handler.go`'s `VerifyAccount` branches on the account's `exchange` column: Bybit keeps its exact current code path unchanged; Binance calls the new function and maps its response into the SAME JSON shape the frontend already expects (`read_only`, `permissions` keyed by Bybit's own category names `ContractTrade`/`Wallet`, `ips`, `expires_at`) — this is a deliberate choice confirmed with the user: reusing Bybit's response shape means **zero frontend code changes are needed**, since `frontend/src/pages/AccountsPage.tsx`'s existing permission-parsing logic (`verify.permissions?.ContractTrade?.length`, `verify.permissions?.Wallet?.some(...)`) already reads exactly those keys.

**Key constraint confirmed with the user:** Binance's `apiRestrictions` endpoint does NOT expose the account's actual IP whitelist (only whether IP restriction is on/off, via `ipRestrict`) — Binance never returns the list itself via API, for security reasons. Per the user's explicit choice, Binance accounts get **no IP-based proxy binding**: `VerifyAccount` always returns `ips: []` for Binance (never populates `exchange_accounts.whitelisted_ips`), so `pkg/proxy.PickForIPs` treats Binance accounts as unrestricted (any proxy works) — mirroring how a Bybit key with no IP restriction already behaves (`ips: ["*"]` → normalized to `nil`). This is an accepted, deliberate limitation, not a bug — the frontend's existing "не настроен" (not configured) label will display for every Binance account regardless of whether IP restriction is actually on within Binance itself; this minor cosmetic imprecision was explicitly accepted rather than building a separate "unknown" state.

**Tech Stack:** Go (backend), no frontend logic changes beyond one boolean flag flip.

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #4c (all merged) — `trader.Exchange`, `pkg/trader/binance`'s existing signed-request infrastructure (`doRequest`, `sign`, `signParams`, `checkBinanceError`).

---

## Before you start

Read:
- `services/api-gateway/accounts_handler.go:196-263` — the full current `VerifyAccount` handler this plan modifies.
- `pkg/trader/bybit.go:994-1009` — `QueryAPI`, the Bybit function `VerifyAccount` currently calls; this plan's new Binance function serves the same purpose but returns a different (typed, not raw-JSON) shape since Binance's response fields don't map 1:1 onto Bybit's `readOnly`/`permissions`/`ips`/`expiredTime` shape.
- `pkg/trader/binance/client.go:1-137` — the existing signed-request infrastructure (`doRequest`, `doSignedGET`, `sign`, `signParams`, `checkBinanceError`). Note `binanceBase = "https://fapi.binance.com"` (the FUTURES API host) is hardcoded inside `doRequest`. `/sapi/v1/account/apiRestrictions` lives on a DIFFERENT Binance host (`https://api.binance.com`, the general/spot API), so this plan's Task 1 must NOT simply call the existing `doSignedGET` — it needs `doRequest` refactored to accept a base URL parameter (Step 1 below), used only by the new function; every existing caller of `doSignedGET`/`doSignedPOST`/`doSignedDELETE` keeps calling `binanceBase` exactly as before, unchanged.
- `frontend/src/pages/AccountsPage.tsx:75-84` — the `EXCHANGES` config object with the `supported` flag.
- `frontend/src/pages/AccountsPage.tsx:417-420` — exactly how the frontend currently parses `verify.permissions`/`verify.read_only` into the `perms.futures`/`perms.withdraw` booleans shown as chips. This plan's backend response must match this parsing exactly, since this plan makes NO frontend changes.

**In scope for this plan:**
- New `pkg/trader/binance` function querying Binance's API-key-restrictions endpoint.
- `services/api-gateway/accounts_handler.go`'s `VerifyAccount` — branch on exchange, build a Bybit-shaped response for Binance.
- `frontend/src/pages/AccountsPage.tsx` — flip `binance.supported` to `true`.

**Explicitly OUT of scope for this plan:**
- Any change to how hedge-mode/leverage get set — already fully generic and working via `pkg/strategy/cycle.go`'s existing `ensurePositionMode`/`SetLeverage` calls through `sr.runner.Exchange()`.
- Any change to `pkg/proxy.PickForIPs` itself — Binance accounts simply always have `whitelisted_ips = NULL`, which this function already treats as unrestricted (matching existing Bybit-no-restriction behavior); no code change needed there, confirmed by reading `NormalizeWhitelistedIPs`'s existing nil-mapping behavior before writing this plan.
- Any Binance-specific IP-whitelist UI treatment (e.g. a distinct "unknown" state) — deliberately deferred per the user's explicit choice above.
- `pkg/trader/syncer.go`'s `refreshWhitelistedIPs` (a separate periodic Bybit-only IP-whitelist refresh loop, distinct from `VerifyAccount`) — out of scope; it's Bybit-specific by name and already only ever invoked for Bybit accounts today (it's not called from any Binance code path), so it needs no defensive changes for this plan to be safe.

---

### Task 1: `pkg/trader/binance` — add `QueryAPIRestrictions`

**Files:**
- Modify: `pkg/trader/binance/client.go`
- Create: `pkg/trader/binance/account_restrictions.go`
- Create: `pkg/trader/binance/account_restrictions_test.go`

- [ ] **Step 1: Refactor `doRequest` to accept a base URL, without changing any existing caller**

Current code at `pkg/trader/binance/client.go:81-120` (verified against the live file while writing this plan):
```go
func doRequest(ctx context.Context, method, path string, values url.Values, creds trader.Credentials, apiKey string, body bool) ([]byte, error) {
	var req *http.Request
	var err error
	if body {
		req, err = http.NewRequestWithContext(ctx, method, binanceBase+path, strings.NewReader(values.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		full := binanceBase + path
		if len(values) > 0 {
			full += "?" + values.Encode()
		}
		req, err = http.NewRequestWithContext(ctx, method, full, nil)
		if err != nil {
			return nil, err
		}
	}
	if apiKey != "" {
		req.Header.Set("X-MBX-APIKEY", apiKey)
	}
	resp, err := proxy.HTTPClientFor(creds.WhitelistedIPs).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := checkBinanceError(data); err != nil {
		return data, err
	}
	return data, nil
}
```
becomes (renamed to `doRequestToBase`, taking `base` as a new first parameter; the OLD `doRequest` becomes a thin one-line wrapper that passes `binanceBase` — every existing call site, which all call `doRequest(...)`, keeps compiling completely unchanged):
```go
func doRequest(ctx context.Context, method, path string, values url.Values, creds trader.Credentials, apiKey string, body bool) ([]byte, error) {
	return doRequestToBase(ctx, binanceBase, method, path, values, creds, apiKey, body)
}

// doRequestToBase is doRequest's actual implementation, parameterized by host — added so
// account_restrictions.go can call Binance's general/spot API host (api.binance.com)
// instead of the futures host (fapi.binance.com) every other call in this package uses,
// while sharing the exact same signing/proxy/error-handling logic. doRequest itself
// remains the entry point for every existing (futures-host) call site, unchanged.
func doRequestToBase(ctx context.Context, base, method, path string, values url.Values, creds trader.Credentials, apiKey string, body bool) ([]byte, error) {
	var req *http.Request
	var err error
	if body {
		req, err = http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(values.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		full := base + path
		if len(values) > 0 {
			full += "?" + values.Encode()
		}
		req, err = http.NewRequestWithContext(ctx, method, full, nil)
		if err != nil {
			return nil, err
		}
	}
	if apiKey != "" {
		req.Header.Set("X-MBX-APIKEY", apiKey)
	}
	resp, err := proxy.HTTPClientFor(creds.WhitelistedIPs).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := checkBinanceError(data); err != nil {
		return data, err
	}
	return data, nil
}
```
Also add a new base-URL var right next to the existing one (`pkg/trader/binance/client.go:24`, verified against the live file):
```go
var binanceBase = "https://fapi.binance.com"
```
becomes:
```go
var binanceBase = "https://fapi.binance.com"

// binanceMainBase is Binance's general/spot API host — a var (not const), same reason as
// binanceBase, so account_restrictions_test.go can point it at an httptest.Server.
// /sapi/v1/account/apiRestrictions (used by QueryAPIRestrictions) lives here, not on the
// futures host every other call in this package uses.
var binanceMainBase = "https://api.binance.com"
```
After this step, run `go build ./...` to confirm every existing caller of `doRequest` in this package still compiles unchanged (it will — the signature is identical, only the body changed).

- [ ] **Step 2: Add `QueryAPIRestrictions`**

Create `pkg/trader/binance/account_restrictions.go`:
```go
package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"sis/pkg/trader"
)

// APIRestrictions is the subset of Binance's /sapi/v1/account/apiRestrictions response
// this package cares about. Field names and JSON tags match Binance's documented response
// exactly. Notably absent: an IP whitelist — Binance's API never exposes the actual list,
// only whether IP restriction is enabled (IPRestrict) and when the key's trading authority
// expires (TradingAuthorityExpirationTime, 0 if it never expires).
type APIRestrictions struct {
	IPRestrict                     bool  `json:"ipRestrict"`
	CreateTime                     int64 `json:"createTime"`
	EnableWithdrawals              bool  `json:"enableWithdrawals"`
	EnableInternalTransfer         bool  `json:"enableInternalTransfer"`
	EnableFutures                  bool  `json:"enableFutures"`
	EnableReading                  bool  `json:"enableReading"`
	EnableSpotAndMarginTrading     bool  `json:"enableSpotAndMarginTrading"`
	TradingAuthorityExpirationTime int64 `json:"tradingAuthorityExpirationTime"`
}

// QueryAPIRestrictions returns the permissions and restrictions of the given API key,
// Binance's equivalent of pkg/trader/bybit.go's QueryAPI — used by VerifyAccount's
// "Проверить" test button when adding a Binance account. Deliberately uses an
// UNRESTRICTED-proxy caller convention: like Bybit's QueryAPI (see VerifyAccount's own
// comment), this exists to discover account state, so it must not be constrained by a
// possibly-stale/wrong previously-stored whitelist — callers should pass creds with
// WhitelistedIPs left empty, same discipline as VerifyAccount already uses for Bybit.
func QueryAPIRestrictions(ctx context.Context, creds trader.Credentials) (APIRestrictions, error) {
	data, err := doRequestToBase(ctx, binanceMainBase, http.MethodGet, "/sapi/v1/account/apiRestrictions", signParams(creds, url.Values{}), creds, creds.APIKey, false)
	if err != nil {
		return APIRestrictions{}, err
	}
	var r APIRestrictions
	if err := json.Unmarshal(data, &r); err != nil {
		return APIRestrictions{}, err
	}
	return r, nil
}
```

- [ ] **Step 3: Write the test**

Create `pkg/trader/binance/account_restrictions_test.go`, following the same `httptest.Server` + base-URL-swap pattern already used by `pkg/trader/binance/client_test.go` (read that file first to match its exact setup/teardown style — e.g. how it swaps `binanceBase` for a test server URL and restores it after). Test at minimum:
1. `QueryAPIRestrictions` sends the request to the configured test server (proving it uses `binanceMainBase`, not `binanceBase`) with a valid `X-MBX-APIKEY` header and signature (mirror `client_test.go`'s `TestDoSignedGET_SendsAPIKeyHeaderAndValidSignature`'s assertion style).
2. A canned JSON response matching Binance's real shape (e.g. `{"ipRestrict":true,"createTime":1623840271000,"enableWithdrawals":false,"enableInternalTransfer":true,"enableFutures":true,"enableReading":true,"enableSpotAndMarginTrading":false,"tradingAuthorityExpirationTime":0}`) is parsed into the correct `APIRestrictions` struct fields.
3. A Binance error response (`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`) surfaces as a non-nil error (via the existing `checkBinanceError` path — confirm this still fires correctly through `doRequestToBase`).

- [ ] **Step 4: Run the test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./pkg/trader/binance/... -v` — expected: all existing tests still pass (confirming Step 1's refactor didn't change behavior for any existing call site), plus the new tests from Step 3 passing.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/client.go pkg/trader/binance/account_restrictions.go pkg/trader/binance/account_restrictions_test.go
git commit -m "$(cat <<'EOF'
feat(binance): add QueryAPIRestrictions for account verification

Plan #5, Task 1 — Binance's equivalent of pkg/trader/bybit.go's QueryAPI,
used by the account-verification "Проверить" flow (Task 2). Hits
/sapi/v1/account/apiRestrictions on Binance's general/spot API host
(api.binance.com), not the futures host (fapi.binance.com) every other
call in this package uses -- doRequest is refactored into a thin wrapper
around a new host-parameterized doRequestToBase so every existing caller
keeps working unchanged.

Binance's API never exposes the account's actual IP whitelist (only
whether IP restriction is on) -- APIRestrictions has no IP list field by
design, not by omission.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `services/api-gateway/accounts_handler.go` — branch `VerifyAccount` on exchange

**Files:**
- Modify: `services/api-gateway/accounts_handler.go`

- [ ] **Step 1: Read the exchange column, branch the verification call**

Current code at `accounts_handler.go:196-263` (verified against the live file while writing this plan):
```go
func (s *Server) VerifyAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var apiKeyEnc, secretEnc string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	// Deliberately unrestricted for this call: VerifyAccount exists to (re)discover the
	// whitelist, so it must not be constrained by whatever was previously stored — a
	// stale or malformed whitelisted_ips (e.g. no proxy matching it) would otherwise
	// self-lock the account out of ever correcting it. Mirrors Syncer.refreshWhitelistedIPs
	// (pkg/trader/syncer.go), which learned this the same way.
	unrestricted := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id}
	raw, err := trader.QueryAPI(r.Context(), unrestricted)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	var parsed struct {
		ReadOnly    int                 `json:"readOnly"`
		Permissions map[string][]string `json:"permissions"`
		IPs         []string            `json:"ips"`
		ExpiredTime int64               `json:"expiredTime"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var expiresAt *time.Time
	if parsed.ExpiredTime > 0 {
		t := time.UnixMilli(parsed.ExpiredTime)
		expiresAt = &t
		_, _ = s.pool.Exec(r.Context(),
			`UPDATE exchange_accounts SET expires_at=$1 WHERE id=$2 AND owner_id=$3`,
			expiresAt, id, userID)
	}
	// Persist the key's actual IP whitelist so future requests route only through
	// proxies whose exit IP is in it (pkg/proxy.PickForIPs). Bybit returns ["*"] (not [])
	// for a key with no IP restriction — NormalizeWhitelistedIPs maps both to nil/NULL.
	normalizedIPs := trader.NormalizeWhitelistedIPs(parsed.IPs)
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2 AND owner_id=$3`,
		normalizedIPs, id, userID)
	var proxyHost string
	if s.proxyManager != nil {
		proxyHost = s.proxyManager.LastPickedHost()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"read_only":   parsed.ReadOnly == 1,
		"permissions": parsed.Permissions,
		"ips":         parsed.IPs,
		"expires_at":  parsed.ExpiredTime,
		"proxy_host":  proxyHost,
	})
}
```
becomes (the Bybit path — everything inside the `if exchangeName != "binance"` block — is BYTE-IDENTICAL to the original code above, just re-indented one level; only the SELECT gains the `exchange` column, and a new Binance branch is added before the final `writeJSON`):
```go
func (s *Server) VerifyAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var apiKeyEnc, secretEnc, exchangeName string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, exchange FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &exchangeName); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	// Deliberately unrestricted for this call: VerifyAccount exists to (re)discover the
	// whitelist, so it must not be constrained by whatever was previously stored — a
	// stale or malformed whitelisted_ips (e.g. no proxy matching it) would otherwise
	// self-lock the account out of ever correcting it. Mirrors Syncer.refreshWhitelistedIPs
	// (pkg/trader/syncer.go), which learned this the same way.
	unrestricted := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id}

	if exchangeName == "binance" {
		s.verifyBinanceAccount(w, r, id, userID, unrestricted)
		return
	}

	raw, err := trader.QueryAPI(r.Context(), unrestricted)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	var parsed struct {
		ReadOnly    int                 `json:"readOnly"`
		Permissions map[string][]string `json:"permissions"`
		IPs         []string            `json:"ips"`
		ExpiredTime int64               `json:"expiredTime"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var expiresAt *time.Time
	if parsed.ExpiredTime > 0 {
		t := time.UnixMilli(parsed.ExpiredTime)
		expiresAt = &t
		_, _ = s.pool.Exec(r.Context(),
			`UPDATE exchange_accounts SET expires_at=$1 WHERE id=$2 AND owner_id=$3`,
			expiresAt, id, userID)
	}
	// Persist the key's actual IP whitelist so future requests route only through
	// proxies whose exit IP is in it (pkg/proxy.PickForIPs). Bybit returns ["*"] (not [])
	// for a key with no IP restriction — NormalizeWhitelistedIPs maps both to nil/NULL.
	normalizedIPs := trader.NormalizeWhitelistedIPs(parsed.IPs)
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2 AND owner_id=$3`,
		normalizedIPs, id, userID)
	var proxyHost string
	if s.proxyManager != nil {
		proxyHost = s.proxyManager.LastPickedHost()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"read_only":   parsed.ReadOnly == 1,
		"permissions": parsed.Permissions,
		"ips":         parsed.IPs,
		"expires_at":  parsed.ExpiredTime,
		"proxy_host":  proxyHost,
	})
}
```
Add `"sis/pkg/trader/binance"` to this file's imports if not already present (check first).

- [ ] **Step 2: Add `verifyBinanceAccount`**

Add this new method right after `VerifyAccount`:
```go
// verifyBinanceAccount is VerifyAccount's Binance branch — mirrors its Bybit counterpart's
// response shape exactly (read_only/permissions/ips/expires_at/proxy_host) using Bybit's
// own permission-category names (ContractTrade, Wallet) so the frontend's existing parsing
// (frontend/src/pages/AccountsPage.tsx: verify.permissions?.ContractTrade?.length,
// verify.permissions?.Wallet?.some(...)) picks these up with zero frontend changes.
// Binance's API never exposes the account's actual IP whitelist (only whether IP
// restriction is on) — ips is always [] here, and whitelisted_ips is deliberately left
// untouched (NULL/unset), so pkg/proxy.PickForIPs treats Binance accounts as unrestricted,
// exactly like a Bybit key with no IP restriction already behaves. This is a confirmed,
// deliberate limitation, not a bug — see this plan's own header comment for the rationale.
func (s *Server) verifyBinanceAccount(w http.ResponseWriter, r *http.Request, id, userID string, creds trader.Credentials) {
	restrictions, err := binance.QueryAPIRestrictions(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}

	permissions := map[string][]string{}
	if restrictions.EnableFutures {
		permissions["ContractTrade"] = []string{"Trade"}
	}
	if restrictions.EnableWithdrawals {
		permissions["Wallet"] = []string{"Withdraw"}
	}

	var expiresAt *time.Time
	if restrictions.TradingAuthorityExpirationTime > 0 {
		t := time.UnixMilli(restrictions.TradingAuthorityExpirationTime)
		expiresAt = &t
		_, _ = s.pool.Exec(r.Context(),
			`UPDATE exchange_accounts SET expires_at=$1 WHERE id=$2 AND owner_id=$3`,
			expiresAt, id, userID)
	}

	var proxyHost string
	if s.proxyManager != nil {
		proxyHost = s.proxyManager.LastPickedHost()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"read_only":   !restrictions.EnableSpotAndMarginTrading && !restrictions.EnableFutures,
		"permissions": permissions,
		"ips":         []string{},
		"expires_at":  restrictions.TradingAuthorityExpirationTime,
		"proxy_host":  proxyHost,
	})
}
```

- [ ] **Step 3: Run the full test suite**

Run: `go build ./...` — expected clean.
Run: `go test ./...` (no tags) — expected: identical to the pre-this-plan baseline, 0 new failures.
Run: `go test -tags=integration ./services/api-gateway/...` — expected: same ~10 pre-existing failures, no growth.
Run: `grep -n "trader.QueryAPI(" services/api-gateway/accounts_handler.go` — expected: still exactly ONE match (the Bybit path, unchanged) — confirms the Bybit branch wasn't accidentally duplicated or removed.

- [ ] **Step 4: Regression coverage for `VerifyAccount`'s Binance branch — decided approach**

`binanceMainBase`/`binanceBase` are unexported `var`s inside `pkg/trader/binance` — a black-box test in `services/api-gateway` (a different package) cannot swap them to point at a local `httptest.Server`, and exporting them just to support one cross-package test isn't worth the API surface it would add. Given that constraint, this plan deliberately does NOT attempt a full HTTP-round-trip integration test of `verifyBinanceAccount` from `services/api-gateway`. Regression coverage for this change comes from three places instead, layered:
1. **Task 1's `account_restrictions_test.go`** already fully covers `QueryAPIRestrictions`'s HTTP call, host, signing, and response parsing in isolation (that's the only piece that talks to Binance).
2. **This task's Step 3 grep** (`trader.QueryAPI(` still matching exactly once) is the regression guard that the Bybit branch was not accidentally altered.
3. **Task 3's manual webapp-testing verification** exercises the real end-to-end user flow (selecting Binance in the UI) — not with real credentials, but enough to confirm the option is wired up and the request reaches the backend correctly.

If you want an additional pure-Go safety net beyond these three, the one thing worth adding here is a small non-integration unit test in `services/api-gateway` asserting that `verifyBinanceAccount`'s response-building logic (the `permissions`/`read_only` mapping from an `APIRestrictions` struct) produces the exact expected JSON shape — this doesn't need a real HTTP call or a live DB, since it's pure data transformation. Add it only if you can write it without contorting `verifyBinanceAccount`'s signature away from matching `VerifyAccount`'s Bybit branch's style; skip it if that would require a disruptive refactor, since the three layers above already give meaningful coverage.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/accounts_handler.go services/api-gateway/accounts_handler_test.go
git commit -m "$(cat <<'EOF'
feat(api-gateway): branch VerifyAccount to support Binance accounts

Plan #5, Task 2 — the "Проверить" test button when adding a new API key
called trader.QueryAPI directly (Bybit-only REST). Now branches on the
account's exchange column: Bybit keeps its exact existing code path;
Binance calls the new binance.QueryAPIRestrictions (Task 1) and maps its
response into the same JSON shape the frontend already expects, reusing
Bybit's own permission-category names (ContractTrade, Wallet) so
AccountsPage.tsx's existing parsing logic picks it up with zero frontend
changes.

Binance accounts always get ips=[] and never touch whitelisted_ips --
Binance's API doesn't expose the actual IP list, only whether IP
restriction is on. Confirmed with the user: Binance accounts get no
IP-based proxy binding, matching how an unrestricted Bybit key already
behaves (pkg/proxy.PickForIPs treats a nil/NULL whitelist as unrestricted).

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Frontend — flip the Binance support flag

**Files:**
- Modify: `frontend/src/pages/AccountsPage.tsx`

- [ ] **Step 1: Flip the flag**

Current code at `frontend/src/pages/AccountsPage.tsx:75-84` (verified against the live file while writing this plan):
```tsx
const EXCHANGES: Record<string, { name: string; color: string; Mark: React.FC; supported?: boolean }> = {
  bybit:   { name: 'Bybit',   color: '#f7a600', Mark: MarkBybit,   supported: true },
  binance: { name: 'Binance', color: '#f0b90b', Mark: MarkBinance },
  okx:     { name: 'OKX',     color: '#e8edf5', Mark: MarkOkx },
  bingx:   { name: 'BingX',   color: '#5b8cff', Mark: MarkBingx },
  bitget:  { name: 'Bitget',  color: '#00d4d4', Mark: MarkBitget },
  mexc:    { name: 'MEXC',    color: '#3fa6ff', Mark: MarkMexc },
  kucoin:  { name: 'KuCoin',  color: '#26d391', Mark: MarkKucoin },
  htx:     { name: 'HTX',     color: '#5dc6ff', Mark: MarkHtx },
}
```
becomes:
```tsx
const EXCHANGES: Record<string, { name: string; color: string; Mark: React.FC; supported?: boolean }> = {
  bybit:   { name: 'Bybit',   color: '#f7a600', Mark: MarkBybit,   supported: true },
  binance: { name: 'Binance', color: '#f0b90b', Mark: MarkBinance, supported: true },
  okx:     { name: 'OKX',     color: '#e8edf5', Mark: MarkOkx },
  bingx:   { name: 'BingX',   color: '#5b8cff', Mark: MarkBingx },
  bitget:  { name: 'Bitget',  color: '#00d4d4', Mark: MarkBitget },
  mexc:    { name: 'MEXC',    color: '#3fa6ff', Mark: MarkMexc },
  kucoin:  { name: 'KuCoin',  color: '#26d391', Mark: MarkKucoin },
  htx:     { name: 'HTX',     color: '#5dc6ff', Mark: MarkHtx },
}
```

- [ ] **Step 2: Typecheck and run the frontend test suite**

Run: `npx tsc --noEmit -p .` (from `frontend/`) — expected clean.
Run: `npx vitest run` — expected: identical to the pre-this-plan baseline, 0 new failures.

- [ ] **Step 3: Manual verification via webapp-testing**

Log in (or reuse an existing authenticated session per this session's established Playwright-based approach), open the Accounts page, open "Подключить новый ключ", and confirm Binance now appears as a selectable option in Step 1 ("Биржа") alongside Bybit. Screenshot for the record. Do NOT actually submit a real Binance key unless the user explicitly provides one for live testing — verifying the option is selectable and the form renders correctly is sufficient for this step.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/AccountsPage.tsx
git commit -m "$(cat <<'EOF'
feat(frontend): allow adding Binance accounts

Plan #5, Task 3 — the last step of the Binance live-trading rollout.
Flips EXCHANGES.binance.supported to true now that VerifyAccount (Task 2)
and every order-write/read path (Plan #4c, merged) work for Binance
accounts end-to-end.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not change hedge-mode/leverage setup — already fully generic and working (see Goal section above).
- Does not add Binance-specific IP whitelist display/handling beyond `ips: []` — deferred per the user's explicit choice.
- Does not touch `pkg/trader/syncer.go`'s `refreshWhitelistedIPs` — Bybit-only by design, already never invoked for Binance accounts.
- Does not add spot-market support, options, or any Binance product beyond USDⓈ-M futures — matches the scope of the whole Binance rollout to date (`pkg/trader/binance` only ever implemented futures).
