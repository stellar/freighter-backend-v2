package migrate

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
)

func TestMigrateCmd_RejectsEmptyDatabaseURL(t *testing.T) {
	// No t.Parallel(): t.Setenv is incompatible with parallel tests, and we must
	// control DATABASE_URL process-wide. Clear it so the PersistentPreRunE env
	// fallback finds nothing — otherwise a developer/CI shell with DATABASE_URL
	// exported would make `migrate up` run against that real database.
	t.Setenv("DATABASE_URL", "")

	migrateCmd := &MigrateCmd{Cfg: &config.Config{}}
	cmd := migrateCmd.Command()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// `migrate up` with no --database-url (and no DATABASE_URL in the env) must
	// fail fast in PersistentPreRunE, before any attempt to touch a database.
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

func TestMigrateCmd_UpRejectsArgs(t *testing.T) {
	t.Parallel()

	// `up` applies all pending migrations and takes no count argument.
	migrateCmd := &MigrateCmd{Cfg: &config.Config{}}
	cmd := migrateCmd.Command()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"up", "3", "--database-url", "postgres://localhost/test"})

	err := cmd.Execute()
	require.Error(t, err)
}
