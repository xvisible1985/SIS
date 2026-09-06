package trader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var wsTestUpgrader = websocket.Upgrader{}

// TestRunPositionStream_ReturnsWhenBybitConnectionGoesSilent guards against a real
// production incident: RunPositionStream had no read deadline on the upstream Bybit
// WS connection, so a silently-dead network path (packets black-holed, no TCP
// RST/FIN — e.g. a flaky VPN/proxy) left the reader goroutine blocked in
// bwsConn.ReadMessage() forever. bybitErrCh never fired, so the function never
// returned, the frontend-facing conn was never closed, and the browser's WS client
// never reconnected to fetch a fresh REST snapshot — the Positions tab in the
// terminal froze showing stale/phantom positions indefinitely (observed: real
// sessions stuck for 10-55+ hours after a Bybit connectivity blip).
func TestRunPositionStream_ReturnsWhenBybitConnectionGoesSilent(t *testing.T) {
	origTimeout := bybitReadTimeout
	bybitReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { bybitReadTimeout = origTimeout })

	mux := http.NewServeMux()
	mux.HandleFunc("/v5/market/time", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"time":1000000000000}`))
	})
	mux.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage() // the auth message
		_ = conn.WriteJSON(map[string]any{"op": "auth", "success": true})
		// Go silent forever after auth — simulating a network path that black-holes
		// without ever sending a close/error. This blocking read only returns once
		// the client (production code) detects its own read-deadline timeout and
		// closes bwsConn — proving the fix actually unblocks both sides.
		_, _, _ = conn.ReadMessage()
	})
	mux.HandleFunc("/frontend", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origBase := bybitBase
	bybitBase = srv.URL
	defer func() { bybitBase = origBase }()

	origWS := bybitPrivateWS
	bybitPrivateWS = "ws" + strings.TrimPrefix(srv.URL, "http") + "/private"
	defer func() { bybitPrivateWS = origWS }()

	frontendURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/frontend"
	frontendConn, _, err := websocket.DefaultDialer.Dial(frontendURL, nil)
	if err != nil {
		t.Fatalf("dial frontend sink: %v", err)
	}
	defer frontendConn.Close()

	done := make(chan struct{})
	go func() {
		RunPositionStream(context.Background(), frontendConn, Credentials{APIKey: "k", SecretKey: "s"}, "test", nil)
		close(done)
	}()

	select {
	case <-done:
		// success: RunPositionStream returned instead of hanging forever
	case <-time.After(3 * time.Second):
		t.Fatal("RunPositionStream did not return after the upstream Bybit connection went silent — read deadline is not being enforced")
	}
}
