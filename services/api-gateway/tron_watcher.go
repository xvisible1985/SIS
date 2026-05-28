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
	From           string `json:"from"`
	To             string `json:"to"`
	Value          string `json:"value"` // в минимальных единицах (6 знаков для USDT)
	Type           string `json:"type"`
	BlockTimestamp int64  `json:"block_timestamp"`
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
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("tron_watcher: recovered from panic: %v", r)
					}
				}()
				s.checkTronDeposits(ctx)
			}()
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

		// Обновляем депозит (AND status='pending' — защита от двойного зачисления)
		tag, err := dbTx.Exec(ctx,
			`UPDATE tron_deposits
			 SET status='confirmed', tx_hash=$1, confirmed_at=NOW()
			 WHERE id=$2 AND status='pending'`,
			tx.TransactionID, depositID,
		)
		if err != nil {
			dbTx.Rollback(ctx)
			log.Printf("tron_watcher: update deposit error: %v", err)
			continue
		}
		if tag.RowsAffected() == 0 {
			dbTx.Rollback(ctx)
			log.Printf("tron_watcher: deposit %s already processed, skipping", depositID)
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
