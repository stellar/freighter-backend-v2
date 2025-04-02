package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	redis *redis.Client
}

func NewRedisStore(host string, port int, password string) (*RedisStore, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	return &RedisStore{
		redis: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
		}),
	}, nil
}

// Ping checks if the Redis server is available
func (s *RedisStore) Ping(ctx context.Context) error {
	return s.redis.Ping(ctx).Err()
}

// Get retrieves a value from Redis by key
func (s *RedisStore) Get(ctx context.Context, key string) (string, error) {
	return s.redis.Get(ctx, key).Result()
}

// Set stores a value in Redis with an optional expiration
func (s *RedisStore) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return s.redis.Set(ctx, key, value, expiration).Err()
}

// Close closes the Redis client
func (s *RedisStore) Close() error {
	return s.redis.Close()
}
