package cmd

import (
	"log"

	"github.com/spf13/cobra"
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
			log.Fatalf("Error calling help command: %s", err.Error())
		}
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	registerSubCommands(
		&serveCmd{},
	)
}

// registerCommands registers multiple commands with the root command
func registerSubCommands(cmds ...SubCommand) {
	for _, cmd := range cmds {
		rootCmd.AddCommand(cmd.Command())
	}
}
