package integrationtests

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/compose"
)

type TestConfig struct {
	TestName      string
	RunInParallel bool
}

type integrationTestSuite struct {
	t     *testing.T
	cfg   *TestConfig
	stack compose.ComposeStack
}

func (s *integrationTestSuite) SetupTest() {
	// Start the freighter backend docker container
	stack, err := compose.NewDockerComposeWith(
		compose.StackIdentifier(s.cfg.TestName),
		compose.WithStackFiles("../../deployments/docker-compose.integration-test.yml"),
	)
	if err != nil {
		s.t.Fatalf("failed to create docker compose stack: %v", err)
	}
	s.stack = stack

	err = s.stack.WithEnv(map[string]string{
		"FREIGHTER_BACKEND_HOST": "0.0.0.0",
		"FREIGHTER_BACKEND_PORT": "3002",
		"REDIS_HOST":             "redis",
		"REDIS_PORT":             "6379",
		"MODE":                   "development",
	}).Up(context.Background(), compose.Wait(true))
	if err != nil {
		s.t.Fatalf("failed to start docker compose stack: %v", err)
	}
}

func (s *integrationTestSuite) TearDownTest() {
	if s.stack != nil {
		err := s.stack.Down(context.Background(), compose.RemoveOrphans(true), compose.RemoveVolumes(true))
		if err != nil {
			s.t.Logf("failed to tear down docker compose stack: %v", err)
		}
	}
}

func (s *integrationTestSuite) GetAPIContainerIP() string {
	container, err := s.stack.ServiceContainer(context.Background(), "api")
	if err != nil {
		s.t.Fatalf("failed to get service container: %v", err)
	}
	ip, err := container.ContainerIP(context.Background())
	if err != nil {
		s.t.Fatalf("failed to get host: %v", err)
	}
	return ip
}

func NewIntegrationTestSuite(t *testing.T, cfg *TestConfig) *integrationTestSuite {
	testSuite := &integrationTestSuite{
		t:   t,
		cfg: cfg,
	}
	if cfg.RunInParallel {
		testSuite.t.Parallel()
	}
	return testSuite
}
