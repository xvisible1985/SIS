package bybitnews

import (
	"reflect"
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
			if !reflect.DeepEqual(got.Markets, tt.want.Markets) {
				t.Errorf("Markets = %v, want %v", got.Markets, tt.want.Markets)
			}
			if got.MaxLeverage != tt.want.MaxLeverage {
				t.Errorf("MaxLeverage = %v, want %v", got.MaxLeverage, tt.want.MaxLeverage)
			}
		})
	}
}
