package main

import (
	"math"
	"testing"
	"time"

	"sis/pkg/trader"
)

func utcDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
func fp(v float64) *float64                       { return &v }

func TestBuildDepositDays_ComponentsAddUpToResult_FlowsExcluded(t *testing.T) {
	d1, d2, d3 := utcDay(2026, 10, 24), utcDay(2026, 10, 25), utcDay(2026, 10, 26)
	rows := []depositEquityRow{
		{Day: d1, Open: fp(1000), Close: fp(1010)},
		{Day: d2, Open: fp(1010), Close: fp(1160), Deposits: 100},
		{Day: d3, Open: fp(1160), Close: fp(1140), Withdrawals: 50},
	}
	trading := map[time.Time]dayTrading{
		d2: {Pnl: 34, Fees: 3, Funding: 1, Trades: 4}, // net +30
		d3: {Pnl: 22, Fees: 2, Funding: 0, Trades: 2}, // net +20
	}
	got := buildDepositDays(rows, trading, d3)
	if len(got) != 3 {
		t.Fatalf("days=%d want 3", len(got))
	}
	// day 2: equity +150, of which 100 is a deposit → result 50 = 30 realized-net + 20 positions
	g := got[1]
	if g.ChangeUSD != 150 || g.ResultUSD != 50 || g.DepositsUSD != 100 {
		t.Errorf("day2 = %+v", g)
	}
	if g.RealizedUSD != 34 || g.FeesUSD != -3 || g.FundingUSD != -1 || math.Abs(g.PositionsUSD-20) > 1e-9 || g.Trades != 4 {
		t.Errorf("day2 components = %+v", g)
	}
	if sum := g.RealizedUSD + g.FeesUSD + g.FundingUSD + g.PositionsUSD; math.Abs(sum-g.ResultUSD) > 1e-9 {
		t.Errorf("components sum %v != result %v", sum, g.ResultUSD)
	}
	// day 3: equity −20, a 50 withdrawal → result +30 = 20 net + 10 positions
	g = got[2]
	if g.ResultUSD != 30 || g.WithdrawalsUSD != 50 || math.Abs(g.PositionsUSD-10) > 1e-9 || !g.Live || g.EquityOpen != 1160 {
		t.Errorf("day3 = %+v", g)
	}
	if got[0].ResultUSD != 10 { // first day measured from its own open
		t.Errorf("day1 result = %v want 10", got[0].ResultUSD)
	}
}

func TestBuildDepositDays_GapDaysAreFoldedIntoNextRecordedDay(t *testing.T) {
	d1, d3 := utcDay(2026, 10, 24), utcDay(2026, 10, 26)
	rows := []depositEquityRow{
		{Day: d1, Close: fp(1000), Open: fp(1000)},
		{Day: d3, Close: fp(1100), Open: fp(1090)},
	}
	got := buildDepositDays(rows, map[time.Time]dayTrading{
		utcDay(2026, 10, 25): {Pnl: 40, Trades: 1},
		d3:                   {Pnl: 10, Trades: 1},
	}, d3)
	if len(got) != 2 || got[1].ResultUSD != 100 || got[1].RealizedUSD != 50 || got[1].Trades != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestBucketFundsFlows_SplitsBySignAndUTCDay(t *testing.T) {
	flows := []trader.FundsFlow{
		{Time: time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC), Amount: 100},
		{Time: time.Date(2026, 10, 26, 0, 10, 0, 0, time.UTC), Amount: -40},
		{Time: time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC), Amount: 10},
	}
	b := bucketFundsFlows(flows)
	if b[utcDay(2026, 10, 25)].deposits != 100 {
		t.Errorf("25th: %+v", b[utcDay(2026, 10, 25)])
	}
	if x := b[utcDay(2026, 10, 26)]; x.deposits != 10 || x.withdrawals != 40 {
		t.Errorf("26th: %+v", x)
	}
}

func TestBuildDepositDays_FloatingPnlAndDrawdown(t *testing.T) {
	d1, d2 := utcDay(2026, 10, 25), utcDay(2026, 10, 26)
	rows := []depositEquityRow{
		{Day: d1, Open: fp(1000), Close: fp(1000)}, // recorded before drawdown tracking
		{Day: d2, Open: fp(1000), Close: fp(950), UnrealizedClose: fp(-50), DDWorstUSD: fp(-80), DDWorstPct: fp(-8)},
	}
	got := buildDepositDays(rows, nil, d2)
	if got[0].UnrealizedUSD != nil || got[0].CleanClose != nil || got[0].DDWorstUSD != nil {
		t.Errorf("legacy day must carry no drawdown figures: %+v", got[0])
	}
	g := got[1]
	if g.UnrealizedUSD == nil || *g.UnrealizedUSD != -50 || *g.CleanClose != 1000 || math.Abs(*g.UnrealizedPct+50.0/950*100) > 1e-9 {
		t.Errorf("floating figures wrong: %+v", g)
	}
	if g.DDWorstUSD == nil || *g.DDWorstUSD != -80 || *g.DDWorstPct != -8 {
		t.Errorf("worst-of-day wrong: %+v", g)
	}
}

func TestBuildDepositDaysFor_BybitRestoresCleanBalanceBackwards(t *testing.T) {
	d1, d2, d3 := utcDay(2026, 10, 25), utcDay(2026, 10, 26), utcDay(2026, 10, 27)
	rows := []depositEquityRow{
		{Day: d1, Open: fp(1000), Close: fp(900)},
		{Day: d2, Open: fp(900), Close: fp(1100), Deposits: 100},
		{Day: d3, Open: fp(1100), Close: fp(1160), UnrealizedClose: fp(-120)}, // clean 1280
	}
	trading := map[time.Time]dayTrading{
		d2: {Pnl: 50, Fees: 5, Trades: 2}, // closedPnl 50 is already net of the 5 fees
		d3: {Pnl: 30, Fees: 2, Trades: 1},
	}
	got := buildDepositDaysFor(rows, trading, d3, true)
	// clean(d3)=1280 → clean(d2)=1280−30=1250 → clean(d1)=1250−50−100=1100
	if *got[2].CleanClose != 1280 || *got[1].CleanClose != 1250 || *got[0].CleanClose != 1100 {
		t.Fatalf("clean = %v %v %v", *got[0].CleanClose, *got[1].CleanClose, *got[2].CleanClose)
	}
	if !got[0].Estimated || !got[1].Estimated || got[2].Estimated {
		t.Errorf("estimated flags wrong: %v %v %v", got[0].Estimated, got[1].Estimated, got[2].Estimated)
	}
	// d2: clean 1100→1250, 100 deposited → result 50 = pnl (gross 55 − fees 5), floating PnL excluded
	g := got[1]
	if g.EquityOpen != 1100 || g.ResultUSD != 50 || g.RealizedUSD != 55 || g.FeesUSD != -5 || math.Abs(g.PositionsUSD) > 1e-9 {
		t.Errorf("d2 = %+v", g)
	}
}

func TestBuildDepositDaysFor_OtherExchangeKeepsEquityUntilCleanSnapshots(t *testing.T) {
	d1, d2 := utcDay(2026, 10, 26), utcDay(2026, 10, 27)
	rows := []depositEquityRow{
		{Day: d1, Open: fp(1000), Close: fp(1050)},
		{Day: d2, Open: fp(1050), Close: fp(1000), UnrealizedClose: fp(-80)}, // clean 1080
	}
	got := buildDepositDaysFor(rows, map[time.Time]dayTrading{d2: {Pnl: 40, Fees: 10}}, d2, false)
	if got[0].CleanClose != nil || got[0].Estimated || got[0].EquityClose != 1050 {
		t.Errorf("history must stay raw: %+v", got[0])
	}
	// first clean day is bridged over its own interval: result = pnl − fees = 30, not mixed bases
	if g := got[1]; g.EquityClose != 1080 || g.EquityOpen != 1050 || g.ResultUSD != 30 || g.Estimated {
		t.Errorf("d2 = %+v", g)
	}
}

func TestBuildDepositDays_ReturnPctUsesCapitalNotTinyOpeningBalance(t *testing.T) {
	d1 := utcDay(2026, 10, 18)
	// account opened the day with $0.04 and received a $1175.83 deposit; trading lost $17.85
	rows := []depositEquityRow{{Day: d1, Open: fp(0.04), Close: fp(1158.03), Deposits: 1175.83}}
	g := buildDepositDays(rows, nil, d1)[0]
	if math.Abs(g.ChangeUSD-1157.99) > 1e-6 || math.Abs(g.ResultUSD+17.84) > 1e-6 {
		t.Fatalf("change/result = %v / %v", g.ChangeUSD, g.ResultUSD)
	}
	if math.Abs(g.ResultPct) > 5 { // ≈ −1.5%, never tens of thousands of percent
		t.Errorf("ResultPct = %v, want a small return on the deposited capital", g.ResultPct)
	}
}
