package strategy

import (
	"fmt"
	"testing"
)

func TestParseStrategyLinkID(t *testing.T) {
	cases := []struct {
		name     string
		linkID   string
		wantOK   bool
		wantKind LinkIDKind
		wantID8  string
	}{
		{"matrix TP positive slot", "SIS_STR-286130b1-tpl2-1-83452541", true, LinkIDMatrixTP, "286130b1"},
		{"matrix TP negative slot", "SIS_STR-286130b1-tpln1-1-83452541", true, LinkIDMatrixTP, "286130b1"},
		{"matrix per-level SL positive", "SIS_STR-479024f8-msl-3-7", true, LinkIDMatrixLevelSL, "479024f8"},
		{"matrix per-level SL negative", "SIS_STR-479024f8-msl-n1-7", true, LinkIDMatrixLevelSL, "479024f8"},
		{"grid TP", "SIS_STR-aaa41b1f-tp-1-12345", true, LinkIDGridTP, "aaa41b1f"},
		{"grid SL", "SIS_STR-aaa41b1f-sl-1-12345", true, LinkIDGridSL, "aaa41b1f"},
		{"self-close (ghost/remnant/full-position market close)", "SIS_STR-aaa41b1f-scl-1-12345", true, LinkIDSelfClose, "aaa41b1f"},
		{"level entry fill — recognized prefix, not a close kind", "SIS_STR-d1c35a82-1-3-0", true, LinkIDUnrecognized, "d1c35a82"},
		{"virtual level — recognized prefix, not a close kind", "SIS_STR-d1c35a82-1-3-0-v", true, LinkIDUnrecognized, "d1c35a82"},
		{"paired-close order — no strategy id, not ours in this sense", "SIS_MPC_1_1720000000000", false, LinkIDUnrecognized, ""},
		{"detach close order", "SIS_DTH_1720000000000", false, LinkIDUnrecognized, ""},
		{"empty", "", false, LinkIDUnrecognized, ""},
		{"external/manual exchange order", "abc-123-manual", false, LinkIDUnrecognized, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseStrategyLinkID(c.linkID)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if got.Kind != c.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, c.wantKind)
			}
			if got.StrategyID8 != c.wantID8 {
				t.Errorf("StrategyID8 = %q, want %q", got.StrategyID8, c.wantID8)
			}
		})
	}
}

// TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP: the matrix global TP order's
// linkId must classify as LinkIDGridTP (a genuine cycle-ending close), not LinkIDMatrixTP
// (ClosedPnlSyncer's "never touch ended_at" case) — matrix TP now ends the cycle the same
// way hedge/grid's does (see matrix-cycle-lifecycle-redesign design doc). Pins the format
// matrixUpdateTP must produce: SIS_STR-{id8}-tp-{cycleNum}-{seq}, with no slot suffix
// embedded between "tp" and the first "-", which is what previously triggered the
// (now-incorrect) LinkIDMatrixTP classification.
func TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP(t *testing.T) {
	parsed, ok := ParseStrategyLinkID("SIS_STR-abc12345-tp-3-2")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if parsed.Kind != LinkIDGridTP {
		t.Errorf("Kind = %v, want LinkIDGridTP", parsed.Kind)
	}
	if parsed.StrategyID8 != "abc12345" {
		t.Errorf("StrategyID8 = %q, want abc12345", parsed.StrategyID8)
	}
}

// TestParseStrategyLinkID_MatrixGlobalTP_OldFormatStillMatrixTP: historical linkIds already
// recorded in trade_history/trader_executions before this fix used the "-tpl{N}-" format —
// the PARSER (not the generator) must keep recognizing those as LinkIDMatrixTP so old data
// is still interpreted correctly. Only the generator (matrixUpdateTP) changes; the parser's
// classification rules are unchanged.
func TestParseStrategyLinkID_MatrixGlobalTP_OldFormatStillMatrixTP(t *testing.T) {
	parsed, ok := ParseStrategyLinkID("SIS_STR-abc12345-tpl2-3-2")
	if !ok || parsed.Kind != LinkIDMatrixTP {
		t.Fatalf("expected old -tpl2- format to still classify as LinkIDMatrixTP (unchanged), got kind=%v ok=%v", parsed.Kind, ok)
	}
}

// TestMatrixTPLinkIDFormat_NoSlotSuffix documents and pins the exact linkId shape
// matrixUpdateTP must now produce (see matrix.go's linkID construction inside
// matrixUpdateTP) — plain "SIS_STR-{id8}-tp-{cycleNum}-{seq}", classified as
// LinkIDGridTP by ParseStrategyLinkID.
func TestMatrixTPLinkIDFormat_NoSlotSuffix(t *testing.T) {
	linkID := fmt.Sprintf("SIS_STR-%s-tp-%d-%d", "abc12345", 3, 2)
	if linkID != "SIS_STR-abc12345-tp-3-2" {
		t.Fatalf("linkID = %q, want SIS_STR-abc12345-tp-3-2", linkID)
	}
	parsed, ok := ParseStrategyLinkID(linkID)
	if !ok || parsed.Kind != LinkIDGridTP {
		t.Fatalf("expected LinkIDGridTP, got kind=%v ok=%v", parsed.Kind, ok)
	}
}
