package main

import (
	"context"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
	"sis/pkg/strategy"
	"sis/pkg/trader"
)

const (
	depositCollectInterval = 30 * time.Minute
	// depositFlowsMaxLookback bounds the first-ever deposit/withdrawal history fetch.
	depositFlowsMaxLookback = 90 * 24 * time.Hour
)

type dayFlows struct{ deposits, withdrawals float64 }

// bucketFundsFlows groups flows by UTC day: positive amounts are deposits, negative are
// withdrawals (stored as a positive magnitude).
func bucketFundsFlows(flows []trader.FundsFlow) map[time.Time]dayFlows {
	out := map[time.Time]dayFlows{}
	for _, f := range flows {
		t := f.Time.UTC()
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		b := out[day]
		if f.Amount > 0 {
			b.deposits += f.Amount
		} else {
			b.withdrawals += -f.Amount
		}
		out[day] = b
	}
	return out
}

// RunDepositCollector snapshots every active account's equity every 30 minutes into
// account_daily_equity (the latest snapshot of a UTC day overwrites the previous one, so a
// finished day keeps its closing value) and refreshes the deposit/withdrawal history.
func RunDepositCollector(ctx context.Context, pool *pgxpool.Pool, encKey string) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(45 * time.Second):
		}
		collectAllDailyEquity(ctx, pool, encKey)
		t := time.NewTicker(depositCollectInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				collectAllDailyEquity(ctx, pool, encKey)
			}
		}
	}()
}

func collectAllDailyEquity(ctx context.Context, pool *pgxpool.Pool, encKey string) {
	rows, err := pool.Query(ctx,
		`SELECT id, api_key_enc, secret_enc, whitelisted_ips, exchange FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("deposit_collector: load accounts: %v", err)
		return
	}
	type acc struct {
		id, keyEnc, secEnc, exchange string
		ips                          []string
	}
	var accs []acc
	for rows.Next() {
		var a acc
		if rows.Scan(&a.id, &a.keyEnc, &a.secEnc, &a.ips, &a.exchange) == nil {
			accs = append(accs, a)
		}
	}
	rows.Close()
	for _, a := range accs {
		if ctx.Err() != nil {
			return
		}
		apiKey, e1 := crypto.Decrypt(a.keyEnc, encKey)
		secret, e2 := crypto.Decrypt(a.secEnc, encKey)
		if e1 != nil || e2 != nil {
			continue
		}
		creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: a.id, WhitelistedIPs: a.ips}
		ex := strategy.ResolveExchange(a.exchange, creds, trader.NewTradeStream(creds))
		collectAccountDailyEquity(ctx, pool, a.id, ex)
		time.Sleep(time.Second) // spread exchange API calls across accounts
	}
}

func collectAccountDailyEquity(ctx context.Context, pool *pgxpool.Pool, accountID string, ex trader.Exchange) {
	snapshotAccountEquity(ctx, pool, accountID, ex)

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	collectAccountFundsFlows(ctx, pool, accountID, ex, now, today)
}

// snapshotAccountEquity writes the account's current equity (and floating PnL) into today's
// account_daily_equity row. Called by the 30-min collector and, to keep the live day fresh,
// when the dashboard widget is opened.
func snapshotAccountEquity(ctx context.Context, pool *pgxpool.Pool, accountID string, ex trader.Exchange) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	if equity, _, err := ex.GetWalletBalance(ctx); err != nil {
		log.Printf("deposit_collector: account=%s balance: %v", accountID, err)
	} else if equity > 0 {
		upl, uplOK := floatingPnl(ctx, ex)
		var qerr error
		if uplOK {
			// worst = the most negative floating PnL of any snapshot today (≤ 0), with that
			// snapshot's share of its own equity; all SET expressions read the pre-update row.
			worst := math.Min(upl, 0)
			worstPct := worst / equity * 100
			_, qerr = pool.Exec(ctx, `
				INSERT INTO account_daily_equity (account_id, day, equity_open, equity_close, unrealized_close, dd_worst_usd, dd_worst_pct)
				VALUES ($1, $2, $3, $3, $4, $5, $6)
				ON CONFLICT (account_id, day) DO UPDATE SET
					equity_open      = COALESCE(account_daily_equity.equity_open, EXCLUDED.equity_open),
					equity_close     = EXCLUDED.equity_close,
					unrealized_close = EXCLUDED.unrealized_close,
					dd_worst_usd     = LEAST(COALESCE(account_daily_equity.dd_worst_usd, 0), EXCLUDED.dd_worst_usd),
					dd_worst_pct     = CASE WHEN EXCLUDED.dd_worst_usd < COALESCE(account_daily_equity.dd_worst_usd, 0)
					                        THEN EXCLUDED.dd_worst_pct
					                        ELSE COALESCE(account_daily_equity.dd_worst_pct, 0) END,
					updated_at       = NOW()`, accountID, today, equity, upl, worst, worstPct)
		} else {
			// positions unavailable this round: keep the previous floating-PnL figures untouched
			_, qerr = pool.Exec(ctx, `
				INSERT INTO account_daily_equity (account_id, day, equity_open, equity_close)
				VALUES ($1, $2, $3, $3)
				ON CONFLICT (account_id, day) DO UPDATE SET
					equity_open  = COALESCE(account_daily_equity.equity_open, EXCLUDED.equity_open),
					equity_close = EXCLUDED.equity_close,
					updated_at   = NOW()`, accountID, today, equity)
		}
		if qerr != nil {
			log.Printf("deposit_collector: account=%s upsert: %v", accountID, qerr)
		}
	}

}

// collectAccountFundsFlows refreshes the per-day deposit/withdrawal history from the exchange.
func collectAccountFundsFlows(ctx context.Context, pool *pgxpool.Pool, accountID string, ex trader.Exchange, now, today time.Time) {
	ff, ok := ex.(trader.FundsFlowFetcher)
	if !ok {
		return
	}
	// Re-read from the UTC midnight before the last sync so a late-reported flow still lands
	// in its day; the first run reaches back to the earliest recorded day (≤ 90 days).
	var from time.Time
	var syncedAt *time.Time
	pool.QueryRow(ctx, `SELECT synced_at FROM account_funds_sync WHERE account_id=$1`, accountID).Scan(&syncedAt) //nolint:errcheck
	if syncedAt != nil {
		s := syncedAt.UTC().Add(-24 * time.Hour)
		from = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.UTC)
	} else {
		var first *time.Time
		pool.QueryRow(ctx, `SELECT MIN(day)::timestamptz FROM account_daily_equity WHERE account_id=$1`, accountID).Scan(&first) //nolint:errcheck
		from = today.AddDate(0, 0, -1)
		if first != nil {
			f := first.UTC()
			from = time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, time.UTC)
		}
		if min := today.Add(-depositFlowsMaxLookback); from.Before(min) {
			from = min
		}
	}
	flows, err := ff.FetchFundsFlows(ctx, from, now)
	if err != nil {
		log.Printf("deposit_collector: account=%s funds flows: %v", accountID, err)
		return
	}
	buckets := bucketFundsFlows(flows)
	for d := from; !d.After(today); d = d.AddDate(0, 0, 1) {
		if b, has := buckets[d]; has {
			pool.Exec(ctx, `
				INSERT INTO account_daily_equity (account_id, day, deposits_usd, withdrawals_usd)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (account_id, day) DO UPDATE SET
					deposits_usd = EXCLUDED.deposits_usd, withdrawals_usd = EXCLUDED.withdrawals_usd, updated_at = NOW()`,
				accountID, d, b.deposits, b.withdrawals)
		} else {
			pool.Exec(ctx, `UPDATE account_daily_equity SET deposits_usd=0, withdrawals_usd=0
				WHERE account_id=$1 AND day=$2 AND (deposits_usd<>0 OR withdrawals_usd<>0)`, accountID, d) //nolint:errcheck
		}
	}
	pool.Exec(ctx, `INSERT INTO account_funds_sync (account_id, synced_at) VALUES ($1, $2)
		ON CONFLICT (account_id) DO UPDATE SET synced_at = EXCLUDED.synced_at`, accountID, now) //nolint:errcheck
}

// floatingPnl sums the unrealised PnL of every open position. ok=false when the positions
// could not be fetched (the caller then leaves the stored figures untouched).
func floatingPnl(ctx context.Context, ex trader.Exchange) (total float64, ok bool) {
	positions, err := ex.FetchPositions(ctx)
	if err != nil {
		return 0, false
	}
	for _, p := range positions {
		if v, err := strconv.ParseFloat(p.UnrealisedPnl, 64); err == nil {
			total += v
		}
	}
	return total, true
}
