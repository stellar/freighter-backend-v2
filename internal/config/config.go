package config

type Config struct {
	AppConfig      AppConfig
	RpcConfig      RPCConfig
	RedisConfig    RedisConfig
	HorizonConfig  HorizonConfig
	PricesConfig   PricesConfig
	BlockaidConfig BlockaidConfig
	CoinbaseConfig CoinbaseConfig
}

type AppConfig struct {
	FreighterBackendHost string
	FreighterBackendPort int
	Mode                 string
	SentryKey            string
	ProtocolsConfigPath  string
}

type RPCConfig struct {
	RpcUrl string
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
