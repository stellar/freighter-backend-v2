// ABOUTME: Orchestrates integration test container infrastructure lifecycle.
// ABOUTME: Provides SharedContainers for setup, initialization, and cleanup of all test containers.
package infrastructure

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/network"
)

// SharedContainers holds all containers used across integration test suites.
type SharedContainers struct {
	freighterBackendImage string
	TestNetwork           *testcontainers.DockerNetwork
	RedisContainer        *redis.RedisContainer
	PostgresContainer     *TestContainer
	AppPostgresContainer  *TestContainer
	StellarCoreContainer  *TestContainer
	RpcContainer          *TestContainer
	FreighterContainer    *FreighterBackendContainer
}

// initializeContainerInfrastructure builds the backend image and starts all
// infrastructure containers (network, Redis, Postgres, Core, RPC).
func (s *SharedContainers) initializeContainerInfrastructure(ctx context.Context) error {
	var err error

	// Build freighter-backend image first so Core has time to advance ledgers
	s.freighterBackendImage, err = ensureFreighterBackendImage(ctx, FreighterContainerTag)
	if err != nil {
		return fmt.Errorf("ensuring freighter backend image: %w", err)
	}

	// Create network
	s.TestNetwork, err = network.New(ctx)
	if err != nil {
		return fmt.Errorf("creating test network: %w", err)
	}

	// Start Redis
	s.RedisContainer, err = createRedisContainer(ctx, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating redis container: %w", err)
	}

	// Start PostgreSQL for Stellar Core
	s.PostgresContainer, err = createPostgresContainer(ctx, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating core DB container: %w", err)
	}

	// Start the app's PostgreSQL (freighter-backend-v2 connects to this via
	// DATABASE_URL; serve pings it on boot).
	s.AppPostgresContainer, err = createAppPostgresContainer(ctx, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating app DB container: %w", err)
	}

	// Start Stellar Core
	s.StellarCoreContainer, err = createStellarCoreContainer(ctx, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating Stellar Core container: %w", err)
	}

	// Start Stellar RPC
	s.RpcContainer, err = createRPCContainer(ctx, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating RPC container: %w", err)
	}

	return nil
}

// NewSharedContainers creates all containers needed for integration tests.
func NewSharedContainers(t *testing.T) *SharedContainers {
	shared := &SharedContainers{}
	ctx := context.Background()

	err := shared.initializeContainerInfrastructure(ctx)
	require.NoError(t, err, "failed to initialize container infrastructure")

	// Start Freighter backend (uses pre-built image)
	shared.FreighterContainer, err = createFreighterContainer(ctx, FreighterContainerName, shared.freighterBackendImage, shared.TestNetwork)
	require.NoError(t, err, "failed to create freighter container")

	return shared
}

// Cleanup terminates all containers and removes the test network.
func (s *SharedContainers) Cleanup(ctx context.Context) {
	if s.FreighterContainer != nil {
		_ = s.FreighterContainer.Terminate(ctx)
	}
	if s.RpcContainer != nil {
		_ = s.RpcContainer.Terminate(ctx)
	}
	if s.StellarCoreContainer != nil {
		_ = s.StellarCoreContainer.Terminate(ctx)
	}
	if s.AppPostgresContainer != nil {
		_ = s.AppPostgresContainer.Terminate(ctx)
	}
	if s.PostgresContainer != nil {
		_ = s.PostgresContainer.Terminate(ctx)
	}
	if s.RedisContainer != nil {
		_ = s.RedisContainer.Terminate(ctx)
	}
	if s.TestNetwork != nil {
		_ = s.TestNetwork.Remove(ctx)
	}
}
