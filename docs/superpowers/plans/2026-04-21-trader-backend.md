# Trader Service — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить в `api-gateway` полный торговый бэкенд: хранение API-ключей, исполнение ордеров Bybit, real-time WS позиций/ордеров, фоновая синхронизация истории сделок и фандинга.

**Architecture:** Новые пакеты `pkg/crypto` (AES-GCM) и `pkg/trader` (Bybit private API + syncer) добавляются в монорепо. Все HTTP/WS-хэндлеры — методы существующего `Server` в `services/api-gateway`. `Server` расширяется полем `encKey string`.

**Tech Stack:** Go 1.25, chi v5, gorilla/websocket, pgx/v5, стандартная библиотека crypto/aes

---

## File Map

| Файл | Действие | Что делает |
|------|----------|------------|
| `migrations/005_exchange_accounts.sql` | Create | exchange_accounts, trader_orders, trader_executions |
| `pkg/crypto/aes.go` | Create | Encrypt/Decrypt AES-256-GCM |
| `pkg/crypto/aes_test.go` | Create | unit-тесты шифрования |
| `pkg/trader/types.go` | Create | Credentials, OrderRequest, Position, Order, Execution и др. |
| `pkg/trader/bybit.go` | Create | sign(), serverTimestamp(), PlaceOrder, CancelOrder, SetLeverage, FetchPositions, FetchOpenOrders, FetchOrderHistory, FetchExecutions |
| `pkg/trader/bybit_test.go` | Create | unit-тест sign() |
| `pkg/trader/ws.go` | Create | RunPositionStream — приватный WS прокси к Bybit |
| `pkg/trader/syncer.go` | Create | Syncer — фоновая синхронизация executions и order history |
| `services/api-gateway/server.go` | Modify | добавить поле encKey в Server, обновить NewServer |
| `services/api-gateway/main.go` | Modify | читать ENCRYPTION_KEY, TRADER_SYNC_*, запускать Syncer, регистрировать маршруты |
| `services/api-gateway/accounts_handler.go` | Create | ListAccounts, CreateAccount, DeleteAccount, VerifyAccount |
| `services/api-gateway/accounts_handler_test.go` | Create | integration-тесты аккаунтов |
| `services/api-gateway/trader_handler.go` | Create | PlaceOrder, CancelOrder, SetLeverage хэндлеры |
| `services/api-gateway/trader_handler_test.go` | Create | unit-тесты валидации |
| `services/api-gateway/trader_history_handler.go` | Create | ListTraderOrders, ListTraderExecutions, GetTraderStats |
| `services/api-gateway/trader_history_handler_test.go` | Create | integration-тесты истории |
| `services/api-gateway/trader_ws_handler.go` | Create | PositionsStream WS-хэндлер |

---

## Task 1: Migration 005

**Files:**
- Create: `migrations/005_exchange_accounts.sql`

- [ ] **Создать файл миграции**

```sql
-- migrations/005_exchange_accounts.sql

CREATE TABLE IF NOT EXISTS exchange_accounts (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exchange    TEXT        NOT NULL CHECK (exchange IN ('bybit', 'binance')),
    label       TEXT        NOT NULL DEFAULT '',
    api_key_enc TEXT        NOT NULL,
    secret_enc  TEXT        NOT NULL,
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, exchange, label)
);
CREATE INDEX IF NOT EXISTS exchange_accounts_owner ON exchange_accounts (owner_id);

CREATE TABLE IF NOT EXISTS trader_orders (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id     UUID        NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    order_link_id  TEXT        NOT NULL UNIQUE,
    order_id       TEXT,
    exchange       TEXT        NOT NULL,
    symbol         TEXT        NOT NULL,
    category       TEXT        NOT NULL,
    side           TEXT        NOT NULL,
    order_type     TEXT        NOT NULL,
    qty            NUMERIC     NOT NULL,
    price          NUMERIC,
    trigger_price  NUMERIC,
    status         TEXT        NOT NULL DEFAULT 'New',
    cum_exec_qty   NUMERIC     NOT NULL DEFAULT 0,
    cum_exec_value NUMERIC     NOT NULL DEFAULT 0,
    cum_exec_fee   NUMERIC     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS trader_orders_owner   ON trader_orders (owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS trader_orders_account ON trader_orders (account_id, status);
CREATE INDEX IF NOT EXISTS trader_orders_link    ON trader_orders (order_link_id);

CREATE TABLE IF NOT EXISTS trader_executions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id    UUID        NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    exec_id       TEXT        NOT NULL,
    order_id      TEXT,
    order_link_id TEXT,
    exchange      TEXT        NOT NULL,
    symbol        TEXT        NOT NULL,
    category      TEXT        NOT NULL,
    side          TEXT,
    exec_type     TEXT        NOT NULL,
    qty           NUMERIC,
    price         NUMERIC,
    exec_value    NUMERIC,
    exec_fee      NUMERIC,
    fee_rate      NUMERIC,
    is_maker      BOOLEAN,
    exec_time     TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (account_id, exec_id)
);
CREATE INDEX IF NOT EXISTS trader_executions_owner   ON trader_executions (owner_id, exec_time DESC);
CREATE INDEX IF NOT EXISTS trader_executions_account ON trader_executions (account_id, exec_type, exec_time DESC);
CREATE INDEX IF NOT EXISTS trader_executions_link    ON trader_executions (order_link_id) WHERE order_link_id IS NOT NULL;
```

- [ ] **Проверить что миграция применяется (api-gateway читает все *.sql из папки migrations)**

```bash
cd services/api-gateway && go run . &
# В логах должно быть: migrate: applied 005_exchange_accounts.sql
# Ctrl+C после проверки
```

- [ ] **Commit**

```bash
git add migrations/005_exchange_accounts.sql
git commit -m "feat: add exchange_accounts, trader_orders, trader_executions migrations"
```

---

## Task 2: pkg/crypto — AES-256-GCM

**Files:**
- Create: `pkg/crypto/aes.go`
- Create: `pkg/crypto/aes_test.go`

- [ ] **Написать failing тест**

```go
// pkg/crypto/aes_test.go
package crypto_test

import (
	"strings"
	"testing"

	"sis/pkg/crypto"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestEncryptDecrypt(t *testing.T) {
	plain := "my-secret-api-key"
	enc, err := crypto.Encrypt(plain, testKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == plain {
		t.Fatal("encrypted text must differ from plaintext")
	}
	got, err := crypto.Decrypt(enc, testKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}

func TestEncrypt_Nondeterministic(t *testing.T) {
	plain := "key"
	a, _ := crypto.Encrypt(plain, testKey)
	b, _ := crypto.Encrypt(plain, testKey)
	if a == b {
		t.Error("two encryptions of same plaintext must produce different ciphertext")
	}
}

func TestDecrypt_BadKey(t *testing.T) {
	enc, _ := crypto.Encrypt("hello", testKey)
	badKey := strings.Repeat("ff", 32)
	_, err := crypto.Decrypt(enc, badKey)
	if err == nil {
		t.Error("expected error with wrong key")
	}
}

func TestEncrypt_InvalidKey(t *testing.T) {
	_, err := crypto.Encrypt("hello", "tooshort")
	if err == nil {
		t.Error("expected error with invalid key")
	}
}
```

- [ ] **Запустить тест — убедиться что FAIL**

```bash
cd c:/Users/123/Projects/sis && go test ./pkg/crypto/... 
# Expected: build failed / package not found
```

- [ ] **Реализовать**

```go
// pkg/crypto/aes.go
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

func keyFromHex(hexKey string) ([]byte, error) {
	b, err := hex.DecodeString(hexKey)
	if err != nil || len(b) != 32 {
		return nil, errors.New("crypto: key must be 32 bytes (64 hex chars)")
	}
	return b, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using hexKey.
// Returns base64(nonce || ciphertext).
func Encrypt(plaintext, hexKey string) (string, error) {
	key, err := keyFromHex(hexKey)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts a base64-encoded AES-256-GCM ciphertext produced by Encrypt.
func Decrypt(encoded, hexKey string) (string, error) {
	key, err := keyFromHex(hexKey)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("crypto: base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: %w", err)
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("crypto: ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}
	return string(plain), nil
}
```

- [ ] **Запустить тест — убедиться что PASS**

```bash
go test ./pkg/crypto/... -v
# Expected: PASS (4 tests)
```

- [ ] **Commit**

```bash
git add pkg/crypto/
git commit -m "feat: add AES-256-GCM encrypt/decrypt package"
```

---

## Task 3: pkg/trader/types.go + bybit.go

**Files:**
- Create: `pkg/trader/types.go`
- Create: `pkg/trader/bybit.go`
- Create: `pkg/trader/bybit_test.go`

- [ ] **Написать failing тест для sign()**

```go
// pkg/trader/bybit_test.go
package trader

import "testing"

func TestSign(t *testing.T) {
	// Known-good HMAC-SHA256: echo -n "abc" | openssl dgst -sha256 -hmac "key"
	got := sign("1000", "APIKEY", "SECRET", "10000", "symbol=BTCUSDT")
	if got == "" {
		t.Fatal("sign returned empty string")
	}
	// Same inputs → same output
	got2 := sign("1000", "APIKEY", "SECRET", "10000", "symbol=BTCUSDT")
	if got != got2 {
		t.Error("sign must be deterministic")
	}
	// Different secret → different output
	got3 := sign("1000", "APIKEY", "OTHER", "10000", "symbol=BTCUSDT")
	if got == got3 {
		t.Error("different secret must produce different signature")
	}
}
```

- [ ] **Запустить тест — FAIL**

```bash
go test ./pkg/trader/... 
# Expected: build failed
```

- [ ] **Реализовать types.go**

```go
// pkg/trader/types.go
package trader

import "time"

// Credentials holds decrypted Bybit API credentials.
type Credentials struct {
	APIKey    string
	SecretKey string
}

// OrderRequest maps to Bybit POST /v5/order/create body.
type OrderRequest struct {
	Symbol           string `json:"symbol"`
	Category         string `json:"category"`
	Side             string `json:"side"`
	OrderType        string `json:"orderType"`
	Qty              string `json:"qty"`
	Price            string `json:"price,omitempty"`
	TriggerPrice     string `json:"triggerPrice,omitempty"`
	TriggerBy        string `json:"triggerBy,omitempty"`
	TriggerDirection int    `json:"triggerDirection,omitempty"`
	TimeInForce      string `json:"timeInForce,omitempty"`
	OrderFilter      string `json:"orderFilter,omitempty"`
	ReduceOnly       bool   `json:"reduceOnly"`
	PositionIdx      int    `json:"positionIdx"`
	OrderLinkId      string `json:"orderLinkId,omitempty"`
}

// CancelRequest maps to Bybit POST /v5/order/cancel body.
type CancelRequest struct {
	Symbol      string `json:"symbol"`
	Category    string `json:"category"`
	OrderId     string `json:"orderId,omitempty"`
	OrderLinkId string `json:"orderLinkId,omitempty"`
	OrderFilter string `json:"orderFilter,omitempty"`
}

// LeverageRequest maps to Bybit POST /v5/position/set-leverage body.
type LeverageRequest struct {
	Symbol       string `json:"symbol"`
	Category     string `json:"category"`
	BuyLeverage  string `json:"buyLeverage"`
	SellLeverage string `json:"sellLeverage"`
}

// OrderResult is returned after placing an order.
type OrderResult struct {
	OrderId     string `json:"orderId"`
	OrderLinkId string `json:"orderLinkId"`
}

// Position represents an open Bybit position.
type Position struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Size          string `json:"size"`
	EntryPrice    string `json:"entryPrice"`
	MarkPrice     string `json:"markPrice"`
	LiqPrice      string `json:"liqPrice"`
	UnrealisedPnl string `json:"unrealisedPnl"`
	Leverage      string `json:"leverage"`
	PositionIdx   int    `json:"positionIdx"`
	Category      string `json:"category"`
}

// Order represents a Bybit order (open or historical).
type Order struct {
	OrderId     string `json:"orderId"`
	OrderLinkId string `json:"orderLinkId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	OrderType   string `json:"orderType"`
	Price       string `json:"price"`
	Qty         string `json:"qty"`
	CumExecQty  string `json:"cumExecQty"`
	CumExecFee  string `json:"cumExecFee"`
	OrderStatus string `json:"orderStatus"`
	TriggerPrice string `json:"triggerPrice"`
	Category    string `json:"category"`
	OrderFilter string `json:"orderFilter"`
	CreatedTime string `json:"createdTime"`
}

// Execution represents a trade fill, funding payment, or fee record.
type Execution struct {
	ExecId      string    `json:"execId"`
	OrderId     string    `json:"orderId"`
	OrderLinkId string    `json:"orderLinkId"`
	Symbol      string    `json:"symbol"`
	Side        string    `json:"side"`
	ExecType    string    `json:"execType"`
	ExecQty     string    `json:"execQty"`
	ExecPrice   string    `json:"execPrice"`
	ExecValue   string    `json:"execValue"`
	ExecFee     string    `json:"execFee"`
	FeeRate     string    `json:"feeRate"`
	IsMaker     bool      `json:"isMaker"`
	ExecTime    time.Time `json:"-"`
	ExecTimeMs  string    `json:"execTime"`
	Category    string    `json:"category"`
}
```

- [ ] **Реализовать bybit.go**

```go
// pkg/trader/bybit.go
package trader

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	bybitBase  = "https://api.bybit.com"
	recvWindow = "10000"
)

// sign builds the Bybit HMAC-SHA256 signature string.
func sign(timestamp, apiKey, secret, recvWin, payload string) string {
	msg := timestamp + apiKey + recvWin + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// --- server timestamp cache ---
var (
	tsMu      sync.Mutex
	tsOffset  int64
	tsUpdated time.Time
)

func serverTimestamp() string {
	tsMu.Lock()
	defer tsMu.Unlock()
	if time.Since(tsUpdated) > 5*time.Minute {
		resp, err := http.Get(bybitBase + "/v5/market/time")
		if err == nil {
			defer resp.Body.Close()
			var r struct {
				Time string `json:"time"`
			}
			if json.NewDecoder(resp.Body).Decode(&r) == nil && r.Time != "" {
				var serverMs int64
				fmt.Sscanf(r.Time, "%d", &serverMs)
				tsOffset = serverMs - time.Now().UnixMilli()
			}
			tsUpdated = time.Now()
		}
	}
	return fmt.Sprintf("%d", time.Now().UnixMilli()+tsOffset)
}

func authHeaders(creds Credentials, payload string) map[string]string {
	ts := serverTimestamp()
	sig := sign(ts, creds.APIKey, creds.SecretKey, recvWindow, payload)
	return map[string]string{
		"X-BAPI-API-KEY":     creds.APIKey,
		"X-BAPI-SIGN":        sig,
		"X-BAPI-TIMESTAMP":   ts,
		"X-BAPI-RECV-WINDOW": recvWindow,
	}
}

func doSignedGET(ctx context.Context, creds Credentials, path, query string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bybitBase+path+"?"+query, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range authHeaders(creds, query) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func checkRetCode(data []byte) error {
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("bybit: parse response: %w", err)
	}
	if r.RetCode != 0 {
		return fmt.Errorf("bybit: retCode=%d: %s", r.RetCode, r.RetMsg)
	}
	return nil
}

// PlaceOrder sends an order to Bybit and returns the order result.
func PlaceOrder(ctx context.Context, creds Credentials, req OrderRequest) (OrderResult, error) {
	data, err := doSignedPOST(ctx, creds, "/v5/order/create", req)
	if err != nil {
		return OrderResult{}, err
	}
	if err := checkRetCode(data); err != nil {
		return OrderResult{}, err
	}
	var r struct {
		Result OrderResult `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return OrderResult{}, err
	}
	return r.Result, nil
}

// CancelOrder cancels an existing order.
func CancelOrder(ctx context.Context, creds Credentials, req CancelRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/order/cancel", req)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

// SetLeverage sets buy and sell leverage for a symbol.
func SetLeverage(ctx context.Context, creds Credentials, req LeverageRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/position/set-leverage", req)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

// FetchPositions returns all open positions (linear USDT + USDC + inverse).
func FetchPositions(ctx context.Context, creds Credentials) ([]Position, error) {
	type req struct {
		category string
		extra    string
	}
	reqs := []req{
		{"linear", "settleCoin=USDT"},
		{"linear", "settleCoin=USDC"},
		{"inverse", ""},
	}
	var all []Position
	for _, r := range reqs {
		q := "category=" + r.category + "&limit=200"
		if r.extra != "" {
			q += "&" + r.extra
		}
		data, err := doSignedGET(ctx, creds, "/v5/position/list", q)
		if err != nil {
			continue
		}
		var resp struct {
			Result struct {
				List []Position `json:"list"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &resp) == nil {
			for _, p := range resp.Result.List {
				p.Category = r.category
				all = append(all, p)
			}
		}
	}
	return all, nil
}

// FetchOpenOrders returns active orders for all categories.
func FetchOpenOrders(ctx context.Context, creds Credentials) ([]Order, error) {
	type req struct {
		category    string
		orderFilter string
		extra       string
	}
	reqs := []req{
		{"linear", "Order", "settleCoin=USDT"},
		{"linear", "StopOrder", "settleCoin=USDT"},
		{"inverse", "Order", ""},
		{"inverse", "StopOrder", ""},
		{"spot", "Order", ""},
		{"spot", "StopOrder", ""},
	}
	var all []Order
	for _, r := range reqs {
		q := "category=" + r.category + "&orderFilter=" + r.orderFilter + "&limit=50"
		if r.extra != "" {
			q += "&" + r.extra
		}
		data, err := doSignedGET(ctx, creds, "/v5/order/realtime", q)
		if err != nil {
			continue
		}
		var resp struct {
			Result struct {
				List []Order `json:"list"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &resp) == nil {
			for _, o := range resp.Result.List {
				o.Category = r.category
				o.OrderFilter = r.orderFilter
				all = append(all, o)
			}
		}
	}
	return all, nil
}

// FetchOrderHistory returns closed orders. cursor="" for first page.
func FetchOrderHistory(ctx context.Context, creds Credentials, category, cursor string) ([]Order, string, error) {
	q := "category=" + category + "&limit=50"
	if cursor != "" {
		q += "&cursor=" + cursor
	}
	data, err := doSignedGET(ctx, creds, "/v5/order/history", q)
	if err != nil {
		return nil, "", err
	}
	if err := checkRetCode(data); err != nil {
		return nil, "", err
	}
	var resp struct {
		Result struct {
			List           []Order `json:"list"`
			NextPageCursor string  `json:"nextPageCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", err
	}
	return resp.Result.List, resp.Result.NextPageCursor, nil
}

// FetchExecutions returns trade executions from the transaction log. cursor="" for first page.
func FetchExecutions(ctx context.Context, creds Credentials, category, cursor string) ([]Execution, string, error) {
	q := "category=" + category + "&limit=100"
	if cursor != "" {
		q += "&cursor=" + cursor
	}
	data, err := doSignedGET(ctx, creds, "/v5/execution/list", q)
	if err != nil {
		return nil, "", err
	}
	if err := checkRetCode(data); err != nil {
		return nil, "", err
	}
	var resp struct {
		Result struct {
			List           []Execution `json:"list"`
			NextPageCursor string      `json:"nextPageCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", err
	}
	return resp.Result.List, resp.Result.NextPageCursor, nil
}
```

- [ ] **Запустить тест — PASS**

```bash
go test ./pkg/trader/... -v -run TestSign
# Expected: PASS
```

- [ ] **Commit**

```bash
git add pkg/trader/
git commit -m "feat: add trader types and Bybit private REST client"
```

---

## Task 4: pkg/trader/ws.go — PositionStream

**Files:**
- Create: `pkg/trader/ws.go`

- [ ] **Реализовать**

```go
// pkg/trader/ws.go
package trader

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const bybitPrivateWS = "wss://stream.bybit.com/v5/private"

// safeSend sends JSON to a gorilla WebSocket connection, ignoring write errors.
func safeSend(conn *websocket.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// RunPositionStream opens a Bybit private WebSocket, authenticates, subscribes
// to position and order topics, fetches a REST snapshot, then relays WS deltas
// to the client conn until ctx is cancelled or conn closes.
func RunPositionStream(ctx context.Context, conn *websocket.Conn, creds Credentials, accountName string) {
	type msg map[string]any
	logMsg := func(m string, errFlag ...bool) {
		isErr := len(errFlag) > 0 && errFlag[0]
		safeSend(conn, msg{"type": "log", "message": m, "error": isErr})
	}

	safeSend(conn, msg{"type": "account", "accountName": accountName})

	// Build auth params using server time
	ts := serverTimestamp()
	var tsMs int64
	fmt.Sscanf(ts, "%d", &tsMs)
	expires := tsMs + 10000
	sigStr := fmt.Sprintf("GET/realtime%d", expires)
	mac := hmac.New(sha256.New, []byte(creds.SecretKey))
	mac.Write([]byte(sigStr))
	wsSign := hex.EncodeToString(mac.Sum(nil))

	bwsConn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		logMsg("Ошибка подключения к Bybit WS: "+err.Error(), true)
		return
	}
	defer bwsConn.Close()

	logMsg("Подключено к Bybit WS, авторизация...")

	// Auth
	authMsg, _ := json.Marshal(map[string]any{
		"op":   "auth",
		"args": []any{creds.APIKey, expires, wsSign},
	})
	bwsConn.WriteMessage(websocket.TextMessage, authMsg) //nolint:errcheck

	// Keepalive ping
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	// Read from Bybit WS in goroutine
	bybitCh := make(chan []byte, 64)
	bybitErrCh := make(chan error, 1)
	go func() {
		for {
			_, data, err := bwsConn.ReadMessage()
			if err != nil {
				bybitErrCh <- err
				return
			}
			bybitCh <- data
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case <-pingTicker.C:
			ping, _ := json.Marshal(map[string]string{"op": "ping"})
			bwsConn.WriteMessage(websocket.TextMessage, ping) //nolint:errcheck

		case err := <-bybitErrCh:
			logMsg("Bybit WS закрыт: "+err.Error(), true)
			return

		case data := <-bybitCh:
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				continue
			}
			op, _ := raw["op"].(string)
			switch op {
			case "auth":
				if success, _ := raw["success"].(bool); success {
					sub, _ := json.Marshal(map[string]any{
						"op":   "subscribe",
						"args": []string{"position", "order"},
					})
					bwsConn.WriteMessage(websocket.TextMessage, sub) //nolint:errcheck
					logMsg("Авторизация OK, подписка на position и order")
					go func() {
						if err := fetchAndSendSnapshot(ctx, conn, creds); err != nil {
							logMsg("[REST] Ошибка снапшота: "+err.Error(), true)
						}
					}()
				} else {
					msg, _ := raw["ret_msg"].(string)
					logMsg("Авторизация провалена: "+msg, true)
					return
				}
			case "pong":
				// ignore
			default:
				topic, _ := raw["topic"].(string)
				if topic == "position" || topic == "order" {
					safeSend(conn, map[string]any{
						"type":     topic,
						"dataType": raw["type"],
						"data":     raw["data"],
					})
					if items, ok := raw["data"].([]any); ok {
						log.Printf("trader ws: %s/%v count=%d", topic, raw["type"], len(items))
					}
				}
			}
		}
	}
}

func fetchAndSendSnapshot(ctx context.Context, conn *websocket.Conn, creds Credentials) error {
	positions, err := FetchPositions(ctx, creds)
	if err != nil {
		return fmt.Errorf("positions: %w", err)
	}
	safeSend(conn, map[string]any{"type": "position", "dataType": "snapshot", "data": positions})

	orders, err := FetchOpenOrders(ctx, creds)
	if err != nil {
		return fmt.Errorf("orders: %w", err)
	}
	safeSend(conn, map[string]any{"type": "order", "dataType": "snapshot", "data": orders})

	safeSend(conn, map[string]any{
		"type":    "log",
		"message": fmt.Sprintf("[REST] Позиции: %d | Ордера: %d", len(positions), len(orders)),
	})
	return nil
}
```

- [ ] **Убедиться что компилируется**

```bash
go build ./pkg/trader/...
# Expected: no output (success)
```

- [ ] **Commit**

```bash
git add pkg/trader/ws.go
git commit -m "feat: add PositionStream private WebSocket proxy"
```

---

## Task 5: pkg/trader/syncer.go

**Files:**
- Create: `pkg/trader/syncer.go`

- [ ] **Реализовать**

```go
// pkg/trader/syncer.go
package trader

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
)

// accountRow is a minimal DB row for an exchange account.
type accountRow struct {
	id        string
	ownerID   string
	exchange  string
	apiKeyEnc string
	secretEnc string
}

// Syncer periodically pulls execution history from Bybit and upserts into trader_executions.
type Syncer struct {
	pool     *pgxpool.Pool
	encKey   string
	syncDays int
	mu       sync.Mutex
	running  map[string]context.CancelFunc // accountID → cancel
}

// NewSyncer creates a Syncer. encKey is the hex encryption key. syncDays is backfill depth.
func NewSyncer(pool *pgxpool.Pool, encKey string, syncDays int) *Syncer {
	return &Syncer{
		pool:     pool,
		encKey:   encKey,
		syncDays: syncDays,
		running:  make(map[string]context.CancelFunc),
	}
}

// Start loads all active accounts and begins a sync goroutine per account.
// It also rescans for new accounts every 5 minutes.
func (s *Syncer) Start(ctx context.Context) {
	go func() {
		s.loadAndLaunch(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.loadAndLaunch(ctx)
			}
		}
	}()
}

func (s *Syncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, exchange, api_key_enc, secret_enc
		 FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.id, &a.ownerID, &a.exchange, &a.apiKeyEnc, &a.secretEnc); err != nil {
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

func (s *Syncer) launch(ctx context.Context, a accountRow) {
	childCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.running[a.id] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, a.id)
			s.mu.Unlock()
		}()
		s.runAccount(childCtx, a)
	}()
}

func (s *Syncer) runAccount(ctx context.Context, a accountRow) {
	apiKey, err := crypto.Decrypt(a.apiKeyEnc, s.encKey)
	if err != nil {
		log.Printf("syncer: decrypt api_key account=%s: %v", a.id, err)
		return
	}
	secret, err := crypto.Decrypt(a.secretEnc, s.encKey)
	if err != nil {
		log.Printf("syncer: decrypt secret account=%s: %v", a.id, err)
		return
	}
	creds := Credentials{APIKey: apiKey, SecretKey: secret}

	// Initial backfill
	s.syncExecutions(ctx, a, creds)

	execTicker := time.NewTicker(60 * time.Second)
	histTicker := time.NewTicker(5 * time.Minute)
	defer execTicker.Stop()
	defer histTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-execTicker.C:
			s.syncExecutions(ctx, a, creds)
		case <-histTicker.C:
			s.syncOrderHistory(ctx, a, creds)
		}
	}
}

func (s *Syncer) syncExecutions(ctx context.Context, a accountRow, creds Credentials) {
	since := time.Now().AddDate(0, 0, -s.syncDays)
	for _, category := range []string{"linear", "inverse", "spot"} {
		cursor := ""
		for {
			execs, next, err := FetchExecutions(ctx, creds, category, cursor)
			if err != nil {
				log.Printf("syncer: fetch executions account=%s category=%s: %v", a.id, category, err)
				break
			}
			for _, e := range execs {
				var execTimeMs int64
				fmt.Sscanf(e.ExecTimeMs, "%d", &execTimeMs)
				execTime := time.UnixMilli(execTimeMs)
				if execTime.Before(since) {
					next = "" // stop pagination
					break
				}
				isMaker := strconv.FormatBool(e.IsMaker)
				_ = isMaker
				_, err := s.pool.Exec(ctx, `
					INSERT INTO trader_executions
					  (owner_id, account_id, exec_id, order_id, order_link_id,
					   exchange, symbol, category, side, exec_type,
					   qty, price, exec_value, exec_fee, fee_rate, is_maker, exec_time)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
					ON CONFLICT (account_id, exec_id) DO NOTHING`,
					a.ownerID, a.id, e.ExecId, nullStr(e.OrderId), nullStr(e.OrderLinkId),
					a.exchange, e.Symbol, category, nullStr(e.Side), e.ExecType,
					nullNum(e.ExecQty), nullNum(e.ExecPrice), nullNum(e.ExecValue),
					nullNum(e.ExecFee), nullNum(e.FeeRate), e.IsMaker, execTime,
				)
				if err != nil {
					log.Printf("syncer: upsert exec %s: %v", e.ExecId, err)
				}
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
}

func (s *Syncer) syncOrderHistory(ctx context.Context, a accountRow, creds Credentials) {
	for _, category := range []string{"linear", "inverse", "spot"} {
		cursor := ""
		for {
			orders, next, err := FetchOrderHistory(ctx, creds, category, cursor)
			if err != nil {
				break
			}
			for _, o := range orders {
				if o.OrderLinkId == "" || len(o.OrderLinkId) < 3 || o.OrderLinkId[:3] != "sis" {
					continue // только наши ордера
				}
				_, err := s.pool.Exec(ctx, `
					UPDATE trader_orders
					SET status=$1, cum_exec_qty=$2, cum_exec_fee=$3, order_id=COALESCE(NULLIF($4,''), order_id), updated_at=NOW()
					WHERE order_link_id=$5`,
					o.OrderStatus, nullNum(o.CumExecQty), nullNum(o.CumExecFee), o.OrderId, o.OrderLinkId,
				)
				if err != nil {
					log.Printf("syncer: update order %s: %v", o.OrderLinkId, err)
				}
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
}

// nullStr returns nil if s is empty, otherwise s.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullNum parses s as a number; returns nil if empty or unparseable.
func nullNum(s string) any {
	if s == "" {
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return nil
	}
	return f
}
```

- [ ] **Убедиться что компилируется**

```bash
go build ./pkg/trader/...
# Expected: no output
```

- [ ] **Commit**

```bash
git add pkg/trader/syncer.go
git commit -m "feat: add background execution/order history syncer"
```

---

## Task 6: Server + accounts_handler.go

**Files:**
- Modify: `services/api-gateway/server.go`
- Create: `services/api-gateway/accounts_handler.go`
- Create: `services/api-gateway/accounts_handler_test.go`

- [ ] **Обновить server.go — добавить encKey**

Заменить в `services/api-gateway/server.go`:
```go
type Server struct {
	pool      *pgxpool.Pool
	rdb       *redis.Client
	jwtSecret []byte
}

func NewServer(pool *pgxpool.Pool, rdb *redis.Client, jwtSecret string) *Server {
	return &Server{pool: pool, rdb: rdb, jwtSecret: []byte(jwtSecret)}
}
```
на:
```go
type Server struct {
	pool      *pgxpool.Pool
	rdb       *redis.Client
	jwtSecret []byte
	encKey    string
}

func NewServer(pool *pgxpool.Pool, rdb *redis.Client, jwtSecret, encKey string) *Server {
	return &Server{pool: pool, rdb: rdb, jwtSecret: []byte(jwtSecret), encKey: encKey}
}
```

- [ ] **Написать failing тест**

```go
//go:build integration

// services/api-gateway/accounts_handler_test.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testEncKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestCreateListDeleteAccount(t *testing.T) {
	s := newTestServer(t)
	s.encKey = testEncKey
	userID := createWHUser(t, s, "acc1")

	// Create
	body := `{"exchange":"bybit","label":"main","api_key":"TESTKEY","secret":"TESTSECRET"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateAccount(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	accID, _ := created["id"].(string)
	if accID == "" {
		t.Fatal("expected id in response")
	}
	// Response must not contain raw keys
	if _, ok := created["api_key"]; ok {
		t.Error("response must not expose api_key")
	}

	// List
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req2 = withUserID(req2, userID)
	s.ListAccounts(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list: got %d", rec2.Code)
	}
	var list []map[string]any
	json.NewDecoder(rec2.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list))
	}

	// Delete
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/accounts/"+accID, nil)
	req3 = withUserID(req3, userID)
	req3 = withChiParams(req3, map[string]string{"id": accID})
	s.DeleteAccount(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec3.Code)
	}

	// Verify deleted
	var count int
	s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM exchange_accounts WHERE id=$1", accID).Scan(&count)
	if count != 0 {
		t.Error("account should be deleted")
	}
}

func TestCreateAccount_NoEncKey(t *testing.T) {
	s := newTestServer(t)
	// encKey is empty — should 500
	userID := createWHUser(t, s, "acc2")
	body := `{"exchange":"bybit","label":"x","api_key":"K","secret":"S"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateAccount(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}
```

- [ ] **Запустить тест — FAIL**

```bash
go test -tags integration ./services/api-gateway/... -run TestCreateListDeleteAccount -v
# Expected: compile error (CreateAccount not defined)
```

- [ ] **Реализовать accounts_handler.go**

```go
// services/api-gateway/accounts_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/crypto"
)

type accountRow struct {
	ID        string    `json:"id"`
	Exchange  string    `json:"exchange"`
	Label     string    `json:"label"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// ListAccounts returns exchange accounts for the authenticated user (no keys).
// GET /accounts
func (s *Server) ListAccounts(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, exchange, label, is_active, created_at
		 FROM exchange_accounts WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]accountRow, 0)
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, a)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateAccount encrypts and stores a new exchange account.
// POST /accounts
func (s *Server) CreateAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Exchange string `json:"exchange"`
		Label    string `json:"label"`
		APIKey   string `json:"api_key"`
		Secret   string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Exchange == "" || req.APIKey == "" || req.Secret == "" {
		writeError(w, http.StatusBadRequest, "exchange, api_key and secret are required")
		return
	}
	encKey, err := crypto.Encrypt(req.APIKey, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption error")
		return
	}
	encSecret, err := crypto.Encrypt(req.Secret, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption error")
		return
	}
	var a accountRow
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, exchange, label, is_active, created_at`,
		userID, req.Exchange, req.Label, encKey, encSecret,
	).Scan(&a.ID, &a.Exchange, &a.Label, &a.IsActive, &a.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// DeleteAccount removes an exchange account owned by the caller.
// DELETE /accounts/:id
func (s *Server) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	_, err := s.pool.Exec(r.Context(),
		`DELETE FROM exchange_accounts WHERE id=$1 AND owner_id=$2`, id, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VerifyAccount checks that the stored API keys are valid via Bybit.
// GET /accounts/:id/verify
func (s *Server) VerifyAccount(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	var apiKeyEnc, secretEnc string
	err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		id, userID,
	).Scan(&apiKeyEnc, &secretEnc)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decryption error")
		return
	}

	import_creds := struct{ APIKey, SecretKey string }{apiKey, secret}
	_ = import_creds
	// Call Bybit /v5/user/query-api
	import_trader "sis/pkg/trader"
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret}
	data, err := trader.QueryAPI(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": data})
}
```

Обратите внимание: в `VerifyAccount` нужна функция `trader.QueryAPI`. Добавьте в конец `pkg/trader/bybit.go`:

```go
// QueryAPI calls /v5/user/query-api and returns the raw result JSON.
func QueryAPI(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	data, err := doSignedGET(ctx, creds, "/v5/user/query-api", "")
	if err != nil {
		return nil, err
	}
	if err := checkRetCode(data); err != nil {
		return nil, err
	}
	var r struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return r.Result, nil
}
```

Также исправьте `VerifyAccount` — убрать псевдо-импорт в теле функции (это Go, импорты вверху файла):

```go
// services/api-gateway/accounts_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/crypto"
	"sis/pkg/trader"
)
```

И упрощённый `VerifyAccount`:
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
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret}
	info, err := trader.QueryAPI(r.Context(), creds)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}
```

- [ ] **Запустить тесты — PASS**

```bash
go test -tags integration ./services/api-gateway/... -run TestCreateListDeleteAccount -v
# Expected: PASS
```

- [ ] **Commit**

```bash
git add services/api-gateway/server.go services/api-gateway/accounts_handler.go services/api-gateway/accounts_handler_test.go pkg/trader/bybit.go
git commit -m "feat: add exchange accounts CRUD with encrypted key storage"
```

---

## Task 7: trader_handler.go — Place/Cancel/Leverage

**Files:**
- Create: `services/api-gateway/trader_handler.go`
- Create: `services/api-gateway/trader_handler_test.go`

- [ ] **Написать failing тест (unit, без БД)**

```go
// services/api-gateway/trader_handler_test.go
package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlaceOrder_MissingFields(t *testing.T) {
	s := &Server{jwtSecret: []byte("secret"), encKey: ""}
	cases := []struct {
		body string
		want int
	}{
		{`{}`, http.StatusBadRequest},
		{`{"account_id":"x","symbol":"BTCUSDT","side":"Buy","order_type":"Market"}`, http.StatusBadRequest}, // missing qty
		{`{"account_id":"x","symbol":"BTCUSDT","side":"Buy","order_type":"Limit","qty":"0.001"}`, http.StatusBadRequest}, // missing price for Limit
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/trader/order", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req = withUserID(req, "user-1")
		s.TraderPlaceOrder(rec, req)
		if rec.Code != tc.want {
			t.Errorf("body=%s got %d, want %d: %s", tc.body, rec.Code, tc.want, rec.Body.String())
		}
	}
}
```

- [ ] **Запустить тест — FAIL**

```bash
go test ./services/api-gateway/... -run TestPlaceOrder_MissingFields -v
# Expected: compile error (TraderPlaceOrder not defined)
```

- [ ] **Реализовать trader_handler.go**

```go
// services/api-gateway/trader_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sis/pkg/crypto"
	"sis/pkg/trader"
)

// loadCreds looks up an exchange account by id (must be owned by userID), decrypts keys.
func (s *Server) loadCreds(r *http.Request, accountID, userID string) (trader.Credentials, error) {
	var apiKeyEnc, secretEnc string
	err := s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		accountID, userID,
	).Scan(&apiKeyEnc, &secretEnc)
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
	return trader.Credentials{APIKey: apiKey, SecretKey: secret}, nil
}

// makeOrderLinkID generates a unique order link ID with the "sis" prefix.
func makeOrderLinkID(accountID string) string {
	short := accountID
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("sis%s%d", short, time.Now().UnixMilli())
}

type placeOrderReq struct {
	AccountID        string `json:"account_id"`
	Symbol           string `json:"symbol"`
	Category         string `json:"category"`
	Side             string `json:"side"`
	OrderType        string `json:"order_type"`
	Qty              string `json:"qty"`
	Price            string `json:"price"`
	TriggerPrice     string `json:"trigger_price"`
	TriggerBy        string `json:"trigger_by"`
	TriggerDirection int    `json:"trigger_direction"`
	TimeInForce      string `json:"time_in_force"`
	OrderFilter      string `json:"order_filter"`
	ReduceOnly       bool   `json:"reduce_only"`
	PositionIdx      int    `json:"position_idx"`
}

// TraderPlaceOrder places an order via Bybit and records it in trader_orders.
// POST /trader/order
func (s *Server) TraderPlaceOrder(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req placeOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" || req.Side == "" || req.OrderType == "" {
		writeError(w, http.StatusBadRequest, "account_id, symbol, side, order_type are required")
		return
	}
	if req.Qty == "" || req.Qty == "0" {
		writeError(w, http.StatusBadRequest, "qty is required")
		return
	}
	if req.OrderType == "Limit" && req.Price == "" {
		writeError(w, http.StatusBadRequest, "price is required for Limit orders")
		return
	}
	if req.Category == "" {
		req.Category = "linear"
	}

	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	orderLinkID := makeOrderLinkID(req.AccountID)
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

	result, err := trader.PlaceOrder(r.Context(), creds, orderReq)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}

	// Store in trader_orders
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

// TraderCancelOrder cancels an order via Bybit.
// DELETE /trader/order
func (s *Server) TraderCancelOrder(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		AccountID   string `json:"account_id"`
		Symbol      string `json:"symbol"`
		Category    string `json:"category"`
		OrderID     string `json:"order_id"`
		OrderFilter string `json:"order_filter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" || req.OrderID == "" {
		writeError(w, http.StatusBadRequest, "account_id, symbol and order_id are required")
		return
	}
	if req.Category == "" {
		req.Category = "linear"
	}
	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if err := trader.CancelOrder(r.Context(), creds, trader.CancelRequest{
		Symbol:      req.Symbol,
		Category:    req.Category,
		OrderId:     req.OrderID,
		OrderFilter: req.OrderFilter,
	}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	// Update local status
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE trader_orders SET status='Cancelled', updated_at=NOW() WHERE order_id=$1 AND owner_id=$2`,
		req.OrderID, userID,
	)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// TraderSetLeverage sets leverage for a symbol.
// POST /trader/leverage
func (s *Server) TraderSetLeverage(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		AccountID string `json:"account_id"`
		Symbol    string `json:"symbol"`
		Category  string `json:"category"`
		Leverage  string `json:"leverage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" || req.Leverage == "" {
		writeError(w, http.StatusBadRequest, "account_id, symbol and leverage are required")
		return
	}
	if req.Category == "" {
		req.Category = "linear"
	}
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
}

func nullNum(s string) any {
	if s == "" {
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return nil
	}
	return f
}
```

> **Внимание:** `nullNum` также определена в `syncer.go`. Переместите её в отдельный файл `pkg/trader/util.go` или используйте только в одном пакете. Поскольку `syncer.go` — в пакете `trader`, а `trader_handler.go` — в пакете `main`, коллизии нет.

- [ ] **Запустить тест — PASS**

```bash
go test ./services/api-gateway/... -run TestPlaceOrder_MissingFields -v
# Expected: PASS
```

- [ ] **Commit**

```bash
git add services/api-gateway/trader_handler.go services/api-gateway/trader_handler_test.go
git commit -m "feat: add order placement, cancellation and leverage handlers"
```

---

## Task 8: trader_history_handler.go + trader_ws_handler.go

**Files:**
- Create: `services/api-gateway/trader_history_handler.go`
- Create: `services/api-gateway/trader_history_handler_test.go`
- Create: `services/api-gateway/trader_ws_handler.go`

- [ ] **Написать failing тест для истории**

```go
//go:build integration

// services/api-gateway/trader_history_handler_test.go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListTraderOrders_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th1")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/orders?status=all", nil)
	req = withUserID(req, userID)
	s.ListTraderOrders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTraderExecutions_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th2")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/executions?type=all", nil)
	req = withUserID(req, userID)
	s.ListTraderExecutions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetTraderStats_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th3")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/stats", nil)
	req = withUserID(req, userID)
	s.GetTraderStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func createTestAccount(t *testing.T, s *Server, userID string) string {
	t.Helper()
	s.encKey = testEncKey
	var id string
	encKey, _ := s.pool.QueryRow(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,'bybit','test','enc_key','enc_secret') RETURNING id`, userID,
	).Scan(&id)
	_ = encKey
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", id)
	})
	return id
}

func TestListTraderOrders_WithData(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th4")
	accID := createTestAccount(t, s, userID)

	// Insert a test order
	s.pool.Exec(context.Background(),
		`INSERT INTO trader_orders (owner_id, account_id, order_link_id, exchange, symbol, category, side, order_type, qty)
		 VALUES ($1,$2,'sis_test_001','bybit','BTCUSDT','linear','Buy','Market',0.001)`,
		userID, accID,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/orders?status=all&limit=10", nil)
	req = withUserID(req, userID)
	s.ListTraderOrders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInsertAndQueryExecution(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "th5")
	accID := createTestAccount(t, s, userID)

	s.pool.Exec(context.Background(),
		`INSERT INTO trader_executions
		 (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_time)
		 VALUES ($1,$2,'exec_001','bybit','BTCUSDT','linear','Trade',$3)`,
		userID, accID, time.Now(),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/executions?type=Trade", nil)
	req = withUserID(req, userID)
	s.ListTraderExecutions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Запустить тест — FAIL**

```bash
go test -tags integration ./services/api-gateway/... -run TestListTraderOrders_Empty -v
# Expected: compile error
```

- [ ] **Реализовать trader_history_handler.go**

```go
// services/api-gateway/trader_history_handler.go
package main

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type traderOrderRow struct {
	ID           string     `json:"id"`
	AccountID    string     `json:"account_id"`
	OrderLinkID  string     `json:"order_link_id"`
	OrderID      *string    `json:"order_id"`
	Exchange     string     `json:"exchange"`
	Symbol       string     `json:"symbol"`
	Category     string     `json:"category"`
	Side         string     `json:"side"`
	OrderType    string     `json:"order_type"`
	Qty          string     `json:"qty"`
	Price        *string    `json:"price"`
	TriggerPrice *string    `json:"trigger_price"`
	Status       string     `json:"status"`
	CumExecQty   string     `json:"cum_exec_qty"`
	CumExecFee   string     `json:"cum_exec_fee"`
	CreatedAt    time.Time  `json:"created_at"`
}

type execRow struct {
	ID          string    `json:"id"`
	ExecID      string    `json:"exec_id"`
	OrderID     *string   `json:"order_id"`
	OrderLinkID *string   `json:"order_link_id"`
	Symbol      string    `json:"symbol"`
	Category    string    `json:"category"`
	Side        *string   `json:"side"`
	ExecType    string    `json:"exec_type"`
	Qty         *string   `json:"qty"`
	Price       *string   `json:"price"`
	ExecValue   *string   `json:"exec_value"`
	ExecFee     *string   `json:"exec_fee"`
	FeeRate     *string   `json:"fee_rate"`
	IsMaker     *bool     `json:"is_maker"`
	ExecTime    time.Time `json:"exec_time"`
}

// ListTraderOrders returns paginated orders for the authenticated user.
// GET /trader/orders?account_id=&status=open|closed|all&symbol=&page=1&limit=50
func (s *Server) ListTraderOrders(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	status := q.Get("status")
	symbol := q.Get("symbol")
	accountID := q.Get("account_id")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	where := "owner_id=$1"
	args := []any{userID}
	i := 2
	if accountID != "" {
		where += fmt.Sprintf(" AND account_id=$%d", i)
		args = append(args, accountID)
		i++
	}
	if symbol != "" {
		where += fmt.Sprintf(" AND symbol=$%d", i)
		args = append(args, symbol)
		i++
	}
	switch status {
	case "open":
		where += " AND status NOT IN ('Filled','Cancelled','Rejected','Deactivated')"
	case "closed":
		where += " AND status IN ('Filled','Cancelled','Rejected','Deactivated')"
	}

	var total int
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM trader_orders WHERE "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := s.pool.Query(r.Context(),
		fmt.Sprintf(`SELECT id, account_id, order_link_id, order_id, exchange, symbol, category, side, order_type,
			qty::text, price::text, trigger_price::text, status, cum_exec_qty::text, cum_exec_fee::text, created_at
			FROM trader_orders WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, i, i+1),
		args...,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]traderOrderRow, 0)
	for rows.Next() {
		var o traderOrderRow
		if err := rows.Scan(&o.ID, &o.AccountID, &o.OrderLinkID, &o.OrderID, &o.Exchange,
			&o.Symbol, &o.Category, &o.Side, &o.OrderType,
			&o.Qty, &o.Price, &o.TriggerPrice, &o.Status,
			&o.CumExecQty, &o.CumExecFee, &o.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": result, "total": total, "page": page})
}

// ListTraderExecutions returns paginated executions for the authenticated user.
// GET /trader/executions?account_id=&type=Trade|Funding|Fee|all&symbol=&from=&to=&page=1&limit=100
func (s *Server) ListTraderExecutions(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	execType := q.Get("type")
	symbol := q.Get("symbol")
	accountID := q.Get("account_id")
	fromStr := q.Get("from")
	toStr := q.Get("to")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := (page - 1) * limit

	where := "owner_id=$1"
	args := []any{userID}
	i := 2
	if accountID != "" {
		where += fmt.Sprintf(" AND account_id=$%d", i)
		args = append(args, accountID)
		i++
	}
	if execType != "" && execType != "all" {
		where += fmt.Sprintf(" AND exec_type=$%d", i)
		args = append(args, execType)
		i++
	}
	if symbol != "" {
		where += fmt.Sprintf(" AND symbol=$%d", i)
		args = append(args, symbol)
		i++
	}
	if fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			where += fmt.Sprintf(" AND exec_time>=$%d", i)
			args = append(args, t)
			i++
		}
	}
	if toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			where += fmt.Sprintf(" AND exec_time<=$%d", i)
			args = append(args, t)
			i++
		}
	}

	var total int
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM trader_executions WHERE "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := s.pool.Query(r.Context(),
		fmt.Sprintf(`SELECT id, exec_id, order_id, order_link_id, symbol, category, side, exec_type,
			qty::text, price::text, exec_value::text, exec_fee::text, fee_rate::text, is_maker, exec_time
			FROM trader_executions WHERE %s ORDER BY exec_time DESC LIMIT $%d OFFSET $%d`, where, i, i+1),
		args...,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]execRow, 0)
	for rows.Next() {
		var e execRow
		if err := rows.Scan(&e.ID, &e.ExecID, &e.OrderID, &e.OrderLinkID, &e.Symbol, &e.Category,
			&e.Side, &e.ExecType, &e.Qty, &e.Price, &e.ExecValue, &e.ExecFee, &e.FeeRate,
			&e.IsMaker, &e.ExecTime); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": result, "total": total, "page": page})
}

// GetTraderStats returns aggregated fee/funding/pnl stats.
// GET /trader/stats?account_id=&from=&to=
func (s *Server) GetTraderStats(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	where := "owner_id=$1"
	args := []any{userID}
	i := 2
	if acc := q.Get("account_id"); acc != "" {
		where += fmt.Sprintf(" AND account_id=$%d", i)
		args = append(args, acc)
		i++
	}
	if f := q.Get("from"); f != "" {
		if t, err := time.Parse(time.RFC3339, f); err == nil {
			where += fmt.Sprintf(" AND exec_time>=$%d", i)
			args = append(args, t)
			i++
		}
	}
	if t := q.Get("to"); t != "" {
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			where += fmt.Sprintf(" AND exec_time<=$%d", i)
			args = append(args, ts)
		}
	}

	var totalFee, totalFunding float64
	var tradeCount int
	s.pool.QueryRow(r.Context(),
		fmt.Sprintf(`SELECT
			COALESCE(SUM(exec_fee) FILTER (WHERE exec_type='Trade'),0),
			COALESCE(SUM(exec_fee) FILTER (WHERE exec_type='Funding'),0),
			COUNT(*) FILTER (WHERE exec_type='Trade')
			FROM trader_executions WHERE %s`, where),
		args...,
	).Scan(&totalFee, &totalFunding, &tradeCount)

	writeJSON(w, http.StatusOK, map[string]any{
		"total_fee":     totalFee,
		"total_funding": totalFunding,
		"trade_count":   tradeCount,
	})
}
```

- [ ] **Реализовать trader_ws_handler.go**

```go
// services/api-gateway/trader_ws_handler.go
package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"sis/pkg/auth"
	"sis/pkg/crypto"
	"sis/pkg/trader"
)

// PositionsStream streams Bybit private positions and orders to the client.
// GET /ws/trader/positions?token=<JWT>&account_id=<UUID>
func (s *Server) PositionsStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		http.Error(w, "account_id required", http.StatusBadRequest)
		return
	}

	var apiKeyEnc, secretEnc, label string
	err = s.pool.QueryRow(r.Context(),
		`SELECT api_key_enc, secret_enc, label FROM exchange_accounts WHERE id=$1 AND owner_id=$2 AND is_active=TRUE`,
		accountID, userID,
	).Scan(&apiKeyEnc, &secretEnc, &label)
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

	creds := trader.Credentials{APIKey: apiKey, SecretKey: secretKey}
	trader.RunPositionStream(r.Context(), conn, creds, label)
}
```

- [ ] **Запустить тесты — PASS**

```bash
go test -tags integration ./services/api-gateway/... -run "TestListTraderOrders|TestListTraderExecutions|TestGetTraderStats|TestInsertAndQuery" -v
# Expected: PASS
```

- [ ] **Commit**

```bash
git add services/api-gateway/trader_history_handler.go services/api-gateway/trader_history_handler_test.go services/api-gateway/trader_ws_handler.go
git commit -m "feat: add trader history, stats, and positions WebSocket handlers"
```

---

## Task 9: Обновить main.go — маршруты + syncer

**Files:**
- Modify: `services/api-gateway/main.go`

- [ ] **Обновить main.go**

Заменить блок `main()` на:

```go
func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")
	jwtSecret := mustEnv("JWT_SECRET")
	encKey := mustEnv("ENCRYPTION_KEY")
	listenAddr := getEnv("LISTEN_ADDR", ":8080")

	syncDays := 30
	if v := os.Getenv("TRADER_SYNC_DAYS"); v != "" {
		fmt.Sscanf(v, "%d", &syncDays)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, "migrations"); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	s := NewServer(pool, rdb, jwtSecret, encKey)

	// Start background syncer
	syncer := traderPkg.NewSyncer(pool, encKey, syncDays)
	syncer.Start(ctx)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Auth
	r.Post("/auth/register", s.Register)
	r.Post("/auth/login", s.Login)

	// Protected
	r.Group(func(r chi.Router) {
		r.Use(s.RequireAuth)

		r.Get("/signals", s.ListSignals)
		r.Post("/signals", s.CreateSignal)
		r.Get("/signals/{id}", s.GetSignal)
		r.Put("/signals/{id}", s.UpdateSignal)
		r.Delete("/signals/{id}", s.DeleteSignal)

		r.Post("/signals/{id}/backtest", s.SubmitBacktest)
		r.Post("/signals/{id}/optimize", s.SubmitOptimize)
		r.Get("/signals/{id}/backtest-results", s.GetBacktestResults)
		r.Get("/signals/{id}/optimization-results", s.GetOptimizationResults)

		r.Get("/webhooks", s.ListWebhooks)
		r.Post("/webhooks", s.CreateWebhook)
		r.Get("/webhooks/{id}", s.GetWebhook)
		r.Put("/webhooks/{id}", s.UpdateWebhook)
		r.Delete("/webhooks/{id}", s.DeleteWebhook)

		// Exchange accounts
		r.Get("/accounts", s.ListAccounts)
		r.Post("/accounts", s.CreateAccount)
		r.Delete("/accounts/{id}", s.DeleteAccount)
		r.Get("/accounts/{id}/verify", s.VerifyAccount)

		// Trader
		r.Post("/trader/order", s.TraderPlaceOrder)
		r.Delete("/trader/order", s.TraderCancelOrder)
		r.Post("/trader/leverage", s.TraderSetLeverage)
		r.Get("/trader/orders", s.ListTraderOrders)
		r.Get("/trader/executions", s.ListTraderExecutions)
		r.Get("/trader/stats", s.GetTraderStats)
	})

	// WebSocket endpoints — auth via ?token= query param
	r.Get("/ws/jobs/{id}/progress", s.JobProgress)
	r.Get("/ws/trader/positions", s.PositionsStream)

	srv := &http.Server{Addr: listenAddr, Handler: r}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("api-gateway: listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
	log.Println("api-gateway: stopped")
}
```

Добавить в imports:
```go
traderPkg "sis/pkg/trader"
```

- [ ] **Убедиться что компилируется**

```bash
go build ./services/api-gateway/...
# Expected: no output
```

- [ ] **Запустить все тесты**

```bash
go test ./pkg/crypto/... ./pkg/trader/... ./services/api-gateway/... -v
go test -tags integration ./services/api-gateway/... -v
```

- [ ] **Commit**

```bash
git add services/api-gateway/main.go
git commit -m "feat: wire trader routes, accounts routes, and background syncer into api-gateway"
```

---

## Self-Review

**Spec coverage:**
- ✅ exchange_accounts таблица с шифрованием (Task 1, 2, 6)
- ✅ trader_orders + trader_executions (Task 1)
- ✅ orderLinkId = "sis_..." при размещении ордера (Task 7)
- ✅ PlaceOrder / CancelOrder / SetLeverage (Task 7)
- ✅ PositionStream WS с REST-снапшотом (Task 4, 8)
- ✅ Фоновая синхронизация executions + order history (Task 5)
- ✅ История ордеров / executions / stats API (Task 8)
- ✅ ENCRYPTION_KEY / TRADER_SYNC_DAYS env vars (Task 9)
- ✅ Маршруты в main.go (Task 9)

**Placeholder scan:** нет TBD/TODO. ✅

**Type consistency:**
- `trader.Credentials` используется в ws.go, syncer.go, accounts_handler.go, trader_handler.go ✅
- `trader.OrderRequest`, `trader.CancelRequest`, `trader.LeverageRequest` определены в types.go и используются в trader_handler.go ✅
- `nullNum` определена в trader_handler.go (пакет main) — не конфликтует с syncer.go (пакет trader) ✅
- `makeOrderLinkID` и `loadCreds` — методы/функции только в trader_handler.go ✅
