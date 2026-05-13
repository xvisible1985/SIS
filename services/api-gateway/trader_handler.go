package main

import (
	"context"
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

// makeOrderLinkID returns a SIS_TRM-N order link ID for terminal (manual) orders.
func (s *Server) makeOrderLinkID(ctx context.Context) string {
	var n int64
	if err := s.pool.QueryRow(ctx, "SELECT nextval('sis_order_seq')").Scan(&n); err != nil {
		return fmt.Sprintf("SIS_TRM-%d", time.Now().UnixMilli())
	}
	return fmt.Sprintf("SIS_TRM-%d", n)
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

// TraderSwitchPositionMode switches position mode for a symbol (0=one-way, 3=hedge).
// POST /trader/position-mode
func (s *Server) TraderSwitchPositionMode(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		AccountID string `json:"account_id"`
		Symbol    string `json:"symbol"`
		Category  string `json:"category"`
		Mode      int    `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "account_id and symbol are required")
		return
	}
	if req.Category == "" {
		req.Category = "linear"
	}
	creds, err := s.loadCreds(r, req.AccountID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := trader.SwitchPositionMode(r.Context(), creds, req.Category, req.Symbol, req.Mode); err != nil {
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
