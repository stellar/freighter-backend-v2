// ABOUTME: Provides Redis store implementation for caching and data persistence.
// ABOUTME: Implements the Service interface for health checking capabilities.

package store

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	serviceName = "redis"
)

type RedisStore struct {
	redis *redis.Client
}

func NewRedisStore(host string, port int, password string) *RedisStore {
	addr := fmt.Sprintf("%s:%d", host, port)

	return &RedisStore{
		redis: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
		}),
	}
}

func (r *RedisStore) Name() string {
	return serviceName
}

func (r *RedisStore) GetHealth(ctx context.Context) (types.GetHealthResponse, error) {
	_, err := r.redis.Ping(ctx).Result()
	if err != nil {
		return types.GetHealthResponse{Status: types.StatusError}, err
	}

	return types.GetHealthResponse{
		Status: types.StatusHealthy,
	}, nil
}
