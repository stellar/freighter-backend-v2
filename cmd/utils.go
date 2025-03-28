package cmd

import (
	"github.com/spf13/cobra"
)

const (
	envPrefix = "FREIGHTER_BACKEND"
	envConfigFileName = "freighter-backend.env"
	envConfigFilePath = "./configs"
)

func initializeConfig(cmd *cobra.Command) error {
	return nil
}
