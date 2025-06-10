package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestNewRedisStore(t *testing.T) {
	t.Run("creates store with password", func(t *testing.T) {
		host := "localhost"
		port := 6379
		password := "testpass"

		store := NewRedisStore(host, port, password)

		require.NotNil(t, store)
		assert.NotNil(t, store.redis)
	})

	t.Run("creates store without password", func(t *testing.T) {
		host := "localhost"
		port := 6379
		password := ""

		store := NewRedisStore(host, port, password)

		require.NotNil(t, store)
		assert.NotNil(t, store.redis)
	})

	t.Run("creates store with custom port", func(t *testing.T) {
		host := "redis-cluster"
		port := 16379
		password := "cluster-pass"

		store := NewRedisStore(host, port, password)

		require.NotNil(t, store)
		assert.NotNil(t, store.redis)
	})
}

func TestRedisStore_Name(t *testing.T) {
	store := NewRedisStore("localhost", 6379, "")

	name := store.Name()

	assert.Equal(t, "redis", name)
}

func TestRedisStore_GetHealth(t *testing.T) {
	t.Run("returns error status when connection is refused", func(t *testing.T) {
		// Use a port that's unlikely to have Redis running
		store := NewRedisStore("localhost", 9999, "")

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		response, err := store.GetHealth(ctx)

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})

	t.Run("returns error status when host is invalid", func(t *testing.T) {
		// Use an invalid host
		store := NewRedisStore("invalid-redis-host-that-does-not-exist", 6379, "")

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		response, err := store.GetHealth(ctx)

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})

	t.Run("returns error status when timeout occurs", func(t *testing.T) {
		// Use a very short timeout to ensure it fails
		store := NewRedisStore("localhost", 6379, "")

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Give context time to expire
		time.Sleep(2 * time.Millisecond)

		response, err := store.GetHealth(ctx)

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})
}
