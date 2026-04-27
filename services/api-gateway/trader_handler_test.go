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
