// pkg/heartbeat/heartbeat.go
package heartbeat

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const ttl = 30 * time.Second
const interval = 10 * time.Second

// Start writes a heartbeat key to Redis every 10 s until ctx is cancelled.
// Key pattern: service:heartbeat:<name>
func Start(ctx context.Context, rdb *redis.Client, name string) {
	key := "service:heartbeat:" + name
	beat := func() {
		rdb.Set(ctx, key, "1", ttl)
	}
	beat()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			beat()
		}
	}
}
