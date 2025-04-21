package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

const (
	envConfigFileName = "freighter-backend"
)

// InitializeConfig initializes the configuration using viper
func InitializeConfig(cmd *cobra.Command) error {
	v := viper.New()

	// Check if a specific config file path was provided via flag
	configFilePath, _ := cmd.Flags().GetString("config-path")

	if configFilePath != "" {
		// Use the specific config file path provided
		v.SetConfigFile(configFilePath)
	} else {
		// No specific path provided, search in standard locations
		v.SetConfigName(envConfigFileName) // Name of config file (without extension)
		v.AddConfigPath(".")               // Search in current directory

		// Search in user's config directory (e.g., ~/.config/freighter-backend)
		userConfigDir, err := os.UserConfigDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(userConfigDir, envConfigFileName))
		} else {
			logger.Warn("Could not determine user config directory", "error", err)
		}

		// Search in system-wide config directory (e.g., /etc/freighter-backend)
		v.AddConfigPath("/etc/freighter-backend")
	}

	// Attempt to read the config file.
	// Viper gracefully handles Not Found errors if no config file is present in searched paths.
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("error reading config file: %w", err)
		}
		logger.Debug("No config file found or specified. Using defaults/env vars/flags.")
	} else {
		logger.Info("Using config file", "path", v.ConfigFileUsed())
	}

	// Bind to environment variables (e.g., FREIGHTER_BACKEND_PORT)
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_")) // Match env var format (e.g., freighter-backend-port -> FREIGHTER_BACKEND_PORT)

	// Bind the current command's flags to viper
	bindFlags(cmd, v)

	return nil
}

func bindFlags(cmd *cobra.Command, v *viper.Viper) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		// Since viper reads config names with an underscore,
		// we need to bind the flag name to the environment variable by replacing dashes with underscores.
		configNameWithUnderscores := strings.ReplaceAll(f.Name, "-", "_")
		if !f.Changed && v.IsSet(configNameWithUnderscores) {
			val := v.Get(configNameWithUnderscores)
			err := cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
			if err != nil {
				logger.Error("Error setting flag value", "error", err)
			}
			return
		}
	})
}
