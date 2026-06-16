package config

import (
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
