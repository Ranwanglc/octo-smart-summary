package redis

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/go-redis/redis/v8"
)

// Client wraps a Redis connection used for token → uid resolution.
type Client struct {
	rdb *redis.Client
}

// New creates a new Redis client. Returns nil if addr is empty.
func New(addr string, db int) *Client {
	if addr == "" {
		return nil
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: addr,
		DB:   db,
	})
	log.Printf("[redis] connecting to %s db=%d", addr, db)
	return &Client{rdb: rdb}
}

// ResolveUID looks up "token:{token}" in Redis and extracts uid.
// Key format: token:{token_str} → value: {uid}@{name}@
func (c *Client) ResolveUID(ctx context.Context, token string) (string, error) {
	if c == nil || token == "" {
		return "", nil
	}
	key := fmt.Sprintf("token:%s", token)
	val, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(val, "@", 2)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], nil
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	return c.rdb.Close()
}
