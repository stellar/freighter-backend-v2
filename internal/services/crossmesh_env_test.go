// ABOUTME: Tests for the Cross Mesh per-environment constants and the configured-network rule.
// ABOUTME: Covers TR-2: only networks with an API key and pinned contract addresses are configured.
package services

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestCrossMeshConfig_TR2_ConfiguredNetworks(t *testing.T) {
	t.Parallel()

	withKeys := config.CrossMeshConfig{MainnetAPIKey: "mainnet-key", TestnetAPIKey: "testnet-key"}

	cases := []struct {
		name    string
		cfg     config.CrossMeshConfig
		network string
		want    bool
	}{
		{"pubnet with key but no pinned contracts", withKeys, types.PUBLIC, false},
		{"testnet with key", withKeys, types.TESTNET, true},
		{"testnet without key", config.CrossMeshConfig{}, types.TESTNET, false},
		{"futurenet is never configured", withKeys, types.FUTURENET, false},
		{"unknown network", withKeys, "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CrossMeshConfigured(tc.cfg, tc.network))
		})
	}
}
