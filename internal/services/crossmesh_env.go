// ABOUTME: Pinned per-environment Cross Mesh values (USDC issuer, contract addresses) and tuning constants.
// ABOUTME: Decides whether a Stellar network has a configured Cross Mesh environment (key set and contracts pinned).
package services

import (
	"time"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	// CrossMeshChainsCacheTTL is how long a network's chains list is cached.
	// Cross Mesh asks integrators to cache it for a minute at most.
	CrossMeshChainsCacheTTL = 60 * time.Second
	// CrossMeshSettlementCacheTTL is how long a completed settlement is cached.
	CrossMeshSettlementCacheTTL = 24 * time.Hour
	// CrossMeshSettlementConcurrency caps concurrent settlement fetches per request.
	CrossMeshSettlementConcurrency = 2
)

// CrossMeshEnv holds the values pinned in this release for one Cross Mesh
// environment. Factory and Implementation are the EVM contracts every deposit
// address is derived from; they are pinned here rather than read from the API
// so the backend can verify the addresses the API returns.
type CrossMeshEnv struct {
	USDCIssuer     string
	Factory        string
	Implementation string
}

// crossMeshEnvs maps each Stellar network to its Cross Mesh environment.
// Mainnet's contracts stay empty until Cross Mesh confirms them, which keeps
// pubnet unconfigured.
var crossMeshEnvs = map[string]CrossMeshEnv{
	types.PUBLIC: {
		USDCIssuer: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	},
	types.TESTNET: {
		USDCIssuer:     "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
		Factory:        "0xAD93f57DeA4F6bE812c0e1790dF5D3d1161BEd09",
		Implementation: "0xb0216D95AFf228EB8b3A53C58f780D115eFfEC0F",
	},
}

// CrossMeshEnvFor returns the pinned environment for a Stellar network, and
// false for a network Cross Mesh does not serve.
func CrossMeshEnvFor(network string) (CrossMeshEnv, bool) {
	env, ok := crossMeshEnvs[network]
	return env, ok
}

// CrossMeshConfigured reports whether network has a configured Cross Mesh
// environment: its API key is set and both contract addresses are pinned.
func CrossMeshConfigured(cfg config.CrossMeshConfig, network string) bool {
	env, ok := CrossMeshEnvFor(network)
	_, key := crossMeshEndpoint(cfg, network)
	return ok && key != "" && env.Factory != "" && env.Implementation != ""
}

// crossMeshEndpoint returns the base URL and API key serving network, or
// empty strings for a network Cross Mesh does not serve.
func crossMeshEndpoint(cfg config.CrossMeshConfig, network string) (baseURL, apiKey string) {
	switch network {
	case types.PUBLIC:
		return cfg.MainnetURL, cfg.MainnetAPIKey
	case types.TESTNET:
		return cfg.TestnetURL, cfg.TestnetAPIKey
	default:
		return "", ""
	}
}
