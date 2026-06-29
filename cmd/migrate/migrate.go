package migrate

import (
	"fmt"
	"os"
	"strconv"

	migrate "github.com/rubenv/sql-migrate"
	"github.com/spf13/cobra"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/db"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// MigrateCmd runs schema migrations out-of-band from `serve`. Migrations are a
// deploy-time / operator action (run as a Job, or once by hand locally), never
// on the serve path — so multiple replicas or local processes pointed at a
// shared database can't race to migrate or mutate it on boot.
type MigrateCmd struct {
	Cfg *config.Config
}

func (c *MigrateCmd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Schema migration helpers (up/down)",
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			// migrate needs only DATABASE_URL — no config file (matching the
			// wallet-backend / horizon migration tooling). Fall back to the
			// environment so deploy Jobs and docker-compose can inject it.
			if c.Cfg.DatabaseConfig.URL == "" {
				c.Cfg.DatabaseConfig.URL = os.Getenv("DATABASE_URL")
			}
			return c.Cfg.DatabaseConfig.Validate()
		},
	}

	cmd.PersistentFlags().StringVar(&c.Cfg.DatabaseConfig.URL, "database-url", "", "PostgreSQL connection string (env DATABASE_URL). Required.")

	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// count 0 = apply all pending. Migrations are idempotent, so applying
			// all is the only mode we need — a re-run once up to date is a no-op.
			applied, err := db.Migrate(cmd.Context(), c.Cfg.DatabaseConfig.URL, migrate.Up, 0)
			if err != nil {
				return err
			}
			logger.Info("Applied migrations up", "count", applied)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "down <count>",
		Short: "Roll back <count> migrations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("migration count %q must be an integer: %w", args[0], err)
			}
			// Require a positive count. sql-migrate treats max<=0 as "no limit",
			// so `down 0` (or a negative) would roll back EVERY migration — a
			// schema wipe. Unlike `up`, where applying all is the expected default,
			// rolling back must be explicit about how far.
			if count < 1 {
				return fmt.Errorf("down requires a positive count; got %d (0 or negative would roll back ALL migrations)", count)
			}
			applied, err := db.Migrate(cmd.Context(), c.Cfg.DatabaseConfig.URL, migrate.Down, count)
			if err != nil {
				return err
			}
			logger.Info("Rolled migrations down", "count", applied)
			return nil
		},
	})

	return cmd
}

// Run satisfies the SubCommand interface; the real work lives in the up/down
// subcommands, so invoking `migrate` with no subcommand just shows help.
func (c *MigrateCmd) Run() error { return nil }
