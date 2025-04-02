package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializeConfig(t *testing.T) {
	defaultHost := "localhost"
	defaultPort := 3002
	defaultMode := "development"

	testCases := []struct {
		name string
		cliArgs []string
		envVars map[string]string
		configFileContent string
		expectedValues map[string]string
		expectError bool
	}{
		{
			name: "Uses command line flags", 
			cliArgs: []string{"--freighter-backend-host", "test-host", "--freighter-backend-port", "8000", "--mode", "test-mode"}, 
			envVars: map[string]string{},
			configFileContent: "",
			expectedValues: map[string]string{
				"freighter-backend-host": "test-host",
				"freighter-backend-port": "8000",
				"mode": "test-mode",
			},
			expectError: false,
		},
		{
			name: "Uses environment variables when no command line flags are provided",
			cliArgs: []string{},
			envVars: map[string]string{
				"FREIGHTER_BACKEND_HOST": "test-host",
				"FREIGHTER_BACKEND_PORT": "8000",
				"MODE": "test-mode",
			},
			configFileContent: "",
			expectedValues: map[string]string{
				"freighter-backend-host": "test-host",
				"freighter-backend-port": "8000",
				"mode": "test-mode",
			},
			expectError: false,
		},
		{
			name: "Uses config file when no command line flags or environment variables are provided",
			cliArgs: []string{},
			envVars: map[string]string{},
			configFileContent: `
				FREIGHTER_BACKEND_HOST = 'test-host'
				FREIGHTER_BACKEND_PORT = 8000
				MODE = 'test-mode'
			`,
			expectedValues: map[string]string{
				"freighter-backend-host": "test-host",
				"freighter-backend-port": "8000",
				"mode": "test-mode",
			},
			expectError: false,
		},
		{
			name: "Reads from mixed sources",
			envVars: map[string]string{
				"FREIGHTER_BACKEND_PORT": "9090", // Port from env
			},
			configFileContent: `
				FREIGHTER_BACKEND_HOST = "host_from_config" # Host from config
			`,
			cliArgs: []string{"--freighter-backend-host", "host_from_flag"}, // Key overridden by flag
			expectedValues: map[string]string{
				"freighter-backend-host": "host_from_flag", // Flag wins
				"freighter-backend-port": "9090",
				"mode": defaultMode,
			},
			expectError: false,
		},
		{
			name: "Returns error when config file is invalid",
			envVars: nil,
			configFileContent: `
				FREIGHTER_BACKEND_HOST = "config_host"
				FREIGHTER_BACKEND_PORT = invalid_int_value # Invalid TOML
			`,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testCmd := &cobra.Command{}
			testCmd.PersistentFlags().String("config-path", "", "Config path")
			testCmd.Flags().String("freighter-backend-host", defaultHost, "Host")
			testCmd.Flags().Int("freighter-backend-port", defaultPort, "Port")
			testCmd.Flags().String("mode", defaultMode, "Mode")

			// Read the config file
			if tc.configFileContent != "" {
				tempDir := t.TempDir() // Creates a temp dir, cleaned up automatically
				configFilePath := filepath.Join(tempDir, "testconfig.toml")
				err := os.WriteFile(configFilePath, []byte(tc.configFileContent), 0600)
				require.NoError(t, err, "Failed to write temp config file")
				// Add the config path flag ONLY if we created a file
				tc.cliArgs = append(tc.cliArgs, "--config-path", configFilePath)
			}

			// Read the command line arguments
			err := testCmd.ParseFlags(tc.cliArgs)
			require.NoError(t, err)

			// Read the environment variables
			for key, value := range tc.envVars {
				t.Setenv(key, value)
			}

			err = InitializeConfig(testCmd)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				for key, value := range tc.expectedValues {
					assert.Equal(t, value, testCmd.Flag(key).Value.String())
				}
			}
		})
	}
}
