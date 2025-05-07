package integrationtests

import (
	"context"
	"testing"

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
