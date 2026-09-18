package main

import (
	"net/http"
	"time"
)

type timesfmPredictionRow struct {
	ID                 string    `json:"id"`
	Symbol             string    `json:"symbol"`
	Timeframe          string    `json:"timeframe"`
	PredictedAt        time.Time `json:"predicted_at"`
	PriceAtPredict     float64   `json:"price_at_predict"`
	PredictedPct       float64   `json:"predicted_pct"`
	PredictedDirection string    `json:"predicted_direction"`
	TargetAt           time.Time `json:"target_at"`
	ActualPrice        *float64  `json:"actual_price"`
	ActualDirection    *string   `json:"actual_direction"`
	Correct            *bool     `json:"correct"`
}

type timesfmPredictionsResponse struct {
	Predictions []timesfmPredictionRow `json:"predictions"`
	Total       int                    `json:"total"`
	Checked     int                    `json:"checked"`
	CorrectN    int                    `json:"correct"`
	WinRate     float64                `json:"win_rate"`
}

// GetTimesfmPredictions returns the most recent timesfm predictions for a symbol, plus an
// aggregate win-rate over the checked (outcome already known) subset.
// GET /signals/timesfm/predictions?symbol=BTCUSDT
func (s *Server) GetTimesfmPredictions(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}
	const limit = 50

	rows, err := s.pool.Query(r.Context(), `
		SELECT id, symbol, timeframe, predicted_at, price_at_predict, predicted_pct,
		       predicted_direction, target_at, actual_price, actual_direction, correct
		FROM timesfm_predictions
		WHERE symbol=$1
		ORDER BY predicted_at DESC
		LIMIT $2`,
		symbol, limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	var out timesfmPredictionsResponse
	out.Predictions = make([]timesfmPredictionRow, 0)
	for rows.Next() {
		var p timesfmPredictionRow
		if err := rows.Scan(&p.ID, &p.Symbol, &p.Timeframe, &p.PredictedAt, &p.PriceAtPredict,
			&p.PredictedPct, &p.PredictedDirection, &p.TargetAt, &p.ActualPrice, &p.ActualDirection, &p.Correct); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		out.Predictions = append(out.Predictions, p)
		out.Total++
		if p.Correct != nil {
			out.Checked++
			if *p.Correct {
				out.CorrectN++
			}
		}
	}
	if out.Checked > 0 {
		out.WinRate = float64(out.CorrectN) / float64(out.Checked) * 100
	}
	writeJSON(w, http.StatusOK, out)
}
