package bybitnews

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/proxy"
)

const (
	announcementsURL = "https://api.bybit.com/v5/announcements/index"
	pollInterval     = 1 * time.Minute
	queryLimit       = 50
)

// Scraper periodically fetches Bybit announcements and stores new ones.
type Scraper struct {
	db *pgxpool.Pool
}

// NewScraper creates a Scraper.
func NewScraper(db *pgxpool.Pool) *Scraper {
	return &Scraper{db: db}
}

// Start runs the polling loop until ctx is cancelled.
func (s *Scraper) Start(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// immediate first fetch
	if err := s.Fetch(ctx); err != nil {
		log.Printf("bybitnews: initial fetch: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Fetch(ctx); err != nil {
				log.Printf("bybitnews: fetch: %v", err)
			}
		}
	}
}

// ForceFetch triggers an immediate fetch. Safe for concurrent use.
func (s *Scraper) ForceFetch(ctx context.Context) error {
	return s.Fetch(ctx)
}

func (s *Scraper) Fetch(ctx context.Context) error {
	client := proxy.HTTPClient()
	url := fmt.Sprintf("%s?locale=en-US&limit=%d", announcementsURL, queryLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	var payload struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Total int            `json:"total"`
			List  []Announcement `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if payload.RetCode != 0 {
		return fmt.Errorf("bybit retCode=%d: %s", payload.RetCode, payload.RetMsg)
	}

	for _, a := range payload.Result.List {
		if err := s.upsert(ctx, a); err != nil {
			log.Printf("bybitnews: upsert %q: %v", a.Title, err)
		}
	}
	return nil
}

func extractID(rawURL string) string {
	// URLs look like:
	// https://announcements.bybit.com/en-US/article/some-slug--bltf662314c211a8616/
	parts := strings.Split(strings.TrimSuffix(rawURL, "/"), "--")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	// fallback: hash the url
	return fmt.Sprintf("hash-%d", hashString(rawURL))
}

func hashString(s string) uint32 {
	var h uint32 = 5381
	for i := 0; i < len(s); i++ {
		h = ((h << 5) + h) + uint32(s[i])
	}
	return h
}

func classify(key string) (isListing, isDelisting bool) {
	switch key {
	case "new_crypto":
		return true, false
	case "delisting":
		return false, true
	}
	// fuzzy match for keys that contain these words
	lower := strings.ToLower(key)
	if strings.Contains(lower, "list") && !strings.Contains(lower, "delist") {
		return true, false
	}
	if strings.Contains(lower, "delist") {
		return false, true
	}
	return false, false
}

func (s *Scraper) upsert(ctx context.Context, a Announcement) error {
	id := extractID(a.URL)
	isListing, isDelisting := classify(a.Type.Key)
	parsed := ParseListingDetails(a.Title, a.Description)

	var launchAt interface{}
	if parsed.LaunchAt != nil {
		launchAt = *parsed.LaunchAt
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO bybit_announcements (
			announcement_id, title, description, type_key, type_title, tags, url,
			date_ts, start_date_ts, end_date_ts, is_new_listing, is_delisting,
			symbols, markets, max_leverage, launch_at, is_pre_market, parsed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, now())
		ON CONFLICT (announcement_id) DO UPDATE SET
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			type_key = EXCLUDED.type_key,
			type_title = EXCLUDED.type_title,
			tags = EXCLUDED.tags,
			url = EXCLUDED.url,
			date_ts = EXCLUDED.date_ts,
			start_date_ts = EXCLUDED.start_date_ts,
			end_date_ts = EXCLUDED.end_date_ts,
			is_new_listing = EXCLUDED.is_new_listing,
			is_delisting = EXCLUDED.is_delisting,
			symbols = EXCLUDED.symbols,
			markets = EXCLUDED.markets,
			max_leverage = EXCLUDED.max_leverage,
			launch_at = EXCLUDED.launch_at,
			is_pre_market = EXCLUDED.is_pre_market,
			parsed_at = now()
	`, id, a.Title, a.Description, a.Type.Key, a.Type.Title, a.Tags, a.URL,
		a.DateTS, a.StartDateTS, a.EndDateTS, isListing, isDelisting,
		parsed.Symbols, parsed.Markets, parsed.MaxLeverage, launchAt, parsed.IsPreMarket)

	return err
}

// Latest returns the most recent listing/delisting announcements.
func (s *Scraper) Latest(ctx context.Context, limit int) ([]DBAnnouncement, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, announcement_id, title, description, type_key, type_title, tags, url,
			date_ts, start_date_ts, end_date_ts, is_new_listing, is_delisting,
			symbols, markets, max_leverage, launch_at, is_pre_market, parsed_at, created_at
		 FROM bybit_announcements
		 WHERE is_new_listing = true OR is_delisting = true
		 ORDER BY date_ts DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DBAnnouncement
	for rows.Next() {
		var a DBAnnouncement
		if err := rows.Scan(&a.ID, &a.AnnouncementID, &a.Title, &a.Description, &a.TypeKey, &a.TypeTitle,
			&a.Tags, &a.URL, &a.DateTS, &a.StartDateTS, &a.EndDateTS, &a.IsNewListing, &a.IsDelisting,
			&a.Symbols, &a.Markets, &a.MaxLeverage, &a.LaunchAt, &a.IsPreMarket, &a.ParsedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListingAnnouncements returns new listings with parsed symbols and launch time,
// filtered by minimum date. Used by the news bot processor.
func (s *Scraper) ListingAnnouncements(ctx context.Context, since time.Time) ([]DBAnnouncement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, announcement_id, title, description, type_key, type_title, tags, url,
			date_ts, start_date_ts, end_date_ts, is_new_listing, is_delisting,
			symbols, markets, max_leverage, launch_at, is_pre_market, parsed_at, created_at
		 FROM bybit_announcements
		 WHERE is_new_listing = true
		   AND launch_at IS NOT NULL
		   AND symbols IS NOT NULL AND array_length(symbols, 1) > 0
		   AND created_at >= $1
		 ORDER BY date_ts DESC
		 LIMIT 50
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DBAnnouncement
	for rows.Next() {
		var a DBAnnouncement
		if err := rows.Scan(&a.ID, &a.AnnouncementID, &a.Title, &a.Description, &a.TypeKey, &a.TypeTitle,
			&a.Tags, &a.URL, &a.DateTS, &a.StartDateTS, &a.EndDateTS, &a.IsNewListing, &a.IsDelisting,
			&a.Symbols, &a.Markets, &a.MaxLeverage, &a.LaunchAt, &a.IsPreMarket, &a.ParsedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DelistingSymbols returns all unique symbols from delisting announcements.
func (s *Scraper) DelistingSymbols(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT unnest(symbols)
		FROM bybit_announcements
		WHERE is_delisting = true
		  AND symbols IS NOT NULL
		  AND array_length(symbols, 1) > 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// List returns recent announcements from the DB.
func (s *Scraper) List(ctx context.Context, limit int, typeKey string, onlyListings, onlyDelistings bool) ([]DBAnnouncement, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, announcement_id, title, description, type_key, type_title, tags, url,
		date_ts, start_date_ts, end_date_ts, is_new_listing, is_delisting,
		symbols, markets, max_leverage, launch_at, is_pre_market, parsed_at, created_at
	 FROM bybit_announcements WHERE 1=1`
	args := []any{}
	argIdx := 1

	if typeKey != "" {
		query += fmt.Sprintf(" AND type_key = $%d", argIdx)
		args = append(args, typeKey)
		argIdx++
	}
	if onlyListings {
		query += " AND is_new_listing = true"
	}
	if onlyDelistings {
		query += " AND is_delisting = true"
	}

	query += fmt.Sprintf(" ORDER BY date_ts DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DBAnnouncement
	for rows.Next() {
		var a DBAnnouncement
		if err := rows.Scan(&a.ID, &a.AnnouncementID, &a.Title, &a.Description, &a.TypeKey, &a.TypeTitle,
			&a.Tags, &a.URL, &a.DateTS, &a.StartDateTS, &a.EndDateTS, &a.IsNewListing, &a.IsDelisting,
			&a.Symbols, &a.Markets, &a.MaxLeverage, &a.LaunchAt, &a.IsPreMarket, &a.ParsedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
