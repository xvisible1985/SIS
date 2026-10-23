package trader

import (
	"encoding/json"
	"testing"
)

// Regression for the 2026-10-22 incident: PositionEvent.AvgPrice was tagged json:"avgPrice",
// but Bybit's WS "position" topic names that field entryPrice (avgPrice is only used by REST
// /v5/position/list and the WS "order" topic) — see docs reference_bybit_api.md. The wrong
// tag left AvgPrice permanently "0" from every live position update, which silently stalled
// anything reading it (e.g. pkg/strategy/als.go's alsPriceTick never saw a real main-position
// entry price and could never place a hedge order).
func TestPositionEvent_ParsesEntryPriceAsAvgPrice(t *testing.T) {
	// A representative Bybit WS "position" topic data[] item.
	raw := []byte(`{
		"symbol": "BTRUSDT",
		"side": "Buy",
		"size": "162",
		"entryPrice": "0.06029133",
		"category": "linear",
		"positionIdx": 1,
		"leverage": "150"
	}`)

	var ev PositionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if ev.AvgPrice != "0.06029133" {
		t.Errorf("AvgPrice = %q, want %q (from entryPrice field)", ev.AvgPrice, "0.06029133")
	}
	if ev.Symbol != "BTRUSDT" || ev.PositionIdx != 1 {
		t.Errorf("unexpected Symbol/PositionIdx: %+v", ev)
	}
}

// A payload using the REST-style "avgPrice" key (never sent by the WS position topic in
// practice) must NOT populate AvgPrice — asserting this pins the field mapping precisely,
// so a future "helpful" revert back to json:"avgPrice" fails loudly instead of silently
// reintroducing the stalled-position-tracking bug.
func TestPositionEvent_IgnoresAvgPriceKey(t *testing.T) {
	raw := []byte(`{"symbol": "BTCUSDT", "avgPrice": "50000", "positionIdx": 1}`)

	var ev PositionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if ev.AvgPrice != "" {
		t.Errorf("AvgPrice = %q, want empty — PositionEvent must read entryPrice, not avgPrice", ev.AvgPrice)
	}
}
