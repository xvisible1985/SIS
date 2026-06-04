package strategy

import (
	"encoding/json"
	"testing"
)

func TestAdoptPositionData_RoundTrip(t *testing.T) {
	apd := AdoptPositionData{Size: "35", EntryPrice: "0.4647"}
	b, err := json.Marshal(apd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got AdoptPositionData
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Size != apd.Size || got.EntryPrice != apd.EntryPrice {
		t.Fatalf("got %+v, want %+v", got, apd)
	}
}

func TestStrategy_AdoptPositionDataField(t *testing.T) {
	s := Strategy{}
	if s.AdoptPositionData != nil {
		t.Fatal("should be nil by default")
	}
	s.AdoptPositionData = &AdoptPositionData{Size: "10", EntryPrice: "1.23"}
	if s.AdoptPositionData.Size != "10" {
		t.Fatal("field not set")
	}
}
