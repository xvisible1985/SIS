package main

import (
	"context"
	"testing"
)

// TestMatrixActivationSignalOK_EmptyAlwaysPasses pins backward compatibility: matrix
// bots created before the activation-signal feature existed (or with the "Активация"
// tab left empty) must keep opening both legs immediately, exactly as before. Only the
// fast-path (len==0, no signalEngine/exchange access) is exercised here — the
// ComputeStateForce path mirrors hedge's existing "Optional activation signal filter"
// (hedge_engine.go), which has no unit coverage in this package either, since it needs a
// live signalEngine; that path is covered by manual/integration testing instead.
func TestMatrixActivationSignalOK_EmptyAlwaysPasses(t *testing.T) {
	s := &Server{}
	cfg := botCfgJSON{}
	if !s.matrixActivationSignalOK("BTCUSDT", cfg) {
		t.Fatal("expected true — empty ActivationSignals must not block matrix bots (backward compatibility)")
	}
}

// TestMatrixBatchCheckActivation_EmptySignalsAllTrueNoNetwork pins the fast path: with no
// activation signals configured, every symbol must resolve true without touching
// s.signalEngine (nil here — would panic if the concurrent path were reached instead).
// The ComputeStateForce-backed path (non-empty signals) has no unit coverage in this
// package, same as matrixActivationSignalOK's — it needs a live signalEngine.
func TestMatrixBatchCheckActivation_EmptySignalsAllTrueNoNetwork(t *testing.T) {
	s := &Server{}
	symbols := []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"}
	got := s.matrixBatchCheckActivation(context.Background(), symbols, botCfgJSON{})
	if len(got) != len(symbols) {
		t.Fatalf("result count = %d, want %d", len(got), len(symbols))
	}
	for _, sym := range symbols {
		if !got[sym] {
			t.Errorf("%s: got false, want true (empty ActivationSignals)", sym)
		}
	}
}
