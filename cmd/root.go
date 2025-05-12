package cmd

import (
	"github.com/spf13/cobra"

	"github.com/stellar/freighter-backend-v2/cmd/serve"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// SubCommand defines the interface for all subcommands
type SubCommand interface {
	Command() *cobra.Command
	Run() error
}

type RootCmd struct {
	cmd *cobra.Command
}

func NewRootCmd() *RootCmd {
	cmd := &cobra.Command{
		Use:           "freighter-backend",
		Short:         "Freighter Backend Server",
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := cmd.Help()
			if err != nil {
				logger.Error("Error calling help command", "error", err)
			}
			return nil
		},
	}

	subcommands := []SubCommand{
		&serve.ServeCmd{
			Cfg: &config.Config{},
		},
	}
	for _, subcmd := range subcommands {
		cmd.AddCommand(subcmd.Command())
	}
	return &RootCmd{
		cmd: cmd,
	}
}

func (r *RootCmd) Execute() error {
	return r.cmd.Execute()
}
