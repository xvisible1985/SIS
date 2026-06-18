package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"sis/pkg/auth"
)

// DebugEventsStream streams aggregated strategy + bot events for the current user.
// GET /ws/debug-events?token=<jwt>&since=<rfc3339nano>
// On first connect (no since): sends last 100 events from the past 6 hours.
// On reconnect: sends only new events since given timestamp.
func (s *Server) DebugEventsStream(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	sinceStr := r.URL.Query().Get("since")
	firstConnect := sinceStr == ""
	var since time.Time
	if !firstConnect {
		if t, err2 := time.Parse(time.RFC3339Nano, sinceStr); err2 == nil {
			since = t
		} else {
			firstConnect = true
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws debug events: upgrade: %v", err)
		return
	}
	defer conn.Close()

	type debugEvent struct {
		Source    string    `json:"source"`
		SourceID  string    `json:"source_id"`
		Symbol    string    `json:"symbol"`
		Direction string    `json:"direction"`
		BotName   string    `json:"bot_name"`
		Category  string    `json:"category"`
		Message   string    `json:"message"`
		Level     string    `json:"level"`
		CreatedAt time.Time `json:"created_at"`
	}

	scan := func(rows pgx.Rows) []debugEvent {
		var events []debugEvent
		for rows.Next() {
			var e debugEvent
			if rows.Scan(&e.Source, &e.SourceID, &e.Symbol, &e.Direction,
				&e.BotName, &e.Category, &e.Message, &e.Level, &e.CreatedAt) == nil {
				events = append(events, e)
			}
		}
		rows.Close()
		return events
	}

	send := func(events []debugEvent) bool {
		data, _ := json.Marshal(events)
		return conn.WriteMessage(websocket.TextMessage, data) == nil
	}

	// On first connect: load last 100 events from past 6 hours (DESC), reverse to ASC.
	if firstConnect {
		const q = `
			SELECT source, source_id, symbol, direction, bot_name, category, message, level, created_at
			FROM (
				SELECT
					'strategy'::text            AS source,
					LEFT(se.strategy_id::text, 8) AS source_id,
					st.symbol,
					st.direction,
					COALESCE(b.name, '')        AS bot_name,
					''::text                    AS category,
					se.message,
					se.level,
					se.created_at
				FROM strategy_events se
				JOIN strategies st ON st.id = se.strategy_id
				LEFT JOIN bots b ON b.id = st.bot_id
				WHERE st.owner_id = $1
				  AND se.created_at > NOW() - INTERVAL '6 hours'
				UNION ALL
				SELECT
					'bot'::text,
					LEFT(be.bot_id::text, 8),
					''::text,
					''::text,
					b.name,
					be.category,
					be.message,
					be.level,
					be.created_at
				FROM bot_events be
				JOIN bots b ON b.id = be.bot_id
				WHERE b.owner_id = $1
				  AND be.created_at > NOW() - INTERVAL '6 hours'
			) t
			ORDER BY created_at DESC LIMIT 100`

		rows, qErr := s.pool.Query(r.Context(), q, userID)
		since = time.Now()
		if qErr == nil {
			events := scan(rows)
			if len(events) > 0 {
				// reverse: DESC→ASC
				for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
					events[i], events[j] = events[j], events[i]
				}
				since = events[len(events)-1].CreatedAt
				if !send(events) {
					return
				}
			}
		}
	}

	const pollQ = `
		SELECT source, source_id, symbol, direction, bot_name, category, message, level, created_at
		FROM (
			SELECT
				'strategy'::text              AS source,
				LEFT(se.strategy_id::text, 8) AS source_id,
				st.symbol,
				st.direction,
				COALESCE(b.name, '')          AS bot_name,
				''::text                      AS category,
				se.message,
				se.level,
				se.created_at
			FROM strategy_events se
			JOIN strategies st ON st.id = se.strategy_id
			LEFT JOIN bots b ON b.id = st.bot_id
			WHERE st.owner_id = $1 AND se.created_at > $2
			UNION ALL
			SELECT
				'bot'::text,
				LEFT(be.bot_id::text, 8),
				''::text,
				''::text,
				b.name,
				be.category,
				be.message,
				be.level,
				be.created_at
			FROM bot_events be
			JOIN bots b ON b.id = be.bot_id
			WHERE b.owner_id = $1 AND be.created_at > $2
		) t
		ORDER BY created_at ASC LIMIT 200`

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			rows, qErr := s.pool.Query(r.Context(), pollQ, userID, since)
			if qErr != nil {
				continue
			}
			events := scan(rows)
			if len(events) == 0 {
				continue
			}
			since = events[len(events)-1].CreatedAt
			if !send(events) {
				return
			}
		}
	}
}
