package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"sis/pkg/proxy"
)

// ── Response types ─────────────────────────────────────────────────────────

type lvAccount struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	OwnerUsername string `json:"ownerUsername"`
}

type lvStrategy struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Direction    string `json:"direction"`
	StrategyType string `json:"strategyType"`
	Status       string `json:"status"`
}

type lvEvent struct {
	Message string  `json:"message"`
	Level   string  `json:"level"`
	TsMs    float64 `json:"tsMs"`
}

type lvLevel struct {
	LevelIdx    int     `json:"levelIdx"`
	Side        string  `json:"side"`
	FilledPrice float64 `json:"filledPrice"`
	Qty         string  `json:"qty"`
	Status      string  `json:"status"`
	TsMs        float64 `json:"tsMs"`
}

type lvCandle struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V float64 `json:"v"`
}

// ── GET /admin/log-visualizer/accounts ─────────────────────────────────────

func (s *Server) LVGetAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT a.id, a.label, COALESCE(u.username, u.email) AS owner_username
		FROM exchange_accounts a
		JOIN users u ON u.id = a.owner_id
		ORDER BY u.username NULLS LAST, u.email, a.label
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvAccount
	for rows.Next() {
		var a lvAccount
		if err := rows.Scan(&a.ID, &a.Label, &a.OwnerUsername); err == nil {
			out = append(out, a)
		}
	}
	if out == nil {
		out = []lvAccount{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/strategies?account_id= ───────────────────────

func (s *Server) LVGetStrategies(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, symbol, direction, strategy_type, status
		FROM strategies
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvStrategy
	for rows.Next() {
		var s lvStrategy
		if err := rows.Scan(&s.ID, &s.Symbol, &s.Direction, &s.StrategyType, &s.Status); err == nil {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []lvStrategy{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/events?strategy_id=&from=&to= ───────────────

func (s *Server) LVGetEvents(w http.ResponseWriter, r *http.Request) {
	stratID, fromMs, toMs, err := lvParseParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT message, level,
		       EXTRACT(EPOCH FROM created_at) * 1000 AS ts_ms
		FROM strategy_events
		WHERE strategy_id = $1
		  AND created_at >= to_timestamp($2::bigint / 1000.0)
		  AND created_at <  to_timestamp($3::bigint / 1000.0)
		ORDER BY created_at ASC
	`, stratID, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvEvent
	for rows.Next() {
		var e lvEvent
		if err := rows.Scan(&e.Message, &e.Level, &e.TsMs); err == nil {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []lvEvent{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/levels?strategy_id=&from=&to= ───────────────

func (s *Server) LVGetLevels(w http.ResponseWriter, r *http.Request) {
	stratID, fromMs, toMs, err := lvParseParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT level_idx, side,
		       COALESCE(filled_price, 0),
		       qty, status,
		       EXTRACT(EPOCH FROM filled_at) * 1000 AS ts_ms
		FROM strategy_levels
		WHERE strategy_id = $1
		  AND filled_at IS NOT NULL
		  AND filled_at >= to_timestamp($2::bigint / 1000.0)
		  AND filled_at <  to_timestamp($3::bigint / 1000.0)
		ORDER BY filled_at ASC
	`, stratID, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvLevel
	for rows.Next() {
		var l lvLevel
		if err := rows.Scan(&l.LevelIdx, &l.Side, &l.FilledPrice, &l.Qty, &l.Status, &l.TsMs); err == nil {
			out = append(out, l)
		}
	}
	if out == nil {
		out = []lvLevel{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/klines?symbol=&interval=&from=&to= ──────────

func (s *Server) LVGetKlines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	symbol   := q.Get("symbol")
	interval := q.Get("interval")
	fromStr  := q.Get("from")
	toStr    := q.Get("to")

	if symbol == "" || interval == "" || fromStr == "" || toStr == "" {
		writeError(w, http.StatusBadRequest, "symbol, interval, from, to required")
		return
	}
	fromMs, err1 := strconv.ParseInt(fromStr, 10, 64)
	toMs, err2   := strconv.ParseInt(toStr, 10, 64)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "from and to must be unix milliseconds")
		return
	}

	bybitIv := bybitChartTF(interval)
	candles, err := lvFetchBybitKlines(r.Context(), symbol, bybitIv, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch klines: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, candles)
}

// lvFetchBybitKlines fetches OHLCV candles from Bybit for the given ms range.
// Bybit returns max 1000 per request (newest-first), so we paginate backwards.
func lvFetchBybitKlines(ctx context.Context, symbol, interval string, fromMs, toMs int64) ([]lvCandle, error) {
	const bybitURL = "https://api.bybit.com/v5/market/kline"
	var all []lvCandle
	cursor := toMs

	for {
		url := fmt.Sprintf("%s?category=linear&symbol=%s&interval=%s&start=%d&end=%d&limit=1000",
			bybitURL, symbol, interval, fromMs, cursor)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := proxy.HTTPClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			RetCode int `json:"retCode"`
			Result  struct {
				List [][]string `json:"list"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse bybit response: %w", err)
		}
		if result.RetCode != 0 {
			return nil, fmt.Errorf("bybit retCode %d: %s", result.RetCode, string(body))
		}

		list := result.Result.List
		if len(list) == 0 {
			break
		}

		// Parse batch (list is newest-first → reverse to oldest-first)
		batch := make([]lvCandle, 0, len(list))
		for i := len(list) - 1; i >= 0; i-- {
			row := list[i]
			if len(row) < 6 {
				continue
			}
			ts, _  := strconv.ParseInt(row[0], 10, 64)
			batch = append(batch, lvCandle{
				T: ts,
				O: lvParseF(row[1]),
				H: lvParseF(row[2]),
				L: lvParseF(row[3]),
				C: lvParseF(row[4]),
				V: lvParseF(row[5]),
			})
		}

		// Prepend batch (batch is oldest-first, all grows forward in time)
		all = append(batch, all...)

		if len(list) < 1000 {
			break
		}
		// Oldest timestamp in this batch = list[len-1][0] (newest-first list)
		oldestTs, _ := strconv.ParseInt(list[len(list)-1][0], 10, 64)
		if oldestTs <= fromMs {
			break
		}
		cursor = oldestTs - 1
	}

	// Deduplicate and sort ascending by T
	seen := map[int64]bool{}
	deduped := make([]lvCandle, 0, len(all))
	for _, c := range all {
		if !seen[c.T] {
			seen[c.T] = true
			deduped = append(deduped, c)
		}
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].T < deduped[j].T })

	return deduped, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func lvParseParams(r *http.Request) (stratID string, fromMs, toMs int64, err error) {
	q := r.URL.Query()
	stratID = q.Get("strategy_id")
	if stratID == "" {
		err = fmt.Errorf("strategy_id required")
		return
	}
	fromMs, err = strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil {
		err = fmt.Errorf("from must be unix milliseconds")
		return
	}
	toMs, err = strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil {
		err = fmt.Errorf("to must be unix milliseconds")
		return
	}
	return
}

func lvParseF(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// Ensure time import is used
var _ = time.Now
