// ABOUTME: Container types, image management, and creation functions for integration tests.
// ABOUTME: Provides TestContainer wrapper, pre-built image management, and per-service container factories.
package infrastructure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	RedisContainerName      = "redis"
	RedisContainerPort      = "6379"
	RedisContainerImage     = "redis:7-alpine"
	FreighterContainerName  = "freighter"
	FreighterContainerHost  = "0.0.0.0"
	FreighterContainerPort  = "3002"
	FreighterContainerTag   = "integration-test"
	FreighterDockerfilePath = "deployments/Dockerfile"

	RPCHealthTimeout = 120 * time.Second
)

// TestContainer wraps a testcontainer with convenience methods for host/port/connection info.
type TestContainer struct {
	testcontainers.Container
	MappedPortStr string
}

// GetConnectionString returns the HTTP connection string for the container.
func (c *TestContainer) GetConnectionString(ctx context.Context) (string, error) {
	host, err := c.GetHost(ctx)
	if err != nil {
		return "", fmt.Errorf("getting host: %w", err)
	}

	port, err := c.GetPort(ctx)
	if err != nil {
		return "", fmt.Errorf("getting port: %w", err)
	}

	return fmt.Sprintf("http://%s:%s", host, port), nil
}

// GetHost returns the mapped host for the container.
func (c *TestContainer) GetHost(ctx context.Context) (string, error) {
	return c.Host(ctx)
}

// GetPort returns the mapped port string for the container.
func (c *TestContainer) GetPort(ctx context.Context) (string, error) {
	p, err := c.MappedPort(ctx, nat.Port(c.MappedPortStr))
	if err != nil {
		return "", err
	}
	return p.Port(), nil
}

// FreighterBackendContainer wraps a TestContainer for the freighter backend service.
type FreighterBackendContainer struct {
	*TestContainer
}

// getGitCommitHash returns the current git commit hash (short form).
// Falls back to "latest" if not in a git repository or if git command fails.
func getGitCommitHash() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "latest"
	}
	return strings.TrimSpace(string(out))
}

// imageExists checks whether a Docker image with the given name exists locally.
func imageExists(imageName string) bool {
	cmd := exec.Command("docker", "images", "-q", imageName)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// shouldRebuildImage returns true if the image should be rebuilt, either because
// it doesn't exist or FORCE_REBUILD is set.
func shouldRebuildImage(imageTag string) bool {
	if os.Getenv("FORCE_REBUILD") == "true" {
		return true
	}
	return !imageExists(imageTag)
}

// ensureFreighterBackendImage builds the freighter-backend Docker image if needed.
// Building the image before starting containers gives Stellar Core time to advance ledgers.
func ensureFreighterBackendImage(ctx context.Context, baseTag string) (string, error) {
	commitHash := getGitCommitHash()
	imageTag := fmt.Sprintf("freighter-backend:%s-%s", baseTag, commitHash)
	if !shouldRebuildImage(imageTag) {
		return imageTag, nil
	}

	// Build from repo root
	_, filename, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..", "..")

	cmd := exec.CommandContext(ctx, "docker", "build",
		"-t", imageTag,
		"-f", filepath.Join(repoRoot, FreighterDockerfilePath),
		repoRoot,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building freighter-backend image: %w", err)
	}

	return imageTag, nil
}

// createRedisContainer starts a Redis container for testing.
func createRedisContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*redis.RedisContainer, error) {
	container, err := redis.Run(ctx,
		RedisContainerImage,
		network.WithNetwork([]string{RedisContainerName}, testNetwork),
	)
	if err != nil {
		return nil, fmt.Errorf("creating redis container: %w", err)
	}
	return container, nil
}

// createPostgresContainer starts a PostgreSQL container for Stellar Core.
func createPostgresContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*TestContainer, error) {
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
		return nil, fmt.Errorf("creating core postgres container: %w", err)
	}

	return &TestContainer{Container: container, MappedPortStr: "5432"}, nil
}

// createStellarCoreContainer starts a Stellar Core container in standalone mode.
func createStellarCoreContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*TestContainer, error) {
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
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(dir, "config", "core-start.sh"),
				ContainerFilePath: "/start",
				FileMode:          0o755,
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
		return nil, fmt.Errorf("creating stellar-core container: %w", err)
	}

	tc := &TestContainer{Container: container, MappedPortStr: "11626"}

	if err := triggerProtocolUpgrade(ctx, tc); err != nil {
		return nil, fmt.Errorf("triggering protocol upgrade: %w", err)
	}

	return tc, nil
}

// triggerProtocolUpgrade triggers a protocol upgrade on the Stellar Core container
// to advance it to the latest protocol version.
func triggerProtocolUpgrade(ctx context.Context, coreContainer *TestContainer) error {
	_, _, err := coreContainer.Exec(ctx, []string{
		"curl", "-s",
		"http://localhost:11626/upgrades?mode=set&upgradetime=1970-01-01T00:00:00Z&protocolversion=22",
	})
	if err != nil {
		return fmt.Errorf("executing protocol upgrade command: %w", err)
	}
	return nil
}

// createRPCContainer starts a Stellar RPC container for testing.
func createRPCContainer(ctx context.Context, testNetwork *testcontainers.DockerNetwork) (*TestContainer, error) {
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
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(dir, "config", "stellar_rpc_config.toml"),
				ContainerFilePath: "/config/stellar_rpc_config.toml",
				FileMode:          0o644,
			},
		},
		Cmd:          []string{"--config-path", "/config/stellar_rpc_config.toml"},
		Networks:     []string{testNetwork.Name},
		ExposedPorts: []string{"8000/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("8000/tcp"),
			wait.ForExec([]string{"sh", "-c", `curl -s -X POST http://localhost:8000 -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"getHealth","id":1}' | grep -q '"result"'`}).
				WithStartupTimeout(RPCHealthTimeout).
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
		return nil, fmt.Errorf("creating RPC container: %w", err)
	}

	return &TestContainer{Container: container, MappedPortStr: "8000"}, nil
}

// createFreighterContainer creates a new Freighter container using a pre-built image.
func createFreighterContainer(ctx context.Context, name string, imageName string, testNetwork *testcontainers.DockerNetwork) (*FreighterBackendContainer, error) {
	containerRequest := testcontainers.ContainerRequest{
		Name:  name,
		Image: imageName,
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
			"PUBNET_RPC_URL":         "http://stellar-rpc:8000",
			"TESTNET_RPC_URL":        "http://horizon-testnet.stellar.org",
			"FUTURENET_RPC_URL":      "http://horizon-futurenet.stellar.org",
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
		return nil, fmt.Errorf("creating freighter container: %w", err)
	}

	return &FreighterBackendContainer{
		TestContainer: &TestContainer{
			Container:     container,
			MappedPortStr: FreighterContainerPort,
		},
	}, nil
}
