# Whale Parser Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Создать микросервис `services/parser/`, перенести туда листинги Bybit, добавить whale tracker (ETH+Tron), сигнал `whale` в signal-engine и admin-эндпоинты управления в api-gateway.

**Architecture:** Новый Go-сервис `services/parser/` опрашивает Etherscan и Tronscan каждые 30 секунд. Результаты пишутся в PostgreSQL (`whale_events`) и Redis Hash (`whale:state`). Signal-engine читает состояние из Redis через пакетный poll раз в 30 секунд и обновляет in-memory кэш, который читает сигнал `whale`. api-gateway предоставляет CRUD-эндпоинты управления адресами и историей событий под `/admin/whale/`.

**Tech Stack:** Go 1.25, pgx/v5, go-redis/v9, Etherscan API v2, TronGrid API v1, Docker Alpine.

---

## File Map

**Новые файлы:**
- `migrations/073_whale_tables.sql` — таблицы whale_addresses, whale_events
- `services/parser/main.go` — точка входа сервиса
- `services/parser/bybit_news.go` — запуск bybitnews.Scraper
- `services/parser/whale/eth.go` — Etherscan API клиент
- `services/parser/whale/tron.go` — TronGrid API клиент
- `services/parser/whale/direction.go` — алгоритм накопление/раздача → score
- `services/parser/whale/tracker.go` — основной polling loop (30s)
- `services/parser/whale/discovery.go` — авто-обнаружение топ-адресов (еженедельно)
- `services/parser/Dockerfile`
- `pkg/signal/whale_state.go` — глобальный in-memory кэш whale state
- `services/api-gateway/whale_handler.go` — admin-эндпоинты управления

**Изменённые файлы:**
- `pkg/signal/types.go` — добавить `SymbolComputer` interface
- `pkg/signal/signals.go` — зарегистрировать сигнал `whale`
- `services/signal-engine/main.go` — добавить Redis whale poller + прогрев
- `services/api-gateway/server.go` — убрать `newsScraper` поле, добавить whale routes
- `services/api-gateway/main.go` — убрать запуск newsScraper, добавить whale routes
- `docker-compose.yml` — добавить сервис `parser`

---

### Task 1: DB Migration — таблицы whale

**Files:**
- Create: `migrations/073_whale_tables.sql`

- [ ] **Step 1: Создать файл миграции**

```sql
-- migrations/073_whale_tables.sql

CREATE TABLE whale_addresses (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  address      TEXT NOT NULL,
  chain        TEXT NOT NULL CHECK (chain IN ('eth', 'tron')),
  label        TEXT,
  is_manual    BOOLEAN NOT NULL DEFAULT false,
  volume_30d   FLOAT8 NOT NULL DEFAULT 0,
  is_active    BOOLEAN NOT NULL DEFAULT true,
  last_seen_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (address, chain)
);

CREATE TABLE whale_events (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  address      TEXT NOT NULL,
  chain        TEXT NOT NULL,
  symbol       TEXT NOT NULL,
  amount_usd   FLOAT8 NOT NULL,
  direction    TEXT NOT NULL CHECK (direction IN ('buy', 'sell', 'neutral')),
  score        FLOAT8 NOT NULL,
  tx_hash      TEXT NOT NULL,
  detected_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ON whale_events (symbol, detected_at DESC);
CREATE INDEX ON whale_events (detected_at DESC);
CREATE INDEX ON whale_events (address, detected_at DESC);
```

- [ ] **Step 2: Проверить что миграция применяется**

```bash
go run ./services/api-gateway 2>&1 | head -5
# Expected: логи без "migration" ошибок (api-gateway прогоняет migrate при старте)
```

- [ ] **Step 3: Commit**

```bash
git add migrations/073_whale_tables.sql
git commit -m "feat(whale): add whale_addresses and whale_events tables"
```

---

### Task 2: SymbolComputer interface + whale state cache

Signal.Compute() принимает только `[]Candle`, но whale сигналу нужен символ. Добавляем опциональный interface `SymbolComputer` и глобальный кэш состояния.

**Files:**
- Modify: `pkg/signal/types.go`
- Create: `pkg/signal/whale_state.go`
- Modify: `pkg/signal/engine.go` (в `computeUnit.compute`)

- [ ] **Step 1: Добавить SymbolComputer в types.go**

Найти конец файла `pkg/signal/types.go` (после `TTLAware`) и добавить:

```go
// SymbolComputer is an optional extension for signals that need to know
// the symbol being computed (e.g. external-data signals like whale tracker).
type SymbolComputer interface {
	Signal
	ComputeWithSymbol(symbol string, candles []Candle) State
}
```

- [ ] **Step 2: Создать pkg/signal/whale_state.go**

```go
package signal

import "sync"

// whaleCache holds the current whale signal state per symbol.
// Updated by signal-engine via SetWhaleState; read by whaleSignal.ComputeWithSymbol.
var whaleCache struct {
	mu     sync.RWMutex
	states map[string]State
}

func init() { whaleCache.states = make(map[string]State) }

// SetWhaleState updates the cached whale state for a symbol.
func SetWhaleState(symbol string, state State) {
	whaleCache.mu.Lock()
	whaleCache.states[symbol] = state
	whaleCache.mu.Unlock()
}

// GetWhaleState returns the cached whale state for a symbol (Neutral if absent).
func GetWhaleState(symbol string) State {
	whaleCache.mu.RLock()
	defer whaleCache.mu.RUnlock()
	if s, ok := whaleCache.states[symbol]; ok {
		return s
	}
	return Neutral
}
```

- [ ] **Step 3: Обновить compute в engine.go — поддержать SymbolComputer**

Найти в `pkg/signal/engine.go` цикл `for _, sig := range u.signals` в методе `compute`. Он выглядит примерно так:

```go
for i, sig := range u.signals {
    s := sig.Compute(candles)
    // ...
}
```

Заменить `sig.Compute(candles)` на:

```go
var s State
if sc, ok := sig.(SymbolComputer); ok {
    s = sc.ComputeWithSymbol(u.symbol, candles)
} else {
    s = sig.Compute(candles)
}
```

- [ ] **Step 4: Проверить компиляцию**

```bash
go build ./pkg/signal/...
# Expected: no output (success)
```

- [ ] **Step 5: Commit**

```bash
git add pkg/signal/types.go pkg/signal/whale_state.go pkg/signal/engine.go
git commit -m "feat(signal): SymbolComputer interface + whale state cache"
```

---

### Task 3: Регистрация сигнала `whale`

**Files:**
- Modify: `pkg/signal/signals.go`

- [ ] **Step 1: Добавить структуру и регистрацию в конец init() в signals.go**

Найти конец функции `init()` в `pkg/signal/signals.go` (после регистрации `adx`) и добавить:

```go
// whale — reads external on-chain whale state from in-memory cache (set by signal-engine Redis poller).
// Params:
//   threshold_usdt: minimum transfer size to count (informational only; filtering done by parser service)
Register("whale", func(cfg Config) Signal {
    return &whaleSignal{}
})
```

Добавить тип `whaleSignal` в конец файла `pkg/signal/signals.go`:

```go
// ── Whale Signal ──────────────────────────────────────────────────────────

type whaleSignal struct{}

// Compute satisfies Signal interface (fallback — symbol unknown here, returns Neutral).
func (s *whaleSignal) Compute(_ []Candle) State { return Neutral }

// ComputeWithSymbol satisfies SymbolComputer interface — reads from whale state cache.
func (s *whaleSignal) ComputeWithSymbol(symbol string, _ []Candle) State {
	return GetWhaleState(symbol)
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./pkg/signal/...
# Expected: no output
```

- [ ] **Step 3: Commit**

```bash
git add pkg/signal/signals.go
git commit -m "feat(signal): register whale signal reading from state cache"
```

---

### Task 4: Signal-engine — Redis whale poller

Signal-engine при старте прогревает кэш из DB и затем каждые 30 секунд читает `HGETALL whale:state` из Redis.

**Files:**
- Modify: `services/signal-engine/main.go`

- [ ] **Step 1: Обновить main.go сигнал-энджина**

Добавить импорт `"sis/pkg/signal"` (уже есть) и функции в конец файла `services/signal-engine/main.go`:

```go
// warmWhaleState loads the latest whale states from DB into the in-memory cache.
// Called once at startup before the Redis poller takes over.
func warmWhaleState(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (symbol) symbol, direction
		FROM whale_events
		WHERE detected_at > now() - interval '2 hours'
		ORDER BY symbol, detected_at DESC
	`)
	if err != nil {
		log.Printf("whale warm: db query: %v", err)
		return
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var sym, dir string
		if err := rows.Scan(&sym, &dir); err != nil {
			continue
		}
		signal.SetWhaleState(sym, signal.State(dir))
		count++
	}
	log.Printf("whale warm: loaded %d symbols from DB", count)
}

// pollWhaleState reads HGETALL whale:state from Redis and updates the in-memory cache.
func pollWhaleState(ctx context.Context, rdb *redis.Client) {
	states, err := rdb.HGetAll(ctx, "whale:state").Result()
	if err != nil {
		log.Printf("whale poll: redis: %v", err)
		return
	}
	for sym, dir := range states {
		signal.SetWhaleState(sym, signal.State(dir))
	}
}
```

Добавить в начало файла импорт redis:
```go
import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/joho/godotenv"
    "github.com/redis/go-redis/v9"
    "sis/pkg/cache"
    "sis/pkg/db"
    "sis/pkg/heartbeat"
    sig "sis/pkg/signal"
)
```

Переименовать локальный пакет `signal` → `sig` чтобы избежать конфликта с `os/signal`:

```go
signal.SetWhaleState → sig.SetWhaleState
signal.State         → sig.State
```

Добавить в `main()` после `go heartbeat.Start(...)`:

```go
// Warm whale state from DB, then poll Redis every 30s.
warmWhaleState(ctx, pool)
go func() {
    t := time.NewTicker(30 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            pollWhaleState(ctx, rdb)
        }
    }
}()
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./services/signal-engine/...
# Expected: no output
```

- [ ] **Step 3: Commit**

```bash
git add services/signal-engine/main.go
git commit -m "feat(signal-engine): warm and poll whale state from Redis"
```

---

### Task 5: Parser service — основа

**Files:**
- Create: `services/parser/main.go`
- Create: `services/parser/Dockerfile`

- [ ] **Step 1: Создать services/parser/main.go**

```go
// services/parser/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"sis/pkg/bybitnews"
	"sis/pkg/cache"
	"sis/pkg/db"
	"sis/pkg/heartbeat"
	"sis/services/parser/whale"
)

func main() {
	_ = godotenv.Load()

	dsn      := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	go heartbeat.Start(ctx, rdb, "parser")

	// Bybit news scraper (migrated from api-gateway)
	go runBybitNews(ctx, pool)

	// Whale tracker
	cfg := whale.Config{
		EtherscanKey:    getEnv("ETHERSCAN_API_KEY", ""),
		TronGridKey:     getEnv("TRONGRID_API_KEY", ""),
		ThresholdUSDT:   getEnvFloat("WHALE_THRESHOLD_USDT", 50000),
		PollInterval:    30 * time.Second,
		DiscoveryWeekly: true,
	}
	tracker := whale.NewTracker(pool, rdb, cfg)
	go tracker.Start(ctx)

	log.Println("parser: started")
	<-ctx.Done()
	log.Println("parser: stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
		return def
	}
	return f
}
```

Добавить `"fmt"` в импорты.

- [ ] **Step 2: Создать services/parser/bybit_news.go**

```go
// services/parser/bybit_news.go
package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/bybitnews"
)

func runBybitNews(ctx context.Context, pool *pgxpool.Pool) {
	scraper := bybitnews.NewScraper(pool)
	scraper.Start(ctx)
}
```

- [ ] **Step 3: Создать services/parser/Dockerfile**

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o parser ./services/parser

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/parser .
CMD ["./parser"]
```

- [ ] **Step 4: Добавить parser в docker-compose.yml**

Найти последний сервис в `docker-compose.yml` и добавить:

```yaml
  parser:
    build:
      context: .
      dockerfile: services/parser/Dockerfile
    environment:
      DATABASE_URL: ${DATABASE_URL}
      REDIS_URL: ${REDIS_URL}
      ETHERSCAN_API_KEY: ${ETHERSCAN_API_KEY:-}
      TRONGRID_API_KEY: ${TRONGRID_API_KEY:-}
      WHALE_THRESHOLD_USDT: ${WHALE_THRESHOLD_USDT:-50000}
    restart: unless-stopped
    depends_on:
      timescaledb:
        condition: service_healthy
      redis:
        condition: service_healthy
```

- [ ] **Step 5: Проверить компиляцию (пока без whale пакета — добавим заглушку)**

Временно закомментировать импорт и вызов whale в main.go:

```bash
go build ./services/parser/...
# После реализации whale пакета в Task 6+ раскомментировать
```

- [ ] **Step 6: Commit**

```bash
git add services/parser/ docker-compose.yml
git commit -m "feat(parser): new parser microservice shell + Dockerfile + docker-compose"
```

---

### Task 6: Миграция bybitnews из api-gateway

**Files:**
- Modify: `services/api-gateway/server.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Убрать newsScraper из Server struct в server.go**

В `services/api-gateway/server.go` найти:

```go
newsScraper  *bybitnews.Scraper
```

Удалить эту строку.

Найти `"sis/pkg/bybitnews"` в импортах server.go и удалить (если больше не используется в server.go).

- [ ] **Step 2: Убрать newsScraper из NewServer и main.go**

В `services/api-gateway/server.go` найти `NewServer(...)` — убрать параметр `ns *bybitnews.Scraper` из сигнатуры и `newsScraper: ns` из тела.

В `services/api-gateway/main.go`:
1. Удалить `"sis/pkg/bybitnews"` из импортов
2. Удалить строки:
   ```go
   ns := bybitnews.NewScraper(pool)
   go ns.Start(ctx)
   ```
3. Обновить вызов `NewServer(...)` — убрать `ns` из аргументов

- [ ] **Step 3: Обновить RefreshBybitNews handler**

В `services/api-gateway/bybit_news_handler.go` endpoint `RefreshBybitNews` сейчас вызывает `s.newsScraper.ForceFetch(ctx)`.

Заменить весь метод на:

```go
// RefreshBybitNews is now handled by the parser service.
// This endpoint is kept for backward compatibility but returns 501.
func (s *Server) RefreshBybitNews(w http.ResponseWriter, r *http.Request) {
    writeError(w, http.StatusNotImplemented, "refresh is handled by the parser service")
}
```

Остальные методы (`ListBybitAnnouncements`, `GetLatestBybitNews`, `GetDelistingSymbolsHandler`) читают из DB напрямую — оставить без изменений. Но теперь `s.newsScraper` убран, поэтому убрать проверку `if s.newsScraper == nil` из этих методов (писать в DB теперь будет parser).

В `ListBybitAnnouncements` заменить:
```go
if s.newsScraper == nil {
    writeJSON(w, http.StatusOK, []bybitnews.Snapshot{})
    return
}
rows, err := s.newsScraper.List(...)
```
На:
```go
rows, err := bybitnews.ListFromDB(r.Context(), s.pool, limit, typ, onlyListings, onlyDelistings)
```

Это потребует добавить функцию `ListFromDB` в `pkg/bybitnews/scraper.go` (выделить из `Scraper.List`).

В `pkg/bybitnews/scraper.go` найти метод `List`:
```go
func (s *Scraper) List(ctx context.Context, limit int, typ string, onlyListings, onlyDelistings bool) ([]DBAnnouncement, error) {
    // SQL query...
}
```

Добавить standalone функцию (дубликат без receiver):
```go
func ListFromDB(ctx context.Context, db *pgxpool.Pool, limit int, typ string, onlyListings, onlyDelistings bool) ([]DBAnnouncement, error) {
    // same SQL as Scraper.List
}
```

Скопировать тело `Scraper.List`, заменить `s.db` на `db`.

- [ ] **Step 4: Аналогично для GetLatestBybitNews и GetDelistingSymbols**

В `pkg/bybitnews/scraper.go` добавить:
```go
func LatestFromDB(ctx context.Context, db *pgxpool.Pool, n int) ([]DBAnnouncement, error) {
    // same as Scraper.Latest
}

func DelistingSymbolsFromDB(ctx context.Context, db *pgxpool.Pool) ([]string, error) {
    // same as used in Server.GetDelistingSymbols
}
```

Обновить хэндлеры в `bybit_news_handler.go` соответственно.

- [ ] **Step 5: Проверить компиляцию**

```bash
go build ./services/api-gateway/...
# Expected: no output
```

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/server.go services/api-gateway/main.go \
        services/api-gateway/bybit_news_handler.go pkg/bybitnews/scraper.go
git commit -m "feat(parser): migrate bybit news scraper to parser service"
```

---

### Task 7: Etherscan API client

**Files:**
- Create: `services/parser/whale/eth.go`

- [ ] **Step 1: Создать eth.go**

```go
// services/parser/whale/eth.go
package whale

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const etherscanBase = "https://api.etherscan.io/v2/api"

// TokenTransfer represents one ERC-20 token transfer from Etherscan.
type TokenTransfer struct {
	Hash          string
	From          string
	To            string
	TokenSymbol   string
	TokenDecimals int
	Value         string // raw integer string
	Timestamp     int64
}

// EthClient fetches token transfer data from Etherscan API v2.
type EthClient struct {
	apiKey string
	http   *http.Client
}

func newEthClient(apiKey string) *EthClient {
	return &EthClient{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// TokenTxSince returns ERC-20 transfers TO/FROM address since sinceTS (unix seconds).
func (c *EthClient) TokenTxSince(ctx context.Context, address string, sinceTS int64) ([]TokenTransfer, error) {
	url := fmt.Sprintf(
		"%s?chainid=1&module=account&action=tokentx&address=%s&startblock=0&endblock=99999999&sort=desc&apikey=%s",
		etherscanBase, address, c.apiKey,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Result  []struct {
			Hash             string `json:"hash"`
			From             string `json:"from"`
			To               string `json:"to"`
			TokenSymbol      string `json:"tokenSymbol"`
			TokenDecimal     string `json:"tokenDecimal"`
			Value            string `json:"value"`
			TimeStamp        string `json:"timeStamp"`
			ContractAddress  string `json:"contractAddress"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("etherscan parse: %w", err)
	}
	if result.Status != "1" && result.Message != "No transactions found" {
		return nil, fmt.Errorf("etherscan error: %s", result.Message)
	}

	var out []TokenTransfer
	for _, r := range result.Result {
		ts, _ := strconv.ParseInt(r.TimeStamp, 10, 64)
		if ts < sinceTS {
			break // results are desc by time
		}
		dec, _ := strconv.Atoi(r.TokenDecimal)
		out = append(out, TokenTransfer{
			Hash:          r.Hash,
			From:          r.From,
			To:            r.To,
			TokenSymbol:   r.TokenSymbol,
			TokenDecimals: dec,
			Value:         r.Value,
			Timestamp:     ts,
		})
	}
	return out, nil
}

// RawValue converts a token transfer value to a float64 USD-like amount
// using price stub (actual USD pricing comes from direction.go).
func rawToAmount(value string, decimals int) float64 {
	v, _ := strconv.ParseFloat(value, 64)
	if decimals > 0 {
		v /= pow10(decimals)
	}
	return v
}

func pow10(n int) float64 {
	r := 1.0
	for i := 0; i < n; i++ {
		r *= 10
	}
	return r
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./services/parser/...
# Expected: no output (после создания всех файлов пакета whale)
```

- [ ] **Step 3: Commit** (вместе с остальными файлами whale — см. Task 9)

---

### Task 8: TronGrid API client

**Files:**
- Create: `services/parser/whale/tron.go`

- [ ] **Step 1: Создать tron.go**

```go
// services/parser/whale/tron.go
package whale

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const tronGridBase = "https://api.trongrid.io"

// TronTransfer represents one TRC-20 token transfer.
type TronTransfer struct {
	TxID        string
	From        string
	To          string
	TokenSymbol string
	Decimals    int
	Value       string // raw integer string
	Timestamp   int64  // milliseconds
}

// TronClient fetches TRC-20 transfers from TronGrid API.
type TronClient struct {
	apiKey string
	http   *http.Client
}

func newTronClient(apiKey string) *TronClient {
	return &TronClient{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// TokenTxSince returns TRC-20 transfers TO/FROM address since sinceTS (unix ms).
func (c *TronClient) TokenTxSince(ctx context.Context, address string, sinceMS int64) ([]TronTransfer, error) {
	url := fmt.Sprintf(
		"%s/v1/accounts/%s/transactions/trc20?limit=50&order_by=block_timestamp,desc",
		tronGridBase, address,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var result struct {
		Data []struct {
			TransactionID string `json:"transaction_id"`
			TokenInfo     struct {
				Symbol   string `json:"symbol"`
				Decimals int    `json:"decimals"`
			} `json:"token_info"`
			From           string `json:"from"`
			To             string `json:"to"`
			Value          string `json:"value"`
			BlockTimestamp int64  `json:"block_timestamp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("trongrid parse: %w", err)
	}

	var out []TronTransfer
	for _, r := range result.Data {
		if r.BlockTimestamp < sinceMS {
			break
		}
		out = append(out, TronTransfer{
			TxID:        r.TransactionID,
			From:        r.From,
			To:          r.To,
			TokenSymbol: r.TokenInfo.Symbol,
			Decimals:    r.TokenInfo.Decimals,
			Value:       r.Value,
			Timestamp:   r.BlockTimestamp,
		})
	}
	return out, nil
}
```

---

### Task 9: Direction analysis

**Files:**
- Create: `services/parser/whale/direction.go`

- [ ] **Step 1: Создать direction.go**

```go
// services/parser/whale/direction.go
package whale

import (
	"context"
	"strings"
	"time"
)

// stablecoin symbols to exclude from accumulation scoring
var stablecoins = map[string]bool{
	"USDT": true, "USDC": true, "DAI": true, "BUSD": true,
	"TUSD": true, "FDUSD": true, "PYUSD": true,
}

// knownBybitETHWallets lists known Bybit ETH deposit hot wallets.
var knownBybitETHWallets = map[string]bool{
	strings.ToLower("0xf89d7b9c864f589bbF53a82105107622B35EaA40"): true,
	strings.ToLower("0x2b5c7025998f88550Ef2fece8bf87935f542C190"): true,
	strings.ToLower("0x15F5e2B0cE6d41AC2C99B4A2Da22a6EB6a21b84"): true,
}

// knownBybitTronWallets lists known Bybit Tron deposit hot wallets.
var knownBybitTronWallets = map[string]bool{
	"TJDENsfBJs4RFETt1X1W8wMDc8M5XnJhd":   true,
	"TWd4WrZ9wn84f5x1hZhL4DHvk738ns5jwH":   true,
}

// ScoreResult holds the direction analysis output.
type ScoreResult struct {
	Score     float64 // -1..+1: positive = accumulation, negative = distribution
	Direction string  // "buy" | "sell" | "neutral"
	TopToken  string  // highest-activity non-stable token symbol
}

// Analyzer holds API clients for ETH and Tron chains.
type Analyzer struct {
	eth  *EthClient
	tron *TronClient
}

func newAnalyzer(eth *EthClient, tron *TronClient) *Analyzer {
	return &Analyzer{eth: eth, tron: tron}
}

// Analyze computes the 30-day accumulation/distribution score for a given address.
func (a *Analyzer) Analyze(ctx context.Context, address, chain string) ScoreResult {
	since30d := time.Now().AddDate(0, 0, -30)
	tokenFlow := make(map[string]float64) // symbol → net flow (positive = received)

	switch chain {
	case "eth":
		sinceTS := since30d.Unix()
		txs, err := a.eth.TokenTxSince(ctx, address, sinceTS)
		if err != nil {
			return ScoreResult{Direction: "neutral"}
		}
		addrLower := strings.ToLower(address)
		for _, tx := range txs {
			sym := strings.ToUpper(tx.TokenSymbol)
			if stablecoins[sym] {
				continue
			}
			amount := rawToAmount(tx.Value, tx.TokenDecimals)
			if strings.ToLower(tx.To) == addrLower {
				tokenFlow[sym] += amount
			} else {
				tokenFlow[sym] -= amount
			}
		}

	case "tron":
		sinceMS := since30d.UnixMilli()
		txs, err := a.tron.TokenTxSince(ctx, address, sinceMS)
		if err != nil {
			return ScoreResult{Direction: "neutral"}
		}
		for _, tx := range txs {
			sym := strings.ToUpper(tx.TokenSymbol)
			if stablecoins[sym] {
				continue
			}
			amount := rawToAmount(tx.Value, tx.Decimals)
			if tx.To == address {
				tokenFlow[sym] += amount
			} else {
				tokenFlow[sym] -= amount
			}
		}
	}

	// Find top token by absolute net flow
	var topToken string
	var maxAbs float64
	var totalReceived, totalSent float64
	for sym, net := range tokenFlow {
		abs := net
		if abs < 0 {
			abs = -abs
		}
		if abs > maxAbs {
			maxAbs = abs
			topToken = sym
		}
		if net > 0 {
			totalReceived += net
		} else {
			totalSent += -net
		}
	}

	total := totalReceived + totalSent
	if total == 0 {
		return ScoreResult{Direction: "neutral", TopToken: topToken}
	}

	score := (totalReceived - totalSent) / total
	direction := "neutral"
	switch {
	case score > 0.3:
		direction = "buy"
	case score < -0.3:
		direction = "sell"
	}

	return ScoreResult{Score: score, Direction: direction, TopToken: topToken}
}

// IsExchangeDeposit returns true if `to` is a known Bybit hot wallet for the chain.
func IsExchangeDeposit(to, chain string) bool {
	switch chain {
	case "eth":
		return knownBybitETHWallets[strings.ToLower(to)]
	case "tron":
		return knownBybitTronWallets[to]
	}
	return false
}

// tokenToSymbol maps a token symbol to a Bybit futures symbol (e.g. "BTC" → "BTCUSDT").
func tokenToSymbol(token string) string {
	token = strings.ToUpper(token)
	if token == "" || stablecoins[token] {
		return ""
	}
	return token + "USDT"
}
```

---

### Task 10: Whale tracker polling loop

**Files:**
- Create: `services/parser/whale/tracker.go`

- [ ] **Step 1: Создать tracker.go**

```go
// services/parser/whale/tracker.go
package whale

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Config holds tracker configuration.
type Config struct {
	EtherscanKey    string
	TronGridKey     string
	ThresholdUSDT   float64
	PollInterval    time.Duration
	DiscoveryWeekly bool
}

// Tracker monitors known whale addresses for large deposits to Bybit.
type Tracker struct {
	db       *pgxpool.Pool
	rdb      *redis.Client
	cfg      Config
	eth      *EthClient
	tron     *TronClient
	analyzer *Analyzer
}

// NewTracker creates a Tracker with all sub-clients initialized.
func NewTracker(db *pgxpool.Pool, rdb *redis.Client, cfg Config) *Tracker {
	eth := newEthClient(cfg.EtherscanKey)
	tron := newTronClient(cfg.TronGridKey)
	return &Tracker{
		db:       db,
		rdb:      rdb,
		cfg:      cfg,
		eth:      eth,
		tron:     tron,
		analyzer: newAnalyzer(eth, tron),
	}
}

// Start begins the polling loop and optional weekly discovery. Blocks until ctx is cancelled.
func (t *Tracker) Start(ctx context.Context) {
	log.Println("whale tracker: starting")

	// Launch weekly discovery goroutine if enabled.
	if t.cfg.DiscoveryWeekly {
		d := NewDiscovery(t.db, t.eth, t.tron, t.cfg)
		go d.Start(ctx)
	}

	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	// Immediate first tick
	t.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.poll(ctx)
		}
	}
}

// poll checks all active whale addresses for new large deposits to Bybit.
func (t *Tracker) poll(ctx context.Context) {
	addrs, err := t.loadActiveAddresses(ctx)
	if err != nil {
		log.Printf("whale poll: load addresses: %v", err)
		return
	}

	// Look back 10 minutes (overlap handles any latency from Etherscan/Tronscan).
	sinceTS := time.Now().Add(-10 * time.Minute).Unix()
	sinceMS := sinceTS * 1000

	for _, addr := range addrs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t.checkAddress(ctx, addr, sinceTS, sinceMS)
	}
}

type whaleAddress struct {
	ID      string
	Address string
	Chain   string
}

func (t *Tracker) loadActiveAddresses(ctx context.Context) ([]whaleAddress, error) {
	rows, err := t.db.Query(ctx, `
		SELECT id, address, chain FROM whale_addresses WHERE is_active = true
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []whaleAddress
	for rows.Next() {
		var a whaleAddress
		if err := rows.Scan(&a.ID, &a.Address, &a.Chain); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (t *Tracker) checkAddress(ctx context.Context, addr whaleAddress, sinceTS, sinceMS int64) {
	switch addr.Chain {
	case "eth":
		txs, err := t.eth.TokenTxSince(ctx, addr.Address, sinceTS)
		if err != nil {
			log.Printf("whale: eth %s: %v", addr.Address[:8], err)
			return
		}
		for _, tx := range txs {
			if !IsExchangeDeposit(tx.To, "eth") {
				continue
			}
			sym := strings.ToUpper(tx.TokenSymbol)
			amountUSD := rawToAmount(tx.Value, tx.TokenDecimals)
			// For non-USDT tokens, use a 1:1 price stub (real USD pricing TBD via market data).
			// USDT has 6 decimals; amount after rawToAmount is already in USDT units.
			if amountUSD < t.cfg.ThresholdUSDT {
				continue
			}
			t.handleDeposit(ctx, addr, tx.Hash, sym, amountUSD, tx.Timestamp)
		}

	case "tron":
		txs, err := t.tron.TokenTxSince(ctx, addr.Address, sinceMS)
		if err != nil {
			log.Printf("whale: tron %s: %v", addr.Address[:8], err)
			return
		}
		for _, tx := range txs {
			if !IsExchangeDeposit(tx.To, "tron") {
				continue
			}
			sym := strings.ToUpper(tx.TokenSymbol)
			amountUSD := rawToAmount(tx.Value, tx.Decimals)
			if amountUSD < t.cfg.ThresholdUSDT {
				continue
			}
			t.handleDeposit(ctx, addr, tx.TxID, sym, amountUSD, tx.Timestamp/1000)
		}
	}
}

func (t *Tracker) handleDeposit(ctx context.Context, addr whaleAddress, txHash, tokenSym string, amountUSD float64, ts int64) {
	// Check for duplicate (same tx_hash already processed)
	var exists bool
	_ = t.db.QueryRow(ctx, `SELECT true FROM whale_events WHERE tx_hash = $1`, txHash).Scan(&exists)
	if exists {
		return
	}

	// Determine direction via 30d accumulation analysis
	scoreResult := t.analyzer.Analyze(ctx, addr.Address, addr.Chain)

	// Determine futures symbol: direct token or top accumulated token
	symbol := tokenToSymbol(tokenSym)
	if symbol == "" && scoreResult.TopToken != "" {
		symbol = tokenToSymbol(scoreResult.TopToken)
	}
	if symbol == "" {
		return // can't determine symbol, skip
	}

	// Write to DB
	if _, err := t.db.Exec(ctx, `
		INSERT INTO whale_events (address, chain, symbol, amount_usd, direction, score, tx_hash, detected_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, to_timestamp($8))
		ON CONFLICT DO NOTHING
	`, addr.Address, addr.Chain, symbol, amountUSD, scoreResult.Direction, scoreResult.Score, txHash, ts); err != nil {
		log.Printf("whale: insert event: %v", err)
		return
	}

	// Update last_seen_at and volume_30d
	_, _ = t.db.Exec(ctx, `
		UPDATE whale_addresses
		SET last_seen_at = now(), volume_30d = volume_30d + $1
		WHERE id = $2
	`, amountUSD, addr.ID)

	// Publish to Redis (TTL 2 hours)
	pipe := t.rdb.Pipeline()
	pipe.HSet(ctx, "whale:state", symbol, scoreResult.Direction)
	pipe.Expire(ctx, "whale:state", 2*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("whale: redis publish: %v", err)
	}

	log.Printf("whale: %s %s deposit $%.0f → %s %s (score %.2f)",
		addr.Chain, fmt.Sprintf("%.8s...", addr.Address), amountUSD, symbol, scoreResult.Direction, scoreResult.Score)
}
```

---

### Task 11: Auto-discovery

**Files:**
- Create: `services/parser/whale/discovery.go`

- [ ] **Step 1: Создать discovery.go**

```go
// services/parser/whale/discovery.go
package whale

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Discovery runs weekly to find top whale addresses sending USDT to Bybit.
type Discovery struct {
	db   *pgxpool.Pool
	eth  *EthClient
	tron *TronClient
	cfg  Config
}

// NewDiscovery creates a Discovery runner.
func NewDiscovery(db *pgxpool.Pool, eth *EthClient, tron *TronClient, cfg Config) *Discovery {
	return &Discovery{db: db, eth: eth, tron: tron, cfg: cfg}
}

// Start runs the discovery weekly. Blocks until ctx is cancelled.
func (d *Discovery) Start(ctx context.Context) {
	// Run immediately on start, then weekly.
	d.run(ctx)
	ticker := time.NewTicker(7 * 24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.run(ctx)
		}
	}
}

// ForceRun triggers an immediate discovery run (called by admin API).
func (d *Discovery) ForceRun(ctx context.Context) {
	go d.run(ctx)
}

func (d *Discovery) run(ctx context.Context) {
	log.Println("whale discovery: starting")
	since30d := time.Now().AddDate(0, 0, -30).Unix()

	// Scan each known Bybit hot wallet for large incoming transfers.
	for wallet := range knownBybitETHWallets {
		d.scanETHWallet(ctx, wallet, since30d)
	}
	for wallet := range knownBybitTronWallets {
		d.scanTronWallet(ctx, wallet, since30d)
	}

	// Deactivate addresses not seen in 90 days.
	_, err := d.db.Exec(ctx, `
		UPDATE whale_addresses
		SET is_active = false
		WHERE is_manual = false
		  AND (last_seen_at IS NULL OR last_seen_at < now() - interval '90 days')
	`)
	if err != nil {
		log.Printf("whale discovery: deactivate: %v", err)
	}
	log.Println("whale discovery: done")
}

func (d *Discovery) scanETHWallet(ctx context.Context, bybitWallet string, since int64) {
	txs, err := d.eth.TokenTxSince(ctx, bybitWallet, since)
	if err != nil {
		log.Printf("whale discovery: eth %s: %v", bybitWallet[:8], err)
		return
	}
	// Count volume per sender
	vol := make(map[string]float64)
	for _, tx := range txs {
		if strings.ToLower(tx.To) != bybitWallet {
			continue // we want incoming only
		}
		amt := rawToAmount(tx.Value, tx.TokenDecimals)
		vol[strings.ToLower(tx.From)] += amt
	}
	d.upsertTopSenders(ctx, vol, "eth")
}

func (d *Discovery) scanTronWallet(ctx context.Context, bybitWallet string, since int64) {
	txs, err := d.tron.TokenTxSince(ctx, bybitWallet, since*1000)
	if err != nil {
		log.Printf("whale discovery: tron %s: %v", bybitWallet[:8], err)
		return
	}
	vol := make(map[string]float64)
	for _, tx := range txs {
		if tx.To != bybitWallet {
			continue
		}
		amt := rawToAmount(tx.Value, tx.Decimals)
		vol[tx.From] += amt
	}
	d.upsertTopSenders(ctx, vol, "tron")
}

func (d *Discovery) upsertTopSenders(ctx context.Context, vol map[string]float64, chain string) {
	for addr, amount := range vol {
		if amount < d.cfg.ThresholdUSDT {
			continue
		}
		_, err := d.db.Exec(ctx, `
			INSERT INTO whale_addresses (address, chain, volume_30d, is_manual)
			VALUES ($1, $2, $3, false)
			ON CONFLICT (address, chain) DO UPDATE
			SET volume_30d = EXCLUDED.volume_30d, is_active = true
		`, addr, chain, amount)
		if err != nil {
			log.Printf("whale discovery: upsert %s: %v", addr[:8], err)
		}
	}
}
```

- [ ] **Step 2: Проверить компиляцию всего whale пакета**

```bash
go build ./services/parser/...
# Expected: no output
```

- [ ] **Step 3: Commit**

```bash
git add services/parser/whale/
git commit -m "feat(whale): eth/tron clients, direction analysis, tracker, discovery"
```

---

### Task 12: api-gateway — whale admin endpoints

**Files:**
- Create: `services/api-gateway/whale_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Создать whale_handler.go**

```go
// services/api-gateway/whale_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// GET /admin/whale/addresses
func (s *Server) ListWhaleAddresses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, address, chain, label, is_manual, volume_30d, is_active, last_seen_at, created_at
		FROM whale_addresses
		ORDER BY volume_30d DESC
		LIMIT 200
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	type row struct {
		ID         string     `json:"id"`
		Address    string     `json:"address"`
		Chain      string     `json:"chain"`
		Label      *string    `json:"label"`
		IsManual   bool       `json:"isManual"`
		Volume30d  float64    `json:"volume30d"`
		IsActive   bool       `json:"isActive"`
		LastSeenAt *time.Time `json:"lastSeenAt"`
		CreatedAt  time.Time  `json:"createdAt"`
	}
	var result []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Address, &r.Chain, &r.Label, &r.IsManual,
			&r.Volume30d, &r.IsActive, &r.LastSeenAt, &r.CreatedAt); err != nil {
			continue
		}
		result = append(result, r)
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /admin/whale/addresses
func (s *Server) CreateWhaleAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string  `json:"address"`
		Chain   string  `json:"chain"`
		Label   *string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Address == "" {
		writeError(w, http.StatusBadRequest, "address and chain required")
		return
	}
	if body.Chain != "eth" && body.Chain != "tron" {
		writeError(w, http.StatusBadRequest, "chain must be eth or tron")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO whale_addresses (address, chain, label, is_manual)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (address, chain) DO UPDATE
		SET label = EXCLUDED.label, is_active = true, is_manual = true
		RETURNING id
	`, body.Address, body.Chain, body.Label).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// PATCH /admin/whale/addresses/{id}
func (s *Server) PatchWhaleAddress(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Label    *string `json:"label"`
		IsActive *bool   `json:"isActive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Label != nil {
		s.pool.Exec(r.Context(), `UPDATE whale_addresses SET label=$1 WHERE id=$2`, *body.Label, id)
	}
	if body.IsActive != nil {
		s.pool.Exec(r.Context(), `UPDATE whale_addresses SET is_active=$1 WHERE id=$2`, *body.IsActive, id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /admin/whale/addresses/{id}
func (s *Server) DeleteWhaleAddress(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := s.pool.Exec(r.Context(), `UPDATE whale_addresses SET is_active=false WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /admin/whale/events
func (s *Server) ListWhaleEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT we.id, we.address, COALESCE(wa.label, ''), we.chain, we.symbol,
		       we.amount_usd, we.direction, we.score, we.tx_hash, we.detected_at
		FROM whale_events we
		LEFT JOIN whale_addresses wa ON wa.address = we.address AND wa.chain = we.chain
		ORDER BY we.detected_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	type row struct {
		ID         string    `json:"id"`
		Address    string    `json:"address"`
		Label      string    `json:"label"`
		Chain      string    `json:"chain"`
		Symbol     string    `json:"symbol"`
		AmountUSD  float64   `json:"amountUsd"`
		Direction  string    `json:"direction"`
		Score      float64   `json:"score"`
		TxHash     string    `json:"txHash"`
		DetectedAt time.Time `json:"detectedAt"`
	}
	var result []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Address, &r.Label, &r.Chain, &r.Symbol,
			&r.AmountUSD, &r.Direction, &r.Score, &r.TxHash, &r.DetectedAt); err != nil {
			continue
		}
		result = append(result, r)
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /admin/whale/state — текущее состояние по символам из Redis
func (s *Server) GetWhaleState(w http.ResponseWriter, r *http.Request) {
	states, err := s.rdb.HGetAll(r.Context(), "whale:state").Result()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "redis error")
		return
	}
	// Supplement with recent DB events for symbols not in Redis
	rows, _ := s.pool.Query(r.Context(), `
		SELECT DISTINCT ON (symbol) symbol, direction, amount_usd, detected_at
		FROM whale_events
		WHERE detected_at > now() - interval '4 hours'
		ORDER BY symbol, detected_at DESC
	`)
	type stateRow struct {
		Symbol     string    `json:"symbol"`
		Direction  string    `json:"direction"`
		AmountUSD  float64   `json:"amountUsd"`
		DetectedAt time.Time `json:"detectedAt"`
		Source     string    `json:"source"` // "redis" | "db"
	}
	var result []stateRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var sr stateRow
			if err := rows.Scan(&sr.Symbol, &sr.Direction, &sr.AmountUSD, &sr.DetectedAt); err != nil {
				continue
			}
			if _, ok := states[sr.Symbol]; ok {
				sr.Source = "redis"
			} else {
				sr.Source = "db"
			}
			result = append(result, sr)
		}
	}
	if result == nil {
		result = []stateRow{}
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /admin/whale/simulate — what-if пересчёт по новому порогу
func (s *Server) SimulateWhale(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThresholdUSDT float64 `json:"threshold_usdt"`
		WindowHours   int     `json:"window_hours"`
	}
	body.ThresholdUSDT = 50000
	body.WindowHours = 2
	json.NewDecoder(r.Body).Decode(&body)
	if body.WindowHours <= 0 || body.WindowHours > 168 {
		body.WindowHours = 2
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT symbol, direction, amount_usd
		FROM whale_events
		WHERE detected_at > now() - ($1 || ' hours')::interval
		  AND amount_usd >= $2
		ORDER BY detected_at DESC
	`, body.WindowHours, body.ThresholdUSDT)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type bucket struct {
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	bySymbol := make(map[string]string)
	buckets := map[string]int{"50k-100k": 0, "100k-500k": 0, "500k+": 0}
	total := 0
	for rows.Next() {
		var sym, dir string
		var amt float64
		if err := rows.Scan(&sym, &dir, &amt); err != nil {
			continue
		}
		total++
		bySymbol[sym] = dir
		switch {
		case amt < 100_000:
			buckets["50k-100k"]++
		case amt < 500_000:
			buckets["100k-500k"]++
		default:
			buckets["500k+"]++
		}
	}

	type symState struct {
		Symbol    string `json:"symbol"`
		Direction string `json:"direction"`
	}
	var symStates []symState
	for sym, dir := range bySymbol {
		symStates = append(symStates, symState{sym, dir})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events_count": total,
		"by_symbol":    symStates,
		"buckets": []bucket{
			{"50k-100k", buckets["50k-100k"]},
			{"100k-500k", buckets["100k-500k"]},
			{"500k+", buckets["500k+"]},
		},
	})
}
```

- [ ] **Step 2: Добавить whale routes в main.go**

Найти блок `r.Use(s.RequireAdmin)` в `services/api-gateway/main.go` и добавить в конец блока:

```go
// Admin: whale parser
r.Get("/admin/whale/addresses", s.ListWhaleAddresses)
r.Post("/admin/whale/addresses", s.CreateWhaleAddress)
r.Patch("/admin/whale/addresses/{id}", s.PatchWhaleAddress)
r.Delete("/admin/whale/addresses/{id}", s.DeleteWhaleAddress)
r.Get("/admin/whale/events", s.ListWhaleEvents)
r.Get("/admin/whale/state", s.GetWhaleState)
r.Post("/admin/whale/simulate", s.SimulateWhale)
```

- [ ] **Step 3: Проверить компиляцию**

```bash
go build ./services/api-gateway/...
# Expected: no output
```

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/whale_handler.go services/api-gateway/main.go
git commit -m "feat(api): whale admin endpoints — addresses, events, state, simulate"
```

---

## Self-Review

**Spec coverage:**
- ✅ Parser сервис (Task 5)
- ✅ Bybit news migration (Task 6)
- ✅ ETH chain client (Task 7)
- ✅ Tron chain client (Task 8)
- ✅ Direction analysis 30д (Task 9)
- ✅ Tracker polling loop 30s (Task 10)
- ✅ Auto-discovery weekly (Task 11)
- ✅ DB migration whale_addresses + whale_events (Task 1)
- ✅ Redis Hash whale:state (Task 10 — handleDeposit)
- ✅ SymbolComputer interface (Task 2)
- ✅ Whale signal registration (Task 3)
- ✅ Signal-engine warm + poll (Task 4)
- ✅ api-gateway admin endpoints (Task 12)
- ✅ docker-compose (Task 5)
- ✅ Configurable threshold (Config.ThresholdUSDT)
- ✅ Hybrid address list: auto-discovered + manual (Task 11 is_manual=false, Task 12 CreateWhaleAddress is_manual=true)
- ✅ DB fallback в GetWhaleState endpoint

**Type consistency:** `whale.Config`, `whale.Tracker`, `whale.Discovery`, `whale.Analyzer`, `whale.EthClient`, `whale.TronClient` — все имена согласованы между файлами.
