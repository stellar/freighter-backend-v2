// ABOUTME: Provides container management utilities for integration tests.
// ABOUTME: Includes both shared suite-level containers and helper functions.

package integrationtests

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	RedisContainerName = "redis"
	RedisContainerPort = "6379"
	RedisContainerImage = "redis:7-alpine"
	FreighterContainerName = "freighter"
	FreighterContainerPort = "3002"
	FreighterContainerTag = "integration-test"
	FreighterDockerfilePath = "deployments/Dockerfile"
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
	testNetwork        *testcontainers.DockerNetwork
	redisContainer     *redis.RedisContainer
	freighterContainer *freighterBackendContainer
}

// SetupSuite starts shared containers once for all tests in the suite
func (s *BaseTestSuite) SetupSuite() {
	ctx := context.Background()
	t := s.T()

	// Create network
	var err error
	s.testNetwork, err = network.New(ctx)
	s.Require().NoError(err)

	// Start Redis
	s.redisContainer, err = s.createRedisContainer(ctx, s.testNetwork)
	s.Require().NoError(err)

	// Start Freighter backend
	s.freighterContainer = s.createFreighterContainer(t, FreighterContainerName, FreighterContainerTag)
}

// TearDownSuite cleans up shared containers after all tests complete
func (s *BaseTestSuite) TearDownSuite() {
	ctx := context.Background()

	if s.freighterContainer != nil {
		_ = s.freighterContainer.Terminate(ctx)
	}
	if s.redisContainer != nil {
		_ = s.redisContainer.Terminate(ctx)
	}
	if s.testNetwork != nil {
		_ = s.testNetwork.Remove(ctx)
	}
}

// startRedisContainer starts a Redis container for testing
func (s *BaseTestSuite) createRedisContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*redis.RedisContainer, error) {
	return redis.Run(ctx,
		RedisContainerImage,
		network.WithNetwork([]string{RedisContainerName}, testNetwork),
	)
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
			"FREIGHTER_BACKEND_HOST": "0.0.0.0",
			"FREIGHTER_BACKEND_PORT": FreighterContainerPort,
			"REDIS_HOST":             RedisContainerName,
			"REDIS_PORT":             RedisContainerPort,
			"MODE":                   "development",
			"RPC_URL":                "https://soroban-testnet.stellar.org",
		},
		Networks:   []string{s.testNetwork.Name},
		WaitingFor: wait.ForHTTP("/api/v1/ping"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            false,
		Started:          true,
		Logger:           log.TestLogger(t),
	})
	s.Require().NoError(err)

	return &freighterBackendContainer{
		Container: container,
	}
}
