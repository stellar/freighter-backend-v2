package integrationtests

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

func TestIntegrationTests(t *testing.T) {
	if os.Getenv("ENABLE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration tests: ENABLE_INTEGRATION_TESTS is not 'true'")
	}

	t.Run("ProtocolsTestSuite", func(t *testing.T) {
		suite.Run(t, new(ProtocolsTestSuite))
	})
	t.Run("HealthTestSuite", func(t *testing.T) {
		suite.Run(t, new(HealthTestSuite))
	})
}
