package migrate

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
)

func TestMigrateCmd_RejectsEmptyDatabaseURL(t *testing.T) {
	t.Parallel()

	migrateCmd := &MigrateCmd{Cfg: &config.Config{}}
	cmd := migrateCmd.Command()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// `migrate up` with no --database-url must fail fast in PersistentPreRunE,
	// before any attempt to touch a database.
	cmd.SetArgs([]string{"up"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database-url")
}

func TestMigrateCmd_DownRejectsNonPositiveCount(t *testing.T) {
	t.Parallel()

	// `down 0` / `down -1` must be rejected before reaching the DB, since
	// sql-migrate would interpret a non-positive max as "roll back everything".
	// (`-1` needs `--` so pflag doesn't treat it as a flag.)
	cases := [][]string{
		{"down", "0", "--database-url", "postgres://localhost/test"},
		{"down", "--database-url", "postgres://localhost/test", "--", "-1"},
	}
	for _, args := range cases {
		migrateCmd := &MigrateCmd{Cfg: &config.Config{}}
		cmd := migrateCmd.Command()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)

		err := cmd.Execute()
		require.Error(t, err, "args %v should be rejected", args)
		assert.Contains(t, err.Error(), "positive count")
	}
}

func TestParseMigrationCount(t *testing.T) {
	t.Parallel()

	t.Run("no args means all (0)", func(t *testing.T) {
		t.Parallel()
		n, err := parseMigrationCount(nil)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	t.Run("numeric arg is parsed", func(t *testing.T) {
		t.Parallel()
		n, err := parseMigrationCount([]string{"3"})
		require.NoError(t, err)
		assert.Equal(t, 3, n)
	})

	t.Run("non-numeric arg errors", func(t *testing.T) {
		t.Parallel()
		_, err := parseMigrationCount([]string{"abc"})
		require.Error(t, err)
	})
}
