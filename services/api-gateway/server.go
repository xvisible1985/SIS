// services/api-gateway/server.go
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"sis/pkg/bybitnews"
	"sis/pkg/coinicons"
	"sis/pkg/proxy"
	"sis/pkg/signal"
	"sis/pkg/strategy"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	pool         *pgxpool.Pool
	rdb          *redis.Client
	jwtSecret    []byte
	encKey       string
	engine       *strategy.Engine
	signalEngine *signal.Engine
	globalWarmer *signal.GlobalWarmer
	adminEmails  map[string]bool
	coinIcons    *coinicons.Store
	proxyManager *proxy.Manager
	newsScraper  *bybitnews.Scraper

	reactiveSignals chan reactiveOpp
	botSubsMu       sync.Mutex
	botSubs         map[string]bool

	botSnapshotMu   sync.RWMutex
	botSnapshot     []botEngineRow
	botSnapshotCfgs map[string]botCfgJSON

	// botWorkers holds a dedicated goroutine per active bot that receives
	// candidate opportunities and applies limits/ranking before creating strategies.
	botWorkers sync.Map // key: botID string → *botWorkerEntry

	allSymbolsSnapMu sync.RWMutex
	allSymbolsSnap   []string

	delistMu        sync.RWMutex
	delistSymbols   []string
	delistUpdatedAt time.Time

}

// NewServer creates a Server.
func NewServer(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client, jwtSecret, encKey string, adminEmails map[string]bool, pm *proxy.Manager, ns *bybitnews.Scraper) *Server {
	exec := signal.ExecFn(func(ctx context.Context, sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	})
	se := signal.NewEngine(ctx, exec)
	gw := signal.NewGlobalWarmer(se.Hub(), se.PriceHub())
	s := &Server{
		pool:            pool,
		rdb:             rdb,
		jwtSecret:       []byte(jwtSecret),
		encKey:          encKey,
		signalEngine:    se,
		globalWarmer:    gw,
		adminEmails:     adminEmails,
		coinIcons:       coinicons.NewStore(pool),
		proxyManager:    pm,
		newsScraper:     ns,
		reactiveSignals: make(chan reactiveOpp, 1024),
		botSubs:         make(map[string]bool),
		botSnapshotCfgs: make(map[string]botCfgJSON),
	}
	s.engine = strategy.New(pool, encKey)
	s.engine.SetSignalEngine(se)
	go s.refreshDelistCache(ctx)
	return s
}

// refreshDelistCache periodically refreshes the list of delisting symbols.
func (s *Server) refreshDelistCache(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.newsScraper == nil {
				continue
			}
			syms, err := s.newsScraper.DelistingSymbols(ctx)
			if err != nil {
				continue
			}
			s.delistMu.Lock()
			s.delistSymbols = syms
			s.delistUpdatedAt = time.Now()
			s.delistMu.Unlock()
		}
	}
}

// GetDelistingSymbols returns the cached delisting symbols (thread-safe).
func (s *Server) GetDelistingSymbols() []string {
	s.delistMu.RLock()
	defer s.delistMu.RUnlock()
	out := make([]string, len(s.delistSymbols))
	copy(out, s.delistSymbols)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
