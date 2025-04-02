package serve

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeCmd_Command(t *testing.T) {
	t.Parallel()

	mockConfig := &config.Config{
		AppConfig: config.AppConfig{
			FreighterBackendHost: "test_host",
			FreighterBackendPort: 3002,
			Mode:                 "test_mode",
		},
	}

	serveCmd := &ServeCmd{
		Cfg: mockConfig,
	}

	cmd := serveCmd.Command()
	assert.Equal(t, "serve", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.True(t, cmd.Flags().HasFlags())

	// Test flags are registered
	hostFlag, _ := cmd.Flags().GetString("freighter-backend-host")
	assert.Equal(t, mockConfig.AppConfig.FreighterBackendHost, hostFlag)
	portFlag, _ := cmd.Flags().GetInt("freighter-backend-port")
	assert.Equal(t, mockConfig.AppConfig.FreighterBackendPort, portFlag)
	modeFlag, _ := cmd.Flags().GetString("mode")
	assert.Equal(t, mockConfig.AppConfig.Mode, modeFlag)

	// Test flag default values
	redisHostFlag, _ := cmd.Flags().GetString("redis-host")
	assert.Equal(t, "localhost", redisHostFlag)
	redisPortFlag, _ := cmd.Flags().GetInt("redis-port")
	assert.Equal(t, 6379, redisPortFlag)
}

func TestServeCmd_Execute(t *testing.T) {
	t.Parallel()

	// Override the RunE function for the test
	var configUsed bool

	serveCmd := &ServeCmd{
		Cfg: &config.Config{},
	}

	// Get the command but override its RunE function
	cmd := serveCmd.Command()
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		configUsed = true

		// This will print to the buffer you set with cmd.SetOut(b)
		fmt.Fprintf(cmd.OutOrStdout(), "freighter-backend-host=%s\n", cmd.Flag("freighter-backend-host").Value)
		fmt.Fprintf(cmd.OutOrStdout(), "mode=%s\n", cmd.Flag("mode").Value)

		return nil
	}

	b := bytes.NewBufferString("")
	cmd.SetOut(b)
	cmd.SetArgs([]string{"--freighter-backend-host", "test_host", "--mode", "test_mode"})
	err := cmd.Execute()
	require.NoError(t, err)

	out, err := io.ReadAll(b)
	require.NoError(t, err)
	assert.Contains(t, string(out), "freighter-backend-host=test_host")
	assert.Contains(t, string(out), "mode=test_mode")
	assert.True(t, configUsed)
}
