package config

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseConfig_Validate(t *testing.T) {
	t.Parallel()

	t.Run("empty URL is rejected", func(t *testing.T) {
		t.Parallel()
		err := DatabaseConfig{}.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database-url")
	})

	t.Run("non-empty URL is accepted", func(t *testing.T) {
		t.Parallel()
		err := DatabaseConfig{URL: "postgres://localhost/db"}.Validate()
		require.NoError(t, err)
	})
}

func TestDatabaseConfig_ValidatePoolConfig(t *testing.T) {
	t.Parallel()

	t.Run("valid pool config passes", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, DatabaseConfig{MaxConns: 10, MinConns: 2}.ValidatePoolConfig())
	})

	cases := map[string]DatabaseConfig{
		"max < 1":             {MaxConns: 0, MinConns: 0},
		"min negative":        {MaxConns: 10, MinConns: -1},
		"min > max":           {MaxConns: 2, MinConns: 5},
		"max overflows int32": {MaxConns: math.MaxInt32 + 1, MinConns: 1},
	}
	for name, cfg := range cases {
		t.Run("rejects "+name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, cfg.ValidatePoolConfig())
		})
	}
}
