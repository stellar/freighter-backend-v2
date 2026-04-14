package integrationtests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
)

type HealthTestSuite struct {
	suite.Suite
	freighterContainer *infrastructure.FreighterBackendContainer
	connectionString   string
}

func (s *HealthTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	s.connectionString, err = s.freighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.Require().NotEmpty(s.connectionString)
}

func (s *HealthTestSuite) TestGetHealthReturns200StatusCode() {
	t := s.T()

	resp, err := http.Get(fmt.Sprintf("%s/api/v1/ping", s.connectionString))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (s *HealthTestSuite) TestGetRPCHealthReturns200StatusCode() {
	t := s.T()

	resp, err := http.Get(fmt.Sprintf("%s/api/v1/rpc-health", s.connectionString))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify the response body contains a status field with a valid value
	var body map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	require.Contains(t, body, "status")
	status, ok := body["status"].(string)
	require.True(t, ok, "status should be a string")
	require.Contains(t, []string{"healthy", "unhealthy"}, status, "status should be either 'healthy' or 'unhealthy'")
}
