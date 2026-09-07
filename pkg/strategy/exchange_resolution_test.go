package strategy

import (
	"testing"

	"sis/pkg/trader"
	"sis/pkg/trader/binance"
)

func testCreds() trader.Credentials {
	return trader.Credentials{APIKey: "test-key", SecretKey: "test-secret"}
}

func TestResolveExchange_Bybit_ReturnsBybitExchange(t *testing.T) {
	ts := trader.NewTradeStream(testCreds())
	ex := resolveExchange("bybit", testCreds(), ts)

	if _, ok := ex.(*trader.BybitExchange); !ok {
		t.Fatalf("resolveExchange(bybit, ...) returned %T, want *trader.BybitExchange", ex)
	}
}

func TestResolveExchange_Binance_ReturnsBinanceExchange(t *testing.T) {
	ts := trader.NewTradeStream(testCreds())
	ex := resolveExchange("binance", testCreds(), ts)

	if _, ok := ex.(*binance.BinanceExchange); !ok {
		t.Fatalf("resolveExchange(binance, ...) returned %T, want *binance.BinanceExchange", ex)
	}
}

func TestResolveExchange_UnknownDefaultsToBybit(t *testing.T) {
	ts := trader.NewTradeStream(testCreds())
	ex := resolveExchange("", testCreds(), ts)

	if _, ok := ex.(*trader.BybitExchange); !ok {
		t.Fatalf("resolveExchange(\"\", ...) returned %T, want *trader.BybitExchange (defensive default)", ex)
	}
}

func TestNewAccountRunner_SetsExchangeFromExchangeNameParam(t *testing.T) {
	cancel := func() {}
	ar := newAccountRunner("acct-1", "label", "owner", testCreds(), nil, nil, nil, cancel)
	if _, ok := ar.Exchange().(*trader.BybitExchange); !ok {
		t.Fatalf("newAccountRunner with default exchangeName: Exchange() = %T, want *trader.BybitExchange", ar.Exchange())
	}

	arBinance := newAccountRunnerWithExchange("acct-2", "label", "owner", testCreds(), nil, nil, nil, cancel, "binance")
	if _, ok := arBinance.Exchange().(*binance.BinanceExchange); !ok {
		t.Fatalf("newAccountRunnerWithExchange(..., \"binance\"): Exchange() = %T, want *binance.BinanceExchange", arBinance.Exchange())
	}
}
