// ABOUTME: Provides container management utilities for integration tests.
// ABOUTME: Includes both shared suite-level containers and helper functions.

package infrastructure

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	RedisContainerName         = "redis"
	RedisContainerPort         = "6379"
	RedisContainerImage        = "redis:7-alpine"
	FreighterContainerName     = "freighter"
	FreighterContainerHost     = "0.0.0.0"
	FreighterContainerPort     = "3002"
	FreighterContainerTag      = "integration-test"
	FreighterDockerfilePath    = "deployments/Dockerfile"
	FreighterDockerfileContext = "../../"
)

// FreighterBackendContainer wraps a testcontainer with connection string helper
type FreighterBackendContainer struct {
	testcontainers.Container
	ConnectionString string
}

// GetConnectionString returns the HTTP connection string for the container
func (c *FreighterBackendContainer) GetConnectionString(ctx context.Context) (string, error) {
	host, err := c.Host(ctx)
	if err != nil {
		return "", err
	}

	port, err := c.MappedPort(ctx, FreighterContainerPort)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("http://%s:%s", host, port.Port()), nil
}

// BaseTestSuite provides shared container management for integration tests
type SharedContainers struct {
	TestNetwork          *testcontainers.DockerNetwork
	RedisContainer       *redis.RedisContainer
	PostgresContainer    *testcontainers.Container
	StellarCoreContainer *testcontainers.Container
	RpcContainer         *testcontainers.Container
	FreighterContainer   *FreighterBackendContainer
}

func NewSharedContainers(t *testing.T) *SharedContainers {
	shared := &SharedContainers{}

	ctx := context.Background()

	// Create network
	var err error
	shared.TestNetwork, err = network.New(ctx)
	require.NoError(t, err)

	// Start Redis
	shared.RedisContainer, err = createRedisContainer(ctx, shared.TestNetwork)
	require.NoError(t, err)

	// Start PostgreSQL for Stellar Core
	shared.PostgresContainer, err = createPostgresContainer(ctx, shared.TestNetwork)
	require.NoError(t, err)

	// Start Stellar Core
	shared.StellarCoreContainer, err = createStellarCoreContainer(ctx, shared.TestNetwork)
	require.NoError(t, err)

	// Start Stellar RPC
	shared.RpcContainer, err = createRPCContainer(ctx, shared.TestNetwork)
	require.NoError(t, err)

	// Start Freighter backend
	shared.FreighterContainer, err = createFreighterContainer(ctx, FreighterContainerName, FreighterContainerTag, shared.TestNetwork)
	require.NoError(t, err)

	return shared
}

// TearDownSuite cleans up shared containers after all tests complete
func (s *SharedContainers) Cleanup(ctx context.Context) {
	if s.FreighterContainer != nil {
		_ = s.FreighterContainer.Terminate(ctx)
	}
	if s.RpcContainer != nil {
		_ = (*s.RpcContainer).Terminate(ctx)
	}
	if s.StellarCoreContainer != nil {
		_ = (*s.StellarCoreContainer).Terminate(ctx)
	}
	if s.PostgresContainer != nil {
		_ = (*s.PostgresContainer).Terminate(ctx)
	}
	if s.RedisContainer != nil {
		_ = s.RedisContainer.Terminate(ctx)
	}
	if s.TestNetwork != nil {
		_ = s.TestNetwork.Remove(ctx)
	}
}

// createRedisContainer starts a Redis container for testing
func createRedisContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*redis.RedisContainer, error) {
	return redis.Run(ctx,
		RedisContainerImage,
		network.WithNetwork([]string{RedisContainerName}, testNetwork),
	)
}

// createRPCContainer starts a Stellar RPC container for testing
func createRPCContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*testcontainers.Container, error) {
	// Get the directory of the current source file
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)

	containerRequest := testcontainers.ContainerRequest{
		Name:  "stellar-rpc",
		Image: "stellar/stellar-rpc:latest",
		Labels: map[string]string{
			"org.testcontainers.session-id": "freighter-integration-tests",
		},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(dir, "config", "captive-core.cfg"),
				ContainerFilePath: "/config/captive-core.cfg",
				FileMode:          0644,
			},
			{
				HostFilePath:      filepath.Join(dir, "config", "stellar_rpc_config.toml"),
				ContainerFilePath: "/config/stellar_rpc_config.toml",
				FileMode:          0644,
			},
		},
		Cmd:          []string{"--config-path", "/config/stellar_rpc_config.toml"},
		Networks:     []string{testNetwork.Name},
		ExposedPorts: []string{"8000/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("8000/tcp"),
			wait.ForExec([]string{"sh", "-c", `curl -s -X POST http://localhost:8000 -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"getHealth","id":1}' | grep -q '"result"'`}).
				WithPollInterval(2*time.Second).
				WithExitCodeMatcher(func(exitCode int) bool { return exitCode == 0 }),
		),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return &container, nil
}

// createPostgresContainer starts a PostgreSQL container for Stellar Core
func createPostgresContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*testcontainers.Container, error) {
	containerRequest := testcontainers.ContainerRequest{
		Name:  "core-postgres",
		Image: "postgres:9.6.17-alpine",
		Labels: map[string]string{
			"org.testcontainers.session-id": "freighter-integration-tests",
		},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "mysecretpassword",
			"POSTGRES_DB":       "stellar",
		},
		Networks:     []string{testNetwork.Name},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor:   wait.ForListeningPort("5432/tcp"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return &container, nil
}

// createStellarCoreContainer starts a Stellar Core container in standalone mode
func createStellarCoreContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*testcontainers.Container, error) {
	// Get the directory of the current source file
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)

	containerRequest := testcontainers.ContainerRequest{
		Name:  "stellar-core",
		Image: "stellar/stellar-core:22",
		Labels: map[string]string{
			"org.testcontainers.session-id": "freighter-integration-tests",
		},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(dir, "config", "standalone-core.cfg"),
				ContainerFilePath: "/stellar-core.cfg",
				FileMode:          0644,
			},
			{
				HostFilePath:      filepath.Join(dir, "config", "core-start.sh"),
				ContainerFilePath: "/start",
				FileMode:          0755,
			},
		},
		Entrypoint: []string{"/bin/bash"},
		Cmd:        []string{"/start", "standalone"},
		Networks:   []string{testNetwork.Name},
		ExposedPorts: []string{
			"11625/tcp", // Peer port
			"11626/tcp", // HTTP port
			"1570/tcp",  // History archive port
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("11626/tcp"),
			wait.ForHTTP("/info").
				WithPort("11626/tcp").
				WithPollInterval(2*time.Second),
			wait.ForLog("Ledger close complete: 8"),
		),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return &container, nil
}

// createFreighterContainer creates a new Freighter container using the shared network
func createFreighterContainer(ctx context.Context, name string, tag string, testNetwork *testcontainers.DockerNetwork) (*FreighterBackendContainer, error) {
	containerRequest := testcontainers.ContainerRequest{
		Name: name,
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    FreighterDockerfileContext,
			Dockerfile: FreighterDockerfilePath,
			KeepImage:  true,
			Tag:        tag,
		},
		Labels: map[string]string{
			"org.testcontainers.session-id": "freighter-integration-tests",
		},
		Cmd:          []string{"./freighter-backend", "serve"},
		ExposedPorts: []string{fmt.Sprintf("%s/tcp", FreighterContainerPort)},
		Env: map[string]string{
			"FREIGHTER_BACKEND_HOST": FreighterContainerHost,
			"FREIGHTER_BACKEND_PORT": FreighterContainerPort,
			"REDIS_HOST":             RedisContainerName,
			"REDIS_PORT":             RedisContainerPort,
			"RPC_URL":                "http://stellar-rpc:8000",
		},
		Networks:   []string{testNetwork.Name},
		WaitingFor: wait.ForHTTP("/api/v1/ping"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return &FreighterBackendContainer{
		Container: container,
	}, nil
}
