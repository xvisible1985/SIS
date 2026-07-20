package strategy

import "regexp"

// LinkIDKind classifies what a SIS_STR-prefixed orderLinkId represents, so callers
// (primarily ClosedPnlSyncer) can decide how a closed-pnl event should be attributed
// without guessing from timing.
type LinkIDKind int

const (
	// LinkIDUnrecognized: matched our SIS_STR-{id8}- prefix, but the suffix isn't one of
	// the known close-kind patterns (e.g. a plain level/entry fill's linkId, or a virtual
	// level) — not attributable to a specific close event kind.
	LinkIDUnrecognized LinkIDKind = iota
	// LinkIDMatrixTP: global matrix TP re-arm. The cycle does NOT end — this is the
	// exact case the old time-window heuristics get wrong (a cycle that "looks ended"
	// or "looks zombie" from the outside, but is actually healthy and still trading).
	LinkIDMatrixTP
	// LinkIDMatrixLevelSL: per-level matrix SL fill. Already recorded directly into
	// strategy_levels.realized_pnl by the in-process engine (handleMatrixSLFill) —
	// callers must NOT also write this into trade_history (would duplicate/orphan).
	LinkIDMatrixLevelSL
	// LinkIDGridTP: grid/hedge global TP. A genuine cycle-ending close.
	LinkIDGridTP
	// LinkIDGridSL: grid/hedge global SL. A genuine cycle-ending close.
	LinkIDGridSL
	// LinkIDSelfClose: a generic self-issued market close placed directly by the strategy
	// runner outside the normal TP/SL flow — closePositionAtMarket, closeGhostPosition,
	// the matrix TP re-arm's leftover-tail cleanup, and the post-cycle remnant-dust close
	// in handlePartialPositionChange. These previously placed their market order with NO
	// orderLinkId at all, so ClosedPnlSyncer could never recognize them via step 1b and
	// fell through to the old time-window zombie heuristic (step 3) — which, seeing an
	// unattributed close near an otherwise perfectly healthy open cycle (most commonly the
	// matrix TP tail-close, fired on every re-arm), force-marked the cycle ghost_close even
	// though the strategy was actively continuing. Found live (2026-07-20): a fast-moving
	// matrix pair re-arming every few minutes flapped "цикл оживлён" dozens of times over
	// hours while its TP/SL kept getting cancelled and never reliably re-placed.
	LinkIDSelfClose
)

// ParsedLinkID is the result of successfully parsing one of our own orderLinkId strings.
type ParsedLinkID struct {
	Kind LinkIDKind
	// StrategyID8 is the first 8 hex characters of the owning strategy's UUID — enough
	// to look it up via `strategies.id::text LIKE '{StrategyID8}%'` (callers should also
	// filter by account_id to rule out a theoretical 8-hex-prefix collision).
	StrategyID8 string
}

// Patterns are mutually exclusive by construction: the character(s) immediately after
// "-{id8}-" differ for every kind ("tpl" vs "tp-" vs "msl-" vs "sl-"), so match order
// does not matter for correctness — kept in a fixed, readable order below.
var (
	reMatrixTP      = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-tpl`)
	reMatrixLevelSL = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-msl-`)
	reGridTP        = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-tp-`)
	reGridSL        = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-sl-`)
	reSelfClose     = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-scl-`)
	reOurs          = regexp.MustCompile(`^SIS_STR-([0-9a-f]{8})-`)
)

// ParseStrategyLinkID classifies an orderLinkId that Bybit echoes back on a ClosedPnl
// entry. Returns ok=false when linkID doesn't match our SIS_STR- format at all — e.g. a
// SIS_MPC_/SIS_DTH_ order (bot-engine-placed but without an embedded strategy id) or a
// genuinely external/manual exchange order. Callers must fall back to other attribution
// methods in that case; this function makes no attempt to identify those orders.
func ParseStrategyLinkID(linkID string) (ParsedLinkID, bool) {
	if m := reMatrixTP.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDMatrixTP, StrategyID8: m[1]}, true
	}
	if m := reMatrixLevelSL.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDMatrixLevelSL, StrategyID8: m[1]}, true
	}
	if m := reGridTP.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDGridTP, StrategyID8: m[1]}, true
	}
	if m := reGridSL.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDGridSL, StrategyID8: m[1]}, true
	}
	if m := reSelfClose.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDSelfClose, StrategyID8: m[1]}, true
	}
	if m := reOurs.FindStringSubmatch(linkID); m != nil {
		return ParsedLinkID{Kind: LinkIDUnrecognized, StrategyID8: m[1]}, true
	}
	return ParsedLinkID{}, false
}
