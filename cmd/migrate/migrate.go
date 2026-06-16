package migrate

import (
	"fmt"
	"strconv"

	migrate "github.com/rubenv/sql-migrate"
	"github.com/spf13/cobra"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/db"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/utils"
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
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := utils.InitializeConfig(cmd); err != nil {
				return err
			}
			return c.Cfg.DatabaseConfig.Validate()
		},
	}

	var configFilePath string
	cmd.PersistentFlags().StringVar(&configFilePath, "config-path", "", "Path to config file (e.g., /etc/freighter/config.toml)")
	cmd.PersistentFlags().StringVar(&c.Cfg.DatabaseConfig.URL, "database-url", "", "PostgreSQL connection string (env DATABASE_URL). Required.")

	cmd.AddCommand(&cobra.Command{
		Use:   "up [count]",
		Short: "Apply all pending migrations, or [count] of them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := parseMigrationCount(args)
			if err != nil {
				return err
			}
			applied, err := db.Migrate(cmd.Context(), c.Cfg.DatabaseConfig.URL, migrate.Up, count)
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
			count, err := parseMigrationCount(args)
			if err != nil {
				return err
			}
			// Require a positive count. sql-migrate treats max<=0 as "no limit",
			// so `down 0` (or a negative) would roll back EVERY migration — a
			// schema wipe. Up has no such footgun (all-by-default is expected).
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

// parseMigrationCount interprets the optional positional arg: absent means "all"
// (0, which sql-migrate treats as no limit); otherwise it must be an integer.
func parseMigrationCount(args []string) (int, error) {
	if len(args) == 0 {
		return 0, nil
	}
	count, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, fmt.Errorf("migration count %q must be an integer: %w", args[0], err)
	}
	return count, nil
}
