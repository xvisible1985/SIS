package main

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type depositDay struct {
	Day         string  `json:"day"` // YYYY-MM-DD, UTC
	EquityClose float64 `json:"equity_close"`
	EquityOpen  float64 `json:"equity_open"` // previous recorded close (or the day's own open)
	// ChangeUSD is the raw equity change (the difference between the deposits); ResultUSD is the
	// day's real performance — the same change with deposits/withdrawals taken out:
	// ResultUSD = Realized + Fees + Funding + Positions, ChangeUSD = ResultUSD + Deposits − Withdrawals.
	// ResultPct is the return of ResultUSD on (EquityOpen + Deposits).
	ChangeUSD float64 `json:"change_usd"`
	ResultUSD float64 `json:"result_usd"`
	ResultPct float64 `json:"result_pct"`
	// Components of ResultUSD (all signed, so they add up to it exactly):
	RealizedUSD  float64 `json:"realized_usd"`  // gross PnL of trades closed that day
	FeesUSD      float64 `json:"fees_usd"`      // ≤ 0
	FundingUSD   float64 `json:"funding_usd"`   // signed: + received, − paid
	PositionsUSD float64 `json:"positions_usd"` // change in floating PnL of open positions (+ small exchange adjustments)
	// External funds movement (not part of the result):
	DepositsUSD    float64 `json:"deposits_usd"`
	WithdrawalsUSD float64 `json:"withdrawals_usd"` // positive magnitude
	Trades         int     `json:"trades"`
	Live           bool    `json:"live"` // today: the close is still moving
	// Floating PnL of open positions (EquityClose already includes it — it is the "dirty"
	// deposit). Nil for days recorded before the collector captured it.
	UnrealizedUSD *float64 `json:"unrealized_usd"` // at the day's last snapshot; negative = drawdown
	UnrealizedPct *float64 `json:"unrealized_pct"` // share of EquityClose, percent
	CleanClose    *float64 `json:"clean_close"`    // EquityClose without floating PnL
	Estimated     bool     `json:"estimated"`      // clean close restored from trades/transfers, not a real snapshot
	DDWorstUSD    *float64 `json:"dd_worst_usd"`   // worst floating PnL of any snapshot that day (≤ 0)
	DDWorstPct    *float64 `json:"dd_worst_pct"`   // …as a percent of that snapshot's equity (≤ 0)
}

type depositEquityRow struct {
	Day         time.Time
	Open, Close *float64
	Deposits    float64
	Withdrawals float64

	UnrealizedClose, DDWorstUSD, DDWorstPct *float64
}

// dayTrading is one UTC day of closed-trade results from trade_history.
type dayTrading struct {
	Pnl, Fees, Funding float64 // as stored: net = Pnl − Fees − Funding (Funding > 0 means paid)
	Trades             int
}

// buildDepositDays turns stored daily rows + closed-trade results per day into the widget's
// per-day breakdown. A day's change is measured from the previous recorded close (or the
// first day's own open); trades/flows of days without an equity snapshot are folded into the
// next day that has one. Positions is the remainder after realized results and fund flows,
// so the components always add up to the result exactly.
func buildDepositDays(rows []depositEquityRow, trading map[time.Time]dayTrading, today time.Time) []depositDay {
	return buildDepositDaysFor(rows, trading, today, false)
}

// buildDepositDaysFor measures the deposit WITHOUT floating PnL: a day's close is
// equity − unrealized where the snapshot carries it. Bybit's closedPnl is already net of fees,
// so for bybit=true (a) fees are not subtracted a second time in the breakdown and (b) the
// clean balance of earlier days (recorded before floating PnL was captured) is restored
// backwards from the nearest real clean close: clean(prev) = clean − pnl − deposits + withdrawals.
// Other exchanges keep their raw-equity history until real clean snapshots start (only the
// first clean day is bridged, over its own interval). Restored days are flagged Estimated.
func buildDepositDaysFor(rows []depositEquityRow, trading map[time.Time]dayTrading, today time.Time, bybit bool) []depositDay {
	if len(rows) == 0 {
		return []depositDay{}
	}
	byDay := make(map[time.Time]depositEquityRow, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}
	// pnlOf: stored pnl of days in (from, to]; net of what Bybit already includes.
	pnlBetween := func(from, to time.Time) float64 {
		var s float64
		for d := from.AddDate(0, 0, 1); !d.After(to); d = d.AddDate(0, 0, 1) {
			t := trading[d]
			if bybit {
				s += t.Pnl - t.Funding
			} else {
				s += t.Pnl - t.Fees - t.Funding
			}
		}
		return s
	}
	clean := make([]*float64, len(rows))
	estimated := make([]bool, len(rows))
	for i, r := range rows {
		if r.Close != nil && r.UnrealizedClose != nil {
			c := *r.Close - *r.UnrealizedClose
			clean[i] = &c
		}
	}
	if bybit {
		for i := len(rows) - 2; i >= 0; i-- {
			if clean[i] != nil || rows[i].Close == nil || clean[i+1] == nil {
				continue
			}
			c := *clean[i+1] - pnlBetween(rows[i].Day, rows[i+1].Day) - rows[i+1].Deposits + rows[i+1].Withdrawals
			clean[i] = &c
			estimated[i] = true
		}
	}
	idx := make(map[time.Time]int, len(rows))
	for i, r := range rows {
		idx[r.Day] = i
	}
	out := []depositDay{}
	var prevClose *float64
	prevClean := false
	var acc dayTrading
	var accDep, accWd float64
	for d := rows[0].Day; !d.After(today); d = d.AddDate(0, 0, 1) {
		t := trading[d]
		acc.Pnl += t.Pnl
		acc.Fees += t.Fees
		acc.Funding += t.Funding
		acc.Trades += t.Trades
		r, has := byDay[d]
		if !has {
			continue
		}
		accDep += r.Deposits
		accWd += r.Withdrawals
		if r.Close == nil {
			continue
		}
		i := idx[d]
		close := r.Close
		isClean := clean[i] != nil
		if isClean {
			close = clean[i]
		}
		realizedNet := acc.Pnl - acc.Fees - acc.Funding
		if bybit {
			realizedNet = acc.Pnl - acc.Funding
		}
		base := prevClose
		if base != nil && isClean && !prevClean {
			// previous close is on another basis (with floating PnL): bridge this interval
			// from the realized results instead of mixing the two.
			b := *close - realizedNet - accDep + accWd
			base = &b
		}
		if base == nil {
			base = r.Open
		}
		if base == nil {
			base = close
		}
		change := *close - *base
		result := change - accDep + accWd
		// Return on the capital that was actually at work: opening equity plus what was
		// deposited meanwhile (a tiny opening balance would otherwise blow the percent up).
		var pct float64
		if capital := *base + accDep; capital > 0 {
			pct = result / capital * 100
		}
		feesShown, realizedShown := acc.Fees, acc.Pnl
		if bybit {
			realizedShown = acc.Pnl + acc.Fees // gross: closedPnl already had the fees taken out
		}
		day := depositDay{
			Day:            d.Format("2006-01-02"),
			EquityClose:    *close,
			EquityOpen:     *base,
			ChangeUSD:      change,
			ResultUSD:      result,
			ResultPct:      pct,
			RealizedUSD:    realizedShown,
			FeesUSD:        -feesShown,
			FundingUSD:     -acc.Funding,
			PositionsUSD:   result - realizedNet,
			DepositsUSD:    accDep,
			WithdrawalsUSD: accWd,
			Trades:         acc.Trades,
			Live:           d.Equal(today),
			DDWorstUSD:     r.DDWorstUSD,
			DDWorstPct:     r.DDWorstPct,
		}
		if isClean {
			cc := *close
			day.CleanClose = &cc
			day.Estimated = estimated[i]
		}
		if r.UnrealizedClose != nil {
			u := *r.UnrealizedClose
			day.UnrealizedUSD = &u
			if *r.Close != 0 {
				pc := u / *r.Close * 100
				day.UnrealizedPct = &pc
			}
		}
		out = append(out, day)
		c := *close
		prevClose = &c
		prevClean = isClean
		acc = dayTrading{}
		accDep, accWd = 0, 0
	}
	return out
}

func loadDepositRows(ctx context.Context, s *Server, accountID, userID string) ([]depositEquityRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.day, e.equity_open, e.equity_close, e.deposits_usd, e.withdrawals_usd,
		       e.unrealized_close, e.dd_worst_usd, e.dd_worst_pct
		FROM account_daily_equity e
		JOIN exchange_accounts ea ON ea.id = e.account_id
		WHERE e.account_id = $1 AND ea.owner_id = $2
		ORDER BY e.day ASC`, accountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []depositEquityRow
	for rows.Next() {
		var r depositEquityRow
		if err := rows.Scan(&r.Day, &r.Open, &r.Close, &r.Deposits, &r.Withdrawals, &r.UnrealizedClose, &r.DDWorstUSD, &r.DDWorstPct); err != nil {
			return nil, err
		}
		r.Day = r.Day.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetDashboardDeposit returns the per-UTC-day deposit (equity) history of ONE account with
// the change split into trading / deposits / withdrawals / other.
// GET /dashboard/deposit?account_id=&period=1d|7d|30d|90d|1y|all
func (s *Server) GetDashboardDeposit(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"days": []depositDay{}})
		return
	}
	ctx := r.Context()
	s.refreshLiveDepositSnapshot(r, accountID, userID)
	rows, err := loadDepositRows(ctx, s, accountID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "deposit history unavailable")
		return
	}
	trading := map[time.Time]dayTrading{}
	if len(rows) > 0 {
		tr, err := s.pool.Query(ctx, `
			SELECT (th.closed_at AT TIME ZONE 'UTC')::date AS d,
			       COALESCE(SUM(th.pnl),0), COALESCE(SUM(th.fees),0), COALESCE(SUM(th.funding),0), COUNT(*)
			FROM trade_history th
			WHERE th.owner_id = $1 AND th.account_id = $2 AND th.closed_at >= $3
			GROUP BY d`, userID, accountID, rows[0].Day)
		if err == nil {
			defer tr.Close()
			for tr.Next() {
				var d time.Time
				var t dayTrading
				if tr.Scan(&d, &t.Pnl, &t.Fees, &t.Funding, &t.Trades) == nil {
					trading[d.UTC()] = t
				}
			}
		}
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var exchange string
	s.pool.QueryRow(ctx, `SELECT exchange FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		accountID, userID).Scan(&exchange) //nolint:errcheck
	days := buildDepositDaysFor(rows, trading, today, strings.EqualFold(exchange, "bybit"))

	if since := dashboardPeriodSince(r.URL.Query().Get("period"), now); since != nil {
		cut := since.UTC().Format("2006-01-02")
		kept := days[:0]
		for _, d := range days {
			if d.Day >= cut {
				kept = append(kept, d)
			}
		}
		days = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}

// depositSnapshotMaxAge is how stale today's stored snapshot may be before opening the widget
// re-reads the live balance (the collector alone runs every 30 minutes).
const depositSnapshotMaxAge = 60 * time.Second

// refreshLiveDepositSnapshot brings today's row up to date with the exchange so the live day
// shows the current deposit, not the one from up to 30 minutes ago. Best effort: on any
// failure or timeout the stored snapshot is served as is. Ownership is enforced by
// loadExchange (account must belong to the caller).
func (s *Server) refreshLiveDepositSnapshot(r *http.Request, accountID, userID string) {
	var updated *time.Time
	s.pool.QueryRow(r.Context(), `
		SELECT e.updated_at FROM account_daily_equity e
		JOIN exchange_accounts ea ON ea.id = e.account_id
		WHERE e.account_id=$1 AND ea.owner_id=$2 AND e.day = (NOW() AT TIME ZONE 'UTC')::date`,
		accountID, userID).Scan(&updated) //nolint:errcheck
	if updated != nil && time.Since(*updated) < depositSnapshotMaxAge {
		return
	}
	ex, err := s.loadExchange(r, accountID, userID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	snapshotAccountEquity(ctx, s.pool, accountID, ex)
}
