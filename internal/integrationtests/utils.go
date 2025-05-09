package integrationtests

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
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

func NewFreighterBackendContainer(t *testing.T) *freighterBackendContainer {
	containerRequest := testcontainers.ContainerRequest{
		Name: "freighter-backend-integration-test",
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    "../../",
			Dockerfile: "deployments/Dockerfile",
			KeepImage:  true,
		},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      "../../internal/integrationtests/infrastructure/testdata/protocols.json",
				ContainerFilePath: "/app/config/protocols.json",
			},
		},
		Cmd:          []string{"./freighter-backend", "serve"},
		ExposedPorts: []string{"3002/tcp"},
		Env: map[string]string{
			"FREIGHTER_BACKEND_HOST": "0.0.0.0",
			"FREIGHTER_BACKEND_PORT": "3002",
			"REDIS_HOST":             "redis",
			"REDIS_PORT":             "6379",
			"MODE":                   "development",
		},
		WaitingFor: wait.ForHTTP("/api/v1/ping"),
	}

	container, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest,
		Reuse:            true,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to create freighter backend container: %v", err)
	}

	return &freighterBackendContainer{
		Container: container,
	}
}
