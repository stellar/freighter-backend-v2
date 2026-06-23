package serve

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/stellar/freighter-backend-v2/internal/api"
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/services"
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
			if n := s.Cfg.AppConfig.MaxLedgerKeyAddresses; n > services.MaxLedgerEntryKeys {
				return fmt.Errorf("--max-ledger-key-addresses=%d exceeds upstream Stellar RPC ceiling of %d keys per getLedgerEntries call", n, services.MaxLedgerEntryKeys)
			}
			// Reject non-positive values explicitly: errgroup.SetLimit(0) would block
			// every goroutine, and SetLimit(-1) means unbounded — neither is what an
			// operator who passes 0 or a negative flag would expect.
			if n := s.Cfg.AppConfig.WalletBackendBalanceConcurrency; n <= 0 {
				return fmt.Errorf("--wallet-backend-balance-concurrency=%d must be positive", n)
			}
			if n := s.Cfg.PricesConfig.MaxTokensPerRequest; n <= 0 {
				return fmt.Errorf("--max-tokens-per-request=%d must be positive", n)
			}
			if n := s.Cfg.PricesConfig.PriceFetchTimeoutSeconds; n < 0 {
				return fmt.Errorf("--price-fetch-timeout-seconds=%d must be >= 0", n)
			}

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
	cmd.Flags().StringVar(&s.Cfg.AppConfig.MetricsHost, "metrics-host", "localhost", "The host of the internal metrics server (Prometheus /metrics)")
	cmd.Flags().IntVar(&s.Cfg.AppConfig.MetricsPort, "metrics-port", 9090, "The port of the internal metrics server (Prometheus /metrics)")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.Mode, "mode", "development", "The mode of the server")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.SentryKey, "sentry-key", "", "The Sentry key")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.ProtocolsConfigPath, "protocols-config-path", "/app/config/protocols.json", "The path to the protocols config file while lists all supported protocols in Freighter")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.MeridianPayTreasureHuntAddress, "meridian-pay-treasure-hunt-address", "", "The Meridian Pay Treasure Hunt collection address")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.MeridianPayTreasurePoapAddress, "meridian-pay-poap-address", "", "The Meridian Pay Poap collection address")
	cmd.Flags().StringVar(&s.Cfg.AppConfig.MeridianPayStellarHouseAddress, "meridian-pay-stellar-house-address", "", "The Meridian Pay Stellar House collection address")
	cmd.Flags().Int64Var(&s.Cfg.AppConfig.MaxRequestBodySize, "max-request-body-size", 1<<20, "Maximum request body size in bytes (default: 1MB)")
	cmd.Flags().IntVar(&s.Cfg.AppConfig.MaxBalanceAddresses, "max-balance-addresses", 100, "Maximum number of addresses allowed in account balances request")
	cmd.Flags().IntVar(&s.Cfg.AppConfig.MaxLedgerKeyAddresses, "max-ledger-key-addresses", 100, "Maximum number of public keys allowed in a ledger-key/accounts request")
	cmd.Flags().IntVar(&s.Cfg.AppConfig.WalletBackendBalanceConcurrency, "wallet-backend-balance-concurrency", 10, "Per-request maximum number of concurrent wallet-backend balance fetches (the /accounts/balances handler fans out to one accountByAddress call per address)")

	// RPC Config
	cmd.Flags().StringVar(&s.Cfg.RpcConfig.PubnetRpcUrl, "pubnet-rpc-url", "", "The Pubnet URL of the Pubnet RPC instance")
	cmd.Flags().StringVar(&s.Cfg.RpcConfig.TestnetRpcUrl, "testnet-rpc-url", "", "The Testnet URL of the Testnet RPC instance")
	cmd.Flags().StringVar(&s.Cfg.RpcConfig.FuturenetRpcUrl, "futurenet-rpc-url", "", "The Futurenet URL of the Futurenet RPC instance")
	cmd.Flags().IntVar(&s.Cfg.RpcConfig.MaxConcurrentRPCCalls, "max-concurrent-rpc-calls", 10, "Maximum number of concurrent RPC calls")

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

	// Wallet Backend Config
	cmd.Flags().StringVar(&s.Cfg.WalletBackendConfig.PubnetUrl, "wallet-backend-pubnet-url", "", "Wallet backend pubnet URL")
	cmd.Flags().StringVar(&s.Cfg.WalletBackendConfig.TestnetUrl, "wallet-backend-testnet-url", "", "Wallet backend testnet URL")
	cmd.Flags().StringVar(&s.Cfg.WalletBackendConfig.PubnetSigningKey, "wallet-backend-pubnet-signing-key", "", "Wallet backend pubnet JWT signing key (Stellar secret key)")
	cmd.Flags().StringVar(&s.Cfg.WalletBackendConfig.TestnetSigningKey, "wallet-backend-testnet-signing-key", "", "Wallet backend testnet JWT signing key (Stellar secret key)")

	// Token Prices Config
	cmd.Flags().StringVar(&s.Cfg.PricesConfig.StellarExpertPubnetURL, "stellar-expert-pubnet-url", "https://api.stellar.expert/explorer/public", "Stellar Expert base URL for pubnet")
	cmd.Flags().StringVar(&s.Cfg.PricesConfig.StellarExpertTestnetURL, "stellar-expert-testnet-url", "https://api.stellar.expert/explorer/testnet", "Stellar Expert base URL for testnet")
	cmd.Flags().StringVar(&s.Cfg.PricesConfig.StellarExpertAPIKey, "stellar-expert-api-key", "", "Bearer token for the Stellar Expert API (required)")
	cmd.Flags().StringVar(&s.Cfg.PricesConfig.StellarExpertOrigin, "stellar-expert-origin", "https://stellar.expert", "Origin header sent on Stellar Expert requests; Stellar Expert associates the API key with this origin (e.g. https://api.freighter.app in production)")
	cmd.Flags().IntVar(&s.Cfg.PricesConfig.PriceCacheTTLSeconds, "price-cache-ttl-seconds", 30, "TTL for cached token prices in Redis (seconds)")
	cmd.Flags().IntVar(&s.Cfg.PricesConfig.PriceFetchTimeoutSeconds, "price-fetch-timeout-seconds", 9, "Budget for uncached token price fetches before returning best-effort results (seconds)")
	cmd.Flags().IntVar(&s.Cfg.PricesConfig.MaxTokensPerRequest, "max-tokens-per-request", 1000, "Maximum tokens accepted in a single token-prices request")
	cmd.Flags().IntVar(&s.Cfg.PricesConfig.MaxConcurrentPriceFetches, "max-concurrent-price-fetches", 25, "Per-request token-in-flight cap; each token issues GetAsset and GetAssetCandles in parallel, so the upstream HTTP-call ceiling is up to 2× this value")
	return cmd
}

func (s *ServeCmd) Run() error {
	server := api.NewApiServer(s.Cfg)
	return server.Start()
}
