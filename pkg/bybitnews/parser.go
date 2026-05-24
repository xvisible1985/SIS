package bybitnews

import (
	"regexp"
	"strings"
	"time"
)

// ParsedDetails holds extracted trading information from an announcement.
type ParsedDetails struct {
	Symbols     []string   `json:"symbols"`
	Markets     []string   `json:"markets"`
	MaxLeverage string     `json:"max_leverage,omitempty"`
	LaunchAt    *time.Time `json:"launch_at,omitempty"`
	IsPreMarket bool       `json:"is_pre_market"`
}

var (
	// Trading pair patterns: BTCUSDT, ETHUSDC, 1000PEPEUSDT, etc.
	symbolRegex = regexp.MustCompile(`\b([A-Z0-9]{2,}(?:USDT|USDC|BTC|ETH))\b`)

	// Leverage patterns
	leverageRegex = regexp.MustCompile(`(?i)(?:up\s+to\s+)?(\d+)x(?:\s+leverage)?`)

	// Symbol from "List XXX (XXX)" or "Delist XXX (...)" pattern
	listSymbolRegex = regexp.MustCompile(`(?i)(?:list|delist)\s+([A-Z][A-Z0-9]*)\s*\(`)

	// Standalone ticker symbols (uppercase, 2-10 chars) in title
	standaloneSymbolRegex = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9})\b`)

	// Pre-market patterns
	preMarketRegex = regexp.MustCompile(`(?i)pre[-\s]?market`)

	// Date extraction regexes (tries several common announcement formats)
	dateRegexes = []*regexp.Regexp{
		// 2024-01-15 08:00:00 UTC  or  2024-01-15T08:00:00Z  or  2024-01-15 08:00 UTC
		regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?(?:\s*UTC)?)\b`),
		// Jan 15, 2024, 8:00 AM UTC  or  January 15, 2024, 08:00 UTC
		regexp.MustCompile(`\b((?:January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[,\s]+\d{1,2},?\s+\d{4},?\s+\d{1,2}:\d{2}(?::\d{2})?(?:\s*(?:AM|PM))?\s*UTC)\b`),
		// 15 Jan 2024 08:00 UTC
		regexp.MustCompile(`\b(\d{1,2}\s+(?:January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{4}\s+\d{2}:\d{2}(?::\d{2})?(?:\s*UTC)?)\b`),
	}
)

var commonWords = map[string]struct{}{
	"BYBIT": {}, "WILL": {}, "LIST": {}, "DELIST": {}, "AND": {}, "THE": {},
	"FOR": {}, "ON": {}, "WITH": {}, "UP": {}, "TO": {}, "NOW": {}, "LIVE": {},
	"NEW": {}, "CRYPTO": {}, "SPOT": {}, "DERIVATIVES": {}, "PERPETUAL": {},
	"CONTRACT": {}, "CONTRACTS": {}, "MARKET": {}, "MARKETS": {}, "TRADING": {},
	"UPDATE": {}, "MARGIN": {}, "TIER": {}, "TIERS": {}, "LEVERAGE": {},
	"USDT": {}, "USDC": {}, "BTC": {}, "ETH": {}, "UTC": {}, "AM": {}, "PM": {},
	"FROM": {}, "STARTING": {}, "EFFECTIVE": {}, "AVAILABLE": {},
}

// dateLayouts ordered by likelihood in Bybit announcements.
var dateLayouts = []string{
	"2006-01-02 15:04:05 UTC",
	"2006-01-02 15:04 UTC",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05 UTC",
	"January 2, 2006, 3:04 PM UTC",
	"January 2, 2006, 15:04 UTC",
	"Jan 2, 2006, 3:04 PM UTC",
	"Jan 2, 2006, 15:04 UTC",
	"Jan 2, 2006 15:04 UTC",
	"January 2, 2006 15:04 UTC",
	"2 Jan 2006 15:04:05 UTC",
	"2 Jan 2006 15:04 UTC",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// ParseListingDetails extracts symbols, markets, leverage, launch date and pre-market flag.
func ParseListingDetails(title, description string) ParsedDetails {
	text := title + " " + description
	result := ParsedDetails{}
	seen := make(map[string]struct{})

	// --- Symbols ---
	// Pattern 1: explicit trading pairs like ARKMUSDT, BTCUSDC
	for _, m := range symbolRegex.FindAllString(text, -1) {
		upper := strings.ToUpper(m)
		if _, ok := seen[upper]; !ok {
			seen[upper] = struct{}{}
			result.Symbols = append(result.Symbols, upper)
		}
	}

	// Pattern 2: "List SYMBOL (SYMBOL)"
	if ms := listSymbolRegex.FindStringSubmatch(title); len(ms) > 1 {
		addSymbol(&result, seen, ms[1], text)
	}

	// Pattern 3: standalone uppercase tokens
	for _, m := range standaloneSymbolRegex.FindAllString(title, -1) {
		sym := strings.ToUpper(m)
		if _, isCommon := commonWords[sym]; isCommon {
			continue
		}
		// Skip if already a prefix of an explicit pair (e.g. "XYZ" vs "XYZUSDT")
		alreadyPaired := false
		for s := range seen {
			if strings.HasPrefix(s, sym) && len(s) > len(sym) {
				alreadyPaired = true
				break
			}
		}
		if alreadyPaired {
			continue
		}
		addSymbol(&result, seen, sym, text)
	}

	// --- Markets ---
	lower := strings.ToLower(text)
	marketSet := make(map[string]struct{})
	if strings.Contains(lower, "spot") {
		marketSet["spot"] = struct{}{}
	}
	if strings.Contains(lower, "derivatives") || strings.Contains(lower, "perpetual") || strings.Contains(lower, "futures") {
		marketSet["derivatives"] = struct{}{}
	}
	if strings.Contains(lower, "inverse") {
		marketSet["inverse"] = struct{}{}
	}
	for m := range marketSet {
		result.Markets = append(result.Markets, m)
	}

	// --- Max Leverage ---
	if ms := leverageRegex.FindStringSubmatch(text); len(ms) > 1 {
		result.MaxLeverage = ms[1] + "x"
	}

	// --- Launch Date ---
	if t := extractDate(text); t != nil {
		result.LaunchAt = t
	}

	// --- Pre-Market ---
	result.IsPreMarket = preMarketRegex.MatchString(text)

	return result
}

// addSymbol adds a raw symbol, expanding it to SYMBOLUSDT if USDT is mentioned nearby.
func addSymbol(result *ParsedDetails, seen map[string]struct{}, sym, text string) {
	if sym == "" {
		return
	}
	upper := strings.ToUpper(sym)
	if _, ok := seen[upper]; ok {
		return
	}
	seen[upper] = struct{}{}

	lowerText := strings.ToLower(text)
	if !strings.HasSuffix(upper, "USDT") && !strings.HasSuffix(upper, "USDC") &&
		!strings.HasSuffix(upper, "BTC") && !strings.HasSuffix(upper, "ETH") {
		if strings.Contains(lowerText, "usdt") {
			pair := upper + "USDT"
			if _, ok := seen[pair]; !ok {
				seen[pair] = struct{}{}
				result.Symbols = append(result.Symbols, pair)
				return
			}
		}
		if strings.Contains(lowerText, "usdc") {
			pair := upper + "USDC"
			if _, ok := seen[pair]; !ok {
				seen[pair] = struct{}{}
				result.Symbols = append(result.Symbols, pair)
				return
			}
		}
	}

	result.Symbols = append(result.Symbols, upper)
}

// extractDate tries several regexes and time layouts to find a launch date.
func extractDate(text string) *time.Time {
	for _, re := range dateRegexes {
		for _, m := range re.FindAllString(text, -1) {
			clean := strings.TrimSpace(m)
			// Normalize common variations
			clean = strings.ReplaceAll(clean, "  ", " ")
			clean = strings.ReplaceAll(clean, ",", ", ")
			clean = strings.ReplaceAll(clean, ",  ", ", ")
			for _, layout := range dateLayouts {
				if t, err := time.Parse(layout, clean); err == nil {
					return &t
				}
			}
		}
	}
	return nil
}
