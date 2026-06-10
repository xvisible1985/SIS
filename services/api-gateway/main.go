// services/api-gateway/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"sis/pkg/bybitnews"
	"sis/pkg/cache"
	"sis/pkg/db"
	"sis/pkg/proxy"
	traderPkg "sis/pkg/trader"
)

func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")
	jwtSecret := mustEnv("JWT_SECRET")
	botSecret := getEnv("TELEGRAM_BOT_SECRET", "")
	tronAddr := getEnv("TRON_RECEIVE_ADDRESS", "")
	encKey := mustEnv("ENCRYPTION_KEY")
	listenAddr := getEnv("LISTEN_ADDR", ":8080")

	syncDays := 30
	if v := os.Getenv("TRADER_SYNC_DAYS"); v != "" {
		fmt.Sscanf(v, "%d", &syncDays)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, "migrations"); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	adminEmails := make(map[string]bool)
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			adminEmails[e] = true
		}
	}

	var pm *proxy.Manager
	if pm, err = proxy.NewManager(ctx, pool, encKey); err != nil {
		log.Printf("proxy manager disabled: %v", err)
	} else {
		proxy.InitGlobalManager(pm)
	}

	ns := bybitnews.NewScraper(pool)
	go ns.Start(ctx)

	s := NewServer(ctx, pool, rdb, jwtSecret, encKey, botSecret, tronAddr, adminEmails, pm, ns)
	bootstrapAdmins(ctx, pool, adminEmails)

	// Start system health monitor (CPU sampler every 10 s, DB size tracker every 5 min).
	StartSystemHealthMonitor(ctx, pool)

	// Start strategy engine
	go s.engine.Start(ctx)

	// Start coin icon cache refresher
	s.coinIcons.StartRefresher(ctx)

	// Start background syncer
	syncer := traderPkg.NewSyncer(pool, encKey, syncDays)
	syncer.Start(ctx)

	// Start max-leverage DB refresher (every 10 min, covers all active strategy symbols)
	RunLeverageRefresher(ctx, pool)

	// Start bot automation engine
	go s.RunBotEngine(ctx)

	// Start hedge bot automation engine
	go s.runHedgeEngine(ctx)

	// Start Telegram notification polling
	go s.startTgNotifier(ctx)

	// Start TRON deposit watcher
	go s.startTronWatcher(ctx)

	// Prime warmer with active strategy symbols so their kline history is fetched first.
	if rows, err := pool.Query(ctx,
		`SELECT DISTINCT symbol FROM strategies WHERE status IN ('active','finishing')`); err == nil {
		var prioritySyms []string
		for rows.Next() {
			var sym string
			if rows.Scan(&sym) == nil {
				prioritySyms = append(prioritySyms, sym)
			}
		}
		rows.Close()
		if len(prioritySyms) > 0 {
			s.globalWarmer.SetPrioritySymbols(prioritySyms)
			log.Printf("global warmer: priority symbols: %v", prioritySyms)
		}
	}
	go s.globalWarmer.Start(ctx)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Auth routes — no JWT required
	r.Post("/auth/register", s.Register)
	r.Post("/auth/login", s.Login)
	r.Post("/auth/telegram-callback", s.TelegramLoginCallback)

	// Bot-to-gateway internal routes — authenticated via TELEGRAM_BOT_SECRET
	r.Group(func(r chi.Router) {
		r.Use(s.RequireBotSecret)
		r.Post("/auth/telegram", s.TelegramLoginRequest)
		r.Get("/bot/summary", s.BotSummary)
		r.Post("/bot/pause-all", s.BotPauseAll)
		r.Post("/bot/resume-all", s.BotResumeAll)
		r.Post("/bot/strategy-status", s.BotStrategyStatus)
		r.Post("/bot/mute", s.BotMute)
	})

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(s.RequireAuth)

		r.Get("/signals/chart-history", s.SignalChartHistory)
		r.Get("/signals", s.ListSignals)
		r.Post("/signals", s.CreateSignal)
		r.Get("/signals/{id}", s.GetSignal)
		r.Put("/signals/{id}", s.UpdateSignal)
		r.Delete("/signals/{id}", s.DeleteSignal)

		r.Post("/signals/{id}/backtest", s.SubmitBacktest)
		r.Post("/signals/{id}/optimize", s.SubmitOptimize)
		r.Get("/signals/{id}/backtest-results", s.GetBacktestResults)
		r.Get("/signals/{id}/optimization-results", s.GetOptimizationResults)

		r.Get("/webhooks", s.ListWebhooks)
		r.Post("/webhooks", s.CreateWebhook)
		r.Get("/webhooks/{id}", s.GetWebhook)
		r.Put("/webhooks/{id}", s.UpdateWebhook)
		r.Delete("/webhooks/{id}", s.DeleteWebhook)

		// User profile
		r.Get("/account/profile", s.GetProfile)
		r.Patch("/account/profile", s.UpdateProfile)
		r.Post("/account/change-password", s.ChangePassword)

		r.Get("/account/telegram-link", s.GetTelegramLink)
		r.Delete("/account/telegram", s.TelegramDisconnect)
		r.Get("/account/notifications", s.GetNotifications)
		r.Patch("/account/notifications", s.UpdateNotifications)
		r.Get("/account/referral", s.GetReferral)
		// TRON payments
		r.Post("/payments/tron/deposit", s.CreateTronDeposit)
		r.Get("/payments/tron/deposit/{id}", s.GetTronDeposit)
		r.Get("/payments/tron/deposits", s.ListTronDeposits)
		r.Get("/account/novabot-balance", s.GetNovabotBalance)

		// Exchange accounts
		r.Get("/accounts", s.ListAccounts)
		r.Post("/accounts", s.CreateAccount)
		r.Delete("/accounts/{id}", s.DeleteAccount)
		r.Get("/accounts/{id}/verify", s.VerifyAccount)
		r.Get("/accounts/{id}/balance", s.GetAccountBalance)
		r.Get("/accounts/{id}/positions", s.GetAccountPositions)
		r.Patch("/accounts/{id}/active", s.ToggleAccountActive)

		// Strategies
		r.Get("/strategies", s.ListStrategies)
		r.Post("/strategies", s.CreateStrategy)
		r.Put("/strategies/{id}", s.UpdateStrategy)
		r.Post("/strategies/{id}/status", s.SetStrategyStatus)
		r.Post("/strategies/{id}/detach", s.DetachFromBot)
		r.Delete("/strategies/{id}", s.DeleteStrategy)

		// Strategy state and events
		r.Get("/strategies/{id}/state", s.GetStrategyState)
		r.Get("/strategies/{id}/hedge-session", s.GetHedgeSession)
		r.Get("/strategies/{id}/cycle-audit", s.GetCycleAudit)
		r.Post("/strategies/{id}/cycle-restart", s.RestartCycle)
		r.Post("/strategies/{id}/dismiss-alert", s.DismissManualAlert)
		r.Get("/strategies/{id}/events", s.GetStrategyEvents)

		// Strategy templates
		r.Get("/strategy-templates", s.ListTemplates)
		r.Post("/strategy-templates", s.CreateTemplate)
		r.Delete("/strategy-templates/{id}", s.DeleteTemplate)

		// Strategy defaults (all authenticated users — read-only)
		r.Get("/strategy-defaults", s.GetStrategyDefaults)

		// Coin filter settings (all authenticated users — read-only)
		r.Get("/coin-filter", s.GetCoinFilter)

		// Trader
		r.Post("/trader/order", s.TraderPlaceOrder)
		r.Delete("/trader/order", s.TraderCancelOrder)
		r.Post("/trader/leverage", s.TraderSetLeverage)
			r.Post("/trader/position-mode", s.TraderSwitchPositionMode)
		r.Get("/trader/orders", s.ListTraderOrders)
		r.Get("/trader/executions", s.ListTraderExecutions)
		r.Get("/trader/pnl", s.GetClosedPnl)
		r.Get("/trader/stats", s.GetTraderStats)

		// Instrument constraints (leverage, lot size limits)
		r.Get("/instrument-info", s.GetInstrumentConstraints)

		// Signal/indicator types — enabled list (all authenticated users)
		r.Get("/signal-types", s.ListEnabledSignalTypes)
		r.Get("/indicator-types", s.ListEnabledIndicatorTypes)

		// Bots
		r.Get("/bots", s.ListBots)
		r.Post("/bots", s.CreateBot)
		r.Get("/bots/{id}", s.GetBot)
		r.Patch("/bots/{id}", s.PatchBot)
		r.Delete("/bots/{id}", s.DeleteBot)
		r.Post("/bots/{id}/deploy", s.DeployBot)
		r.Post("/bots/{id}/fork", s.ForkBot)
		r.Post("/bots/{id}/start", s.StartBot)
		r.Post("/bots/{id}/stop", s.StopBot)
		r.Post("/bots/{id}/publish", s.PublishBot)
		r.Post("/bots/{id}/request-approval", s.RequestBotApproval)
		r.Get("/bots/{id}/events", s.GetBotEvents)
		r.Get("/bots/{id}/scan", s.ScanBot)
		r.Post("/bots/{id}/trigger", s.TriggerBot)
		r.Post("/bots/{id}/blacklist-add", s.AddBotBlacklist)
		r.Post("/bots/signal-scan", s.ScanSignals)

			// Trade history
			r.Get("/trade-history", s.GetTradeHistory)
			r.Get("/trade-history/symbols", s.GetTradeHistorySymbols)

			// Dashboard
			r.Get("/dashboard", s.GetDashboard)

		// Admin
		r.Get("/admin/metrics", s.GetAdminMetrics)

		// Admin: signal and indicator types management (admin only)
		r.Group(func(r chi.Router) {
			r.Use(s.RequireAdmin)
			// Admin: user management
			r.Get("/admin/users", s.ListAdminUsers)
			r.Patch("/admin/users/{id}", s.PatchAdminUser)
			r.Post("/admin/users/{id}/email/verify", s.AdminVerifyEmail)
			r.Post("/admin/users/{id}/email/reset", s.AdminResetEmail)
			r.Post("/admin/users/{id}/email/resend", s.AdminResendEmail)
			r.Post("/admin/users/{id}/password", s.AdminSetPassword)
			r.Post("/admin/users/{id}/balance/adjust", s.AdjustNovabotBalance)
			r.Get("/admin/users/{id}/transactions", s.ListNovabotTransactions)
			r.Post("/admin/users/{id}/block", s.BlockAdminUser)
			r.Post("/admin/users/{id}/unblock", s.UnblockAdminUser)
			r.Delete("/admin/users/{id}/accounts/{aid}", s.DeleteAdminAccount)
			// Admin: bots management
			r.Get("/admin/bots", s.ListAdminBots)
			r.Post("/admin/bots", s.CreateOfficialBot)
			r.Post("/admin/bots/{id}/approve", s.ApproveBotPublication)
			r.Post("/admin/bots/{id}/reject",  s.RejectBotPublication)
			r.Post("/admin/bots/{id}/publish-to-catalog", s.PublishBotToCatalog)
			r.Delete("/admin/bots/{id}", s.DeleteAdminBot)
			// Admin: signal and indicator types management
			r.Get("/admin/signal-types", s.ListSignalTypes)
			r.Patch("/admin/signal-types/{id}", s.ToggleSignalType)
			r.Get("/admin/indicator-types", s.ListIndicatorTypes)
			r.Patch("/admin/indicator-types/{id}", s.ToggleIndicatorType)
			r.Get("/admin/signal-override/{name}", s.GetSignalOverride)
			r.Put("/admin/signal-override/{name}", s.SetSignalOverride)

			// Admin: strategy defaults management
			r.Get("/admin/strategy-defaults", s.GetStrategyDefaults)
			r.Put("/admin/strategy-defaults/{type}", s.UpdateStrategyDefaults)

			// Admin: coin filter management
			r.Put("/admin/coin-filter", s.UpdateCoinFilter)

			// Admin: proxy management
			r.Get("/admin/proxies", s.ListProxies)
			r.Post("/admin/proxies", s.CreateProxy)
			r.Patch("/admin/proxies/{id}", s.UpdateProxy)
			r.Delete("/admin/proxies/{id}", s.DeleteProxy)
			r.Get("/admin/proxy-metrics", s.GetProxyMetrics)

			// Admin: Bybit news
			r.Get("/admin/bybit-news", s.ListBybitAnnouncements)
			r.Get("/admin/bybit-news/latest", s.GetLatestBybitNews)
			r.Post("/admin/bybit-news/refresh", s.RefreshBybitNews)

			// Admin: log visualizer
			r.Get("/admin/log-visualizer/accounts",   s.LVGetAccounts)
			r.Get("/admin/log-visualizer/strategies", s.LVGetStrategies)
			r.Get("/admin/log-visualizer/events",     s.LVGetEvents)
			r.Get("/admin/log-visualizer/levels",     s.LVGetLevels)
			r.Get("/admin/log-visualizer/klines",     s.LVGetKlines)

			// Admin: system health
			r.Get("/admin/system-health", s.GetSystemHealth)

				// Admin: sign Bybit trading agreement (disabled — requires master API key permissions)
				// r.Post("/admin/accounts/{id}/sign-agreement", s.AdminSignAgreement)
		})
	})

	// Telegram bot callback — no auth, token is the secret
	r.Post("/account/telegram-verify", s.TelegramVerify)

	// Coin icons — public, no auth
	r.Get("/coin-icon/{symbol}", s.GetCoinIcon)
	// Bybit news delistings — public, no auth (used by coin picker)
	r.Get("/bybit-news/delistings", s.GetDelistingSymbolsHandler)

	// WebSocket endpoints — auth via ?token= query param
	r.Get("/ws/jobs/{id}/progress", s.JobProgress)
	r.Get("/ws/trader/positions", s.PositionsStream)
	r.Get("/ws/strategies/updates", s.StrategiesUpdatesStream)
	r.Get("/ws/strategies/{id}/events", s.StrategyEventsStream)
	r.Get("/ws/bots/updates", s.BotSignalUpdatesStream)
	r.Get("/ws/bots/{id}/events", s.BotEventsStream)

	srv := &http.Server{Addr: listenAddr, Handler: r}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("api-gateway: listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
	log.Println("api-gateway: stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// bootstrapAdmins upgrades users from ADMIN_EMAILS env to role='admin' in the DB.
// Allows a smooth migration from env-based to DB-based admin check.
func bootstrapAdmins(ctx context.Context, pool *pgxpool.Pool, adminEmails map[string]bool) {
	for email := range adminEmails {
		pool.Exec(ctx, `UPDATE users SET role='admin' WHERE email=$1 AND role!='admin'`, email)
	}
}
