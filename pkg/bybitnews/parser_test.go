package bybitnews

import (
	"reflect"
	"sort"
	"testing"
)

func TestParseListingDetails(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		want        ParsedDetails
	}{
		{
			name:        "spot + derivatives with leverage",
			title:       "Bybit Will List ARKM (ARKM) on Spot and USDT Perpetual Contracts with up to 50x Leverage",
			description: "",
			want: ParsedDetails{
				Symbols:     []string{"ARKMUSDT"},
				Markets:     []string{"spot", "derivatives"},
				MaxLeverage: "50x",
			},
		},
		{
			name:        "spot only",
			title:       "Bybit Will List WLD (WLD) on Spot Market",
			description: "",
			want: ParsedDetails{
				Symbols: []string{"WLD"},
				Markets: []string{"spot"},
			},
		},
		{
			name:        "delisting",
			title:       "Bybit Will Delist BNXUSDT and BNXUSDC Perpetual Contracts",
			description: "",
			want: ParsedDetails{
				Symbols: []string{"BNXUSDT", "BNXUSDC"},
				Markets: []string{"derivatives"},
			},
		},
		{
			name:        "XAU perpetual",
			title:       "XAUUSDT Perpetual Contract Now Live",
			description: "",
			want: ParsedDetails{
				Symbols: []string{"XAUUSDT"},
				Markets: []string{"derivatives"},
			},
		},
		{
			name:        "multiple pairs in description",
			title:       "Bybit Will List XYZ on Spot",
			description: "Trading pairs: XYZUSDT, XYZUSDC will be available with up to 25x leverage.",
			want: ParsedDetails{
				Symbols:     []string{"XYZUSDT", "XYZUSDC"},
				Markets:     []string{"spot"},
				MaxLeverage: "25x",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseListingDetails(tt.title, tt.description)
			if !reflect.DeepEqual(got.Symbols, tt.want.Symbols) {
				t.Errorf("Symbols = %v, want %v", got.Symbols, tt.want.Symbols)
			}
			// Markets order is non-deterministic (map iteration); sort before comparing.
			sortedGot := append([]string{}, got.Markets...)
			sortedWant := append([]string{}, tt.want.Markets...)
			sort.Strings(sortedGot)
			sort.Strings(sortedWant)
			if !reflect.DeepEqual(sortedGot, sortedWant) {
				t.Errorf("Markets = %v, want %v", got.Markets, tt.want.Markets)
			}
			if got.MaxLeverage != tt.want.MaxLeverage {
				t.Errorf("MaxLeverage = %v, want %v", got.MaxLeverage, tt.want.MaxLeverage)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		key        string
		wantList   bool
		wantDelist bool
	}{
		{"new_crypto", true, false},
		{"new_spot_listing", true, false},
		{"spot_listing", true, false},
		{"new_fiat_listings", true, false},
		{"new_inverse_contract", true, false},
		{"new_usdc_contract", true, false},
		{"new_unknown_listing_type", true, false},  // new_* + "list" → listing
		{"delisting", false, true},
		{"spot_listings", false, false},  // category label, NOT a listing action
		{"trading_update", false, false},
		{"market_update", false, false},
		{"activities", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			gotList, gotDelist := classify(tt.key)
			if gotList != tt.wantList {
				t.Errorf("classify(%q) isListing = %v, want %v", tt.key, gotList, tt.wantList)
			}
			if gotDelist != tt.wantDelist {
				t.Errorf("classify(%q) isDelisting = %v, want %v", tt.key, gotDelist, tt.wantDelist)
			}
		})
	}
}

func TestHasListingLanguage(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		want        bool
	}{
		{
			name:  "will list",
			title: "Bybit Will List HYPE (HYPE) on Spot Market",
			want:  true,
		},
		{
			name:  "spot trading now open",
			title: "HYPEUSDT Spot Trading Is Now Available on Bybit",
			want:  true,
		},
		{
			name:  "perpetual contract",
			title: "XAUUSDT Perpetual Contract Now Live",
			want:  true,
		},
		{
			name:  "promo event - HYPE Token Splash (the false-positive case)",
			title: "HYPE Token Splash— Grab a share of the 100,000 USDT prize pool",
			description: "Join our HYPE Token Splash event to share from 100,000 USDT.",
			want:  false,
		},
		{
			name:  "trading competition",
			title: "BTC Price Prediction Competition — Win 50,000 USDT",
			want:  false,
		},
		{
			name:  "holders event",
			title: "SOL Holders Special Bonus — Exclusive Rewards",
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasListingLanguage(tt.title, tt.description)
			if got != tt.want {
				t.Errorf("hasListingLanguage(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}
