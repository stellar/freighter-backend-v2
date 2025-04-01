package utils

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stellar/freighter-backend-v2/internal/logger"
)

const (
	envConfigFileName = "freighter-backend-config"
)

// getProjectRoot returns the path to the project root directory
func getProjectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	// Go up one directory from the cmd directory to get to the project root
	return filepath.Dir(filepath.Dir(filename))
}

// InitializeConfig initializes the configuration using viper
func InitializeConfig(cmd *cobra.Command) error {
	v := viper.New()

	// Set the base name of the config file, without the file extension.
	v.SetConfigName(envConfigFileName)

	// Set the config file path to the absolute path
	v.AddConfigPath(filepath.Join(getProjectRoot(), "configs"))

	// Attempt to read the config file, gracefully ignoring errors
	// caused by a config file not being found. Return an error
	// if we cannot parse the config file.
	if err := v.ReadInConfig(); err != nil {
		// It's okay if there isn't a config file
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
	} else {
		logger.Info("Using config file:", "file", v.ConfigFileUsed())
	}

	// Bind to environment variables
	// Works great for simple config names, but needs help for names
	// like --favorite-color which we fix in the bindFlags function
	v.AutomaticEnv()

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
			cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
			return
		}
	})
}
