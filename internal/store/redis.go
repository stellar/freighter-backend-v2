package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stellar/freighter-backend-v2/internal/config"
)

type RedisStore struct {
	redis *redis.Client
}

func NewRedisStore(cfg *config.Config) (*RedisStore, error) {
	addr := fmt.Sprintf("%s:%d", cfg.RedisConfig.Host, cfg.RedisConfig.Port)

	return &RedisStore{
		redis: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: cfg.RedisConfig.Password,
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

// TSGet retrieves a value from Redis timeseries structure
func (s *RedisStore) TSGet(ctx context.Context, key string) (redis.TSTimestampValue, error) {
	return s.redis.TSGet(ctx, key).Result()
}
