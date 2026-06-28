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

	// Etherscan returns "result" as a string on errors (e.g. rate limit) and as
	// an array on success. Unmarshal into a raw envelope first to detect this.
	var envelope struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("etherscan parse: %w", err)
	}
	if envelope.Status != "1" {
		if envelope.Message == "No transactions found" {
			return nil, nil
		}
		var msg string
		_ = json.Unmarshal(envelope.Result, &msg)
		if msg == "" {
			msg = envelope.Message
		}
		return nil, fmt.Errorf("etherscan error: %s", msg)
	}
	var rows []struct {
		Hash            string `json:"hash"`
		From            string `json:"from"`
		To              string `json:"to"`
		TokenSymbol     string `json:"tokenSymbol"`
		TokenDecimal    string `json:"tokenDecimal"`
		Value           string `json:"value"`
		TimeStamp       string `json:"timeStamp"`
		ContractAddress string `json:"contractAddress"`
	}
	var result = &rows
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return nil, fmt.Errorf("etherscan parse rows: %w", err)
	}

	var out []TokenTransfer
	for _, r := range rows {
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
