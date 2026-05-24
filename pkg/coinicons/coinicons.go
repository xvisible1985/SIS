package coinicons

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var cdnSources = []string{
	"https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/%s.png",
	"https://assets.coincap.io/assets/icons/%s@2x.png",
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Get returns icon bytes for a base symbol (e.g. "btc").
// On first call it fetches from CDN and caches; subsequent calls read from DB.
func (s *Store) Get(ctx context.Context, base string) ([]byte, string, error) {
	base = strings.ToLower(base)

	var data []byte
	var ct string
	var fetchedAt time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT data, content_type, fetched_at FROM coin_icons WHERE symbol=$1`, base,
	).Scan(&data, &ct, &fetchedAt)

	if err == nil && time.Since(fetchedAt) < time.Hour {
		return data, ct, nil
	}

	// Not cached or stale — fetch now.
	data, ct = fetchFromCDNs(base)
	s.upsert(context.Background(), base, data, ct)
	return data, ct, nil
}

// StartRefresher runs a goroutine that refreshes all cached icons every hour.
func (s *Store) StartRefresher(ctx context.Context) {
	go func() {
		// Wait 5 minutes after startup before first refresh pass.
		select {
		case <-time.After(5 * time.Minute):
		case <-ctx.Done():
			return
		}
		for {
			s.refreshAll(ctx)
			select {
			case <-time.After(time.Hour):
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Store) refreshAll(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `SELECT symbol FROM coin_icons`)
	if err != nil {
		return
	}
	var symbols []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err == nil {
			symbols = append(symbols, sym)
		}
	}
	rows.Close()

	for _, sym := range symbols {
		select {
		case <-ctx.Done():
			return
		default:
		}
		data, ct := fetchFromCDNs(sym)
		s.upsert(ctx, sym, data, ct)
	}
	log.Printf("coinicons: refreshed %d icons", len(symbols))
}

func (s *Store) upsert(ctx context.Context, base string, data []byte, ct string) {
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO coin_icons (symbol, data, content_type, fetched_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (symbol) DO UPDATE
		  SET data=EXCLUDED.data, content_type=EXCLUDED.content_type, fetched_at=EXCLUDED.fetched_at
	`, base, data, ct)
}

func fetchFromCDNs(base string) ([]byte, string) {
	client := &http.Client{Timeout: 8 * time.Second}
	for _, tpl := range cdnSources {
		url := strings.ReplaceAll(tpl, "%s", base)
		resp, err := client.Get(url)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "image/png"
		}
		return data, ct
	}
	return nil, "image/png"
}
