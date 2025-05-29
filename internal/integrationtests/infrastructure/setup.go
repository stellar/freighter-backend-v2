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

	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/log"
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

// freighterBackendContainer wraps a testcontainer with connection string helper
type freighterBackendContainer struct {
	testcontainers.Container
	ConnectionString string
}

// GetConnectionString returns the HTTP connection string for the container
func (c *freighterBackendContainer) GetConnectionString(ctx context.Context) (string, error) {
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
type BaseTestSuite struct {
	suite.Suite
	TestNetwork          *testcontainers.DockerNetwork
	RedisContainer       *redis.RedisContainer
	PostgresContainer    *testcontainers.Container
	StellarCoreContainer *testcontainers.Container
	RpcContainer         *testcontainers.Container
	FreighterContainer   *freighterBackendContainer
}

// SetupSuite starts shared containers once for all tests in the suite
func (s *BaseTestSuite) SetupSuite() {
	ctx := context.Background()
	t := s.T()

	// Create network
	var err error
	s.TestNetwork, err = network.New(ctx)
	s.Require().NoError(err)

	// Start Redis
	s.RedisContainer, err = s.createRedisContainer(ctx)
	s.Require().NoError(err)

	// Start PostgreSQL for Stellar Core
	s.PostgresContainer, err = s.createPostgresContainer(ctx)
	s.Require().NoError(err)

	// Start Stellar Core
	s.StellarCoreContainer, err = s.createStellarCoreContainer(ctx)
	s.Require().NoError(err)

	// Start Stellar RPC
	s.RpcContainer, err = s.createRPCContainer(ctx)
	s.Require().NoError(err)

	// Start Freighter backend
	s.FreighterContainer = s.createFreighterContainer(t, FreighterContainerName, FreighterContainerTag)
}

// TearDownSuite cleans up shared containers after all tests complete
func (s *BaseTestSuite) TearDownSuite() {
	ctx := context.Background()

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
func (s *BaseTestSuite) createRedisContainer(ctx context.Context) (*redis.RedisContainer, error) {
	return redis.Run(ctx,
		RedisContainerImage,
		network.WithNetwork([]string{RedisContainerName}, s.TestNetwork),
	)
}

// createRPCContainer starts a Stellar RPC container for testing
func (s *BaseTestSuite) createRPCContainer(ctx context.Context) (*testcontainers.Container, error) {
	// Get the directory of the current source file
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)

	containerRequest := testcontainers.ContainerRequest{
		Name:  "stellar-rpc",
		Image: "stellar/stellar-rpc:latest",
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(dir, "docker", "captive-core.cfg"),
				ContainerFilePath: "/config/captive-core.cfg",
				FileMode:          0644,
			},
			{
				HostFilePath:      filepath.Join(dir, "docker", "stellar_rpc_config.toml"),
				ContainerFilePath: "/config/stellar_rpc_config.toml",
				FileMode:          0644,
			},
		},
		Cmd:          []string{"--config-path", "/config/stellar_rpc_config.toml"},
		Networks:     []string{s.TestNetwork.Name},
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
	s.Require().NoError(err)

	return &container, nil
}

// createPostgresContainer starts a PostgreSQL container for Stellar Core
func (s *BaseTestSuite) createPostgresContainer(ctx context.Context) (*testcontainers.Container, error) {
	containerRequest := testcontainers.ContainerRequest{
		Name:  "core-postgres",
		Image: "postgres:9.6.17-alpine",
		Env: map[string]string{
			"POSTGRES_PASSWORD": "mysecretpassword",
			"POSTGRES_DB":       "stellar",
		},
		Networks:     []string{s.TestNetwork.Name},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor:   wait.ForListeningPort("5432/tcp"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
	})
	s.Require().NoError(err)

	return &container, nil
}

// createStellarCoreContainer starts a Stellar Core container in standalone mode
func (s *BaseTestSuite) createStellarCoreContainer(ctx context.Context) (*testcontainers.Container, error) {
	// Get the directory of the current source file
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)

	containerRequest := testcontainers.ContainerRequest{
		Name:  "stellar-core",
		Image: "stellar/stellar-core:22",
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(dir, "docker", "standalone-core.cfg"),
				ContainerFilePath: "/stellar-core.cfg",
				FileMode:          0644,
			},
			{
				HostFilePath:      filepath.Join(dir, "docker", "core-start.sh"),
				ContainerFilePath: "/start",
				FileMode:          0755,
			},
		},
		Entrypoint: []string{"/bin/bash"},
		Cmd:        []string{"/start", "standalone"},
		Networks:   []string{s.TestNetwork.Name},
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
	s.Require().NoError(err)

	return &container, nil
}

// createFreighterContainer creates a new Freighter container using the shared network
func (s *BaseTestSuite) createFreighterContainer(t *testing.T, name string, tag string) *freighterBackendContainer {
	ctx := context.Background()

	containerRequest := testcontainers.ContainerRequest{
		Name: name,
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    FreighterDockerfileContext,
			Dockerfile: FreighterDockerfilePath,
			KeepImage:  true,
			Tag:        tag,
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
		Networks:   []string{s.TestNetwork.Name},
		WaitingFor: wait.ForHTTP("/api/v1/ping"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
		Logger:           log.TestLogger(t),
	})
	s.Require().NoError(err)

	return &freighterBackendContainer{
		Container: container,
	}
}
