// Package cache provides a Redis client wrapper with rate limiting and
// distributed locking capabilities for Nkore Bank.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Redis client with banking-specific helpers.
type Client struct {
	rdb *redis.Client
}

// New creates a new Redis client from the given URL (e.g. "redis://localhost:6379/0").
func New(redisURL string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse url: %w", err)
	}
	rdb := redis.NewClient(opts)

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Close gracefully closes the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// HealthCheck verifies the Redis connection is alive.
func (c *Client) HealthCheck(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("cache: health check: %w", err)
	}
	return nil
}

// Set stores a value with an expiration.
func (c *Client) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Get retrieves a value. Returns empty string and nil error when the key does not exist.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	val, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: get %q: %w", key, err)
	}
	return val, nil
}

// Delete removes one or more keys.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("cache: delete: %w", err)
	}
	return nil
}

// AllowRequest implements a sliding-window rate limiter using Redis INCR with TTL.
// It returns true if the request is within the limit for the given key.
func (c *Client) AllowRequest(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	pipe := c.rdb.Pipeline()

	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)

	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("cache: rate limit: %w", err)
	}

	return incr.Val() <= int64(limit), nil
}

// ErrLockNotAcquired is returned when a distributed lock cannot be obtained.
var ErrLockNotAcquired = errors.New("cache: lock not acquired")

// AcquireLock attempts to obtain a distributed lock for the given key.
// It uses SET NX with a TTL so the lock auto-expires if the holder crashes.
// Returns a release function on success.
func (c *Client) AcquireLock(ctx context.Context, key string, ttl time.Duration) (release func() error, err error) {
	lockKey := "lock:" + key
	ok, err := c.rdb.SetNX(ctx, lockKey, "1", ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: acquire lock: %w", err)
	}
	if !ok {
		return nil, ErrLockNotAcquired
	}

	release = func() error {
		if err := c.rdb.Del(ctx, lockKey).Err(); err != nil {
			return fmt.Errorf("cache: release lock: %w", err)
		}
		return nil
	}
	return release, nil
}
