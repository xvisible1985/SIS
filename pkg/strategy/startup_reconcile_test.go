package strategy

import (
	"reflect"
	"sort"
	"testing"
)

// Regression for a live incident (2026-07-17): bot-created matrix strategies stayed
// invisible to every e.runners-based lookup (signal state, safe zone, relative-slots
// preview) indefinitely, because the one-shot Notify() call fired at creation time had
// no retry. reconcileMissingRunners' selection logic (missingIDs) is what finds these —
// pin it directly since the surrounding function needs a live DB/engine to exercise.

func TestMissingIDs_FindsUnregisteredActiveStrategies(t *testing.T) {
	active := []string{"strat-1", "strat-2", "strat-3"}
	registered := map[string]bool{"strat-1": true, "strat-3": true}
	got := missingIDs(active, registered)
	if !reflect.DeepEqual(got, []string{"strat-2"}) {
		t.Fatalf("missingIDs = %v, want [strat-2]", got)
	}
}

func TestMissingIDs_NoneMissing(t *testing.T) {
	active := []string{"strat-1", "strat-2"}
	registered := map[string]bool{"strat-1": true, "strat-2": true}
	if got := missingIDs(active, registered); len(got) != 0 {
		t.Fatalf("missingIDs = %v, want empty", got)
	}
}

func TestMissingIDs_AllMissing(t *testing.T) {
	active := []string{"strat-1", "strat-2"}
	registered := map[string]bool{}
	got := missingIDs(active, registered)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"strat-1", "strat-2"}) {
		t.Fatalf("missingIDs = %v, want [strat-1 strat-2]", got)
	}
}

func TestMissingIDs_EmptyActiveList(t *testing.T) {
	if got := missingIDs(nil, map[string]bool{"strat-1": true}); len(got) != 0 {
		t.Fatalf("missingIDs = %v, want empty", got)
	}
}
