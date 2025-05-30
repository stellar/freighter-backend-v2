package integrationtests

import (
	"context"
	"os"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
	"github.com/stretchr/testify/suite"
)

func TestIntegrationTests(t *testing.T) {
	if os.Getenv("ENABLE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration tests: ENABLE_INTEGRATION_TESTS is not 'true'")
	}

	containers := infrastructure.NewSharedContainers(t)
	defer containers.Cleanup(context.Background())

	t.Run("ProtocolsTestSuite", func(t *testing.T) {
		suite.Run(t, &ProtocolsTestSuite{
			freighterContainer: containers.FreighterContainer,
		})
	})
	t.Run("HealthTestSuite", func(t *testing.T) {
		suite.Run(t, &HealthTestSuite{
			freighterContainer: containers.FreighterContainer,
		})
	})
}
