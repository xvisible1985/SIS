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

// TokenTxSince returns TRC-20 transfers TO/FROM address since sinceMS (unix ms).
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
