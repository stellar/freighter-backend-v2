package config

type Config struct {
	RpcConfig RPCConfig
	RedisConfig RedisConfig
	HorizonConfig HorizonConfig
	PricesConfig PricesConfig
	BlockaidConfig BlockaidConfig
	CoinbaseConfig CoinbaseConfig
}

type RPCConfig struct {
	RpcPubnetURL string
	RpcTestnetURL string
}

type RedisConfig struct {
	ConnectionName string
	Host string
	Port int
}

type HorizonConfig struct {
	HorizonPubnetURL string
	HorizonTestnetURL string
}

type PricesConfig struct {
	BatchUpdateDelayMilliseconds int
	CalculationTimeoutMilliseconds int
	UpdateIntervalMilliseconds int
	UpdateBatchSize int
	PriceStalenessThreshold int
}

type BlockaidConfig struct {
	UseBlockaidDappScanning bool
	UseBlockaidTxScanning bool
	UseBlockaidAssetScanning bool
	UseBlockaidAssetWarningReporting bool
	UseBlockaidTransactionWarningReporting bool
}

type CoinbaseConfig struct {
	CoinbaseAPIKey string
	CoinbaseAPISecret string
}