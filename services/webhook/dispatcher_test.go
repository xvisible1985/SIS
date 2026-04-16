// services/webhook/dispatcher_test.go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseSignalPayload_Valid(t *testing.T) {
	raw := `{"signal_id":"uuid-123","signal_name":"RSI cross","symbol":"BTCUSDT","exchange":"binance","market":"spot","direction":"LONG","price":"67420.50","timestamp":"2026-04-16T12:00:00Z"}`
	s, err := parseSignalPayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.SignalID != "uuid-123" {
		t.Errorf("got SignalID=%q, want uuid-123", s.SignalID)
	}
	if s.Symbol != "BTCUSDT" {
		t.Errorf("got Symbol=%q, want BTCUSDT", s.Symbol)
	}
	if s.Direction != "LONG" {
		t.Errorf("got Direction=%q, want LONG", s.Direction)
	}
	if s.Price != "67420.50" {
		t.Errorf("got Price=%q, want 67420.50", s.Price)
	}
}

func TestParseSignalPayload_InvalidJSON(t *testing.T) {
	_, err := parseSignalPayload("not json {")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRetryDelays(t *testing.T) {
	delays := retryDelays()
	if len(delays) != 3 {
		t.Fatalf("expected 3 delays, got %d", len(delays))
	}
	expected := []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second}
	for i, want := range expected {
		if delays[i] != want {
			t.Errorf("delay[%d]: got %v, want %v", i, delays[i], want)
		}
	}
}

func TestDeliverOnce_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := FiredSignal{SignalID: "s1", Symbol: "BTCUSDT", Direction: "LONG", Price: "50000"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, payload)
	if !result.Success {
		t.Errorf("expected Success=true, got false, err=%s", result.Error)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("got StatusCode=%d, want 200", result.StatusCode)
	}
	if result.ResponseMs < 0 {
		t.Errorf("ResponseMs should be non-negative")
	}
}

func TestDeliverOnce_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	payload := FiredSignal{SignalID: "s1"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, payload)
	if result.Success {
		t.Error("expected Success=false for 500 response")
	}
	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("got StatusCode=%d, want 500", result.StatusCode)
	}
}

func TestDeliverOnce_NetworkError(t *testing.T) {
	// Port 1 is guaranteed to refuse connections
	payload := FiredSignal{SignalID: "s1"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 1 * time.Second}, "http://127.0.0.1:1", payload)
	if result.Success {
		t.Error("expected Success=false for network error")
	}
	if result.Error == "" {
		t.Error("expected non-empty error for network error")
	}
}

func TestDeliverOnce_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := FiredSignal{SignalID: "s1"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 50 * time.Millisecond}, srv.URL, payload)
	if result.Success {
		t.Error("expected Success=false for timeout")
	}
	if result.Error == "" {
		t.Error("expected non-empty error for timeout")
	}
}
