// services/api-gateway/bot_engine.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"sis/pkg/crypto"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

const botEngineInterval = 30 * time.Second
const botEngineSymbolSem = 20

// reactiveOpp carries a single signal-fired trading opportunity.
type reactiveOpp struct {
	symbol   string
	interval string
	hash     string
	state    signal.State
}

// botEngineMetrics holds the last-tick stats for admin monitoring.
type botEngineMetrics struct {
	mu             sync.Mutex
	lastTickAt     time.Time
	lastTickMs     int64
	botsActive     int
	groupsComputed int
	opportunities  int
}

var globalBotMetrics botEngineMetrics

// ── Per-bot worker ────────────────────────────────────────────────────────────

// botOpportunity is a candidate strategy opening produced by signal workers
// and sent to the bot's dedicated goroutine for limit-aware, ranked processing.
type botOpportunity struct {
	sym    string  // trading symbol
	dir    string  // "long" | "short"
	score  float64 // ranking metric — higher = preferred when limit applies.
	// Currently 1.0 for all; replace with e.g. 24h volume for quality ranking.
	source string // "tick" | "reactive" (for event log)
}

// botWorkerEntry holds the inbound channel and lifecycle control for one bot's worker.
type botWorkerEntry struct {
	ch   chan botOpportunity // buffered; signal workers write, bot goroutine reads
	stop context.CancelFunc
}

// ensureBotWorker starts a worker goroutine for botID if one is not already running.
// Idempotent and goroutine-safe.
func (s *Server) ensureBotWorker(ctx context.Context, botID string) {
	if _, ok := s.botWorkers.Load(botID); ok {
		return
	}
	wCtx, cancel := context.WithCancel(ctx)
	entry := &botWorkerEntry{
		ch:   make(chan botOpportunity, 64),
		stop: cancel,
	}
	if _, loaded := s.botWorkers.LoadOrStore(botID, entry); loaded {
		cancel() // lost the race — another goroutine already started the worker
		return
	}
	go s.runBotWorker(wCtx, botID)
}

// stopBotWorker cancels the worker for botID, if any.
func (s *Server) stopBotWorker(botID string) {
	if v, ok := s.botWorkers.LoadAndDelete(botID); ok {
		v.(*botWorkerEntry).stop()
	}
}

// sendBotOpportunity delivers opp to botID's worker channel without blocking.
func (s *Server) sendBotOpportunity(botID string, opp botOpportunity) {
	v, ok := s.botWorkers.Load(botID)
	if !ok {
		return
	}
	select {
	case v.(*botWorkerEntry).ch <- opp:
	default:
		// channel full — tick will retry on the next 30 s interval
	}
}

// runBotWorker is the per-bot goroutine. It batches simultaneously arriving
// opportunities (100 ms drain window) and applies limits ordered by score.
func (s *Server) runBotWorker(ctx context.Context, botID string) {
	v, ok := s.botWorkers.Load(botID)
	if !ok {
		return
	}
	ch := v.(*botWorkerEntry).ch
	for {
		select {
		case <-ctx.Done():
			return
		case opp, ok := <-ch:
			if !ok {
				return
			}
			// Drain any other opportunities that arrived simultaneously so we can
			// rank the whole batch before deciding which ones to actually open.
			opps := []botOpportunity{opp}
			drain := time.NewTimer(100 * time.Millisecond)
		drainLoop:
			for {
				select {
				case more, ok := <-ch:
					if !ok {
						drain.Stop()
						break drainLoop
					}
					opps = append(opps, more)
				case <-drain.C:
					break drainLoop
				}
			}
			drain.Stop()
			s.applyBotOpportunities(ctx, botID, opps)
		}
	}
}

// applyBotOpportunities sorts opportunities by score (best first), then
// sequentially checks limits and creates strategies until all slots are filled.
// Running in a single goroutine per bot means no concurrent writes — no locks needed.
func (s *Server) applyBotOpportunities(ctx context.Context, botID string, opps []botOpportunity) {
	// Refresh bot config from the latest snapshot so changes take effect immediately.
	s.botSnapshotMu.RLock()
	var b botEngineRow
	var cfg botCfgJSON
	found := false
	for _, row := range s.botSnapshot {
		if row.id == botID {
			b = row
			cfg = s.botSnapshotCfgs[botID]
			found = true
			break
		}
	}
	s.botSnapshotMu.RUnlock()
	if !found {
		return // bot was removed while the opportunities were queued
	}

	// Sort by score descending — strongest signal gets a slot first.
	sort.Slice(opps, func(i, j int) bool { return opps[i].score > opps[j].score })

	// De-duplicate: keep only the best-scored occurrence of each sym+dir pair.
	seen := make(map[string]bool, len(opps))
	for _, o := range opps {
		key := o.sym + ":" + o.dir
		if seen[key] {
			continue
		}
		seen[key] = true

		// Re-read count from DB after every insert so limits are always accurate.
		if b.maxStrat > 0 {
			var cnt int
			if err := s.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing')`,
				b.id).Scan(&cnt); err != nil || cnt >= b.maxStrat {
				break // global limit reached; no point checking further
			}
		}
		if dirLimit := b.maxLong; o.dir == "long" && dirLimit > 0 {
			var cnt int
			if err := s.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM strategies WHERE bot_id=$1 AND direction='long' AND status IN ('active','finishing')`,
				b.id).Scan(&cnt); err != nil || cnt >= dirLimit {
				continue
			}
		}
		if dirLimit := b.maxShort; o.dir == "short" && dirLimit > 0 {
			var cnt int
			if err := s.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM strategies WHERE bot_id=$1 AND direction='short' AND status IN ('active','finishing')`,
				b.id).Scan(&cnt); err != nil || cnt >= dirLimit {
				continue
			}
		}
		// Already open (own strategy or detached strategy on same account)?
		var existing int
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM strategies
			 WHERE symbol=$1 AND direction=$2 AND status IN ('active','finishing')
			   AND (bot_id=$3 OR (bot_id IS NULL AND owner_id=$4 AND account_id=$5))`,
			o.sym, o.dir, b.id, b.ownerID, b.accountID).Scan(&existing); err != nil || existing > 0 {
			continue
		}
		if b.maxSymConsecutive > 0 {
			// Count how many of the most recent strategies for this bot are consecutively the same symbol.
			// Query the last maxSymConsecutive strategies ordered newest first.
			symRows, err := s.pool.Query(ctx,
				`SELECT symbol FROM strategies WHERE bot_id=$1 ORDER BY created_at DESC LIMIT $2`,
				b.id, b.maxSymConsecutive)
			if err == nil {
				consecutive := 0
				for symRows.Next() {
					var s string
					if symRows.Scan(&s) == nil && s == o.sym {
						consecutive++
					} else {
						break
					}
				}
				symRows.Close()
				if consecutive >= b.maxSymConsecutive {
					continue // this symbol ran too many times in a row; skip until another pair runs
				}
			}
		}
		if _, err := s.createBotStrategy(ctx, b, cfg, o.sym, o.dir, 0, ""); err != nil {
			if !strings.Contains(err.Error(), "unique") {
				s.logBotEvent(ctx, b.id, fmt.Sprintf("[%s] Ошибка открытия %s %s: %v", o.source, o.sym, o.dir, err), "error", "strategy")
			}
		} else {
			s.logBotEvent(ctx, b.id, fmt.Sprintf("[%s] Открыта стратегия %s %s", o.source, o.sym, o.dir), "info", "strategy")
		}
	}
}

// botEngineStats returns a snapshot of the most recent bot engine tick stats.
func botEngineStats() (lastAt time.Time, ms int64, bots, groups, opps int) {
	globalBotMetrics.mu.Lock()
	defer globalBotMetrics.mu.Unlock()
	return globalBotMetrics.lastTickAt, globalBotMetrics.lastTickMs,
		globalBotMetrics.botsActive, globalBotMetrics.groupsComputed, globalBotMetrics.opportunities
}

// RunBotEngine runs the bot automation loop: polls active bots every 30s,
// checks activation signals, and creates/starts strategies accordingly.
// It also registers a reactive callback on the signal engine so that
// trading opportunities are processed immediately when a signal fires.
func (s *Server) RunBotEngine(ctx context.Context) {
	// Register global signal callback once before the ticker loop.
	s.signalEngine.OnStateChange(func(sym, iv, h string, st signal.State) {
		if st == signal.Buy || st == signal.Sell {
			select {
			case s.reactiveSignals <- reactiveOpp{symbol: sym, interval: iv, hash: h, state: st}:
			default: // channel full; tick will catch it
			}
		}
	})

	// Start the reactive processor goroutine.
	go s.runReactiveProcessor(ctx)

	// Start the news bot ticker (fast polling of local DB for new listings).
	go s.runNewsBotTicker(ctx)

	ticker := time.NewTicker(botEngineInterval)
	defer ticker.Stop()
	s.botEngineTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.botEngineTick(ctx)
		}
	}
}

type botEngineRow struct {
	id        string
	ownerID   string
	accountID string
	whitelist []string
	blacklist []string
	stratCfg  []byte
	maxStrat  int
	maxLong   int
	maxShort  int
	maxSymConsecutive int
	maxMargin         float64
	autoMode  bool
}

// groupKey identifies a unique (interval, signal-config-hash) pair.
type groupKey struct {
	interval string
	hash     string
}

// groupEntry accumulates all bots that share the same signal config group,
// together with the union of symbols they want scanned.
type groupEntry struct {
	sigCfgs []signal.Config
	symbols []string // deduplicated union across all bots in this group
	bots    []struct {
		row botEngineRow
		cfg botCfgJSON
	}
}

func (s *Server) botEngineTick(ctx context.Context) {
	tickStart := time.Now()

	// ── STEP 1: Load bots from DB ─────────────────────────────────────────
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, account_id,
		       symbol_whitelist, symbol_blacklist,
		       strategy_config, max_strategies, max_margin_usdt, auto_mode,
		       max_long_strategies, max_short_strategies, max_sym_consecutive_runs
		FROM bots
		WHERE status = 'active' AND account_id IS NOT NULL`)
	if err != nil {
		log.Printf("bot engine tick: %v", err)
		return
	}

	var bots []botEngineRow
	for rows.Next() {
		var b botEngineRow
		if err := rows.Scan(
			&b.id, &b.ownerID, &b.accountID,
			&b.whitelist, &b.blacklist,
			&b.stratCfg, &b.maxStrat, &b.maxMargin, &b.autoMode,
			&b.maxLong, &b.maxShort, &b.maxSymConsecutive,
		); err == nil {
			bots = append(bots, b)
		}
	}
	rows.Close()

	if len(bots) == 0 {
		return
	}

	// Stop workers for bots that are no longer active (removed or deactivated).
	activeBotIDs := make(map[string]bool, len(bots))
	for _, b := range bots {
		activeBotIDs[b.id] = true
	}
	s.botWorkers.Range(func(k, v any) bool {
		if !activeBotIDs[k.(string)] {
			v.(*botWorkerEntry).stop()
			s.botWorkers.Delete(k)
		}
		return true
	})

	allSymbols, _ := trader.FetchAllLinearSymbols(ctx)

	// Cache allSymbols snapshot for reactive processor
	s.allSymbolsSnapMu.Lock()
	s.allSymbolsSnap = allSymbols
	s.allSymbolsSnapMu.Unlock()

	// Cache bot snapshot for reactive processor
	cfgsSnap := make(map[string]botCfgJSON, len(bots))
	for _, b := range bots {
		var cfg botCfgJSON
		if json.Unmarshal(b.stratCfg, &cfg) == nil {
			cfgsSnap[b.id] = cfg
		}
	}
	s.botSnapshotMu.Lock()
	s.botSnapshot = bots
	s.botSnapshotCfgs = cfgsSnap
	s.botSnapshotMu.Unlock()

	// ── STEP 1b: Cleanup stopped bot strategies ──────────────────────────
	for _, b := range bots {
		var cfg botCfgJSON
		if json.Unmarshal(b.stratCfg, &cfg) == nil && cfg.AfterStopMode == "delete" {
			s.cleanupStoppedBotStrategies(ctx, b, cfg)
		}
	}

	// ── STEP 2: Parse configs, build groups ───────────────────────────────
	groups := make(map[groupKey]*groupEntry)

	for _, b := range bots {
		var cfg botCfgJSON
		if err := json.Unmarshal(b.stratCfg, &cfg); err != nil {
			s.logBotEvent(ctx, b.id, fmt.Sprintf("Ошибка чтения конфига: %v", err), "error", "system")
			continue
		}
		// Hedge bots are handled by the separate hedge engine loop — skip here.
		if cfg.BotKind == "hedge" {
			continue
		}
		if len(cfg.ActivationSignals) == 0 {
			s.logBotEvent(ctx, b.id, "Тик: не настроены сигналы активации — пропуск", "warn", "tick")
			continue
		}

		// Determine candle interval from activation signal params
		interval := "15"
		for _, a := range cfg.ActivationSignals {
			if v, ok := a.Params["tf"].(string); ok && v != "" {
				interval = v
				break
			}
		}

		// Build signal.Config slice for activation check.
		// Skip bots whose signals are not registered in the signal engine
		// (e.g. "bybit-news") — they are handled by dedicated tickers.
		sigCfgs := make([]signal.Config, 0, len(cfg.ActivationSignals))
		skipBot := false
		for _, a := range cfg.ActivationSignals {
			sc := signal.Config{Name: a.Name, Params: a.Params}
			if _, err := signal.Build(sc); err != nil {
				skipBot = true
				break
			}
			sigCfgs = append(sigCfgs, sc)
		}
		if skipBot {
			continue
		}

		// Symbol-agnostic hash (pass "" as symbol)
		hash := signal.HashConfigs("", interval, sigCfgs)
		key := groupKey{interval: interval, hash: hash}

		entry, exists := groups[key]
		if !exists {
			entry = &groupEntry{sigCfgs: sigCfgs}
			groups[key] = entry
		}

		// Merge bot's symbols into the group's union (deduplicated via set)
		delistSymbols := s.GetDelistingSymbols()
		botSymbols := resolveSymbolList(b.whitelist, b.blacklist, delistSymbols, allSymbols)
		symSet := make(map[string]struct{}, len(entry.symbols)+len(botSymbols))
		for _, s := range entry.symbols {
			symSet[s] = struct{}{}
		}
		for _, s := range botSymbols {
			symSet[s] = struct{}{}
		}
		merged := make([]string, 0, len(symSet))
		for s := range symSet {
			merged = append(merged, s)
		}
		entry.symbols = merged

		entry.bots = append(entry.bots, struct {
			row botEngineRow
			cfg botCfgJSON
		}{row: b, cfg: cfg})
	}

	if len(groups) == 0 {
		return
	}

	// ── STEP 3: EnsureIntervals ───────────────────────────────────────────
	intervalSet := make(map[string]struct{}, len(groups))
	for k := range groups {
		intervalSet[k.interval] = struct{}{}
	}
	intervals := make([]string, 0, len(intervalSet))
	for iv := range intervalSet {
		intervals = append(intervals, iv)
	}
	s.globalWarmer.EnsureIntervals(intervals)

	// ── STEP 3b: Subscribe new (symbol×interval×sigCfgs) to signal engine ──
	s.botSubsMu.Lock()
	for key, entry := range groups {
		for _, sym := range entry.symbols {
			subID := "botengine:" + sym + ":" + key.interval + ":" + key.hash
			if !s.botSubs[subID] {
				s.botSubs[subID] = true
				symCopy, ivCopy, cfgCopy := sym, key.interval, make([]signal.Config, len(entry.sigCfgs))
				copy(cfgCopy, entry.sigCfgs)
				go func(id, sym, iv string, cfgs []signal.Config) {
					if err := s.signalEngine.Subscribe(id, sym, iv, cfgs, func(signal.State) {}); err != nil {
						log.Printf("botengine sub %s: %v", id, err)
						s.botSubsMu.Lock()
						delete(s.botSubs, id)
						s.botSubsMu.Unlock()
					}
				}(subID, symCopy, ivCopy, cfgCopy)
			}
		}
	}
	s.botSubsMu.Unlock()

	// ── STEP 4: Compute states once per group ─────────────────────────────
	type stateMap = map[string]signal.State
	stateCache := make(map[groupKey]stateMap, len(groups))
	var stateCacheMu sync.Mutex

	var groupWg sync.WaitGroup
	for key, entry := range groups {
		key, entry := key, entry
		groupWg.Add(1)
		go func() {
			defer groupWg.Done()

			var (
				mu      sync.Mutex
				results = make(stateMap, len(entry.symbols))
				wg      sync.WaitGroup
				sem     = make(chan struct{}, botEngineSymbolSem)
			)

			for _, sym := range entry.symbols {
				sym := sym
				wg.Add(1)
				go func() {
					defer wg.Done()
					select {
					case <-ctx.Done():
						return
					case sem <- struct{}{}:
					}
					defer func() { <-sem }()

					st := s.signalEngine.ComputeStateForce(sym, key.interval, entry.sigCfgs)
					mu.Lock()
					results[sym] = st
					mu.Unlock()
				}()
			}
			wg.Wait()

			stateCacheMu.Lock()
			stateCache[key] = results
			stateCacheMu.Unlock()
		}()
	}
	groupWg.Wait()

	// ── STEP 5: Process bots in parallel (sem=10) ─────────────────────────
	const botSem = 10
	var (
		botWg       sync.WaitGroup
		botSemCh    = make(chan struct{}, botSem)
		totalOppsMu sync.Mutex
		totalOpps   int
	)

	for key, entry := range groups {
		key, entry := key, entry
		symStates := stateCache[key] // computed above; read-only from here

		for _, item := range entry.bots {
			item := item
			botWg.Add(1)
			go func() {
				defer botWg.Done()
				select {
				case <-ctx.Done():
					return
				case botSemCh <- struct{}{}:
				}
				defer func() { <-botSemCh }()

				b := item.row
				cfg := item.cfg

				// Resolve this bot's own symbol list
				botSymbols := resolveSymbolList(b.whitelist, b.blacklist, s.GetDelistingSymbols(), allSymbols)
				if len(botSymbols) == 0 {
					return
				}

				// Load existing open strategies for this bot to avoid duplicates
				// Include detached strategies (bot_id IS NULL) owned by the same user/account.
				type openKey struct{ sym, dir string }
				openRows, err := s.pool.Query(ctx,
					`SELECT symbol, direction FROM strategies
					 WHERE status IN ('active', 'finishing')
					   AND (bot_id = $1 OR (bot_id IS NULL AND owner_id = $2 AND account_id = $3))`,
					b.id, b.ownerID, b.accountID)
				if err != nil {
					return
				}
				opened := make(map[openKey]bool)
				for openRows.Next() {
					var sym, dir string
					if openRows.Scan(&sym, &dir) == nil {
						opened[openKey{sym, dir}] = true
					}
				}
				openRows.Close()

				s.logBotEvent(ctx, b.id, fmt.Sprintf("Тик: сканируем %d символов...", len(botSymbols)), "info", "tick")

				// Collect non-neutral results for this bot's symbols from the group cache
				type sigResult struct {
					sym   string
					state signal.State
				}
				var results []sigResult
				for _, sym := range botSymbols {
					st, ok := symStates[sym]
					if !ok {
						continue
					}
					if st != signal.Neutral {
						results = append(results, sigResult{sym: sym, state: st})
					}
				}

				if len(results) == 0 {
					s.logBotEvent(ctx, b.id, fmt.Sprintf("Тик: нет активных сигналов из %d символов", len(botSymbols)), "info", "tick")
					return
				}
				s.logBotEvent(ctx, b.id, fmt.Sprintf("Тик: активных сигналов %d из %d символов", len(results), len(botSymbols)), "info", "tick")

				// Apply direction / duplicate filtering
				var opportunities []string
				var skippedDir []string
				for _, r := range results {
					var openDir string
					switch cfg.Direction {
					case "long":
						if r.state == signal.Buy {
							openDir = "long"
						}
					case "short":
						if r.state == signal.Sell {
							openDir = "short"
						}
					default: // "both"
						if r.state == signal.Buy {
							openDir = "long"
						} else if r.state == signal.Sell {
							openDir = "short"
						}
					}
					if openDir == "" {
						skippedDir = append(skippedDir, fmt.Sprintf("%s(%s)", r.sym, string(r.state)))
						continue
					}
					if opened[openKey{r.sym, openDir}] {
						continue
					}
					opportunities = append(opportunities, fmt.Sprintf("%s→%s", r.sym, openDir))
				}

				if len(skippedDir) > 0 {
					s.logBotEvent(ctx, b.id,
						fmt.Sprintf("Тик: %d сигналов не соответствуют направлению бота (%s): %s",
							len(skippedDir), cfg.Direction, joinMax(skippedDir, 5)), "info", "tick")
				}
				if len(opportunities) > 0 {
					totalOppsMu.Lock()
					totalOpps += len(opportunities)
					totalOppsMu.Unlock()

					if b.autoMode {
						// Ensure the bot has a dedicated worker goroutine, then send
						// each opportunity to it. The worker batches simultaneous
						// arrivals, sorts by score, and applies limits sequentially —
						// no race conditions, no locks needed.
						s.ensureBotWorker(ctx, b.id)
						for _, r := range results {
							var openDir string
							switch cfg.Direction {
							case "long":
								if r.state == signal.Buy {
									openDir = "long"
								}
							case "short":
								if r.state == signal.Sell {
									openDir = "short"
								}
							default:
								if r.state == signal.Buy {
									openDir = "long"
								} else if r.state == signal.Sell {
									openDir = "short"
								}
							}
							if openDir == "" || opened[openKey{r.sym, openDir}] {
								continue
							}
							score := computeOpportunityScore(s.signalEngine, r.sym, key.interval, entry.sigCfgs, cfg.PrioritySignal)
						s.sendBotOpportunity(b.id, botOpportunity{
							sym: r.sym, dir: openDir, score: score, source: "tick",
						})
						}
					} else {
						s.logBotEvent(ctx, b.id,
							fmt.Sprintf("Тик: %d возможностей для открытия (включите авто-режим или используйте окно сканирования): %s",
								len(opportunities), joinMax(opportunities, 5)), "info", "tick")
					}
				} else if len(skippedDir) == 0 {
					s.logBotEvent(ctx, b.id, "Тик: все сигналы уже в работе", "info", "tick")
				}
			}()
		}
	}
	botWg.Wait()

	// ── Update metrics ────────────────────────────────────────────────────
	elapsed := time.Since(tickStart)
	globalBotMetrics.mu.Lock()
	globalBotMetrics.lastTickAt = time.Now()
	globalBotMetrics.lastTickMs = elapsed.Milliseconds()
	globalBotMetrics.botsActive = len(bots)
	globalBotMetrics.groupsComputed = len(groups)
	globalBotMetrics.opportunities = totalOpps
	globalBotMetrics.mu.Unlock()
}

// botCfgJSON mirrors the strategy_config JSONB for bot automation.
type botCfgJSON struct {
	Direction      string  `json:"direction"`
	Category       string  `json:"category"`
	StrategyType   string  `json:"strategy_type"`
	EntryOrderType string  `json:"entry_order_type"`
	Leverage       int     `json:"leverage"`
	MarginType     string  `json:"margin_type"`
	HedgeMode      bool    `json:"hedge_mode"`
	GridLevels     int     `json:"grid_levels"`
	GridActive     int     `json:"grid_active"`
	GridStepPct    float64 `json:"grid_step_pct"`
	GridSizeUSDT   float64 `json:"grid_size_usdt"`
	TPMode         string  `json:"tp_mode"`
	TPPct          float64 `json:"tp_pct"`
	SLType         string  `json:"sl_type"`
	SLPct          float64 `json:"sl_pct"`
	SignalFilter   bool    `json:"signal_filter"`
	TrailingEnabled  bool    `json:"trailing_stop_enabled"`
	TrailingActPct   float64 `json:"trailing_activation_pct"`
	TrailingCallPct  float64 `json:"trailing_callback_pct"`
	AfterStopMode    string  `json:"after_stop_mode"`
	MaxCycles        int     `json:"max_cycles"`
	PrioritySignal string `json:"priority_signal"`
	ActivationSignals []struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	} `json:"activation_signals"`
	SignalConfigs []struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	} `json:"signal_configs"`
	Steps []struct {
		PriceMovePct float64 `json:"price_move_pct"`
		Lots         float64 `json:"lots"`
	} `json:"steps"`

	// BotKind identifies the bot type ("signal" | "parser" | "hedge").
	BotKind string `json:"bot_kind"`

	// Hedge bot configuration fields.
	HedgeActType         int     `json:"hedge_act_type"`          // 0=last_order%, 1=drawdown%, 2=pnl$, 3=roi%
	HedgeActValue        float64 `json:"hedge_act_value"`
	HedgeCloseType       int     `json:"hedge_close_type"`         // 0=wait_cycle, 1=max_loss$
	HedgeCloseValue      float64 `json:"hedge_close_value"`
	HedgeDeactCloseType  int     `json:"hedge_deact_close_type"`   // 0=pnl$, 1=roi%, 2=breakeven
	HedgeDeactCloseValue float64 `json:"hedge_deact_close_value"`
	HedgeProfitLazy      bool    `json:"hedge_profit_lazy"`
	HedgeProfitLazyPct   float64 `json:"hedge_profit_lazy_pct"`
	HedgeDeactType       int     `json:"hedge_deact_type"`         // 0=drawdown%, 1=pnl$, 2=roi%, 3=last_order%, 4=wait_pair
	HedgeDeactValue      float64 `json:"hedge_deact_value"`

	// Bot whitelist/blacklist: which bots' strategies are eligible for hedging.
	// Empty whitelist means "any bot (or manual)". Blacklist always takes priority.
	HedgeBotWhitelist []string `json:"hedge_bot_whitelist"`
	HedgeBotBlacklist []string `json:"hedge_bot_blacklist"`

	// SizeAsMain: when true, each slot's USDT size is derived from the opposite
	// (main) position's volume instead of a fixed GridSizeUSDT.
	SizeAsMain bool `json:"size_as_main"`

	// Hedge → Main control actions (hedge bots only).
	HedgeCancelMainTp bool `json:"hedge_cancel_main_tp"` // cancel TP on main at hedge activation
	HedgeCancelMainSl bool `json:"hedge_cancel_main_sl"` // cancel all SL on main at hedge activation
	HedgeStopMain     bool `json:"hedge_stop_main"`      // hard-stop main at hedge activation

	// Matrix-specific strategy config (used when StrategyType="matrix").
	MatrixLevels          json.RawMessage `json:"matrix_levels"`
	MatrixEntryLevel      json.RawMessage `json:"matrix_entry_level"`
	SafeZonePct           float64         `json:"safe_zone_pct"`
	ProtectedBuild        bool            `json:"protected_build"`
	MatrixRebuildOnSL     bool            `json:"matrix_rebuild_on_sl"`
	MatrixRebuildFromEntry bool           `json:"matrix_rebuild_from_entry"`
}

// computeOpportunityScore returns a ranking score for a symbol based on the bot's
// priority_signal setting. Higher score = higher priority.
// "st-flip" → TTL remaining (more time left = more recent signal); anything else → signal value.
func computeOpportunityScore(eng *signal.Engine, sym, interval string, cfgs []signal.Config, priority string) float64 {
	if priority == "" {
		return 1.0
	}
	if priority == "st-flip" {
		ttl := eng.QueryTTLRemaining(sym, interval, cfgs)
		if ttl >= 0 {
			return ttl
		}
		return 0
	}
	if vals := eng.QueryValues(sym, interval, cfgs); vals != nil {
		if v, ok := vals[priority]; ok {
			return v
		}
	}
	return 1.0
}

// joinMax joins up to n items and appends "..." if more.
func joinMax(items []string, n int) string {
	if len(items) <= n {
		return fmt.Sprintf("[%s]", join(items))
	}
	return fmt.Sprintf("[%s ... +%d]", join(items[:n]), len(items)-n)
}

func join(items []string) string {
	result := ""
	for i, v := range items {
		if i > 0 {
			result += ", "
		}
		result += v
	}
	return result
}

// createBotStrategy inserts a new bot strategy.
// hedgedStrategyID (non-empty only for hedge bots) records which main strategy this hedge
// covers. A unique partial index on hedged_strategy_id prevents two bots from hedging
// the same main position; if the slot is already taken the INSERT is silently skipped.
func (s *Server) createBotStrategy(ctx context.Context, b botEngineRow, cfg botCfgJSON, sym, dir string, leverageOverride int, hedgedStrategyID string) (string, error) {
	// Apply defaults (same as strategyPayload.applyDefaults)
	category := cfg.Category
	if category == "" {
		category = "linear"
	}
	stratType := cfg.StrategyType
	if stratType == "" {
		stratType = "grid"
	}
	entryType := cfg.EntryOrderType
	if entryType == "" {
		entryType = "limit"
	}
	tpMode := cfg.TPMode
	if tpMode == "" {
		tpMode = "total"
	}
	slType := cfg.SLType
	if slType == "" {
		slType = "conditional"
	}
	leverage := cfg.Leverage
	if leverageOverride > 0 {
		leverage = leverageOverride
	}
	if leverage == 0 {
		leverage = 1
	}
	marginType := cfg.MarginType
	if marginType == "" {
		marginType = "isolated"
	}
	gridLevels := cfg.GridLevels
	if gridLevels == 0 {
		gridLevels = 5
	}
	gridActive := cfg.GridActive
	if gridActive == 0 {
		gridActive = 3
	}
	gridStep := cfg.GridStepPct
	if gridStep == 0 {
		gridStep = 1.0
	}
	gridSize := cfg.GridSizeUSDT
	if gridSize == 0 {
		gridSize = 100
	}
	// Auto-raise grid_size_usdt to exchange minimum for this specific symbol.
	if pubInfo, perr := trader.GetPublicInstrumentInfo(ctx, category, sym); perr != nil {
		log.Printf("createBotStrategy %s/%s: GetPublicInstrumentInfo error: %v (gridSize=%.4f)", sym, category, perr, gridSize)
	} else {
		log.Printf("createBotStrategy %s/%s: MinOrderUSDT=%.4f MinQty=%.6f gridSize=%.4f", sym, category, pubInfo.MinOrderUSDT, pubInfo.MinQty, gridSize)
		if pubInfo.MinOrderUSDT > gridSize {
			gridSize = math.Ceil(pubInfo.MinOrderUSDT*1.1*10) / 10 // 10% buffer above exchange minimum
		}
		// Ensure at least 1 USDT even if API returned 0 (new listings, missing data)
		if gridSize < 1.0 {
			gridSize = 1.0
		}
	}
	tpPct := cfg.TPPct
	if tpPct == 0 {
		tpPct = 2.0
	}
	slPct := cfg.SLPct
	if slPct > 0 {
		slPct = -slPct
	}
	if slPct == 0 {
		slPct = -5.0
	}

	scJSON, _ := json.Marshal(cfg.SignalConfigs)
	if scJSON == nil {
		scJSON = []byte("[]")
	}

	var stepsParam *string
	if len(cfg.Steps) > 0 {
		sb, err := json.Marshal(cfg.Steps)
		if err == nil {
			sv := string(sb)
			stepsParam = &sv
		}
	}

	var trailingActPct *float64
	var trailingCallPct *float64
	if cfg.TrailingEnabled {
		if cfg.TrailingActPct > 0 {
			v := cfg.TrailingActPct
			trailingActPct = &v
		}
		if cfg.TrailingCallPct > 0 {
			v := cfg.TrailingCallPct
			trailingCallPct = &v
		}
	}

	// Matrix level params — NULL when not a matrix strategy so the columns stay clean.
	matrixLevelsParam := nullableJSONB(cfg.MatrixLevels)
	matrixEntryParam := nullableJSONB(cfg.MatrixEntryLevel)

	// signal_filter is always false for hedge-bot strategies: signal_configs serves
	// only as a pool for per-level use_signal buttons and must never block cycle start.
	// Ignoring cfg.SignalFilter intentionally — the backend is authoritative here.
	signalFilter := false

	// NULLIF converts empty hedgedStrategyID to NULL so the unique partial index
	// only fires when a real main-strategy ID is provided.
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO strategies
		  (owner_id, account_id, bot_id, symbol, category, direction, status,
		   grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		   tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
		   leverage, margin_type, hedge_mode, strategy_type, entry_order_type,
		   signal_configs, steps,
		   trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct,
		   cycle_count, max_cycles, size_as_main,
		   matrix_levels, matrix_entry_level, safe_zone_pct,
		   protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry,
		   hedged_strategy_id)
		VALUES ($1,$2,$3,$4,$5,$6,'active',
		        $7,$8,$9,$10,
		        $11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20,
		        $21::jsonb, ($22::text)::jsonb,
		        $23,$24,$25,
		        $26, $27, $28,
		        ($29::text)::jsonb, ($30::text)::jsonb, $31,
		        $32, $33, $34,
		        NULLIF($35,'')::uuid)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		b.ownerID, b.accountID, b.id, sym, category, dir,
		gridLevels, gridActive, gridStep, gridSize,
		tpMode, tpPct, slType, slPct, signalFilter,
		leverage, marginType, cfg.HedgeMode, stratType, entryType,
		string(scJSON), stepsParam,
		cfg.TrailingEnabled, trailingActPct, trailingCallPct,
		0, cfg.MaxCycles, cfg.SizeAsMain,
		matrixLevelsParam, matrixEntryParam, cfg.SafeZonePct,
		cfg.ProtectedBuild, cfg.MatrixRebuildOnSL, cfg.MatrixRebuildFromEntry,
		hedgedStrategyID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Another hedge bot already claimed this main strategy — silently skip.
		return "", nil
	}
	if err != nil {
		return "", err
	}

	// Notify strategy engine to load and start this new strategy
	go s.engine.Notify(context.Background(), id)
	return id, nil
}

// loadBotAccountCreds decrypts and returns trading credentials for an exchange account.
func (s *Server) loadBotAccountCreds(ctx context.Context, accountID string) (trader.Credentials, error) {
	var apiKeyEnc, secretEnc string
	if err := s.pool.QueryRow(ctx,
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc); err != nil {
		return trader.Credentials{}, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, s.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	secret, err := crypto.Decrypt(secretEnc, s.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	return trader.Credentials{APIKey: apiKey, SecretKey: secret}, nil
}

// cleanupStoppedBotStrategies deletes stopped bot strategies that have no open exchange position.
// Called each tick for bots configured with after_stop_mode="delete".
//
// For news bots (bybit-news activation signal) stopped strategies are held for lifetime_minutes
// (the same window configured in the signal) so that processNewsBots cannot immediately re-create
// the same strategy after a TP/SL close. Once the window expires the record is deleted.
func (s *Server) cleanupStoppedBotStrategies(ctx context.Context, b botEngineRow, cfg botCfgJSON) {
	type stoppedStrategy struct {
		id       string
		symbol   string
		category string
	}

	// Detect news bot and read its lifetime_minutes.
	isNewsBot := false
	lifetimeMin := 60
	for _, a := range cfg.ActivationSignals {
		if a.Name == "bybit-news" {
			isNewsBot = true
			if v, ok := a.Params["lifetime_minutes"]; ok {
				switch n := v.(type) {
				case float64:
					if n > 0 {
						lifetimeMin = int(n)
					}
				case int:
					if n > 0 {
						lifetimeMin = n
					}
				}
			}
			break
		}
	}

	// For news bots only include strategies older than lifetime_minutes so that the
	// re-creation guard in processNewsBots remains active until the window expires.
	query := `SELECT id, symbol, category FROM strategies WHERE bot_id=$1 AND status='stopped'`
	if isNewsBot {
		query = fmt.Sprintf(`SELECT id, symbol, category FROM strategies
		         WHERE bot_id=$1 AND status='stopped'
		           AND created_at < NOW() - INTERVAL '%d minutes'`, lifetimeMin)
	}

	rows, err := s.pool.Query(ctx, query, b.id)
	if err != nil {
		return
	}
	var stopped []stoppedStrategy
	for rows.Next() {
		var st stoppedStrategy
		if rows.Scan(&st.id, &st.symbol, &st.category) == nil {
			stopped = append(stopped, st)
		}
	}
	rows.Close()

	if len(stopped) == 0 {
		return
	}

	creds, err := s.loadBotAccountCreds(ctx, b.accountID)
	if err != nil {
		s.logBotEvent(ctx, b.id, fmt.Sprintf("Очистка: ошибка загрузки ключей аккаунта: %v", err), "error", "system")
		return
	}

	positions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		s.logBotEvent(ctx, b.id, fmt.Sprintf("Очистка: ошибка получения позиций с биржи: %v", err), "error", "system")
		return
	}

	openPositions := make(map[string]bool, len(positions))
	for _, p := range positions {
		if size, err2 := strconv.ParseFloat(p.Size, 64); err2 == nil && size > 0 {
			openPositions[p.Symbol] = true
		}
	}

	for _, st := range stopped {
		if openPositions[st.symbol] {
			s.logBotEvent(ctx, b.id,
				fmt.Sprintf("Очистка: стратегия %s ожидает закрытия позиции на бирже", st.symbol),
				"info", "strategy")
			continue
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM strategies WHERE id=$1`, st.id); err != nil {
			s.logBotEvent(ctx, b.id,
				fmt.Sprintf("Очистка: ошибка удаления стратегии %s: %v", st.symbol, err),
				"error", "strategy")
			continue
		}
		s.engine.ForceRemoveStrategy(ctx, st.id, b.accountID)
		s.logBotEvent(ctx, b.id,
			fmt.Sprintf("Очистка: стратегия %s удалена (позиция закрыта)", st.symbol),
			"info", "strategy")
	}
}

// parseLeverage extracts the numeric leverage from strings like "50x".
func parseLeverage(s string) int {
	if s == "" {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(s, "%dx", &n); err == nil {
		return n
	}
	return 0
}

// processNewsBots scans new Bybit listing announcements and creates strategies
// for bots whose activation_signals include "bybit-news".
func (s *Server) processNewsBots(ctx context.Context) {
	if s.newsScraper == nil {
		return
	}

	// Load active bots from DB
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, account_id,
		       symbol_whitelist, symbol_blacklist,
		       strategy_config, max_strategies, max_margin_usdt, auto_mode,
		       max_long_strategies, max_short_strategies
		FROM bots
		WHERE status = 'active' AND account_id IS NOT NULL`)
	if err != nil {
		log.Printf("news bot: load bots: %v", err)
		return
	}
	var bots []botEngineRow
	for rows.Next() {
		var b botEngineRow
		if err := rows.Scan(
			&b.id, &b.ownerID, &b.accountID,
			&b.whitelist, &b.blacklist,
			&b.stratCfg, &b.maxStrat, &b.maxMargin, &b.autoMode,
			&b.maxLong, &b.maxShort,
		); err == nil {
			bots = append(bots, b)
		}
	}
	rows.Close()

	if len(bots) == 0 {
		return
	}

	allSymbols, _ := trader.FetchAllLinearSymbols(ctx)

	// Find bots with bybit-news activation signal
	var newsBots []struct {
		row botEngineRow
		cfg botCfgJSON
	}
	for _, b := range bots {
		var cfg botCfgJSON
		if err := json.Unmarshal(b.stratCfg, &cfg); err != nil {
			continue
		}
		hasNews := false
		for _, a := range cfg.ActivationSignals {
			if a.Name == "bybit-news" {
				hasNews = true
				break
			}
		}
		if hasNews {
			newsBots = append(newsBots, struct {
				row botEngineRow
				cfg botCfgJSON
			}{row: b, cfg: cfg})
		}
	}
	if len(newsBots) == 0 {
		return
	}

	// Fetch all recent listing announcements (broad 24 h window).
	// Per-bot lifetime_minutes filtering happens inside the bot loop below.
	announcements, err := s.newsScraper.ListingAnnouncements(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		log.Printf("news bot: listing announcements: %v", err)
		return
	}
	if len(announcements) == 0 {
		return
	}

	// Build a set of known exchange symbols for availability gating.
	allSymSet := make(map[string]bool, len(allSymbols))
	for _, s := range allSymbols {
		allSymSet[s] = true
	}

	for _, nb := range newsBots {
		b := nb.row
		cfg := nb.cfg
		if !b.autoMode {
			continue
		}

		// Read lifetime_minutes from the bybit-news signal params (default 60).
		// This controls both how long after an announcement the strategy can open
		// and how long a stopped strategy blocks re-entry.
		lifetimeMin := 60
		for _, a := range cfg.ActivationSignals {
			if a.Name == "bybit-news" {
				if v, ok := a.Params["lifetime_minutes"]; ok {
					switch n := v.(type) {
					case float64:
						if n > 0 {
							lifetimeMin = int(n)
						}
					case int:
						if n > 0 {
							lifetimeMin = n
						}
					}
				}
				break
			}
		}
		windowCutoff := time.Now().Add(-time.Duration(lifetimeMin) * time.Minute)

		for _, ann := range announcements {
			// Skip announcements outside this bot's lifetime window.
			if ann.CreatedAt.Before(windowCutoff) {
				continue
			}

			for _, sym := range ann.Symbols {
				// Skip symbols not yet available on the exchange.
				// New listings may be announced before the perpetual contract is live.
				if len(allSymbols) > 0 && !allSymSet[sym] {
					continue
				}

				// Only long direction for listings
				openDir := "long"
				if cfg.Direction == "short" {
					continue
				}

				// Check max_strategies
				if b.maxStrat > 0 {
					var cnt int
					if err := s.pool.QueryRow(ctx,
						`SELECT COUNT(*) FROM strategies WHERE bot_id = $1 AND status IN ('active','finishing')`,
						b.id).Scan(&cnt); err != nil || cnt >= b.maxStrat {
						continue
					}
				}
				// Check per-direction limits
				if dirLimit := b.maxLong; openDir == "long" && dirLimit > 0 {
					var cnt int
					if err := s.pool.QueryRow(ctx,
						`SELECT COUNT(*) FROM strategies WHERE bot_id = $1 AND direction = 'long' AND status IN ('active','finishing')`,
						b.id).Scan(&cnt); err != nil || cnt >= dirLimit {
						continue
					}
				}

				// Guard: skip if strategy already active/finishing OR was stopped within
				// the bot's lifetime window (prevents re-entry on the same announcement).
				var existing int
				if err := s.pool.QueryRow(ctx,
					fmt.Sprintf(`SELECT COUNT(*) FROM strategies
					 WHERE symbol = $1 AND direction = $2
					   AND (bot_id = $3 OR (bot_id IS NULL AND owner_id = $4 AND account_id = $5))
					   AND (
					     status IN ('active','finishing')
					     OR (status = 'stopped' AND created_at > NOW() - INTERVAL '%d minutes')
					   )`, lifetimeMin),
					sym, openDir, b.id, b.ownerID, b.accountID).Scan(&existing); err != nil || existing > 0 {
					continue
				}

				// Determine leverage: from announcement, fallback to bot config
				levOverride := 0
				if ann.MaxLeverage != nil {
					levOverride = parseLeverage(*ann.MaxLeverage)
				}

				if _, err := s.createBotStrategy(ctx, b, cfg, sym, openDir, levOverride, ""); err != nil {
					if !strings.Contains(err.Error(), "unique") {
						s.logBotEvent(ctx, b.id, fmt.Sprintf("Bybit News: ошибка открытия %s %s: %v", sym, openDir, err), "error", "strategy")
					}
				} else {
					s.logBotEvent(ctx, b.id, fmt.Sprintf("Bybit News: открыта стратегия %s %s (плечо %dx)", sym, openDir, levOverride), "info", "strategy")
				}
			}
		}
	}
}

// logBotEvent inserts a bot event into bot_events table.
func (s *Server) logBotEvent(ctx context.Context, botID, msg, level, category string) {
	s.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO bot_events (bot_id, message, level, category) VALUES ($1, $2, $3, $4)`,
		botID, msg, level, category)
}

// runReactiveProcessor drains reactiveSignals and dispatches each opportunity
// to processBotSymbol with a semaphore of 20 concurrent workers.
func (s *Server) runReactiveProcessor(ctx context.Context) {
	const workers = 20
	sem := make(chan struct{}, workers)
	for {
		select {
		case <-ctx.Done():
			return
		case opp := <-s.reactiveSignals:
			sem <- struct{}{}
			go func(o reactiveOpp) {
				defer func() { <-sem }()
				s.processBotSymbol(ctx, o.symbol, o.interval, o.hash, o.state)
			}(opp)
		}
	}
}

// runNewsBotTicker polls the local DB every 5 seconds for new Bybit listing
// announcements and immediately creates strategies for news bots.
func (s *Server) runNewsBotTicker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	running := make(chan struct{}, 1) // prevents concurrent processNewsBots calls
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case running <- struct{}{}: // acquired
				go func() {
					defer func() { <-running }()
					s.processNewsBots(ctx)
				}()
			default: // previous call still running, skip tick
			}
		}
	}
}

// processBotSymbol checks all active bots for a given symbol/interval/state and
// opens strategies where applicable, replicating the logic of botEngineTick but
// scoped to a single symbol that just fired a signal.
func (s *Server) processBotSymbol(ctx context.Context, symbol, interval, hash string, state signal.State) {
	s.botSnapshotMu.RLock()
	bots := make([]botEngineRow, len(s.botSnapshot))
	copy(bots, s.botSnapshot)
	cfgsSnap := make(map[string]botCfgJSON, len(s.botSnapshotCfgs))
	for k, v := range s.botSnapshotCfgs {
		cfgsSnap[k] = v
	}
	s.botSnapshotMu.RUnlock()

	if len(bots) == 0 {
		return
	}

	s.allSymbolsSnapMu.RLock()
	allSymbols := make([]string, len(s.allSymbolsSnap))
	copy(allSymbols, s.allSymbolsSnap)
	s.allSymbolsSnapMu.RUnlock()

	for _, b := range bots {
		cfg, ok := cfgsSnap[b.id]
		if !ok {
			continue
		}
		if len(cfg.ActivationSignals) == 0 {
			continue
		}

		// Determine this bot's interval
		botInterval := "15"
		for _, a := range cfg.ActivationSignals {
			if v, ok := a.Params["tf"].(string); ok && v != "" {
				botInterval = v
				break
			}
		}
		if botInterval != interval {
			continue
		}

		// Compute this bot's signal config hash and compare with the fired hash
		sigCfgs := make([]signal.Config, len(cfg.ActivationSignals))
		for i, a := range cfg.ActivationSignals {
			sigCfgs[i] = signal.Config{Name: a.Name, Params: a.Params}
		}
		botHash := signal.HashConfigs("", interval, sigCfgs)
		if botHash != hash {
			continue
		}

		// Check this symbol passes the bot's whitelist/blacklist
		delistSymbols := s.GetDelistingSymbols()
		botSymbols := resolveSymbolList(b.whitelist, b.blacklist, delistSymbols, allSymbols)
		found := false
		for _, sym := range botSymbols {
			if sym == symbol {
				found = true
				break
			}
		}
		if !found {
			continue
		}

		// Check direction
		var openDir string
		switch cfg.Direction {
		case "long":
			if state == signal.Buy {
				openDir = "long"
			}
		case "short":
			if state == signal.Sell {
				openDir = "short"
			}
		default:
			if state == signal.Buy {
				openDir = "long"
			} else if state == signal.Sell {
				openDir = "short"
			}
		}
		if openDir == "" {
			continue
		}

		// Reactive path only fires for auto_mode bots.
		if !b.autoMode {
			continue
		}

		// Route the opportunity through the bot's dedicated worker goroutine.
		// The worker applies limits and ranking — no race conditions possible
		// because only one goroutine per bot ever calls createBotStrategy.
		s.ensureBotWorker(ctx, b.id)
		reactiveScore := computeOpportunityScore(s.signalEngine, symbol, interval, sigCfgs, cfg.PrioritySignal)
		s.sendBotOpportunity(b.id, botOpportunity{
			sym: symbol, dir: openDir, score: reactiveScore, source: "reactive",
		})
	}
}
