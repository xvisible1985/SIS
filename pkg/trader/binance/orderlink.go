package binance

import (
	"fmt"
	"hash/crc32"
)

// maxClientOrderIDLen is Binance's newClientOrderId cap (docs: "1-36 characters").
const maxClientOrderIDLen = 36

// truncateClientOrderID keeps linkID as-is if it already fits Binance's 36-char
// newClientOrderId cap. If not, it keeps as many leading characters as fit alongside a
// short deterministic hash suffix of the FULL original ID, so two different overflowing
// IDs that happen to share the same leading prefix don't collide into an identical,
// Binance-rejected duplicate clientOrderId. The leading prefix ("SIS_STR-{id8}-{kind}-")
// — the part pkg/strategy.ParseStrategyLinkID actually needs for classification — is
// always short enough (≈20 chars) to survive intact even after truncation; only the
// trailing cycle/seq/level numbers (which no parser depends on) are ever affected.
func truncateClientOrderID(linkID string) string {
	if len(linkID) <= maxClientOrderIDLen {
		return linkID
	}
	sum := crc32.ChecksumIEEE([]byte(linkID))
	suffix := fmt.Sprintf("-%08x", sum) // 9 characters
	keep := maxClientOrderIDLen - len(suffix)
	return linkID[:keep] + suffix
}
