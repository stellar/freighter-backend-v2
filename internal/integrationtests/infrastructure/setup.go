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
	freighterBackendImage       string
	crossMeshImage              string
	TestNetwork                 *testcontainers.DockerNetwork
	RedisContainer              *redis.RedisContainer
	SuspendedRedisContainer     *redis.RedisContainer
	PostgresContainer           *TestContainer
	AppPostgresContainer        *TestContainer
	StellarCoreContainer        *TestContainer
	RpcContainer                *TestContainer
	CrossMeshContainer          *TestContainer
	SuspendedCrossMeshContainer *TestContainer
	FreighterContainer          *FreighterBackendContainer
	SuspendedFreighterContainer *FreighterBackendContainer
}

// initializeContainerInfrastructure builds the backend and fake Cross Mesh
// images and starts all infrastructure containers (network, Redis, Postgres,
// Core, RPC, fake Cross Mesh).
func (s *SharedContainers) initializeContainerInfrastructure(ctx context.Context) error {
	var err error

	// Build freighter-backend image first so Core has time to advance ledgers
	s.freighterBackendImage, err = ensureImage(ctx, "freighter-backend", FreighterContainerTag, FreighterDockerfilePath)
	if err != nil {
		return fmt.Errorf("ensuring freighter backend image: %w", err)
	}
	s.crossMeshImage, err = ensureImage(ctx, "fakecrossmesh", FreighterContainerTag, CrossMeshDockerfilePath)
	if err != nil {
		return fmt.Errorf("ensuring fake cross mesh image: %w", err)
	}

	// Create network
	s.TestNetwork, err = network.New(ctx)
	if err != nil {
		return fmt.Errorf("creating test network: %w", err)
	}

	// Start Redis
	s.RedisContainer, err = createRedisContainer(ctx, RedisContainerName, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating redis container: %w", err)
	}
	s.SuspendedRedisContainer, err = createRedisContainer(ctx, SuspendedRedisContainerName, s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating suspended redis container: %w", err)
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

	// Start the fake Cross Mesh twice: normal, and with every chain suspended
	s.CrossMeshContainer, err = createCrossMeshContainer(ctx, CrossMeshContainerName, s.crossMeshImage, "", s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating fake cross mesh container: %w", err)
	}
	s.SuspendedCrossMeshContainer, err = createCrossMeshContainer(ctx, SuspendedCrossMeshContainerName, s.crossMeshImage, "all_suspended", s.TestNetwork)
	if err != nil {
		return fmt.Errorf("creating suspended fake cross mesh container: %w", err)
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
	shared.FreighterContainer, err = createFreighterContainer(ctx, FreighterContainerName, shared.freighterBackendImage,
		RedisContainerName, crossMeshURL(CrossMeshContainerName), shared.TestNetwork)
	require.NoError(t, err, "failed to create freighter container")
	shared.SuspendedFreighterContainer, err = createFreighterContainer(ctx, SuspendedFreighterContainerName, shared.freighterBackendImage,
		SuspendedRedisContainerName, crossMeshURL(SuspendedCrossMeshContainerName), shared.TestNetwork)
	require.NoError(t, err, "failed to create suspended freighter container")

	return shared
}

// crossMeshURL is the fake Cross Mesh's base URL on the test network.
func crossMeshURL(name string) string {
	return fmt.Sprintf("http://%s:%s", name, CrossMeshContainerPort)
}

// Cleanup terminates all containers and removes the test network.
func (s *SharedContainers) Cleanup(ctx context.Context) {
	if s.FreighterContainer != nil {
		_ = s.FreighterContainer.Terminate(ctx)
	}
	if s.SuspendedFreighterContainer != nil {
		_ = s.SuspendedFreighterContainer.Terminate(ctx)
	}
	if s.CrossMeshContainer != nil {
		_ = s.CrossMeshContainer.Terminate(ctx)
	}
	if s.SuspendedCrossMeshContainer != nil {
		_ = s.SuspendedCrossMeshContainer.Terminate(ctx)
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
	if s.SuspendedRedisContainer != nil {
		_ = s.SuspendedRedisContainer.Terminate(ctx)
	}
	if s.TestNetwork != nil {
		_ = s.TestNetwork.Remove(ctx)
	}
}
