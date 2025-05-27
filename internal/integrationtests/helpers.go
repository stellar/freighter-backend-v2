package integrationtests

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

type freighterBackendContainer struct {
	testcontainers.Container
	ConnectionString string
}

func (c *freighterBackendContainer) GetConnectionString(ctx context.Context) (string, error) {
	host, err := c.Host(ctx)
	if err != nil {
		return "", err
	}

	port, err := c.MappedPort(ctx, "3002")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("http://%s:%s", host, port.Port()), nil
}

// startRedisContainer starts a Redis container for testing
func startRedisContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*redis.RedisContainer, error) {
	return redis.Run(ctx,
		"redis:7-alpine",
		network.WithNetwork([]string{"redis"}, testNetwork),
	)
}

func NewFreighterBackendContainer(t *testing.T, name string, tag string) *freighterBackendContainer {
	ctx := context.Background()

	// Create a network for containers to communicate
	testNetwork, err := network.New(ctx)
	if err != nil {
		t.Fatalf("failed to create network: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := testNetwork.Remove(ctx); removeErr != nil {
			t.Logf("failed to remove network: %v", removeErr)
		}
	})

	// Start Redis container
	redisContainer, err := startRedisContainer(ctx, testNetwork)
	if err != nil {
		t.Fatalf("failed to start Redis container: %v", err)
	}
	t.Cleanup(func() {
		if terminateErr := redisContainer.Terminate(ctx); terminateErr != nil {
			t.Logf("failed to terminate Redis container: %v", terminateErr)
		}
	})
	containerRequest := testcontainers.ContainerRequest{
		Name: name,
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    "../../",
			Dockerfile: "deployments/Dockerfile",
			KeepImage:  true,
			Tag:        tag,
		},
		Cmd:          []string{"./freighter-backend", "serve"},
		ExposedPorts: []string{"3002/tcp"},
		Env: map[string]string{
			"FREIGHTER_BACKEND_HOST": "0.0.0.0",
			"FREIGHTER_BACKEND_PORT": "3002",
			"REDIS_HOST":             "redis",
			"REDIS_PORT":             "6379",
			"MODE":                   "development",
			"RPC_URL":                "https://soroban-testnet.stellar.org", // Provide a valid RPC URL for health check
		},
		Networks:   []string{testNetwork.Name},
		WaitingFor: wait.ForHTTP("/api/v1/ping"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            false,
		Started:          true,
		Logger:           log.TestLogger(t),
	})
	if err != nil {
		t.Fatalf("failed to create freighter backend container: %v", err)
	}

	return &freighterBackendContainer{
		Container: container,
	}
}
