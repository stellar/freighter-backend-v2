package serve

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/services"
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
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "freighter-backend-host=%s\n", cmd.Flag("freighter-backend-host").Value)
		require.NoError(t, err)
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "mode=%s\n", cmd.Flag("mode").Value)
		require.NoError(t, err)

		return nil
	}

	b := bytes.NewBufferString("")
	cmd.SetOut(b)
	cmd.SetArgs([]string{"--freighter-backend-host", "test_host", "--mode", "test_mode", "--database-url", "postgres://localhost/test"})
	err := cmd.Execute()
	require.NoError(t, err)

	out, err := io.ReadAll(b)
	require.NoError(t, err)
	assert.Contains(t, string(out), "freighter-backend-host=test_host")
	assert.Contains(t, string(out), "mode=test_mode")
	assert.True(t, configUsed)
}

func TestServeCmd_RejectsEmptyDatabaseURL(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// No --database-url provided: the DB is a hard dependency, so boot must fail fast.
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database-url")
}

func TestServeCmd_RejectsMaxLedgerKeyAddressesAboveUpstreamCeiling(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--max-ledger-key-addresses", fmt.Sprintf("%d", services.MaxLedgerEntryKeys+1)})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds upstream Stellar RPC ceiling")
}

func TestServeCmd_AcceptsMaxLedgerKeyAddressesAtUpstreamCeiling(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--max-ledger-key-addresses", fmt.Sprintf("%d", services.MaxLedgerEntryKeys), "--database-url", "postgres://localhost/test"})

	require.NoError(t, cmd.Execute())
}

func TestServeCmd_RejectsNonPositiveMaxTokensPerRequest(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--max-tokens-per-request", "0"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--max-tokens-per-request=0 must be positive")
}

func TestServeCmd_RejectsNegativePriceFetchTimeout(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--price-fetch-timeout-seconds", "-1"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--price-fetch-timeout-seconds=-1 must be >= 0")
}

func TestServeCmd_RejectsAccountHistoryMaxLimitAbove100(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--account-history-default-limit", "20",
		"--account-history-max-limit", "101",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max <= 100")
}

func TestServeCmd_RejectsInvalidAuthMode(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--auth-mode", "bogus"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid auth mode")
}

func TestServeCmd_AcceptsStrictAuthMode(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// --database-url is required (the DB is a hard dependency), so supply one here
	// to reach and exercise the auth-mode validation.
	cmd.SetArgs([]string{"--auth-mode", "strict", "--database-url", "postgres://localhost/test"})

	require.NoError(t, cmd.Execute())
}
