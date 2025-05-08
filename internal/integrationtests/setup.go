package integrationtests

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go/modules/compose"
)

type TestConfig struct {
	TestName      string
	RunInParallel bool
	envVars       map[string]string
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

	err = s.stack.WithEnv(s.cfg.envVars).Up(context.Background(), compose.Wait(true))
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

func (s *integrationTestSuite) GetBaseURL() string {
	container, err := s.stack.ServiceContainer(context.Background(), "api")
	if err != nil {
		s.t.Fatalf("failed to get service container: %v", err)
	}
	host, err := container.Host(context.Background())
	if err != nil {
		s.t.Fatalf("failed to get service container host: %v", err)
	}
	port, err := container.MappedPort(context.Background(), nat.Port("3002"))
	if err != nil {
		s.t.Fatalf("failed to get service container port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
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
