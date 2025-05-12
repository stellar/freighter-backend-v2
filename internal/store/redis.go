package store

import (
	"fmt"

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
