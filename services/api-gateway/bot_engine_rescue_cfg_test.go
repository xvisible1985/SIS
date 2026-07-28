package main

import (
	"encoding/json"
	"testing"
)

// TestBotCfgJSON_RescueFieldsRoundTrip проверяет, что новые RescueBot-поля
// корректно проходят JSON-маршалинг/анмаршалинг, включая nil-указатели
// (означающие "триггер выключен") и вложенный TriggerSignal.
func TestBotCfgJSON_RescueFieldsRoundTrip(t *testing.T) {
	movePct := 1.5
	level := 61200.0
	minAccum := 25.0

	original := botCfgJSON{
		BotKind:                         "hedge", // не "rescue" — это доработка hedge-бота, не новый bot_kind
		RescuePartialCloseEnabled:       true,
		RescueTriggerPriceMovePct:       &movePct,
		RescueTriggerPriceLevel:         &level,
		RescueTriggerAccumulatedMinUsdt: &minAccum,
		RescueMinIntervalSec:            300,
		RescueTriggerSignal:             &rescueSignalTrigger{Name: "st-flip", Params: map[string]interface{}{"tf": "15"}},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded botCfgJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.BotKind != "hedge" || !decoded.RescuePartialCloseEnabled {
		t.Errorf("BotKind/Enabled mismatch: %+v", decoded)
	}
	if decoded.RescueTriggerPriceMovePct == nil || *decoded.RescueTriggerPriceMovePct != movePct {
		t.Errorf("RescueTriggerPriceMovePct mismatch: %+v", decoded.RescueTriggerPriceMovePct)
	}
	if decoded.RescueTriggerPriceLevel == nil || *decoded.RescueTriggerPriceLevel != level {
		t.Errorf("RescueTriggerPriceLevel mismatch: %+v", decoded.RescueTriggerPriceLevel)
	}
	if decoded.RescueTriggerAccumulatedMinUsdt == nil || *decoded.RescueTriggerAccumulatedMinUsdt != minAccum {
		t.Errorf("RescueTriggerAccumulatedMinUsdt mismatch: %+v", decoded.RescueTriggerAccumulatedMinUsdt)
	}
	if decoded.RescueTriggerSignal == nil || decoded.RescueTriggerSignal.Name != "st-flip" {
		t.Errorf("RescueTriggerSignal mismatch: %+v", decoded.RescueTriggerSignal)
	}
	if decoded.RescueMinIntervalSec != 300 {
		t.Errorf("RescueMinIntervalSec mismatch: %d", decoded.RescueMinIntervalSec)
	}

	// Триггер без указателя (nil) должен остаться nil после round-trip — это
	// "выключенное" состояние тумблера, критично для rescueTriggersMet.
	original2 := botCfgJSON{BotKind: "hedge"}
	raw2, _ := json.Marshal(original2)
	var decoded2 botCfgJSON
	if err := json.Unmarshal(raw2, &decoded2); err != nil {
		t.Fatalf("unmarshal (empty): %v", err)
	}
	if decoded2.RescueTriggerPriceMovePct != nil || decoded2.RescueTriggerSignal != nil {
		t.Errorf("expected all rescue triggers nil when unset, got: %+v", decoded2)
	}
}
