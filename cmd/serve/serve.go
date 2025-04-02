package serve

import (
	"github.com/spf13/cobra"
	"github.com/stellar/freighter-backend-v2/internal/api"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

type ServeCmd struct {
	Cfg *config.Config
}

func (s *ServeCmd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Start the server",
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := utils.InitializeConfig(cmd); err != nil {
				return err
			}
			logger.Info("Initializing server with config", "config", s.Cfg)
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return s.Run()
		},
	}

	// Add a persistent flag for the config file path
	// This needs to be persistent so it's available in PersistentPreRunE
	var configFilePath string
	cmd.PersistentFlags().StringVar(&configFilePath, "config-path", "", "Path to config file (e.g., /etc/freighter/config.toml)")

	// App Config
	cmd.Flags().StringVar(&s.Cfg.AppConfig.FreighterBackendHost, "freighter-backend-host", "localhost", "The host of the freighter backend server")
	cmd.Flags().IntVar(&s.Cfg.AppConfig.FreighterBackendPort, "freighter-backend-port", 3002, "The port of the freighter backend server")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.Mode, "mode", "development", "The mode of the server")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.SentryKey, "sentry-key", "", "The Sentry key")

	// RPC Config
	cmd.Flags().StringVar(&s.Cfg.RpcConfig.RpcPubnetURL, "rpc-pubnet-url", "", "The URL of the pubnet RPC")
	cmd.Flags().StringVar(&s.Cfg.RpcConfig.RpcTestnetURL, "rpc-testnet-url", "https://soroban-testnet.stellar.org/", "The URL of the testnet RPC")

	// Horizon Config
	cmd.Flags().StringVar(&s.Cfg.HorizonConfig.HorizonPubnetURL, "horizon-pubnet-url", "https://horizon.stellar.org/", "The URL of the pubnet Horizon")
	cmd.Flags().StringVar(&s.Cfg.HorizonConfig.HorizonTestnetURL, "horizon-testnet-url", "https://horizon-testnet.stellar.org", "The URL of the testnet Horizon")

	// Redis Config
	cmd.Flags().StringVar(&s.Cfg.RedisConfig.ConnectionName, "redis-connection-name", "freighter-redis", "The name of the Redis connection")
	cmd.Flags().StringVar(&s.Cfg.RedisConfig.Host, "redis-host", "localhost", "The Redis host")
	cmd.Flags().IntVar(&s.Cfg.RedisConfig.Port, "redis-port", 6379, "The Redis port")
	cmd.Flags().StringVar(&s.Cfg.RedisConfig.Password, "redis-password", "", "Redis password")

	// Blockaid Config
	cmd.Flags().StringVar(&s.Cfg.BlockaidConfig.BlockaidAPIKey, "blockaid-api-key", "", "Blockaid API key")
	cmd.Flags().BoolVar(&s.Cfg.BlockaidConfig.UseBlockaidDappScanning, "use-blockaid-dapp-scanning", false, "Enable Blockaid dapp scanning")
	cmd.Flags().BoolVar(&s.Cfg.BlockaidConfig.UseBlockaidTxScanning, "use-blockaid-tx-scanning", false, "Enable Blockaid transaction scanning")
	cmd.Flags().BoolVar(&s.Cfg.BlockaidConfig.UseBlockaidAssetScanning, "use-blockaid-asset-scanning", false, "Enable Blockaid asset scanning")
	cmd.Flags().BoolVar(&s.Cfg.BlockaidConfig.UseBlockaidAssetWarningReporting, "use-blockaid-asset-warning-reporting", false, "Enable Blockaid asset warning reporting")
	cmd.Flags().BoolVar(&s.Cfg.BlockaidConfig.UseBlockaidTransactionWarningReporting, "use-blockaid-transaction-warning-reporting", false, "Enable Blockaid transaction warning reporting")

	// Coinbase Config
	cmd.Flags().StringVar(&s.Cfg.CoinbaseConfig.CoinbaseAPIKey, "coinbase-api-key", "", "Coinbase API key")
	cmd.Flags().StringVar(&s.Cfg.CoinbaseConfig.CoinbaseAPISecret, "coinbase-api-secret", "", "Coinbase API secret")
	return cmd
}

func (s *ServeCmd) Run() error {
	server := api.NewApiServer(s.Cfg)
	return server.Start()
}
