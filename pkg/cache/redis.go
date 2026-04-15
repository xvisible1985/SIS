// pkg/cache/redis.go
package cache

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"sis/pkg/models"
)

// Connect returns a connected Redis client.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("cache: parse url: %w", err)
	}
	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("cache: ping: %w", err)
	}
	return c, nil
}

// PublishCandle serialises a Candle and publishes it to its Redis channel.
func PublishCandle(ctx context.Context, c *redis.Client, candle models.Candle) error {
	data, err := json.Marshal(candle)
	if err != nil {
		return fmt.Errorf("cache: marshal candle: %w", err)
	}
	return c.Publish(ctx, candle.RedisChannel(), data).Err()
}
