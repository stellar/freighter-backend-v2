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
	// No t.Parallel(): t.Setenv is incompatible with parallel tests. Clear
	// DATABASE_URL so utils.InitializeConfig can't bind it into --database-url
	// from the surrounding shell, which would mask the missing-config path.
	t.Setenv("DATABASE_URL", "")

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

func TestServeCmd_DBEnabledDefaultsTrue(t *testing.T) {
	t.Parallel()

	cmd := (&ServeCmd{Cfg: &config.Config{}}).Command()
	dbEnabled, err := cmd.Flags().GetBool("db-enabled")
	require.NoError(t, err)
	assert.True(t, dbEnabled, "db-enabled should default to true")
}

func TestServeCmd_AllowsEmptyDatabaseURLWhenDBDisabled(t *testing.T) {
	// No t.Parallel(): t.Setenv is incompatible with parallel tests. Clear
	// DATABASE_URL so it can't be bound into --database-url from the shell.
	t.Setenv("DATABASE_URL", "")

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// With the DB disabled, an empty DATABASE_URL must NOT fail boot.
	cmd.SetArgs([]string{"--db-enabled=false"})

	require.NoError(t, cmd.Execute())
}

func TestServeCmd_BalancesEnabledDefaultsTrue(t *testing.T) {
	t.Parallel()

	cmd := (&ServeCmd{Cfg: &config.Config{}}).Command()
	balancesEnabled, err := cmd.Flags().GetBool("balances-enabled")
	require.NoError(t, err)
	assert.True(t, balancesEnabled, "balances-enabled should default to true")
}

// TestServeCmd_BalancesEnabledFalseFromEnv covers the path production actually
// uses: prd disables the account-balances route by setting BALANCES_ENABLED in the
// deployment, not by passing a CLI flag. The api-package tests set
// AppConfig.BalancesEnabled directly, so they would keep passing even if the flag
// were renamed or the env binding broke — and that failure is fail-OPEN: the
// endpoint would stay registered in prd while the manifest says it is off. This
// asserts the whole chain (env var -> viper AutomaticEnv with the '-'->'_'
// replacer -> bindFlags -> the bound config field). The literal
// "BALANCES_ENABLED" pins the env-var spelling kube depends on: renaming the flag
// to a different word breaks this test, while a cosmetic '-'/'_' swap does not,
// because the replacer maps both spellings onto the same variable.
func TestServeCmd_BalancesEnabledFalseFromEnv(t *testing.T) {
	// No t.Parallel(): t.Setenv is incompatible with parallel tests. DATABASE_URL is
	// cleared and the DB disabled so boot validation can't fail for unrelated reasons.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("BALANCES_ENABLED", "false")

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--db-enabled=false"})

	require.NoError(t, cmd.Execute())
	assert.False(t, serveCmd.Cfg.AppConfig.BalancesEnabled,
		"BALANCES_ENABLED=false must reach AppConfig.BalancesEnabled; a true here means prd would still serve the route")
}

// TestServeCmd_BalancesEnabledEnvTrueKeepsRouteOn is the other half: re-enabling in
// prd is done by flipping that same variable to "true" (or removing it), so the
// binding has to work in both directions rather than only ever latching off.
func TestServeCmd_BalancesEnabledEnvTrueKeepsRouteOn(t *testing.T) {
	// No t.Parallel(): see above.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("BALANCES_ENABLED", "true")

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--db-enabled=false"})

	require.NoError(t, cmd.Execute())
	assert.True(t, serveCmd.Cfg.AppConfig.BalancesEnabled)
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

func TestServeCmd_RejectsNegativeAuthClockSkewLeeway(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--auth-clock-skew-leeway", "-1s"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--auth-clock-skew-leeway=-1s must be >= 0 and <= 10m")
}

func TestServeCmd_RejectsAuthClockSkewLeewayAbove10m(t *testing.T) {
	t.Parallel()

	serveCmd := &ServeCmd{Cfg: &config.Config{}}
	cmd := serveCmd.Command()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--auth-clock-skew-leeway", "11m"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be >= 0 and <= 10m")
}

func TestServeCmd_AcceptsAuthClockSkewLeewayBoundaries(t *testing.T) {
	t.Parallel()

	// 0 and the 10m ceiling are both inclusive-valid.
	for _, leeway := range []string{"0s", "10m"} {
		serveCmd := &ServeCmd{Cfg: &config.Config{}}
		cmd := serveCmd.Command()
		cmd.RunE = func(*cobra.Command, []string) error { return nil }
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		// --database-url required to reach and pass the full validation chain.
		cmd.SetArgs([]string{"--auth-clock-skew-leeway", leeway, "--database-url", "postgres://localhost/test"})

		require.NoErrorf(t, cmd.Execute(), "leeway %s should be accepted", leeway)
	}
}
