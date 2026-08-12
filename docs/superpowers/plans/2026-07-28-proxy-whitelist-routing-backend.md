# Whitelist-aware маршрутизация прокси (REST + WS) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Все запросы к Bybit (REST через `proxy.HTTPClient()`, приватные и публичные WebSocket через `websocket.DefaultDialer`) начинают учитывать IP-whitelist API-ключа аккаунта (если он есть) при выборе прокси из пула — вместо аккаунт-агностичного `Manager.Pick()`, который сейчас может систематически роутить запросы ограниченного по IP аккаунта через неподходящий прокси (`retCode=10010`).

**Архитектура:** Новый `Manager.PickForIPs(allowedIPs []string) (*Proxy, error)` в `pkg/proxy` — та же least-connections/round-robin логика, что и `Pick()` (вынесена в общий `pickFrom`), но сначала сужает кандидатов до прокси, чей хост входит в `allowedIPs`; при отсутствии совпадений — `ErrNoWhitelistedProxy` (fail fast, без direct-fallback). `trader.Credentials` получает поля `AccountID`/`WhitelistedIPs` — этого достаточно, чтобы протащить контекст аккаунта до точки выбора прокси без изменения сигнатур `FetchExecutions`/`PlaceOrder`/`RunPrivateStream` и т.д. Whitelist подтягивается через уже существующий `trader.QueryAPI` (`GET /v5/user/query-api`, поле `ips`) и кэшируется в новой колонке `exchange_accounts.whitelisted_ips`.

**Tech Stack:** Go, PostgreSQL (миграции `migrations/NNN_*.sql`), pgx v5 (нативная поддержка сканирования `TEXT[]` в `[]string`, без `pq.Array`), gorilla/websocket (`Dialer.Proxy` уже поддерживается нативно), stdlib `testing` (без testify — как везде в этих пакетах).

Связанные документы: `docs/superpowers/specs/2026-07-28-proxy-whitelist-routing-design.md`

---

## File Structure

```
sis/
  migrations/
    084_exchange_account_whitelisted_ips.sql   # новая колонка exchange_accounts.whitelisted_ips
  pkg/proxy/
    manager.go                                  # + pickFrom, PickForIPs, ErrNoWhitelistedProxy; Pick() рефакторен на pickFrom
    manager_test.go                              # ИСПРАВЛЕНИЕ: HealthStatus -> status (предсуществующий сбой сборки); + тесты PickForIPs
    transport.go                                 # + filteredTransport
    client.go                                    # + HTTPClientFor, WSDialer, WSDialerFor
    client_test.go                               # новый: тесты HTTPClientFor/WSDialer/WSDialerFor (тонкие обёртки)
  pkg/trader/
    types.go                                     # Credentials + AccountID, WhitelistedIPs
    bybit.go                                      # doSignedGET/doSignedPOST -> proxy.HTTPClientFor
    ws.go                                         # RunPositionStream -> proxy.WSDialerFor
    private_stream.go                             # runPrivateOnce -> proxy.WSDialerFor
    trade_ws.go                                   # TradeStream.runOnce -> proxy.WSDialerFor
    syncer.go                                     # runAccount: AccountID/WhitelistedIPs в creds + новый тикер обновления whitelist
  pkg/signal/
    hub.go                                        # KlineHub.runConn -> proxy.WSDialer()
    ticker_hub.go                                 # TickerHub.runConn -> proxy.WSDialer()
  pkg/strategy/
    engine.go                                     # loadAccountInfo: SELECT + Credentials получают AccountID/WhitelistedIPs
    startup_reconcile.go                          # credCache-путь: SELECT + оба Credentials{} литерала
  services/api-gateway/
    accounts_handler.go                           # VerifyAccount пишет whitelisted_ips в БД; GetAccountBalance/GetAccountPositions прокидывают AccountID/WhitelistedIPs
    admin_handler.go                              # SignAgreement-путь
    bot_engine.go                                 # loadBotAccountCreds — покрывает bots_handler.go/matrix_engine.go/hedge_engine.go/strategy_handler.go бесплатно
    trader_handler.go                             # loadCreds — покрывает terminal/manual-эндпоинты бесплатно
    closed_pnl_syncer.go                          # runAccount
    trader_ws_handler.go                          # PositionsStream
```

---

## Task 1: Исправить предсуществующий сбой сборки тестов `pkg/proxy`

**Files:**
- Modify: `pkg/proxy/manager_test.go`

`pkg/proxy/manager_test.go` использует поле `HealthStatus:` в литералах `Proxy{...}`, которого не существует — реальное поле называется `status` (неэкспортированное, задаётся через `SetStatus()`). Файл в пакете `proxy` (не `proxy_test`), поэтому прямой доступ к неэкспортированному полю легален. Сейчас весь пакет `pkg/proxy` **не компилируется** (`go test ./pkg/proxy/...` падает с `unknown field HealthStatus in struct literal of type Proxy`, 11 вхождений) — это предсуществующий баг, не связанный с этой доработкой, но он блокирует запуск любых новых тестов в этом пакете (Task 3), поэтому чинится первым.

- [ ] **Step 1: Убедиться, что пакет действительно не собирается**

Запустить: `go test ./pkg/proxy/... 2>&1`
Ожидается: `FAIL	sis/pkg/proxy [build failed]`, 11 ошибок `unknown field HealthStatus in struct literal of type Proxy` (строки 13,14,15,37,38,39,58,59,80,81,124,125 — по count это 11 отдельных ошибок компилятора на 12 позициях, `too many errors` обрезает вывод).

- [ ] **Step 2: Заменить `HealthStatus:` на `status:` во всех вхождениях**

В `pkg/proxy/manager_test.go` — единообразная замена ключа поля (значения не меняются) в 11 местах:
```go
// Было (пример, строки 13-15):
{ID: 1, Weight: 1, IsActive: true, HealthStatus: "healthy"},
{ID: 2, Weight: 1, IsActive: true, HealthStatus: "healthy"},
{ID: 3, Weight: 1, IsActive: true, HealthStatus: "healthy"},

// Стало:
{ID: 1, Weight: 1, IsActive: true, status: "healthy"},
{ID: 2, Weight: 1, IsActive: true, status: "healthy"},
{ID: 3, Weight: 1, IsActive: true, status: "healthy"},
```
Применить ту же замену (`HealthStatus:` → `status:`, значение без изменений) на строках 37, 38, 39 (`TestPickLeastConnections`), 58, 59 (`TestPickWeight`), 80, 81 (`TestPickNoHealthyReturnsNil` — включая `HealthStatus: "unhealthy"` → `status: "unhealthy"`), 124, 125 (`TestPickConcurrency`).

- [ ] **Step 3: Убедиться, что пакет теперь собирается и все существующие тесты проходят**

Запустить: `go test ./pkg/proxy/... -v 2>&1`
Ожидается: PASS для всех существующих тестов (`TestPickRoundRobin`, `TestPickLeastConnections`, `TestPickWeight`, `TestPickNoHealthyReturnsNil`, `TestPickEmptyPool`, `TestProxyCounters`, `TestPickConcurrency`, `TestPortFromURL`), 8/8, без ошибок сборки.

- [ ] **Step 4: Commit**

```bash
git add pkg/proxy/manager_test.go
git commit -m "fix(proxy): repair pre-existing manager_test.go build failure (HealthStatus -> status)"
```

---

## Task 2: Миграция — `exchange_accounts.whitelisted_ips`

**Files:**
- Create: `migrations/084_exchange_account_whitelisted_ips.sql`

- [ ] **Step 1: Создать файл миграции**

```sql
-- migrations/084_exchange_account_whitelisted_ips.sql
-- Кэш IP-whitelist API-ключа аккаунта (Bybit GET /v5/user/query-api, поле ips[]),
-- используется для маршрутизации запросов только через прокси, чьи IP в этом списке.
-- NULL/пустой массив = ограничений на бирже нет (текущее поведение без изменений).
-- См. docs/superpowers/specs/2026-07-28-proxy-whitelist-routing-design.md.
ALTER TABLE exchange_accounts ADD COLUMN IF NOT EXISTS whitelisted_ips TEXT[];
```

- [ ] **Step 2: Применить миграцию и проверить**

Запустить (из корня worktree): `go run ./cmd/migrate/`
Ожидается: `migrations applied successfully`.

Проверить:
```sql
\d exchange_accounts
```
Ожидается: `whitelisted_ips` (`text[]`) присутствует, без `NOT NULL`.

Дополнительно — подтвердить, что pgx v5 сканирует `TEXT[]` напрямую в `[]string` без обёрток (`pq.Array` не используется нигде в проекте — проверено `grep`): выполнить разовую ручную проверку через `psql`:
```sql
UPDATE exchange_accounts SET whitelisted_ips = ARRAY['1.2.3.4','5.6.7.8'] WHERE id = (SELECT id FROM exchange_accounts LIMIT 1);
SELECT whitelisted_ips FROM exchange_accounts WHERE whitelisted_ips IS NOT NULL LIMIT 1;
-- откатить:
UPDATE exchange_accounts SET whitelisted_ips = NULL WHERE whitelisted_ips = ARRAY['1.2.3.4','5.6.7.8'];
```
(Реальное Go-сканирование в `[]string` будет подтверждено кодом в Task 9/10 — этот шаг только проверяет, что колонка и синтаксис массива работают на уровне SQL.)

- [ ] **Step 3: Commit**

```bash
git add migrations/084_exchange_account_whitelisted_ips.sql
git commit -m "feat(proxy): add exchange_accounts.whitelisted_ips column"
```

---

## Task 3: `Manager.PickForIPs` — выбор прокси с фильтром по whitelist

**Files:**
- Modify: `pkg/proxy/manager.go`
- Modify: `pkg/proxy/manager_test.go`

- [ ] **Step 1: Написать падающие тесты**

Добавить в `pkg/proxy/manager_test.go`:
```go
func TestPickForIPs_EmptyAllowedBehavesLikePick(t *testing.T) {
	m := &Manager{
		proxies: []*Proxy{
			{ID: 1, Weight: 1, IsActive: true, status: "healthy"},
			{ID: 2, Weight: 1, IsActive: true, status: "healthy"},
		},
	}
	p, err := m.PickForIPs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected a proxy, got nil")
	}
}

func TestPickForIPs_FiltersToAllowedHost(t *testing.T) {
	p1 := &Proxy{ID: 1, URL: mustURL("http://10.0.0.1:3128"), Weight: 1, IsActive: true, status: "healthy"}
	p2 := &Proxy{ID: 2, URL: mustURL("http://10.0.0.2:3128"), Weight: 1, IsActive: true, status: "healthy"}
	m := &Manager{proxies: []*Proxy{p1, p2}}

	for i := 0; i < 20; i++ {
		p, err := m.PickForIPs([]string{"10.0.0.2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ID != 2 {
			t.Errorf("expected proxy 2 (10.0.0.2), got %d", p.ID)
		}
	}
}

func TestPickForIPs_MultipleAllowedUsesLeastConnections(t *testing.T) {
	p1 := &Proxy{ID: 1, URL: mustURL("http://10.0.0.1:3128"), Weight: 1, IsActive: true, status: "healthy"}
	p2 := &Proxy{ID: 2, URL: mustURL("http://10.0.0.2:3128"), Weight: 1, IsActive: true, status: "healthy"}
	p3 := &Proxy{ID: 3, URL: mustURL("http://10.0.0.3:3128"), Weight: 1, IsActive: true, status: "healthy"}
	m := &Manager{proxies: []*Proxy{p1, p2, p3}}

	// p3 не в allowedIPs — не должен выбираться, даже если наименее загружен.
	p1.IncPending()
	p1.IncPending()

	for i := 0; i < 20; i++ {
		p, err := m.PickForIPs([]string{"10.0.0.1", "10.0.0.2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ID != 2 {
			t.Errorf("expected proxy 2 (least pending among allowed), got %d", p.ID)
		}
	}
}

func TestPickForIPs_NoMatchReturnsErrNoWhitelistedProxy(t *testing.T) {
	p1 := &Proxy{ID: 1, URL: mustURL("http://10.0.0.1:3128"), Weight: 1, IsActive: true, status: "healthy"}
	m := &Manager{proxies: []*Proxy{p1}}

	p, err := m.PickForIPs([]string{"192.168.1.1"})
	if p != nil {
		t.Errorf("expected nil proxy, got %v", p)
	}
	if !errors.Is(err, ErrNoWhitelistedProxy) {
		t.Errorf("expected ErrNoWhitelistedProxy, got %v", err)
	}
}

func TestPickForIPs_EmptyPoolWithAllowedIPsReturnsError(t *testing.T) {
	m := &Manager{proxies: []*Proxy{}}
	p, err := m.PickForIPs([]string{"10.0.0.1"})
	if p != nil {
		t.Errorf("expected nil proxy, got %v", p)
	}
	if !errors.Is(err, ErrNoWhitelistedProxy) {
		t.Errorf("expected ErrNoWhitelistedProxy, got %v", err)
	}
}

func TestPickForIPs_UnhealthyOrInactiveExcludedEvenIfAllowed(t *testing.T) {
	p1 := &Proxy{ID: 1, URL: mustURL("http://10.0.0.1:3128"), Weight: 1, IsActive: true, status: "unhealthy"}
	p2 := &Proxy{ID: 2, URL: mustURL("http://10.0.0.2:3128"), Weight: 1, IsActive: false, status: "healthy"}
	m := &Manager{proxies: []*Proxy{p1, p2}}

	p, err := m.PickForIPs([]string{"10.0.0.1", "10.0.0.2"})
	if p != nil {
		t.Errorf("expected nil proxy, got %v", p)
	}
	if !errors.Is(err, ErrNoWhitelistedProxy) {
		t.Errorf("expected ErrNoWhitelistedProxy, got %v", err)
	}
}
```

Добавить `"errors"` в импорты `pkg/proxy/manager_test.go`.

- [ ] **Step 2: Запустить тесты — убедиться, что падают**

Запустить: `go test ./pkg/proxy/... -run TestPickForIPs -v`
Ожидается: FAIL (`undefined: PickForIPs`, `undefined: ErrNoWhitelistedProxy`).

- [ ] **Step 3: Вынести общую логику выбора в `pickFrom`, добавить `PickForIPs`**

В `pkg/proxy/manager.go` добавить `"errors"` в импорты. Заменить существующий метод `Pick()` (строки 101-156) на:

```go
// ErrNoWhitelistedProxy is returned by PickForIPs when allowedIPs is non-empty but no
// proxy in the pool matches any of the given IPs — the caller must not fall back to a
// direct connection or an unlisted proxy (the account's exchange API key would very
// likely reject the request with the same IP-mismatch error anyway).
var ErrNoWhitelistedProxy = errors.New("proxy: no proxy matches account's IP whitelist")

// pickFrom applies least-connections (primary) + round-robin (tie-break) scoring to
// candidates and returns the winner, or nil if candidates is empty. Records the pick as
// the manager's last-picked host. Shared by Pick() and PickForIPs() so both use
// identical selection logic over different candidate sets.
func (m *Manager) pickFrom(candidates []*Proxy) *Proxy {
	if len(candidates) == 0 {
		return nil
	}

	var best *Proxy
	bestScore := float64(1<<63 - 1)
	tied := 0
	for _, p := range candidates {
		score := float64(p.Pending()) / float64(p.Weight)
		if score < bestScore {
			bestScore = score
			best = p
			tied = 1
		} else if score == bestScore {
			tied++
		}
	}

	if tied > 1 {
		start := int(m.rrIdx.Add(1))
		idx := 0
		for _, p := range candidates {
			score := float64(p.Pending()) / float64(p.Weight)
			if score == bestScore {
				if idx == start%tied {
					best = p
					break
				}
				idx++
			}
		}
	}

	host := fmt.Sprintf("%s:%d", best.URL.Hostname(), portFromURL(best.URL))
	m.lastPickMu.Lock()
	m.lastPickHost = host
	m.lastPickMu.Unlock()
	return best
}

// Pick selects the best proxy using least-connections (primary) + round-robin (tie-break).
// Returns nil if no healthy proxies are available — caller should fall back to direct connection.
func (m *Manager) Pick() *Proxy {
	m.mu.RLock()
	proxies := m.proxies
	m.mu.RUnlock()

	var candidates []*Proxy
	for _, p := range proxies {
		if p.IsActive && p.Status() == "healthy" {
			candidates = append(candidates, p)
		}
	}
	return m.pickFrom(candidates)
}

// PickForIPs selects a proxy the same way Pick() does, but when allowedIPs is non-empty
// it first restricts candidates to proxies whose host is in allowedIPs. Empty allowedIPs
// means "no IP restriction" and behaves exactly like Pick() (nil, nil = fall back to
// direct connection — the existing, backward-compatible behavior). Non-empty allowedIPs
// with zero matching candidates returns (nil, ErrNoWhitelistedProxy) — the caller must
// not send the request through an unlisted IP.
func (m *Manager) PickForIPs(allowedIPs []string) (*Proxy, error) {
	if len(allowedIPs) == 0 {
		return m.Pick(), nil
	}

	allowed := make(map[string]bool, len(allowedIPs))
	for _, ip := range allowedIPs {
		allowed[ip] = true
	}

	m.mu.RLock()
	proxies := m.proxies
	m.mu.RUnlock()

	var candidates []*Proxy
	for _, p := range proxies {
		if p.IsActive && p.Status() == "healthy" && allowed[p.URL.Hostname()] {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil, ErrNoWhitelistedProxy
	}
	return m.pickFrom(candidates), nil
}
```

- [ ] **Step 4: Запустить тесты — убедиться, что проходят**

Запустить: `go test ./pkg/proxy/... -v`
Ожидается: PASS для всех тестов пакета, включая 6 новых (`TestPickForIPs_*`) и все существующие из Task 1 (не должны были измениться в поведении — `Pick()` теперь делегирует в `pickFrom`, но результат идентичен старой инлайн-версии).

- [ ] **Step 5: Commit**

```bash
git add pkg/proxy/manager.go pkg/proxy/manager_test.go
git commit -m "feat(proxy): add PickForIPs — whitelist-aware proxy selection"
```

---

## Task 4: `HTTPClientFor`, `WSDialer`, `WSDialerFor`

**Files:**
- Modify: `pkg/proxy/transport.go`
- Modify: `pkg/proxy/client.go`
- Create: `pkg/proxy/client_test.go`

- [ ] **Step 1: Написать падающие тесты**

`pkg/proxy/client_test.go`:
```go
package proxy

import (
	"net/http"
	"testing"

	"github.com/gorilla/websocket"
)

// TestHTTPClientFor_EmptyAllowedReturnsUsableClient: без ограничений — клиент строится
// (поведение делегируется в HTTPClient(), не проверяем сетевой вызов здесь).
func TestHTTPClientFor_EmptyAllowedReturnsUsableClient(t *testing.T) {
	c := HTTPClientFor(nil)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Timeout <= 0 {
		t.Error("expected a non-zero timeout (never http.DefaultClient)")
	}
}

// TestHTTPClientFor_NoGlobalManagerWithAllowedIPs_RequestFailsClosed: без сконфигурированного
// globalManager, но с непустым allowedIPs — сам клиент строится (ошибка возникает при
// фактическом запросе через RoundTrip, не раньше), но запрос должен провалиться с
// ErrNoWhitelistedProxy, а не уйти напрямую.
func TestHTTPClientFor_NoGlobalManagerWithAllowedIPs_RequestFailsClosed(t *testing.T) {
	globalManager = nil // тест изолирован в своём пакете; порядок с другими тестами не важен, т.к. globalManager используется только для чтения здесь
	c := HTTPClientFor([]string{"10.0.0.1"})
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected request to fail (no proxy matches whitelist)")
	}
}

func TestWSDialer_NoManagerReturnsDefaultDialer(t *testing.T) {
	globalManager = nil
	d := WSDialer()
	if d != websocket.DefaultDialer {
		t.Error("expected websocket.DefaultDialer when no manager configured")
	}
}

func TestWSDialerFor_EmptyAllowedDelegatesToWSDialer(t *testing.T) {
	globalManager = nil
	d, err := WSDialerFor(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil dialer")
	}
}

func TestWSDialerFor_NoManagerWithAllowedIPs_ReturnsError(t *testing.T) {
	globalManager = nil
	d, err := WSDialerFor([]string{"10.0.0.1"})
	if d != nil {
		t.Errorf("expected nil dialer, got %v", d)
	}
	if err == nil {
		t.Fatal("expected ErrNoWhitelistedProxy")
	}
}
```

- [ ] **Step 2: Запустить тесты — убедиться, что падают**

Запустить: `go test ./pkg/proxy/... -run "TestHTTPClientFor|TestWSDialer" -v`
Ожидается: FAIL (`undefined: HTTPClientFor`, `undefined: WSDialer`, `undefined: WSDialerFor`).

- [ ] **Step 3: Добавить `filteredTransport` в `pkg/proxy/transport.go`**

```go
// filteredTransport routes each request through a proxy selected via
// Manager.PickForIPs(allowedIPs), reusing the same per-proxy connection pools as
// BalancedTransport (via shared.transportFor). Used by HTTPClientFor for accounts with
// an IP-restricted exchange API key.
type filteredTransport struct {
	manager    *Manager
	allowedIPs []string
	shared     *BalancedTransport
}

func (t *filteredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p, err := t.manager.PickForIPs(t.allowedIPs)
	if err != nil {
		return nil, err
	}
	if p == nil {
		// allowedIPs empty and no proxy available — direct connection (matches
		// BalancedTransport.RoundTrip's existing fallback behavior).
		return t.shared.base.RoundTrip(req)
	}

	p.IncPending()
	p.IncTotal()
	defer p.DecPending()

	resp, err := t.shared.transportFor(p.URL).RoundTrip(req)
	if err != nil {
		p.IncFailures()
	}
	return resp, err
}
```

- [ ] **Step 4: Добавить `HTTPClientFor`, `WSDialer`, `WSDialerFor` в `pkg/proxy/client.go`**

Добавить `"github.com/gorilla/websocket"` в импорты. Добавить после существующего `HTTPClient()`:

```go
// HTTPClientFor returns an *http.Client whose requests are routed only through proxies
// in the pool whose host is in allowedIPs. Empty allowedIPs = no restriction (identical
// to HTTPClient()). If allowedIPs is non-empty and, at request time, no proxy in the
// pool matches, requests made with this client fail with ErrNoWhitelistedProxy rather
// than falling back to direct connection or an unlisted proxy.
func HTTPClientFor(allowedIPs []string) *http.Client {
	if len(allowedIPs) == 0 {
		return HTTPClient()
	}
	m := globalManager
	if m == nil {
		m = &Manager{} // zero-value: PickForIPs always returns ErrNoWhitelistedProxy for non-empty allowedIPs
	}
	return &http.Client{
		Transport: &filteredTransport{manager: m, allowedIPs: allowedIPs, shared: NewBalancedTransport(m)},
		Timeout:   30 * time.Second,
	}
}

// WSDialer returns a *websocket.Dialer that routes through the proxy pool using an
// unrestricted pick (no IP whitelist) — for connections with no associated exchange
// account/API key (e.g. public market-data subscriptions). Falls back to
// websocket.DefaultDialer if no proxies are configured or healthy.
func WSDialer() *websocket.Dialer {
	m := globalManager
	if m == nil || m.Count() == 0 {
		return websocket.DefaultDialer
	}
	p := m.Pick()
	if p == nil {
		return websocket.DefaultDialer
	}
	return &websocket.Dialer{Proxy: http.ProxyURL(p.URL)}
}

// WSDialerFor returns a *websocket.Dialer restricted to proxies whose host is in
// allowedIPs (empty allowedIPs = no restriction, same as WSDialer()). Returns
// ErrNoWhitelistedProxy if allowedIPs is non-empty and no proxy matches — the caller
// must not dial in that case (a direct/unlisted-IP connection would likely be rejected
// by the exchange's own IP whitelist anyway).
func WSDialerFor(allowedIPs []string) (*websocket.Dialer, error) {
	if len(allowedIPs) == 0 {
		return WSDialer(), nil
	}
	m := globalManager
	if m == nil {
		m = &Manager{}
	}
	p, err := m.PickForIPs(allowedIPs)
	if err != nil {
		return nil, err
	}
	return &websocket.Dialer{Proxy: http.ProxyURL(p.URL)}, nil
}
```

**Важно про `NewBalancedTransport(m)` в `HTTPClientFor`:** в отличие от `HTTPClient()`, который переиспользует `m.Transport()` (единственный shared `*BalancedTransport` на процесс, через `sync.Once`), `HTTPClientFor` создаёт новый `*BalancedTransport` на каждый вызов **только чтобы получить доступ к его приватному `transportFor`-кэшу и `base`-фолбэку** — это создаёт новый (пустой) `sync.Map`-кэш `*http.Transport` на каждый вызов, то есть НЕ переиспользует TCP/TLS-пул соединений между вызовами `HTTPClientFor`, в отличие от `HTTPClient()`. Так как `doSignedGET`/`doSignedPOST` и так создают новый `*http.Client` на каждый вызов (см. Task 6) — потеря переиспользования пула соединений между отдельными запросами уже была допустима в текущем коде для не-whitelisted аккаунтов (клиент новый, но `Transport` общий); для whitelisted аккаунтов пул не переиспользуется вовсе. Это осознанное упрощение v1 (см. открытый вопрос ниже) — не блокирует корректность, только чуть менее эффективно per-connection для аккаунтов с whitelist. Если это станет узким местом — можно завести `sync.Map` в `Manager` для кэша `*BalancedTransport` per-allowedIPs-набор в будущей доработке, вне рамок этого плана.

- [ ] **Step 5: Запустить тесты — убедиться, что проходят**

Запустить: `go test ./pkg/proxy/... -v`
Ожидается: PASS для всех тестов пакета (включая новые из Task 3 и Task 4).

- [ ] **Step 6: Commit**

```bash
git add pkg/proxy/transport.go pkg/proxy/client.go pkg/proxy/client_test.go
git commit -m "feat(proxy): add HTTPClientFor, WSDialer, WSDialerFor"
```

---

## Task 5: `trader.Credentials` — поля `AccountID`, `WhitelistedIPs`

**Files:**
- Modify: `pkg/trader/types.go`

- [ ] **Step 1: Добавить поля**

В `pkg/trader/types.go`:
```go
type Credentials struct {
	APIKey         string
	SecretKey      string
	AccountID      string   // exchange_accounts.id; используется для whitelist-aware выбора прокси
	WhitelistedIPs []string // exchange_accounts.whitelisted_ips; nil/пусто = ограничений на бирже нет
}
```

- [ ] **Step 2: Проверить, что пакет и весь модуль собираются**

Запустить: `go build ./... 2>&1`
Ожидается: чистая сборка — добавление полей в структуру не ломает ни один существующий литерал `Credentials{APIKey: ..., SecretKey: ...}` (в Go отсутствующие поля в keyed struct literal — не ошибка).

- [ ] **Step 3: Commit**

```bash
git add pkg/trader/types.go
git commit -m "feat(proxy): add AccountID/WhitelistedIPs to trader.Credentials"
```

---

## Task 6: REST — `doSignedGET`/`doSignedPOST` используют `HTTPClientFor`

**Files:**
- Modify: `pkg/trader/bybit.go`

- [ ] **Step 1: Заменить `proxy.HTTPClient()` на `proxy.HTTPClientFor(creds.WhitelistedIPs)`**

В `pkg/trader/bybit.go`, функция `doSignedGET` (строки 76-90):
```go
func doSignedGET(ctx context.Context, creds Credentials, path, query string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bybitBase+path+"?"+query, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range authHeaders(creds, query) {
		req.Header.Set(k, v)
	}
	resp, err := proxy.HTTPClientFor(creds.WhitelistedIPs).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
```

Функция `doSignedPOST` (строки 92-111) — аналогичная замена:
```go
func doSignedPOST(ctx context.Context, creds Credentials, path string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bybitBase+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range authHeaders(creds, string(b)) {
		req.Header.Set(k, v)
	}
	resp, err := proxy.HTTPClientFor(creds.WhitelistedIPs).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
```

**Не трогать** `serverTimestamp()` (строка ~50, использует `proxy.HTTPClient()` напрямую для `/v5/market/time` — это публичный, не подписанный запрос без привязки к аккаунту, не нуждается в whitelist).

- [ ] **Step 2: Собрать и проверить**

Запустить: `go build ./... && go vet ./pkg/trader/...`
Ожидается: чисто.

- [ ] **Step 3: Commit**

```bash
git add pkg/trader/bybit.go
git commit -m "feat(proxy): route signed REST requests through whitelist-aware proxy client"
```

---

## Task 7: Приватные WS — `ws.go`, `private_stream.go`, `trade_ws.go`

**Files:**
- Modify: `pkg/trader/ws.go`
- Modify: `pkg/trader/private_stream.go`
- Modify: `pkg/trader/trade_ws.go`

- [ ] **Step 1: `pkg/trader/ws.go` — `RunPositionStream`**

Добавить `"sis/pkg/proxy"` в импорты. Заменить (строка 43):
```go
	bwsConn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		logMsg("Ошибка подключения к Bybit WS: "+err.Error(), true)
		return
	}
```
на:
```go
	dialer, err := proxy.WSDialerFor(creds.WhitelistedIPs)
	if err != nil {
		logMsg("Ошибка выбора прокси для Bybit WS: "+err.Error(), true)
		return
	}
	bwsConn, _, err := dialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		logMsg("Ошибка подключения к Bybit WS: "+err.Error(), true)
		return
	}
```

- [ ] **Step 2: `pkg/trader/private_stream.go` — `runPrivateOnce`**

Добавить `"sis/pkg/proxy"` в импорты. Заменить (строка 79):
```go
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		return err
	}
```
на:
```go
	dialer, err := proxy.WSDialerFor(creds.WhitelistedIPs)
	if err != nil {
		return err
	}
	conn, _, err := dialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		return err
	}
```
Ошибка из `WSDialerFor` (в т.ч. `ErrNoWhitelistedProxy`) поднимается через тот же `return err`, что и раньше — обрабатывается существующим reconnect-циклом в `RunPrivateStream` (`log.Printf("trader private stream: disconnected (%v), retry in 5s", err)` + retry через 5с) без изменений.

- [ ] **Step 3: `pkg/trader/trade_ws.go` — `TradeStream.runOnce`**

Добавить `"sis/pkg/proxy"` в импорты. Заменить (строка 71):
```go
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitTradeWS, nil)
	if err != nil {
		return err
	}
```
на:
```go
	dialer, err := proxy.WSDialerFor(ts.creds.WhitelistedIPs)
	if err != nil {
		return err
	}
	conn, _, err := dialer.DialContext(ctx, bybitTradeWS, nil)
	if err != nil {
		return err
	}
```
(`ts.creds` — уже существующее поле `TradeStream`, см. `NewTradeStream`.) Ошибка поднимается через `return err`, обрабатывается существующим `Run`-циклом (`log.Printf("trader trade ws: %v, retry in 5s", err)` + retry через 5с).

- [ ] **Step 4: Собрать и проверить**

Запустить: `go build ./... && go vet ./pkg/trader/...`
Ожидается: чисто.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/ws.go pkg/trader/private_stream.go pkg/trader/trade_ws.go
git commit -m "feat(proxy): route private WS connections through whitelist-aware proxy dialer"
```

---

## Task 8: Публичные WS — `pkg/signal/hub.go`, `ticker_hub.go`

**Files:**
- Modify: `pkg/signal/hub.go`
- Modify: `pkg/signal/ticker_hub.go`

- [ ] **Step 1: `pkg/signal/hub.go` — `KlineHub.runConn`**

Добавить `"sis/pkg/proxy"` в импорты. Заменить (строка 206):
```go
		conn, _, err := websocket.DefaultDialer.DialContext(h.ctx, bybitPublicWS, nil)
```
на:
```go
		conn, _, err := proxy.WSDialer().DialContext(h.ctx, bybitPublicWS, nil)
```
(без whitelist — публичные данные, нет привязанного аккаунта/ключа; `WSDialer()` не возвращает ошибку, сигнатура вызова не меняется.)

- [ ] **Step 2: `pkg/signal/ticker_hub.go` — `TickerHub.runConn`**

Добавить `"sis/pkg/proxy"` в импорты. Заменить (строка 166):
```go
		conn, _, err := websocket.DefaultDialer.DialContext(h.ctx, bybitPublicWS, nil)
```
на:
```go
		conn, _, err := proxy.WSDialer().DialContext(h.ctx, bybitPublicWS, nil)
```

- [ ] **Step 3: Собрать и проверить**

Запустить: `go build ./... && go vet ./pkg/signal/...`
Ожидается: чисто.

- [ ] **Step 4: Commit**

```bash
git add pkg/signal/hub.go pkg/signal/ticker_hub.go
git commit -m "feat(proxy): route public WS subscriptions through proxy pool (load spreading)"
```

Этим полностью закрывается унаследованный из `TODO.md` пункт «Phase 2: WebSocket Proxy Support» (уже убран из `TODO.md` в спеке — здесь только код).

---

## Task 9: `Syncer` — заполнение и обновление `whitelisted_ips`

**Files:**
- Modify: `pkg/trader/syncer.go`

- [ ] **Step 1: Прокинуть `AccountID`/`WhitelistedIPs` в `accountRow` и в конструирование `creds`**

В `pkg/trader/syncer.go` расширить `accountRow` (строки 15-21) полем для whitelist:
```go
type accountRow struct {
	id             string
	ownerID        string
	exchange       string
	apiKeyEnc      string
	secretEnc      string
	whitelistedIPs []string
}
```
В `loadAndLaunch` (строки 60-81) добавить колонку в запрос и `Scan`:
```go
func (s *Syncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, exchange, api_key_enc, secret_enc, whitelisted_ips
		 FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.id, &a.ownerID, &a.exchange, &a.apiKeyEnc, &a.secretEnc, &a.whitelistedIPs); err != nil {
			continue
		}
		s.mu.Lock()
		_, ok := s.running[a.id]
		s.mu.Unlock()
		if !ok {
			s.launch(ctx, a)
		}
	}
}
```
В `runAccount` (строки 99-129) прокинуть в `creds`:
```go
	creds := Credentials{APIKey: apiKey, SecretKey: secret, AccountID: a.id, WhitelistedIPs: a.whitelistedIPs}
```

- [ ] **Step 2: Добавить тикер обновления whitelist**

В `runAccount`, рядом с существующими `execTicker`/`histTicker` (строки 114-128):
```go
	s.syncExecutions(ctx, a, creds)

	execTicker := time.NewTicker(60 * time.Second)
	histTicker := time.NewTicker(5 * time.Minute)
	whitelistTicker := time.NewTicker(5 * time.Minute)
	defer execTicker.Stop()
	defer histTicker.Stop()
	defer whitelistTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-execTicker.C:
			s.syncExecutions(ctx, a, creds)
		case <-histTicker.C:
			s.syncOrderHistory(ctx, a, creds)
		case <-whitelistTicker.C:
			s.refreshWhitelistedIPs(ctx, &a, &creds)
		}
	}
```
`a` и `creds` передаются по указателю в `refreshWhitelistedIPs`, чтобы обновлённый `WhitelistedIPs` сразу подхватывался последующими вызовами `syncExecutions`/`syncOrderHistory` в рамках этого же цикла `runAccount` (без необходимости перезапускать горутину).

- [ ] **Step 3: Реализовать `refreshWhitelistedIPs`**

Добавить в `pkg/trader/syncer.go`:
```go
// refreshWhitelistedIPs re-fetches the account's Bybit API key IP whitelist via
// QueryAPI and persists it to exchange_accounts.whitelisted_ips, also updating creds
// in place so subsequent calls in this same runAccount cycle use the fresh value.
func (s *Syncer) refreshWhitelistedIPs(ctx context.Context, a *accountRow, creds *Credentials) {
	raw, err := QueryAPI(ctx, *creds)
	if err != nil {
		log.Printf("syncer: refresh whitelist account=%s: %v", a.id, err)
		return
	}
	var parsed struct {
		IPs []string `json:"ips"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("syncer: parse whitelist account=%s: %v", a.id, err)
		return
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2`,
		parsed.IPs, a.id,
	); err != nil {
		log.Printf("syncer: persist whitelist account=%s: %v", a.id, err)
		return
	}
	a.whitelistedIPs = parsed.IPs
	creds.WhitelistedIPs = parsed.IPs
}
```
Добавить `"encoding/json"` в импорты, если ещё не импортирован (уже есть — используется в `syncExecutions`/`syncOrderHistory`? Проверить фактические импорты файла перед добавлением, не дублировать).

- [ ] **Step 4: Собрать и проверить**

Запустить: `go build ./... && go vet ./pkg/trader/...`
Ожидается: чисто.

- [ ] **Step 5: Ручная проверка на dev-БД**

После применения Task 2 (миграция) — запустить сервис локально (или дождаться следующего тика `whitelistTicker` для активного аккаунта в dev-окружении) и убедиться:
```sql
SELECT id, label, whitelisted_ips FROM exchange_accounts WHERE whitelisted_ips IS NOT NULL;
```
Ожидается: для аккаунтов с валидными ключами колонка заполняется реальным массивом (пустым `{}`, если у ключа нет IP-ограничения, или списком IP, если ограничение есть).

- [ ] **Step 6: Commit**

```bash
git add pkg/trader/syncer.go
git commit -m "feat(proxy): Syncer periodically refreshes and persists account IP whitelist"
```

---

## Task 10: `accounts_handler.go` — `VerifyAccount` пишет whitelist в БД; `GetAccountBalance`/`GetAccountPositions` прокидывают его в `Credentials`

**Files:**
- Modify: `services/api-gateway/accounts_handler.go`

- [ ] **Step 1: `VerifyAccount` — персистить `ips[]` в БД**

В `services/api-gateway/accounts_handler.go`, функция `VerifyAccount` (строки 104-157) — после существующего блока обновления `expires_at` (строки 137-144) добавить обновление `whitelisted_ips`:
```go
	var expiresAt *time.Time
	if parsed.ExpiredTime > 0 {
		t := time.UnixMilli(parsed.ExpiredTime)
		expiresAt = &t
		_, _ = s.pool.Exec(r.Context(),
			`UPDATE exchange_accounts SET expires_at=$1 WHERE id=$2 AND owner_id=$3`,
			expiresAt, id, userID)
	}
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2 AND owner_id=$3`,
		parsed.IPs, id, userID)
```
(`parsed.IPs` уже парсится строкой выше — существующий код, без изменений в парсинге.)

- [ ] **Step 2: `GetAccountBalance` — прокинуть `AccountID`/`WhitelistedIPs` в `Credentials`**

В `GetAccountBalance` (строки 162-214) заменить SELECT и `Credentials{...}` (строки 165-179):
```go
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id, WhitelistedIPs: whitelistedIPs}
```

- [ ] **Step 3: `GetAccountPositions` — аналогичная замена**

В `GetAccountPositions` (строки 218-242) применить ту же замену (SELECT + Scan + Credentials) с теми же переменными `id`/`userID`, уже в скоупе:
```go
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err1 := crypto.Decrypt(apiKeyEnc, s.encKey)
	secret, err2 := crypto.Decrypt(secretEnc, s.encKey)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: id, WhitelistedIPs: whitelistedIPs}
```

- [ ] **Step 4: Собрать и проверить**

Запустить: `go build ./... && go vet ./services/api-gateway/...`
Ожидается: чисто.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/accounts_handler.go
git commit -m "feat(proxy): VerifyAccount persists IP whitelist; balance/positions endpoints propagate it"
```

---

## Task 11: `bot_engine.go` — `loadBotAccountCreds` (покрывает hedge/matrix/bots/strategy бесплатно)

**Files:**
- Modify: `services/api-gateway/bot_engine.go`

`loadBotAccountCreds` (строки 1377-1394) — единственная точка построения `trader.Credentials` для ботов; вызывается из `bot_engine.go`, `bots_handler.go`, `matrix_engine.go`, `hedge_engine.go`, `strategy_handler.go` (9 вызовов суммарно, по `grep`) — правка этой функции автоматически покрывает всех вызывающих без изменений на их стороне, так как они лишь потребляют возвращённое значение `trader.Credentials`.

- [ ] **Step 1: Заменить SELECT и возврат**

```go
// loadBotAccountCreds decrypts and returns trading credentials for an exchange account.
func (s *Server) loadBotAccountCreds(ctx context.Context, accountID string) (trader.Credentials, error) {
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	if err := s.pool.QueryRow(ctx,
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
		return trader.Credentials{}, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	return trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs}, nil
}
```

- [ ] **Step 2: Собрать и проверить весь модуль**

Запустить: `go build ./... && go vet ./services/api-gateway/...`
Ожидается: чисто — вызывающий код (`bots_handler.go`, `matrix_engine.go`, `hedge_engine.go`, `strategy_handler.go`) не требует изменений.

- [ ] **Step 3: Прогнать существующие тесты, использующие эту функцию/её потребителей**

Запустить: `go test ./services/api-gateway/... -run "TestMatrix|TestHedge" -v`
Ожидается: PASS, без регрессий (функция сохраняет прежнее поведение для аккаунтов без whitelist — `WhitelistedIPs` будет `nil` до первого срабатывания `Syncer`-тикера или `VerifyAccount`, что эквивалентно текущему отсутствию ограничений).

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/bot_engine.go
git commit -m "feat(proxy): loadBotAccountCreds propagates AccountID/WhitelistedIPs"
```

---

## Task 12: `trader_handler.go` — `loadCreds` (покрывает terminal/manual-эндпоинты)

**Files:**
- Modify: `services/api-gateway/trader_handler.go`

- [ ] **Step 1: Заменить SELECT и возврат в `loadCreds`**

```go
// loadCreds looks up an exchange account by id (must be owned by userID), decrypts keys.
func (s *Server) loadCreds(r *http.Request, accountID, userID string) (trader.Credentials, error) {
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		accountID, userID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs)
	if err != nil {
		return trader.Credentials{}, fmt.Errorf("account not found")
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		return trader.Credentials{}, fmt.Errorf("decrypt: %w", err)
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		return trader.Credentials{}, fmt.Errorf("decrypt: %w", err)
	}
	return trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs}, nil
}
```

- [ ] **Step 2: Собрать и проверить**

Запустить: `go build ./... && go vet ./services/api-gateway/...`
Ожидается: чисто.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/trader_handler.go
git commit -m "feat(proxy): loadCreds (terminal endpoints) propagates AccountID/WhitelistedIPs"
```

---

## Task 13: Оставшиеся точки построения `Credentials` в `services/api-gateway`

**Files:**
- Modify: `services/api-gateway/admin_handler.go`
- Modify: `services/api-gateway/closed_pnl_syncer.go`
- Modify: `services/api-gateway/trader_ws_handler.go`

Три независимые точки, каждая не покрыта Task 11/12 (не проходят через `loadBotAccountCreds`/`loadCreds`).

- [ ] **Step 1: `admin_handler.go` — SignAgreement-путь**

Найти функцию, содержащую блок вокруг строк 179-198 (SELECT `api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1`, `accID` в скоупе). Заменить:
```go
	var apiKeyEnc, secretEnc string
	var whitelistedIPs []string
	err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE id=$1`, accID,
	).Scan(&apiKeyEnc, &secretEnc, &whitelistedIPs)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt api key failed")
		return
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt secret failed")
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accID, WhitelistedIPs: whitelistedIPs}
```

- [ ] **Step 2: `closed_pnl_syncer.go` — `closedPnlAccount`, `loadAndLaunch`, `runAccount`**

`ClosedPnlSyncer` — отдельный, независимый синкер со своей структурой аккаунта и своим 5-минутным rescan-циклом (`Start` → `loadAndLaunch`, строки 40-75), аналогичным `pkg/trader.Syncer.loadAndLaunch`. Не добавляет собственный тикер обновления whitelist — полагается на то, что `pkg/trader.Syncer` (Task 9) уже периодически обновляет `exchange_accounts.whitelisted_ips` в фоне; `ClosedPnlSyncer` просто перечитывает актуальное значение на каждом своём 5-минутном `loadAndLaunch`.

Заменить `closedPnlAccount` (строки 56-61):
```go
type closedPnlAccount struct {
	id             string
	ownerID        string
	apiKeyEnc      string
	secretEnc      string
	whitelistedIPs []string
}
```

Заменить `loadAndLaunch` (строки 63-75):
```go
func (s *ClosedPnlSyncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("closed_pnl_syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a closedPnlAccount
		if err := rows.Scan(&a.id, &a.ownerID, &a.apiKeyEnc, &a.secretEnc, &a.whitelistedIPs); err != nil {
			continue
		}
```
(остаток тела цикла — `s.mu.Lock()`/`s.running[a.id]`/`s.launch(ctx, a)` — без изменений, продолжение строк 76-82 файла как есть.)

В `runAccount` (строка 111):
```go
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: a.id, WhitelistedIPs: a.whitelistedIPs}
```

- [ ] **Step 3: `trader_ws_handler.go` — `PositionsStream`**

Заменить (строки 32-67):
```go
	var apiKeyEnc, secretEnc, label string
	var whitelistedIPs []string
	err = s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, label, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND owner_id=$2 AND is_active=TRUE`,
		accountID, userID,
	).Scan(&apiKeyEnc, &secretEnc, &label, &whitelistedIPs)
	if err != nil && s.isAdmin(r.Context(), userID) {
		// Admin fallback: allow streaming any account regardless of ownership.
		err = s.pool.QueryRow(r.Context(),
			`SELECT api_key_enc, secret_enc, label, whitelisted_ips FROM exchange_accounts WHERE id=$1 AND is_active=TRUE`,
			accountID,
		).Scan(&apiKeyEnc, &secretEnc, &label, &whitelistedIPs)
	}
	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	secretKey, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("trader ws: upgrade: %v", err)
		return
	}
	defer conn.Close()

	creds := trader.Credentials{APIKey: apiKey, SecretKey: secretKey, AccountID: accountID, WhitelistedIPs: whitelistedIPs}
```

- [ ] **Step 4: Собрать и проверить**

Запустить: `go build ./... && go vet ./services/api-gateway/...`
Ожидается: чисто.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/admin_handler.go services/api-gateway/closed_pnl_syncer.go services/api-gateway/trader_ws_handler.go
git commit -m "feat(proxy): remaining api-gateway Credentials sites propagate AccountID/WhitelistedIPs"
```

---

## Task 14: `pkg/strategy` — `engine.go`, `startup_reconcile.go`

**Files:**
- Modify: `pkg/strategy/engine.go`
- Modify: `pkg/strategy/startup_reconcile.go`

- [ ] **Step 1: `engine.go` — `loadAccountInfo`**

Заменить (строки 574-603):
```go
func (e *Engine) loadAccountInfo(ctx context.Context, accountID string) (accountInfo, error) {
	var apiKeyEnc, secretEnc, label string
	var username *string
	var whitelistedIPs []string
	if err := e.pool.QueryRow(ctx,
		`SELECT ea.api_key_enc, ea.secret_enc, ea.label,
		        NULLIF(COALESCE(u.username, ''), ''), ea.whitelisted_ips
		 FROM exchange_accounts ea
		 JOIN users u ON u.id = ea.owner_id
		 WHERE ea.id = $1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc, &label, &username, &whitelistedIPs); err != nil {
		return accountInfo{}, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, e.encKey)
	if err != nil {
		return accountInfo{}, err
	}
	secret, err := crypto.Decrypt(secretEnc, e.encKey)
	if err != nil {
		return accountInfo{}, err
	}
	un := ""
	if username != nil {
		un = *username
	}
	return accountInfo{
		creds:         trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: whitelistedIPs},
		accountLabel:  label,
		ownerUsername: un,
	}, nil
}
```

- [ ] **Step 2: `reconcileStoppedCycles` (строки 24-191) — `cycleRow`, запрос, `creds`-кэш, оба литерала `Credentials{}`**

Заменить `cycleRow` (строки 25-43) — добавить поле `whitelistedIPs`:
```go
	type cycleRow struct {
		cycleID   string
		cycleNum  int
		startedAt time.Time
		tpOrderID string
		slOrderID string
		// strategy fields (only what RecordStrategyTrade needs)
		stratID   string
		accountID string
		ownerID   string
		symbol    string
		category  string
		direction string
		hedgeMode bool
		botID     *string
		// credentials (encrypted)
		apiKeyEnc      string
		secretEnc      string
		whitelistedIPs []string
	}
```

Заменить запрос и `Scan` (строки 45-80):
```go
	rows, err := e.pool.Query(ctx, `
		SELECT sc.id, sc.cycle_num, sc.started_at,
		       COALESCE(sc.tp_order_id, ''), COALESCE(sc.sl_order_id, ''),
		       s.id, s.account_id, s.owner_id, s.symbol, s.category,
		       s.direction, COALESCE(s.hedge_mode, true),
		       s.bot_id,
		       ea.api_key_enc, ea.secret_enc, ea.whitelisted_ips
		FROM strategy_cycles sc
		JOIN strategies       s  ON s.id  = sc.strategy_id
		JOIN exchange_accounts ea ON ea.id = s.account_id
		WHERE sc.ended_at IS NULL
		  AND s.status NOT IN ('active', 'finishing')
		  AND ea.is_active = true`,
	)
	if err != nil {
		log.Printf("startup reconcile (stopped): query: %v", err)
		return
	}
	defer rows.Close()

	var cycles []cycleRow
	for rows.Next() {
		var r cycleRow
		if err := rows.Scan(
			&r.cycleID, &r.cycleNum, &r.startedAt,
			&r.tpOrderID, &r.slOrderID,
			&r.stratID, &r.accountID, &r.ownerID, &r.symbol, &r.category,
			&r.direction, &r.hedgeMode,
			&r.botID,
			&r.apiKeyEnc, &r.secretEnc, &r.whitelistedIPs,
		); err != nil {
			log.Printf("startup reconcile (stopped): scan: %v", err)
			continue
		}
		cycles = append(cycles, r)
	}
	rows.Close()
```

Заменить объявление локального кэша `creds` (строка 89):
```go
	type creds struct {
		apiKey, secret string
		whitelistedIPs []string
	}
```

Строка 108 (кэширование — внутри блока `if _, seen := credCache[c.accountID]; !seen { ... }`):
```go
			credCache[c.accountID] = &creds{apiKey: apiKey, secret: secret, whitelistedIPs: c.whitelistedIPs}
```

Строка 117-119 (`FetchPositions`):
```go
			positions, err := trader.FetchPositions(ctx, trader.Credentials{
				APIKey: cr.apiKey, SecretKey: cr.secret, AccountID: c.accountID, WhitelistedIPs: cr.whitelistedIPs,
			})
```

Строка 189 (`RecordStrategyTrade`):
```go
		go RecordStrategyTrade(e.pool, trader.Credentials{APIKey: cr.apiKey, SecretKey: cr.secret, AccountID: c.accountID, WhitelistedIPs: cr.whitelistedIPs}, in)
```

- [ ] **Step 3: `reconcileStoppedNoCycle` (строки 196-270) — отдельная функция, отдельный запрос**

Заменить `accountInfo` (строки 203-207) — добавить поле `whitelistedIPs`:
```go
	type accountInfo struct {
		apiKeyEnc      string
		secretEnc      string
		whitelistedIPs []string
		strats         []stratInfo
	}
```

Заменить запрос (строки 210-223):
```go
	rows, err := e.pool.Query(ctx, `
		SELECT s.id, s.account_id, s.symbol, s.direction,
		       COALESCE(s.hedge_mode, false),
		       ea.api_key_enc, ea.secret_enc, ea.whitelisted_ips
		FROM strategies s
		JOIN exchange_accounts ea ON ea.id = s.account_id
		WHERE s.status = 'stopped'
		  AND (COALESCE(s.tp_pct, 0) > 0 OR COALESCE(s.sl_pct, 0) < 0)
		  AND s.updated_at > NOW() - INTERVAL '7 days'
		  AND NOT EXISTS (
		      SELECT 1 FROM strategy_cycles sc
		      WHERE sc.strategy_id = s.id AND sc.ended_at IS NULL
		  )
		  AND ea.is_active = true`)
	if err != nil {
		log.Printf("reconcileStoppedNoCycle: query: %v", err)
		return
	}
```

Заменить сканирование строк (строки 229-242):
```go
	accounts := make(map[string]*accountInfo)
	for rows.Next() {
		var s stratInfo
		var accountID, apiKeyEnc, secretEnc string
		var whitelistedIPs []string
		if err := rows.Scan(&s.stratID, &accountID, &s.symbol, &s.direction,
			&s.hedgeMode, &apiKeyEnc, &secretEnc, &whitelistedIPs); err != nil {
			continue
		}
		if accounts[accountID] == nil {
			accounts[accountID] = &accountInfo{apiKeyEnc: apiKeyEnc, secretEnc: secretEnc, whitelistedIPs: whitelistedIPs}
		}
		accounts[accountID].strats = append(accounts[accountID].strats, s)
	}
	rows.Close()
```

Заменить вызов `FetchPositions` (строки 259-261):
```go
		positions, err := trader.FetchPositions(ctx, trader.Credentials{
			APIKey: apiKey, SecretKey: secret, AccountID: accountID, WhitelistedIPs: acc.whitelistedIPs,
		})
```

- [ ] **Step 4: Собрать и проверить**

Запустить: `go build ./... && go vet ./pkg/strategy/...`
Ожидается: чисто.

- [ ] **Step 5: Прогнать существующие тесты пакета**

Запустить: `go test ./pkg/strategy/... -v`
Ожидается: PASS, без регрессий.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/startup_reconcile.go
git commit -m "feat(proxy): pkg/strategy Credentials sites propagate AccountID/WhitelistedIPs"
```

---

## Task 15: Регрессия — полный прогон

По правилам проекта (CLAUDE.md, «Тест-ревью после реализации») — обязательный явный прогон и отчёт, не просто «тесты прошли».

- [ ] **Step 1: Полная сборка модуля**

Запустить: `go build ./... 2>&1`
Ожидается: чистая сборка.

- [ ] **Step 2: `go vet` по всему модулю, за вычетом уже известного предсуществующего сбоя**

Запустить: `go vet $(go list ./... | grep -v sis/pkg/proxy) 2>&1` — затем отдельно `go vet ./pkg/proxy/... 2>&1`, ожидая ЧИСТЫЙ результат (в отличие от прошлых прогонов на других ветках — Task 1 этого плана уже почини́л `pkg/proxy`, так что здесь `pkg/proxy` **не должен** быть исключением; если и юзаются другие пакеты за пределами `pkg/proxy`, которые тоже падают из-за иных предсуществующих причин — зафиксировать отдельно, не путать с этой доработкой).

- [ ] **Step 3: Полный прогон тестов затронутых пакетов**

Запустить:
```bash
go test ./pkg/proxy/... -v
go test ./pkg/trader/... -v
go test ./pkg/signal/... -v
go test ./pkg/strategy/... -v
go test ./services/api-gateway/... -v
go test ./services/api-gateway/... -tags=integration -v
```
Ожидается: все PASS. Отдельно отметить количество новых тестов (Task 3-4: `pkg/proxy`) против количества уже существовавших (чтобы не пропустить регресс на смежных механиках — прежний `Pick()` теперь делегирует в `pickFrom`, а вся REST/WS-цепочка бота/хеджа/стратегии теперь получает `AccountID`/`WhitelistedIPs`, но не меняет наблюдаемое поведение при `WhitelistedIPs == nil`).

- [ ] **Step 4: Ручная сквозная проверка против реального аккаунта**

Для аккаунта `fda23ac0-d32f-4e0f-a389-7b0b68c0da37` (изначальный инцидент) — после деплоя дождаться срабатывания `whitelistTicker` (Task 9) или вручную дёрнуть `GET /accounts/fda23ac0-.../verify`, затем убедиться:
```sql
SELECT whitelisted_ips FROM exchange_accounts WHERE id='fda23ac0-d32f-4e0f-a389-7b0b68c0da37';
```
— непусто, содержит реальные IP биржевого ключа. Затем понаблюдать в логах `syncer: fetch executions account=fda23ac0-...` — ошибки `retCode=10010` должны прекратиться (если whitelist содержит хотя бы один из `89.232.177.34`/`91.224.86.8`) либо, если ни один из наших прокси не входит в whitelist этого конкретного ключа, синк должен явно и стабильно логировать `ErrNoWhitelistedProxy` вместо перемежающихся успехов/неудач — в этом случае необходимо действие на стороне пользователя (добавить один из наших прокси-IP в whitelist на Bybit или снять ограничение), что и было целью этой доработки — превратить непредсказуемый intermittent-сбой в чёткий, диагностируемый сигнал.

- [ ] **Step 5: Доложить результат пользователю**

По правилам CLAUDE.md — явно перечислить: какие тесты запускались (команды из Step 3), что подтвердилось как неизменное (существующие тесты `pkg/proxy`/`pkg/trader`/`services/api-gateway` остались зелёными), что нового покрыто (Task 3-4 юнит-тесты `PickForIPs`/`HTTPClientFor`/`WSDialer`/`WSDialerFor`), что проверено только вручную и почему (Task 9/13/14 — DB-зависимые обновления whitelist и сквозная проверка против реального аккаунта, нет стенда для мокирования Bybit в этом пакете — тот же прецедент, что и в `RecordRescuePartialClose`/RescueBot-плане).

---

## Что дальше

Вне рамок этого плана (см. «Область» в спеке): UI-индикатор несовпадения whitelist на фронте (сейчас `VerifyAccount` и так отдаёт `ips[]` — этого достаточно для v1); автоматическое определение прямого IP сервера как кандидата (если у аккаунта в whitelist только «голый» IP сервера без прокси — не будет обнаружено автоматически, будет падать в `ErrNoWhitelistedProxy`, что как минимум явно и диагностируемо, в отличие от нынешнего intermittent-поведения).
