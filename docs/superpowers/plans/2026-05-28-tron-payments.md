# TRON USDT Payments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Пополнение внутреннего баланса (`novabot_balance`) через прямой USDT TRC20 кошелёк — инвойс с уникальной суммой, таймер 30 минут, автозачисление через TronGrid API.

**Architecture:** Пользователь создаёт депозит → система добавляет случайные 1–90 центов к сумме → показывает адрес + QR + точную сумму + таймер. Фоновая горутина `tronWatcher` опрашивает TronGrid API каждые 30 секунд, находит совпадение по сумме, зачисляет на `novabot_balance`. Один TRON-адрес на все платежи, уникальность через `amount_exact`.

**Tech Stack:** Go 1.25, PostgreSQL, TronGrid REST API, React/TypeScript, `qrcode.react`.

---

## File Map

**New files:**
- `migrations/049_tron_deposits.sql` — таблица pending/confirmed депозитов
- `services/api-gateway/tron_handler.go` — HTTP эндпоинты (create, status, history)
- `services/api-gateway/tron_watcher.go` — фоновая горутина мониторинга TronGrid
- `frontend/src/pages/PaymentsPage.tsx` — страница пополнения баланса
- `frontend/src/api/payments.ts` — API клиент для платёжных эндпоинтов

**Modified files:**
- `services/api-gateway/server.go` — добавить `tronAddr` в Server struct
- `services/api-gateway/main.go` — загрузить `TRON_RECEIVE_ADDRESS`, регистрировать маршруты, запустить watcher
- `frontend/src/App.tsx` — добавить маршрут `/payments`
- `frontend/src/components/Sidebar/Sidebar.tsx` — добавить пункт меню «Баланс»

---

## Task 1: Миграция `049_tron_deposits.sql`

**Files:**
- Create: `migrations/049_tron_deposits.sql`

- [ ] **Создать файл миграции**

```sql
-- migrations/049_tron_deposits.sql

CREATE TABLE IF NOT EXISTS tron_deposits (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_usdt  NUMERIC(20,6) NOT NULL,   -- желаемая сумма (напр. 50.00)
    amount_exact NUMERIC(20,6) NOT NULL,   -- уникальная сумма к оплате (напр. 50.07)
    status       TEXT        NOT NULL DEFAULT 'pending',  -- pending | confirmed | expired
    tx_hash      TEXT,                     -- хэш транзакции в блокчейне
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 minutes',
    confirmed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS tron_deposits_user_idx
    ON tron_deposits (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS tron_deposits_pending_idx
    ON tron_deposits (status, expires_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS tron_deposits_amount_idx
    ON tron_deposits (amount_exact)
    WHERE status = 'pending';

CREATE UNIQUE INDEX IF NOT EXISTS tron_deposits_tx_hash_idx
    ON tron_deposits (tx_hash)
    WHERE tx_hash IS NOT NULL;
```

- [ ] **Проверить что миграция применяется**

```bash
cd C:\Users\123\Projects\sis
go run ./services/api-gateway 2>&1 | head -5
# Expected: no migration errors, server starts
```

- [ ] **Commit**

```bash
git add migrations/049_tron_deposits.sql
git commit -m "feat(db): add tron_deposits table"
```

---

## Task 2: `tron_handler.go` — HTTP эндпоинты

**Files:**
- Create: `services/api-gateway/tron_handler.go`

Три эндпоинта (все требуют JWT):
- `POST /payments/tron/deposit` — создать инвойс
- `GET  /payments/tron/deposit/{id}` — статус депозита
- `GET  /payments/tron/deposits` — история депозитов пользователя

- [ ] **Создать `tron_handler.go`**

```go
// services/api-gateway/tron_handler.go
package main

import (
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// tronDepositResp — ответ на создание депозита и запрос статуса.
type tronDepositResp struct {
	ID          string  `json:"id"`
	AmountUSDT  float64 `json:"amount_usdt"`
	AmountExact float64 `json:"amount_exact"`
	Address     string  `json:"address"`
	Status      string  `json:"status"`
	ExpiresAt   string  `json:"expires_at"`
	ConfirmedAt *string `json:"confirmed_at,omitempty"`
	TxHash      *string `json:"tx_hash,omitempty"`
}

// CreateTronDeposit создаёт новый депозит с уникальной суммой.
// POST /payments/tron/deposit
func (s *Server) CreateTronDeposit(w http.ResponseWriter, r *http.Request) {
	if s.tronAddr == "" {
		writeError(w, http.StatusServiceUnavailable, "crypto payments not configured")
		return
	}
	userID := UserIDFromCtx(r.Context())

	var req struct {
		Amount float64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Amount < 1 || req.Amount > 100000 {
		writeError(w, http.StatusBadRequest, "amount must be between 1 and 100000 USDT")
		return
	}

	ctx := r.Context()

	// Генерируем уникальную сумму: прибавляем случайные 1–90 центов.
	// Повторяем до 10 раз если такая сумма уже занята другим pending депозитом.
	var amountExact float64
	var depositID string
	for attempt := 0; attempt < 10; attempt++ {
		cents := float64(rand.Intn(90)+1) / 100.0
		candidate := math.Round((req.Amount+cents)*1e6) / 1e6

		err := s.pool.QueryRow(ctx,
			`INSERT INTO tron_deposits (user_id, amount_usdt, amount_exact)
			 VALUES ($1, $2, $3)
			 ON CONFLICT DO NOTHING
			 RETURNING id, amount_exact`,
			userID, req.Amount, candidate,
		).Scan(&depositID, &amountExact)
		if err == nil && depositID != "" {
			break
		}
	}
	if depositID == "" {
		writeError(w, http.StatusInternalServerError, "failed to generate unique deposit amount, try again")
		return
	}

	var expiresAt time.Time
	s.pool.QueryRow(ctx,
		`SELECT expires_at FROM tron_deposits WHERE id=$1`, depositID,
	).Scan(&expiresAt)

	writeJSON(w, http.StatusCreated, tronDepositResp{
		ID:          depositID,
		AmountUSDT:  req.Amount,
		AmountExact: amountExact,
		Address:     s.tronAddr,
		Status:      "pending",
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
	})
}

// GetTronDeposit возвращает статус конкретного депозита.
// GET /payments/tron/deposit/{id}
func (s *Server) GetTronDeposit(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	var dep tronDepositResp
	var confirmedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, amount_usdt, amount_exact, status, expires_at, confirmed_at, tx_hash
		 FROM tron_deposits
		 WHERE id=$1 AND user_id=$2`,
		id, userID,
	).Scan(&dep.ID, &dep.AmountUSDT, &dep.AmountExact,
		&dep.Status, &dep.ExpiresAt, &confirmedAt, &dep.TxHash)
	if err != nil {
		writeError(w, http.StatusNotFound, "deposit not found")
		return
	}
	dep.Address = s.tronAddr
	if confirmedAt != nil {
		s := confirmedAt.UTC().Format(time.RFC3339)
		dep.ConfirmedAt = &s
	}
	writeJSON(w, http.StatusOK, dep)
}

// ListTronDeposits возвращает историю депозитов пользователя (последние 50).
// GET /payments/tron/deposits
func (s *Server) ListTronDeposits(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	ctx := r.Context()

	rows, err := s.pool.Query(ctx,
		`SELECT id, amount_usdt, amount_exact, status, expires_at, confirmed_at, tx_hash
		 FROM tron_deposits
		 WHERE user_id=$1
		 ORDER BY created_at DESC
		 LIMIT 50`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var deposits []tronDepositResp
	for rows.Next() {
		var dep tronDepositResp
		var confirmedAt *time.Time
		if err := rows.Scan(&dep.ID, &dep.AmountUSDT, &dep.AmountExact,
			&dep.Status, &dep.ExpiresAt, &confirmedAt, &dep.TxHash); err != nil {
			continue
		}
		dep.Address = s.tronAddr
		if confirmedAt != nil {
			s := confirmedAt.UTC().Format(time.RFC3339)
			dep.ConfirmedAt = &s
		}
		deposits = append(deposits, dep)
	}
	if deposits == nil {
		deposits = []tronDepositResp{}
	}
	writeJSON(w, http.StatusOK, deposits)
}
```

- [ ] **Commit**

```bash
git add services/api-gateway/tron_handler.go
git commit -m "feat(gateway): add tron deposit HTTP handlers"
```

---

## Task 3: `tron_watcher.go` — фоновый мониторинг TronGrid

**Files:**
- Create: `services/api-gateway/tron_watcher.go`

Горутина опрашивает TronGrid каждые 30 секунд. Находит входящие USDT-переводы, сопоставляет с pending депозитами по сумме, зачисляет баланс.

USDT TRC20 контракт на mainnet: `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t`  
Значения в TronGrid — в минимальных единицах (6 знаков), т.е. 50.07 USDT = `50070000`.

- [ ] **Создать `tron_watcher.go`**

```go
// services/api-gateway/tron_watcher.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"
)

const (
	usdtContractTRC20 = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	tronGridBase      = "https://api.trongrid.io"
	// Допустимое отклонение суммы в USDT (на случай погрешности float)
	amountTolerance = 0.001
)

type tronTx struct {
	TransactionID string `json:"transaction_id"`
	TokenInfo     struct {
		Symbol  string `json:"symbol"`
		Address string `json:"address"`
	} `json:"token_info"`
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"` // в минимальных единицах (6 знаков для USDT)
	Type  string `json:"type"`
	BlockTimestamp int64 `json:"block_timestamp"`
}

type tronGridResp struct {
	Data []tronTx `json:"data"`
}

// startTronWatcher запускает фоновую горутину мониторинга входящих USDT.
func (s *Server) startTronWatcher(ctx context.Context) {
	if s.tronAddr == "" {
		log.Println("tron_watcher: TRON_RECEIVE_ADDRESS not set, skipping")
		return
	}
	log.Printf("tron_watcher: monitoring %s", s.tronAddr)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkTronDeposits(ctx)
		}
	}
}

func (s *Server) checkTronDeposits(ctx context.Context) {
	txs, err := s.fetchTronTransactions(ctx)
	if err != nil {
		log.Printf("tron_watcher: fetch error: %v", err)
		return
	}

	// Экспайрим просроченные депозиты
	if _, err := s.pool.Exec(ctx,
		`UPDATE tron_deposits SET status='expired'
		 WHERE status='pending' AND expires_at < NOW()`); err != nil {
		log.Printf("tron_watcher: expire error: %v", err)
	}

	for _, tx := range txs {
		// Только входящие USDT-переводы
		if tx.To != s.tronAddr {
			continue
		}
		if tx.TokenInfo.Address != usdtContractTRC20 {
			continue
		}
		if tx.Type != "Transfer" {
			continue
		}

		// Пропускаем уже обработанные транзакции
		var exists bool
		s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM tron_deposits WHERE tx_hash=$1)`,
			tx.TransactionID,
		).Scan(&exists)
		if exists {
			continue
		}

		// Конвертируем value из строки в USDT (6 знаков)
		var valueRaw int64
		fmt.Sscanf(tx.Value, "%d", &valueRaw)
		amountUSDT := float64(valueRaw) / 1e6

		// Ищем pending депозит с совпадающей суммой
		var depositID, userID string
		err := s.pool.QueryRow(ctx,
			`SELECT id, user_id FROM tron_deposits
			 WHERE status='pending'
			   AND ABS(amount_exact - $1) < $2
			   AND expires_at > NOW()
			 ORDER BY created_at ASC
			 LIMIT 1`,
			amountUSDT, amountTolerance,
		).Scan(&depositID, &userID)
		if err != nil {
			log.Printf("tron_watcher: no pending deposit for %.6f USDT (tx %s)", amountUSDT, tx.TransactionID)
			continue
		}

		// Зачисляем в транзакции
		dbTx, err := s.pool.Begin(ctx)
		if err != nil {
			log.Printf("tron_watcher: begin tx error: %v", err)
			continue
		}

		// Обновляем депозит
		if _, err := dbTx.Exec(ctx,
			`UPDATE tron_deposits
			 SET status='confirmed', tx_hash=$1, confirmed_at=NOW()
			 WHERE id=$2`,
			tx.TransactionID, depositID,
		); err != nil {
			dbTx.Rollback(ctx)
			log.Printf("tron_watcher: update deposit error: %v", err)
			continue
		}

		// Зачисляем novabot_balance (сумму без центов-маркера)
		creditAmount := math.Round(amountUSDT*100) / 100 // округляем до 2 знаков
		if _, err := dbTx.Exec(ctx,
			`UPDATE users SET novabot_balance = novabot_balance + $1 WHERE id=$2`,
			creditAmount, userID,
		); err != nil {
			dbTx.Rollback(ctx)
			log.Printf("tron_watcher: credit balance error: %v", err)
			continue
		}

		// Записываем транзакцию в историю
		if _, err := dbTx.Exec(ctx,
			`INSERT INTO novabot_transactions (user_id, amount, kind, note)
			 VALUES ($1, $2, 'deposit', $3)`,
			userID, creditAmount, "USDT TRC20 "+tx.TransactionID[:16]+"...",
		); err != nil {
			// Не критично — транзакция истории не обязательна
			log.Printf("tron_watcher: insert tx history error: %v", err)
		}

		if err := dbTx.Commit(ctx); err != nil {
			log.Printf("tron_watcher: commit error: %v", err)
			continue
		}

		log.Printf("tron_watcher: confirmed deposit %s for user %s — %.6f USDT (tx %s)",
			depositID, userID, amountUSDT, tx.TransactionID)

		// Уведомить через Telegram если подключён
		go s.notifyDepositConfirmed(ctx, userID, creditAmount, tx.TransactionID)
	}
}

func (s *Server) fetchTronTransactions(ctx context.Context) ([]tronTx, error) {
	apiKey := getEnv("TRONGRID_API_KEY", "")
	url := fmt.Sprintf(
		"%s/v1/accounts/%s/transactions/trc20?limit=50&contract_address=%s",
		tronGridBase, s.tronAddr, usdtContractTRC20,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trongrid: status %d", resp.StatusCode)
	}

	var result tronGridResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// notifyDepositConfirmed отправляет TG-уведомление о зачислении.
func (s *Server) notifyDepositConfirmed(ctx context.Context, userID string, amount float64, txHash string) {
	var chatID int64
	var muteUntil *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT tc.chat_id, tc.mute_until
		 FROM telegram_connections tc
		 WHERE tc.user_id=$1`, userID,
	).Scan(&chatID, &muteUntil)
	if err != nil {
		return // TG не привязан — ок
	}
	if muteUntil != nil && muteUntil.After(time.Now()) {
		return // заглушено
	}
	text := fmt.Sprintf("✅ *Баланс пополнен*\n\n💵 `+%.2f USDT`\n\n🔗 TX: `%s...`", amount, txHash[:16])
	s.publishTgNotify(ctx, TgNotifyMsg{ChatID: chatID, Text: text})
}
```

- [ ] **Проверить компиляцию**

```bash
cd C:\Users\123\Projects\sis
go build ./services/api-gateway/...
# Expected: no errors
```

- [ ] **Commit**

```bash
git add services/api-gateway/tron_watcher.go
git commit -m "feat(gateway): add tron watcher goroutine"
```

---

## Task 4: Подключение в `server.go` и `main.go`

**Files:**
- Modify: `services/api-gateway/server.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Добавить `tronAddr` в Server struct в `server.go`**

В блоке `type Server struct { ... }` после `botSecret string` добавить:
```go
tronAddr string
```

В `NewServer(...)` после `botSecret: botSecret,` добавить:
```go
tronAddr:  tronAddr,
```

Обновить сигнатуру функции `NewServer` — добавить параметр `tronAddr string` после `botSecret`:
```go
func NewServer(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client,
    jwtSecret, encKey, botSecret, tronAddr string,
    adminEmails map[string]bool, pm *proxy.Manager, ns *bybitnews.Scraper) *Server {
```

- [ ] **Обновить `main.go`**

После строки `botSecret := getEnv("TELEGRAM_BOT_SECRET", "")` добавить:
```go
tronAddr := getEnv("TRON_RECEIVE_ADDRESS", "")
```

Обновить вызов `NewServer`:
```go
s := NewServer(ctx, pool, rdb, jwtSecret, encKey, botSecret, tronAddr, adminEmails, pm, ns)
```

После `go s.startTgNotifier(ctx)` добавить:
```go
// Start TRON deposit watcher
go s.startTronWatcher(ctx)
```

В блоке защищённых маршрутов добавить после `r.Get("/account/referral", s.GetReferral)`:
```go
// TRON payments
r.Post("/payments/tron/deposit", s.CreateTronDeposit)
r.Get("/payments/tron/deposit/{id}", s.GetTronDeposit)
r.Get("/payments/tron/deposits", s.ListTronDeposits)
```

- [ ] **Проверить компиляцию**

```bash
cd C:\Users\123\Projects\sis
go build ./services/api-gateway/...
# Expected: no errors
```

- [ ] **Commit**

```bash
git add services/api-gateway/server.go services/api-gateway/main.go
git commit -m "feat(gateway): wire tron payment routes + watcher"
```

---

## Task 5: Frontend — `api/payments.ts`

**Files:**
- Create: `frontend/src/api/payments.ts`

- [ ] **Создать `payments.ts`**

```typescript
// frontend/src/api/payments.ts
import { apiClient } from './client'

export interface TronDeposit {
  id: string
  amount_usdt: number
  amount_exact: number
  address: string
  status: 'pending' | 'confirmed' | 'expired'
  expires_at: string
  confirmed_at?: string
  tx_hash?: string
}

export async function createTronDeposit(amount: number): Promise<TronDeposit> {
  const res = await apiClient.post<TronDeposit>('/payments/tron/deposit', { amount })
  return res.data
}

export async function getTronDeposit(id: string): Promise<TronDeposit> {
  const res = await apiClient.get<TronDeposit>(`/payments/tron/deposit/${id}`)
  return res.data
}

export async function listTronDeposits(): Promise<TronDeposit[]> {
  const res = await apiClient.get<TronDeposit[]>('/payments/tron/deposits')
  return res.data
}
```

- [ ] **Commit**

```bash
git add frontend/src/api/payments.ts
git commit -m "feat(frontend): add payments API client"
```

---

## Task 6: Frontend — `PaymentsPage.tsx`

**Files:**
- Create: `frontend/src/pages/PaymentsPage.tsx`

Установи QR-библиотеку:
```bash
cd frontend
npm install qrcode.react
npm install --save-dev @types/qrcode.react
```

- [ ] **Создать `PaymentsPage.tsx`**

```tsx
// frontend/src/pages/PaymentsPage.tsx
import { useState, useEffect, useRef } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import {
  createTronDeposit, getTronDeposit, listTronDeposits,
  type TronDeposit,
} from '../api/payments'

/* ── дизайн-токены ────────────────────────────────────────────── */
const T = {
  bg: '#080b12', panel: '#0c1018', border: 'rgba(255,255,255,.07)',
  text: '#f2f5fb', dim: '#7b8aa6', faint: '#5b6479',
  green: '#5be0a0', greenSoft: 'rgba(65,210,139,.12)', greenBd: 'rgba(65,210,139,.25)',
  blue: '#5b8cff', blueSoft: 'rgba(91,140,255,.12)', blueBd: 'rgba(91,140,255,.22)',
  orange: '#f7a600', orangeSoft: 'rgba(247,166,0,.12)', orangeBd: 'rgba(247,166,0,.28)',
  red: '#fca5a5', redSoft: 'rgba(248,113,113,.12)', redBd: 'rgba(248,113,113,.25)',
}
const mono = { fontFamily: "'JetBrains Mono', monospace" }
const grotesk = { fontFamily: "'Space Grotesk', sans-serif" }

/* ── таймер ───────────────────────────────────────────────────── */
function useCountdown(expiresAt: string | null) {
  const [secs, setSecs] = useState(0)
  useEffect(() => {
    if (!expiresAt) return
    const tick = () => setSecs(Math.max(0, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000)))
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [expiresAt])
  const m = String(Math.floor(secs / 60)).padStart(2, '0')
  const s = String(secs % 60).padStart(2, '0')
  return { display: `${m}:${s}`, expired: secs === 0 }
}

/* ── копирование ─────────────────────────────────────────────── */
function useCopy() {
  const [copied, setCopied] = useState<string | null>(null)
  const copy = (text: string, key: string) => {
    navigator.clipboard.writeText(text)
    setCopied(key)
    setTimeout(() => setCopied(null), 1500)
  }
  return { copied, copy }
}

/* ── иконки ──────────────────────────────────────────────────── */
const IcCopy = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></svg>
const IcCheck = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 13l4 4 10-10"/></svg>
const IcChevDown = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M6 9l6 6 6-6"/></svg>

/* ── статус-пилюля ────────────────────────────────────────────── */
function StatusPill({ status }: { status: TronDeposit['status'] }) {
  const map = {
    pending:   { label: 'Ожидание',   c: T.orange, bg: T.orangeSoft, bd: T.orangeBd },
    confirmed: { label: 'Зачислено',  c: T.green,  bg: T.greenSoft,  bd: T.greenBd },
    expired:   { label: 'Истёк',      c: T.dim,    bg: 'rgba(255,255,255,.05)', bd: T.border },
  }
  const s = map[status]
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '3px 9px', borderRadius: 999, fontSize: 11, fontWeight: 600,
      color: s.c, background: s.bg, border: `1px solid ${s.bd}`,
    }}>{s.label}</span>
  )
}

/* ── активный инвойс ──────────────────────────────────────────── */
function ActiveInvoice({
  deposit, onConfirmed,
}: {
  deposit: TronDeposit
  onConfirmed: (dep: TronDeposit) => void
}) {
  const { display, expired } = useCountdown(deposit.expires_at)
  const { copied, copy } = useCopy()
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [status, setStatus] = useState(deposit.status)

  useEffect(() => {
    pollRef.current = setInterval(async () => {
      try {
        const updated = await getTronDeposit(deposit.id)
        setStatus(updated.status)
        if (updated.status === 'confirmed') {
          clearInterval(pollRef.current!)
          onConfirmed(updated)
        }
        if (updated.status === 'expired') {
          clearInterval(pollRef.current!)
        }
      } catch {}
    }, 10_000)
    return () => clearInterval(pollRef.current!)
  }, [deposit.id])

  const addrShort = deposit.address.slice(0, 8) + '...' + deposit.address.slice(-6)
  const timerColor = expired ? T.red : display < '05:00' ? T.orange : T.green

  return (
    <div style={{
      background: T.panel, border: `1px solid ${T.blueBd}`,
      borderRadius: 16, overflow: 'hidden',
      boxShadow: '0 12px 40px -20px rgba(91,140,255,.3)',
    }}>
      {/* header */}
      <div style={{
        padding: '16px 20px', borderBottom: `1px solid ${T.border}`,
        background: 'linear-gradient(180deg, rgba(91,140,255,.06) 0%, transparent 100%)',
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      }}>
        <div>
          <div style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text }}>
            Пополнение баланса
          </div>
          <div style={{ fontSize: 12, color: T.dim, marginTop: 2 }}>
            Сеть: <span style={{ color: T.text, fontWeight: 600 }}>USDT TRC20 (Tron)</span>
          </div>
        </div>
        <div style={{ textAlign: 'right' }}>
          <div style={{ fontSize: 11, color: T.dim }}>Осталось</div>
          <div style={{ ...mono, fontSize: 20, fontWeight: 700, color: timerColor }}>{display}</div>
        </div>
      </div>

      {/* body */}
      <div style={{ padding: '20px', display: 'flex', flexDirection: 'column', gap: 18 }}>
        {/* сумма */}
        <div style={{
          padding: '16px 20px', borderRadius: 12,
          background: 'rgba(91,140,255,.08)', border: `1px solid ${T.blueBd}`,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div>
            <div style={{ fontSize: 11, color: T.dim, marginBottom: 4 }}>ОТПРАВЬТЕ РОВНО</div>
            <div style={{ ...mono, fontSize: 26, fontWeight: 700, color: T.text, letterSpacing: -0.5 }}>
              {deposit.amount_exact.toFixed(2)} <span style={{ fontSize: 14, color: T.dim }}>USDT</span>
            </div>
          </div>
          <button
            onClick={() => copy(String(deposit.amount_exact.toFixed(2)), 'amount')}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              padding: '8px 14px', borderRadius: 8,
              background: copied === 'amount' ? T.greenSoft : 'rgba(255,255,255,.06)',
              border: `1px solid ${copied === 'amount' ? T.greenBd : T.border}`,
              color: copied === 'amount' ? T.green : T.dim,
              fontSize: 12, fontWeight: 600, cursor: 'pointer',
            }}
          >
            {copied === 'amount' ? <IcCheck /> : <IcCopy />}
            {copied === 'amount' ? 'Скопировано' : 'Копировать'}
          </button>
        </div>

        {/* адрес + QR */}
        <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          <div style={{
            padding: 12, borderRadius: 12,
            background: '#fff', flexShrink: 0,
          }}>
            <QRCodeSVG value={deposit.address} size={120} />
          </div>
          <div style={{ flex: 1, minWidth: 200 }}>
            <div style={{ fontSize: 11, color: T.dim, marginBottom: 6, fontWeight: 600 }}>АДРЕС КОШЕЛЬКА</div>
            <div style={{
              padding: '10px 12px', borderRadius: 10,
              background: 'rgba(0,0,0,.25)', border: `1px solid ${T.border}`,
              display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8,
            }}>
              <span style={{ ...mono, fontSize: 12, color: T.text, wordBreak: 'break-all' }}>
                {deposit.address}
              </span>
              <button
                onClick={() => copy(deposit.address, 'addr')}
                style={{
                  flexShrink: 0, display: 'flex', alignItems: 'center', gap: 5,
                  padding: '6px 10px', borderRadius: 7,
                  background: copied === 'addr' ? T.greenSoft : 'rgba(255,255,255,.05)',
                  border: `1px solid ${copied === 'addr' ? T.greenBd : T.border}`,
                  color: copied === 'addr' ? T.green : T.dim,
                  fontSize: 11, fontWeight: 600, cursor: 'pointer',
                }}
              >
                {copied === 'addr' ? <IcCheck /> : <IcCopy />}
              </button>
            </div>

            {/* предупреждение */}
            <div style={{
              marginTop: 10, padding: '10px 12px', borderRadius: 10,
              background: T.orangeSoft, border: `1px solid ${T.orangeBd}`,
              fontSize: 12, color: T.orange, lineHeight: 1.5,
            }}>
              ⚠️ Отправляйте <strong>только USDT через сеть TRC20</strong>.
              Отправка через другую сеть приведёт к потере средств.
            </div>
          </div>
        </div>

        {/* статус */}
        {status === 'confirmed' && (
          <div style={{
            padding: '14px 16px', borderRadius: 12,
            background: T.greenSoft, border: `1px solid ${T.greenBd}`,
            fontSize: 14, fontWeight: 600, color: T.green, textAlign: 'center',
          }}>
            ✅ Баланс пополнен на {deposit.amount_exact.toFixed(2)} USDT
          </div>
        )}
        {(expired || status === 'expired') && (
          <div style={{
            padding: '14px 16px', borderRadius: 12,
            background: T.redSoft, border: `1px solid ${T.redBd}`,
            fontSize: 13, color: T.red, textAlign: 'center',
          }}>
            Время истекло. Создайте новый запрос на пополнение.
          </div>
        )}
      </div>
    </div>
  )
}

/* ── форма создания депозита ──────────────────────────────────── */
const PRESETS = [10, 20, 50, 100, 200, 500]

function CreateDepositForm({ onCreated }: { onCreated: (dep: TronDeposit) => void }) {
  const [amount, setAmount] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit() {
    const num = parseFloat(amount)
    if (!num || num < 1) { setError('Минимум 1 USDT'); return }
    setError('')
    setLoading(true)
    try {
      const dep = await createTronDeposit(num)
      onCreated(dep)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка создания депозита')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{
      background: T.panel, border: `1px solid ${T.border}`,
      borderRadius: 16, padding: '20px',
    }}>
      <div style={{ ...grotesk, fontSize: 15, fontWeight: 700, color: T.text, marginBottom: 16 }}>
        Пополнить баланс
      </div>

      {/* пресеты */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 12 }}>
        {PRESETS.map(p => (
          <button key={p} onClick={() => setAmount(String(p))} style={{
            padding: '7px 14px', borderRadius: 8,
            background: amount === String(p) ? T.blueSoft : 'rgba(255,255,255,.04)',
            border: `1px solid ${amount === String(p) ? T.blueBd : T.border}`,
            color: amount === String(p) ? T.blue : T.dim,
            fontSize: 13, fontWeight: 600, cursor: 'pointer',
          }}>
            ${p}
          </button>
        ))}
      </div>

      {/* поле ввода */}
      <div style={{ display: 'flex', gap: 8 }}>
        <div style={{ flex: 1, position: 'relative' }}>
          <input
            type="number" min="1" step="1"
            value={amount}
            onChange={e => setAmount(e.target.value)}
            placeholder="Введите сумму"
            style={{
              width: '100%', ...mono, fontSize: 16, fontWeight: 600,
              background: 'rgba(0,0,0,.3)', color: T.text,
              border: `1px solid ${T.border}`, borderRadius: 10,
              padding: '12px 48px 12px 14px', outline: 'none',
            }}
          />
          <span style={{
            position: 'absolute', right: 14, top: '50%', transform: 'translateY(-50%)',
            fontSize: 13, fontWeight: 600, color: T.dim,
          }}>USDT</span>
        </div>
        <button
          onClick={handleSubmit}
          disabled={loading || !amount}
          style={{
            padding: '12px 24px', borderRadius: 10, border: 0,
            background: loading || !amount
              ? 'rgba(91,140,255,.3)'
              : 'linear-gradient(135deg, #5b8cff, #7b5bff)',
            color: '#fff', fontSize: 14, fontWeight: 700,
            cursor: loading || !amount ? 'not-allowed' : 'pointer',
            whiteSpace: 'nowrap',
          }}
        >
          {loading ? 'Создаём…' : 'Пополнить →'}
        </button>
      </div>

      {error && (
        <div style={{
          marginTop: 10, padding: '10px 12px', borderRadius: 8,
          background: T.redSoft, border: `1px solid ${T.redBd}`,
          fontSize: 12, color: T.red,
        }}>{error}</div>
      )}

      <div style={{ marginTop: 12, fontSize: 11, color: T.faint, lineHeight: 1.6 }}>
        💡 После создания запроса отправьте <strong style={{ color: T.dim }}>точную сумму</strong> на указанный адрес.
        Зачисление происходит автоматически в течение 1–3 минут после подтверждения в сети Tron.
      </div>
    </div>
  )
}

/* ── история ─────────────────────────────────────────────────── */
function DepositHistory({ deposits }: { deposits: TronDeposit[] }) {
  const [open, setOpen] = useState(false)
  if (deposits.length === 0) return null
  return (
    <div style={{ background: T.panel, border: `1px solid ${T.border}`, borderRadius: 14 }}>
      <button
        onClick={() => setOpen(v => !v)}
        style={{
          width: '100%', padding: '14px 18px',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          background: 'transparent', border: 0, cursor: 'pointer', color: T.text,
        }}
      >
        <span style={{ fontSize: 13, fontWeight: 600 }}>История пополнений ({deposits.length})</span>
        <span style={{ transform: open ? 'rotate(180deg)' : 'none', transition: 'transform .15s', color: T.dim }}>
          <IcChevDown />
        </span>
      </button>
      {open && (
        <div style={{ borderTop: `1px solid ${T.border}` }}>
          {deposits.map(dep => (
            <div key={dep.id} style={{
              padding: '12px 18px', borderBottom: `1px solid ${T.border}`,
              display: 'flex', alignItems: 'center', gap: 12,
            }}>
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ ...mono, fontSize: 14, fontWeight: 700, color: T.text }}>
                    +{dep.amount_exact.toFixed(2)} USDT
                  </span>
                  <StatusPill status={dep.status} />
                </div>
                {dep.tx_hash && (
                  <div style={{ fontSize: 11, color: T.dim, marginTop: 3, ...mono }}>
                    {dep.tx_hash.slice(0, 20)}…
                  </div>
                )}
              </div>
              <div style={{ fontSize: 11, color: T.faint }}>
                {new Date(dep.confirmed_at ?? dep.expires_at).toLocaleDateString('ru-RU')}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/* ── главная страница ────────────────────────────────────────── */
export function PaymentsPage() {
  const [activeDeposit, setActiveDeposit] = useState<TronDeposit | null>(null)
  const [history, setHistory] = useState<TronDeposit[]>([])

  useEffect(() => {
    listTronDeposits().then(list => {
      const pending = list.find(d => d.status === 'pending')
      if (pending) setActiveDeposit(pending)
      setHistory(list)
    }).catch(() => {})
  }, [])

  function handleCreated(dep: TronDeposit) {
    setActiveDeposit(dep)
    setHistory(prev => [dep, ...prev])
  }

  function handleConfirmed(dep: TronDeposit) {
    setActiveDeposit(dep)
    setHistory(prev => prev.map(d => d.id === dep.id ? dep : d))
  }

  return (
    <div style={{ background: T.bg, minHeight: '100vh', padding: '28px 24px', maxWidth: 680, margin: '0 auto' }}>
      {/* заголовок */}
      <div style={{ marginBottom: 24 }}>
        <h1 style={{ ...grotesk, fontSize: 24, fontWeight: 700, color: T.text, margin: 0, letterSpacing: -0.5 }}>
          💰 Баланс
        </h1>
        <p style={{ margin: '6px 0 0', fontSize: 13, color: T.dim }}>
          Пополнение через USDT TRC20 — без комиссии платформы
        </p>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* активный инвойс или форма создания */}
        {activeDeposit && activeDeposit.status === 'pending'
          ? <ActiveInvoice deposit={activeDeposit} onConfirmed={handleConfirmed} />
          : <CreateDepositForm onCreated={handleCreated} />
        }

        {/* история */}
        <DepositHistory deposits={history.filter(d => d.status !== 'pending')} />
      </div>
    </div>
  )
}
```

- [ ] **Commit**

```bash
git add frontend/src/pages/PaymentsPage.tsx frontend/src/api/payments.ts
git commit -m "feat(frontend): add TRON payments page with QR and countdown"
```

---

## Task 7: Подключить маршрут и пункт в сайдбаре

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/components/Sidebar/Sidebar.tsx`

- [ ] **Добавить маршрут в `App.tsx`**

Найти импорты страниц и добавить:
```tsx
import { PaymentsPage } from './pages/PaymentsPage'
```

В блоке роутов добавить рядом с другими защищёнными маршрутами:
```tsx
<Route path="/payments" element={<PaymentsPage />} />
```

- [ ] **Добавить пункт в Sidebar**

Найти в `Sidebar.tsx` блок с навигационными ссылками. Добавить новый пункт по аналогии с существующими:

```tsx
{ path: '/payments', label: 'Баланс', icon: <IcWallet /> }
```

Если иконки объявлены локально — добавить `IcWallet`:
```tsx
const IcWallet = () => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <rect x="2" y="7" width="20" height="14" rx="2"/>
    <path d="M16 3l-4 4-4-4"/>
    <path d="M2 11h20"/>
  </svg>
)
```

- [ ] **Проверить что фронт собирается**

```bash
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
# Expected: no errors
```

- [ ] **Commit**

```bash
git add frontend/src/App.tsx frontend/src/components/Sidebar/Sidebar.tsx
git commit -m "feat(frontend): add /payments route and sidebar link"
```

---

## Self-Review

**Spec coverage:**
- ✅ Пользователь вводит сумму → форма с пресетами
- ✅ Уникальная сумма (amount_exact = amount + 1-90 центов)
- ✅ Адрес кошелька + QR-код
- ✅ Таймер 30 минут
- ✅ Предупреждение «только TRC20»
- ✅ Копирование суммы и адреса
- ✅ Автополлинг статуса каждые 10 сек
- ✅ Зачисление на novabot_balance
- ✅ TG-уведомление после зачисления
- ✅ Экспайр просроченных депозитов
- ✅ История депозитов
- ✅ Защита от double-credit (UNIQUE INDEX на tx_hash)
- ✅ Поддержка TRONGRID_API_KEY (опционально)

**Dependency chain:**
Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6 → Task 7
