package integrationtests

import (
	"context"
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
