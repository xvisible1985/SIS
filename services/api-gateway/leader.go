package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// tradingLeaderKey is the Redis key guarding exclusive ownership of the
// order-managing engines (strategy engine, bot engine, hedge engine).
const tradingLeaderKey = "sis:trading-engine:leader"

// TradingLeader provides a single-holder lock so that at most one api-gateway
// instance runs the order-managing engines at a time. Without it, a second
// instance (accidental double-start, overlapping deploy) would double-manage the
// same accounts and place duplicate orders on the exchange.
//
// Design notes:
//   - No automatic takeover. If the current leader dies, its lock expires by TTL
//     and a freshly started instance can then acquire it. We deliberately avoid
//     hot standby auto-takeover: a Redis lock cannot fence a stalled-but-alive
//     leader (GC pause, network blip), so auto-takeover could briefly create two
//     leaders — worse than the single-point-of-failure it would replace, given
//     this controls live trading.
//   - Fail-open on Redis error at acquisition: a Redis outage must not halt
//     trading on the (normally single) instance. Protection is only lost in the
//     rare combination of Redis-down AND a manual double-start.
type TradingLeader struct {
	rdb   *redis.Client
	key   string
	token string
	ttl   time.Duration
	held  bool
}

func newTradingLeader(rdb *redis.Client, key string, ttl time.Duration) *TradingLeader {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &TradingLeader{rdb: rdb, key: key, token: hex.EncodeToString(b), ttl: ttl}
}

// NewTradingLeader builds a leader for the default trading key with a 30s TTL.
func NewTradingLeader(rdb *redis.Client) *TradingLeader {
	return newTradingLeader(rdb, tradingLeaderKey, 30*time.Second)
}

// Acquire attempts to take leadership. Returns true if this instance may run the
// order-managing engines. On a Redis error it fails open (returns true) — see the
// type doc for the rationale.
func (l *TradingLeader) Acquire(ctx context.Context) bool {
	ok, err := l.rdb.SetNX(ctx, l.key, l.token, l.ttl).Result()
	if err != nil {
		log.Printf("trading leader: redis SETNX failed (%v) — proceeding (fail-open)", err)
		l.held = false
		return true
	}
	l.held = ok
	return ok
}

// renewScript refreshes the TTL only while we still own the lock (value == token).
var renewScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
else
	return 0
end`)

// RenewLoop periodically extends the lock TTL while held. It exits when ctx is
// cancelled or if the lock is lost (e.g. expired during a Redis outage).
func (l *TradingLeader) RenewLoop(ctx context.Context) {
	if !l.held {
		return // fail-open acquisition holds no real lock to renew
	}
	interval := l.ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, err := renewScript.Run(ctx, l.rdb, []string{l.key}, l.token, l.ttl.Milliseconds()).Int()
			if err != nil {
				log.Printf("trading leader: renew failed: %v", err)
				continue
			}
			if res == 0 {
				log.Printf("trading leader: WARNING lost leadership (lock no longer ours) — another instance may take over on restart")
				l.held = false
				return
			}
		}
	}
}

// releaseScript deletes the lock only if we still own it.
var releaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end`)

// Release relinquishes leadership if (and only if) this instance holds it.
func (l *TradingLeader) Release(ctx context.Context) {
	_ = releaseScript.Run(ctx, l.rdb, []string{l.key}, l.token).Err()
	l.held = false
}
