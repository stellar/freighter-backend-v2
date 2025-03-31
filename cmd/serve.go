package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type AppConfig struct {
	Mode      string
	SentryKey string
}

type RPCConfig struct {
	RpcPubnetURL  string
	RpcTestnetURL string
}

type RedisConfig struct {
	ConnectionName string
	Host           string
	Port           int
}

type HorizonConfig struct {
	HorizonPubnetURL  string
	HorizonTestnetURL string
}

type PricesConfig struct {
	HorizonURL                     string
	DisableTokenPrices             bool
	BatchUpdateDelayMilliseconds   int
	CalculationTimeoutMilliseconds int
	UpdateIntervalMilliseconds     int
	UpdateBatchSize                int
	StalenessThreshold             int
}

type BlockaidConfig struct {
	BlockaidAPIKey                         string
	UseBlockaidDappScanning                bool
	UseBlockaidTxScanning                  bool
	UseBlockaidAssetScanning               bool
	UseBlockaidAssetWarningReporting       bool
	UseBlockaidTransactionWarningReporting bool
}

type CoinbaseConfig struct {
	CoinbaseAPIKey    string
	CoinbaseAPISecret string
}

type serveCmd struct {
	cfg *Config
}

func (s *serveCmd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Start the server",
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initializeConfig(cmd)
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return s.Run()
		},
	}

	// App Config
	cmd.Flags().StringVar(&s.cfg.AppConfig.Mode, "mode", "development", "The mode of the server")
	cmd.Flags().StringVar(&s.cfg.AppConfig.SentryKey, "sentry-key", "", "The Sentry key")

	// RPC Config
	cmd.Flags().StringVar(&s.cfg.RpcConfig.RpcPubnetURL, "rpc-pubnet-url", "", "The URL of the pubnet RPC")
	cmd.Flags().StringVar(&s.cfg.RpcConfig.RpcTestnetURL, "rpc-testnet-url", "https://soroban-testnet.stellar.org/", "The URL of the testnet RPC")

	// Horizon Config
	cmd.Flags().StringVar(&s.cfg.HorizonConfig.HorizonPubnetURL, "horizon-pubnet-url", "https://horizon.stellar.org/", "The URL of the pubnet Horizon")
	cmd.Flags().StringVar(&s.cfg.HorizonConfig.HorizonTestnetURL, "horizon-testnet-url", "https://horizon-testnet.stellar.org", "The URL of the testnet Horizon")

	// Redis Config
	cmd.Flags().StringVar(&s.cfg.RedisConfig.ConnectionName, "redis-connection-name", "freighter-redis", "The name of the Redis connection")
	cmd.Flags().StringVar(&s.cfg.RedisConfig.Host, "redis-host", "localhost", "The Redis host")
	cmd.Flags().IntVar(&s.cfg.RedisConfig.Port, "redis-port", 6379, "The Redis port")

	// Prices Config
	cmd.Flags().BoolVar(&s.cfg.PricesConfig.DisableTokenPrices, "disable-token-prices", false, "Disable token prices")
	cmd.Flags().StringVar(&s.cfg.PricesConfig.HorizonURL, "token-prices-horizon-url", "https://horizon.stellar.org", "The URL of the Horizon")
	cmd.Flags().IntVar(&s.cfg.PricesConfig.BatchUpdateDelayMilliseconds, "token-prices-batch-update-delay", 5000, "Delay between batch updates in milliseconds")
	cmd.Flags().IntVar(&s.cfg.PricesConfig.CalculationTimeoutMilliseconds, "token-prices-calculation-timeout", 10000, "Timeout for price calculations in milliseconds")
	cmd.Flags().IntVar(&s.cfg.PricesConfig.UpdateIntervalMilliseconds, "token-prices-update-interval", 30000, "Interval between price updates in milliseconds")
	cmd.Flags().IntVar(&s.cfg.PricesConfig.UpdateBatchSize, "token-prices-update-batch-size", 50, "Size of price update batches")
	cmd.Flags().IntVar(&s.cfg.PricesConfig.StalenessThreshold, "token-prices-staleness-threshold", 300000, "Threshold for price staleness")

	// Blockaid Config
	cmd.Flags().StringVar(&s.cfg.BlockaidConfig.BlockaidAPIKey, "blockaid-api-key", "", "Blockaid API key")
	cmd.Flags().BoolVar(&s.cfg.BlockaidConfig.UseBlockaidDappScanning, "use-blockaid-dapp-scanning", false, "Enable Blockaid dapp scanning")
	cmd.Flags().BoolVar(&s.cfg.BlockaidConfig.UseBlockaidTxScanning, "use-blockaid-tx-scanning", false, "Enable Blockaid transaction scanning")
	cmd.Flags().BoolVar(&s.cfg.BlockaidConfig.UseBlockaidAssetScanning, "use-blockaid-asset-scanning", false, "Enable Blockaid asset scanning")
	cmd.Flags().BoolVar(&s.cfg.BlockaidConfig.UseBlockaidAssetWarningReporting, "use-blockaid-asset-warning-reporting", false, "Enable Blockaid asset warning reporting")
	cmd.Flags().BoolVar(&s.cfg.BlockaidConfig.UseBlockaidTransactionWarningReporting, "use-blockaid-transaction-warning-reporting", false, "Enable Blockaid transaction warning reporting")

	// Coinbase Config
	cmd.Flags().StringVar(&s.cfg.CoinbaseConfig.CoinbaseAPIKey, "coinbase-api-key", "", "Coinbase API key")
	cmd.Flags().StringVar(&s.cfg.CoinbaseConfig.CoinbaseAPISecret, "coinbase-api-secret", "", "Coinbase API secret")
	return cmd
}

func (s *serveCmd) Run() error {
	fmt.Println(s.cfg)
	return nil
}
