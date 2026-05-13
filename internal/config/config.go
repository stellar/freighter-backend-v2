package config

type Config struct {
	AppConfig           AppConfig
	RpcConfig           RPCConfig
	RedisConfig         RedisConfig
	HorizonConfig       HorizonConfig
	PricesConfig        PricesConfig
	BlockaidConfig      BlockaidConfig
	CoinbaseConfig      CoinbaseConfig
	WalletBackendConfig WalletBackendConfig
}

type AppConfig struct {
	FreighterBackendHost           string
	FreighterBackendPort           int
	MetricsHost                    string
	MetricsPort                    int
	Mode                           string
	SentryKey                      string
	ProtocolsConfigPath            string
	MeridianPayTreasureHuntAddress string
	MeridianPayTreasurePoapAddress string
	MeridianPayStellarHouseAddress string
	MaxRequestBodySize             int64
	MaxBalanceAddresses            int
	MaxLedgerKeyAddresses          int
	// WalletBackendBalanceConcurrency caps the number of concurrent wallet-backend
	// fetches per single /api/v1/accounts/balances request. The handler fans out to
	// the per-address accountByAddress query, and this knob bounds the goroutine
	// count for that fan-out. The limit is enforced per-request via errgroup.SetLimit,
	// so peak upstream load is concurrent_requests * WalletBackendBalanceConcurrency.
	WalletBackendBalanceConcurrency int
	// AccountHistoryDefaultLimit is the default page size for the
	// /api/v1/accounts/{address}/transactions, /operations, and /state-changes
	// endpoints when the client does not pass ?limit=. Must be > 0 and
	// <= AccountHistoryMaxLimit.
	AccountHistoryDefaultLimit int
	// AccountHistoryMaxLimit is the maximum page size accepted by the same
	// endpoints. Requests above this limit are rejected with 400. Must be > 0.
	AccountHistoryMaxLimit int
}

type RPCConfig struct {
	PubnetRpcUrl          string
	TestnetRpcUrl         string
	FuturenetRpcUrl       string
	MaxConcurrentRPCCalls int
}

type RedisConfig struct {
	ConnectionName string
	Host           string
	Port           int
	Password       string
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

type WalletBackendConfig struct {
	PubnetUrl         string
	TestnetUrl        string
	PubnetSigningKey  string
	TestnetSigningKey string
}
