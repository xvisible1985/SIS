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
	botSecret    string
	tronAddr     string // TRON/USDT TRC20 receiving address
	coinIcons    *coinicons.Store
	proxyManager *proxy.Manager

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

	exchangeSymsMu      sync.RWMutex
	exchangeSyms        map[string][]string // trading-pair → exchange list
	exchangeSymsUpdated time.Time

	delistMu        sync.RWMutex
	delistSymbols   []string
	delistUpdatedAt time.Time

	// cleanupWaiters tracks when each stopped strategy first entered "waiting for position close"
	// state. Key: strategyID string → time.Time (first wait time). Used to enforce a max wait
	// timeout in cleanupStoppedBotStrategies.
	cleanupWaiters sync.Map

	// Hedge WS price watcher: subscribes to TickerHub for symbols near the activation
	// threshold and triggers an immediate hedge engine tick when price crosses it.
	hedgeWatchMu   sync.RWMutex
	hedgeWatches   map[string]hedgeWatchEntry // symbol → cached threshold
	hedgeUnsubs    []func()                   // TickerHub unsubscribe funcs
	hedgeTriggerCh chan struct{}              // buffered(1): WS price crossed threshold
	flipChan       chan string                // buffered(16): main strategy IDs closed at TP

	// Per-account WS broadcast registry: lets background goroutines (the paired-close
	// watcher) push messages to every currently-open trader-positions WS connection for a
	// given account, without those goroutines knowing anything about *websocket.Conn or
	// HTTP. See docs/superpowers/specs/2026-07-23-paired-close-realtime-design.md
	// component 4.
	broadcastMu   sync.RWMutex
	broadcastSubs map[string][]chan any // accountID → subscriber channels

	// Paired-close watcher: mirrors the hedgeWatches pattern above but for the
	// combined-PnL+накопление threshold rather than a single entry-price level. See
	// docs/superpowers/specs/2026-07-23-paired-close-realtime-design.md components 2-3.
	pairedCloseWatchMu sync.RWMutex
	pairedCloseWatches map[string]pairedCloseWatchEntry // symbol → cached pair state
	pairedCloseUnsubs  []func()                         // TickerHub unsubscribe funcs

	pairedCloseInFlightMu sync.Mutex
	pairedCloseInFlight   map[string]bool // symbol → a verify-and-close is currently running

	pairedCloseSemMu  sync.Mutex
	pairedCloseSemPer map[string]chan struct{} // accountID → bounded concurrency semaphore

	pairedCloseThrottleMu    sync.Mutex
	pairedCloseLastRecompute map[string]time.Time // symbol → last price-tick-triggered recompute
}

// pairedCloseSemaphoreSize caps how many paired-close verify-and-close checks can run
// concurrently for the SAME account — Bybit's rate limits are per-API-key, so a burst on
// one account must not be able to starve or exceed that account's own limit budget.
// Matches matrixBatchCheckActivation's existing sem := make(chan struct{}, 20) constant.
const pairedCloseSemaphoreSize = 20

// pairedCloseRecomputeThrottle bounds how often a single price tick can trigger a
// recompute for the same symbol — a fast-moving market's flood of ticks shouldn't redo the
// same work dozens of times a second.
const pairedCloseRecomputeThrottle = time.Second

// initBroadcastRegistry must be called once before subscribeBroadcast/broadcast are used
// (NewServer does this — tests constructing a bare &Server{} must call it themselves).
func (s *Server) initBroadcastRegistry() {
	s.broadcastMu.Lock()
	defer s.broadcastMu.Unlock()
	if s.broadcastSubs == nil {
		s.broadcastSubs = make(map[string][]chan any)
	}
}

// subscribeBroadcast registers a new channel for accountID and returns it along with an
// unsubscribe function that removes it. Buffered(8) — matches the non-blocking-drop
// philosophy already used for the Bybit-read goroutine in pkg/trader/ws.go: a slow or
// absent reader must never stall the broadcaster.
func (s *Server) subscribeBroadcast(accountID string) (chan any, func()) {
	ch := make(chan any, 8)
	s.broadcastMu.Lock()
	s.broadcastSubs[accountID] = append(s.broadcastSubs[accountID], ch)
	s.broadcastMu.Unlock()

	unsub := func() {
		s.broadcastMu.Lock()
		defer s.broadcastMu.Unlock()
		subs := s.broadcastSubs[accountID]
		for i, c := range subs {
			if c == ch {
				subs = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(subs) == 0 {
			delete(s.broadcastSubs, accountID)
		} else {
			s.broadcastSubs[accountID] = subs
		}
	}
	return ch, unsub
}

// broadcast sends msg to every channel currently subscribed for accountID. Non-blocking
// per-subscriber — a slow/full subscriber's message is dropped rather than blocking the
// caller or other subscribers.
func (s *Server) broadcast(accountID string, msg any) {
	s.broadcastMu.RLock()
	subs := s.broadcastSubs[accountID]
	s.broadcastMu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// NewServer creates a Server.
func NewServer(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client, jwtSecret, encKey, botSecret, tronAddr string, adminEmails map[string]bool, pm *proxy.Manager) *Server {
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
		botSecret:       botSecret,
		tronAddr:        tronAddr,
		signalEngine:    se,
		globalWarmer:    gw,
		adminEmails:     adminEmails,
		coinIcons:       coinicons.NewStore(pool),
		proxyManager:    pm,
		reactiveSignals: make(chan reactiveOpp, 1024),
		botSubs:         make(map[string]bool),
		botSnapshotCfgs: make(map[string]botCfgJSON),
	}
	s.engine = strategy.New(pool, encKey)
	s.engine.SetSignalEngine(se)
	s.hedgeWatches = make(map[string]hedgeWatchEntry)
	s.hedgeTriggerCh = make(chan struct{}, 1)
	s.flipChan = make(chan string, 16)
	s.initBroadcastRegistry()
	s.pairedCloseWatches = make(map[string]pairedCloseWatchEntry)
	s.pairedCloseInFlight = make(map[string]bool)
	s.pairedCloseSemPer = make(map[string]chan struct{})
	s.pairedCloseLastRecompute = make(map[string]time.Time)
	strategy.OnAccumulate = s.onAccumulateChange
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
			syms, err := bybitnews.DelistingSymbolsFromDB(ctx, s.pool)
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
