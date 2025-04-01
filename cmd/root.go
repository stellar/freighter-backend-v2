package cmd

import (
	"github.com/spf13/cobra"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/cmd/serve"
	"github.com/stellar/freighter-backend-v2/internal/config"
)

// SubCommand defines the interface for all subcommands
type SubCommand interface {
	Command() *cobra.Command
	Run() error
}

var rootCmd = &cobra.Command{
	Use:           "freighter-backend",
	Short:         "Freighter Backend Server",
	SilenceErrors: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := cmd.Help()
		if err != nil {
			logger.Error("Error calling help command", "error", err)
		}
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cmds := []SubCommand{
		&serve.ServeCmd{
			Cfg: &config.Config{},
		},
	}
	for _, cmd := range cmds {
		rootCmd.AddCommand(cmd.Command())
	}
}
