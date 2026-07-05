package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// Auth rate-limit budgets. Per client IP, fixed window.
const (
	loginRateLimit     = 10
	loginRateWindow    = 5 * time.Minute
	registerRateLimit  = 5
	registerRateWindow = time.Hour
)

// rateLimit implements a fixed-window counter in Redis. It returns whether the
// call is allowed, how many attempts remain in the current window, and any error.
//
// On a Redis error it fails OPEN (allowed=true) and logs: a rate limiter is a
// hardening control, and turning a transient Redis blip into a full auth outage
// would be worse than briefly losing brute-force protection.
func (s *Server) rateLimit(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, err error) {
	redisKey := "ratelimit:" + key
	n, err := s.rdb.Incr(ctx, redisKey).Result()
	if err != nil {
		log.Printf("rateLimit: redis incr %s: %v — failing open", redisKey, err)
		return true, 0, err
	}
	if n == 1 {
		// First hit in this window — set the expiry.
		if e := s.rdb.Expire(ctx, redisKey, window).Err(); e != nil {
			log.Printf("rateLimit: redis expire %s: %v", redisKey, e)
		}
	}
	if int(n) > limit {
		return false, 0, nil
	}
	return true, limit - int(n), nil
}

// clientIP extracts the originating client IP, honouring the reverse proxy
// headers set by nginx (X-Real-IP / X-Forwarded-For) and falling back to the
// transport remote address.
func clientIP(r *http.Request) string {
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimitAuth returns middleware that limits requests to auth endpoints per
// client IP using the given budget. On exceed it responds 429 with Retry-After.
func (s *Server) rateLimitAuth(name string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := fmt.Sprintf("auth:%s:%s", name, clientIP(r))
			allowed, _, _ := s.rateLimit(r.Context(), key, limit, window)
			if !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
				writeError(w, http.StatusTooManyRequests, "too many attempts, please try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
