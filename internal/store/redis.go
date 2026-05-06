package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

func (r *RedisStore) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	_, err := r.redis.Ping(ctx).Result()
	if err != nil {
		return types.GetHealthResponse{Status: types.StatusError}, err
	}

	return types.GetHealthResponse{
		Status: types.StatusHealthy,
	}, nil
}

// MGetJSON fetches multiple JSON-encoded values in a single round trip.
// makeDest must return a fresh pointer for each hit; the resulting map is
// keyed by the requested key. Misses are absent from the map. Decode errors
// are skipped (logged by callers if desired) so a single bad entry does not
// fail the batch.
func (r *RedisStore) MGetJSON(ctx context.Context, keys []string, makeDest func() any) (map[string]any, error) {
	out := make(map[string]any, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	values, err := r.redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis MGET: %w", err)
	}
	for i, v := range values {
		if v == nil {
			continue
		}
		raw, ok := v.(string)
		if !ok {
			continue
		}
		dest := makeDest()
		if err := json.Unmarshal([]byte(raw), dest); err != nil {
			continue
		}
		out[keys[i]] = dest
	}
	return out, nil
}

// SetJSON stores a JSON-encoded value at key with the given TTL.
func (r *RedisStore) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis encode %s: %w", key, err)
	}
	if err := r.redis.Set(ctx, key, encoded, ttl).Err(); err != nil {
		return fmt.Errorf("redis SET %s: %w", key, err)
	}
	return nil
}
