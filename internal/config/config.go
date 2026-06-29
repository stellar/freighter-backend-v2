package config

import (
	"errors"
	"fmt"
	"math"
	"time"
)

type Config struct {
	AppConfig           AppConfig
	RpcConfig           RPCConfig
	RedisConfig         RedisConfig
	DatabaseConfig      DatabaseConfig
	HorizonConfig       HorizonConfig
	PricesConfig        PricesConfig
	BlockaidConfig      BlockaidConfig
	CoinbaseConfig      CoinbaseConfig
	WalletBackendConfig WalletBackendConfig
}

type AppConfig struct {
	FreighterBackendHost string
	FreighterBackendPort int
	MetricsHost          string
	MetricsPort          int
	Mode                 string
	// AuthMode selects JWT enforcement for gated routes: "permissive" (allow
	// requests with no token through, reject invalid tokens) or "strict"
	// (require a valid token). Parsed via auth.ParseMode; validated at startup.
	AuthMode                       string
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
	// GET /api/v1/accounts/{address}/transactions endpoint when the client
	// does not pass ?limit=. Must be > 0 and <= AccountHistoryMaxLimit.
	AccountHistoryDefaultLimit int
	// AccountHistoryMaxLimit is the maximum page size accepted by that
	// endpoint. Requests above it are rejected with 400. Must be > 0 and
	// <= 100 (the wallet-backend upstream page-size cap).
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

// DatabaseConfig holds the PostgreSQL connection string and pgx pool tunables.
// URL is sourced from DATABASE_URL (via ExternalSecrets in deployed envs).
type DatabaseConfig struct {
	URL             string
	MaxConns        int
	MinConns        int
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// Validate checks required database configuration. The database is a hard
// dependency, so an empty URL is rejected. Shared by the `serve` and `migrate`
// commands so both fail fast with the same message.
func (c DatabaseConfig) Validate() error {
	if c.URL == "" {
		return errors.New("--database-url (env DATABASE_URL) is required")
	}
	return nil
}

// ValidatePoolConfig checks the pgx pool tuning knobs. Called only by `serve`,
// which sources these from flags; `migrate` opens a short-lived pool with the
// package defaults and so does not need it. The bounds prevent a pool that pgx
// would reject at startup (or that would silently misbehave once the int fields
// are narrowed to the int32 the pgx API takes).
func (c DatabaseConfig) ValidatePoolConfig() error {
	switch {
	case c.MaxConns < 1:
		return fmt.Errorf("--db-max-conns=%d must be >= 1", c.MaxConns)
	case c.MaxConns > math.MaxInt32:
		return fmt.Errorf("--db-max-conns=%d exceeds the maximum of %d", c.MaxConns, math.MaxInt32)
	case c.MinConns < 0:
		return fmt.Errorf("--db-min-conns=%d must be >= 0", c.MinConns)
	case c.MinConns > c.MaxConns:
		return fmt.Errorf("--db-min-conns=%d must be <= --db-max-conns=%d", c.MinConns, c.MaxConns)
	}
	return nil
}

type HorizonConfig struct {
	HorizonPubnetURL  string
	HorizonTestnetURL string
}

type PricesConfig struct {
	StellarExpertPubnetURL    string
	StellarExpertTestnetURL   string
	StellarExpertAPIKey       string
	StellarExpertOrigin       string
	PriceCacheTTLSeconds      int
	PriceFetchTimeoutSeconds  int
	MaxTokensPerRequest       int
	MaxConcurrentPriceFetches int
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
