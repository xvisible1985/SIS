package binance

import "testing"

func TestTruncateClientOrderID_UnchangedWhenWithinLimit(t *testing.T) {
	id := "SIS_STR-a1b2c3d4-tp-5-2"
	got := truncateClientOrderID(id)
	if got != id {
		t.Errorf("truncateClientOrderID(%q) = %q, want unchanged (already within 36 chars)", id, got)
	}
}

func TestTruncateClientOrderID_ShortensLongIDsTo36Chars(t *testing.T) {
	id := "SIS_STR-a1b2c3d4-999999-88888888-777-v99"
	if len(id) <= 36 {
		t.Fatalf("test fixture id is %d chars, must be >36 to exercise truncation", len(id))
	}
	got := truncateClientOrderID(id)
	if len(got) != 36 {
		t.Errorf("truncateClientOrderID(%q) len = %d, want exactly 36", id, len(got))
	}
}

func TestTruncateClientOrderID_DeterministicSameInputSameOutput(t *testing.T) {
	id := "SIS_STR-a1b2c3d4-999999-88888888-777-v99"
	got1 := truncateClientOrderID(id)
	got2 := truncateClientOrderID(id)
	if got1 != got2 {
		t.Errorf("truncateClientOrderID must be deterministic: got %q then %q for the same input", got1, got2)
	}
}

func TestTruncateClientOrderID_DifferentOverflowingIDsDontCollide(t *testing.T) {
	idA := "SIS_STR-a1b2c3d4-999999-88888888-AAAA"
	idB := "SIS_STR-a1b2c3d4-999999-88888888-BBBB"
	gotA := truncateClientOrderID(idA)
	gotB := truncateClientOrderID(idB)
	if gotA == gotB {
		t.Errorf("truncateClientOrderID(%q) and truncateClientOrderID(%q) both produced %q — collision", idA, idB, gotA)
	}
}
