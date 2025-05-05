package integrationtests

import (
	"github.com/stellar/freighter-backend-v2/internal/config"
)

type integrationTestSuite struct {
	cfg *config.Config
}

func (s *integrationTestSuite) SetupTest() {
	
}

func (s *integrationTestSuite) TearDownTest() {

}

func NewIntegrationTestSuite(cfg *config.Config) *integrationTestSuite {
	return &integrationTestSuite{
		cfg: cfg,
	}
}
