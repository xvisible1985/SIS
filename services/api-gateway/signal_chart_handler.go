package main

import (
	"encoding/json"
	"net/http"
	"strconv"

	"sis/pkg/signal"
)

type chartEvent struct {
	Time  int64   `json:"time"`
	State string  `json:"state"`
	Price float64 `json:"price"`
}

// SignalChartHistory fetches klines from Bybit, runs the requested signal
// over each progressive candle slice, and returns state-change events.
//
// Query params:
//
//	signal   — signal id (e.g. "st-flip")
//	symbol   — e.g. "BTCUSDT"
//	interval — e.g. "1h"
//	limit    — number of candles (default 500, max 1000)
//	params   — JSON object of signal-specific params
func (s *Server) SignalChartHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sigName := q.Get("signal")
	symbol := q.Get("symbol")
	interval := q.Get("interval")
	if sigName == "" || symbol == "" || interval == "" {
		writeError(w, http.StatusBadRequest, "signal, symbol, interval required")
		return
	}

	limit := 500
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}

	rawParams := q.Get("params")
	var params map[string]interface{}
	if rawParams != "" {
		if err := json.Unmarshal([]byte(rawParams), &params); err != nil {
			writeError(w, http.StatusBadRequest, "invalid params JSON")
			return
		}
	}

	bybitIv := bybitChartTF(interval)

	candles, err := signal.FetchKlineHistory(symbol, bybitIv, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch klines: "+err.Error())
		return
	}

	cfg := signal.Config{Name: sigName, Params: params}
	sig, err := signal.Build(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var events []chartEvent
	prev := signal.Neutral
	for i := 2; i < len(candles); i++ {
		state := sig.Compute(candles[:i+1])
		if state != prev {
			events = append(events, chartEvent{
				Time:  candles[i].Time,
				State: string(state),
				Price: candles[i].Close,
			})
			prev = state
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

// bybitChartTF converts frontend interval names to Bybit REST API interval codes.
func bybitChartTF(tf string) string {
	m := map[string]string{
		"1m": "1", "3m": "3", "5m": "5", "15m": "15", "30m": "30",
		"1h": "60", "2h": "120", "4h": "240", "6h": "360", "12h": "720",
		"1D": "D", "1W": "W", "1M": "M",
	}
	if v, ok := m[tf]; ok {
		return v
	}
	return tf
}
