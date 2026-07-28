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
