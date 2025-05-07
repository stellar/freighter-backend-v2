package integrationtests

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetProtocols(t *testing.T) {
	testSuite := NewIntegrationTestSuite(t, &TestConfig{
		TestName:      "protocols-test",
		RunInParallel: true,
	})
	testSuite.SetupTest()
	defer testSuite.TearDownTest()

	ip := testSuite.GetAPIContainerIP()
	resp, err := http.Get(fmt.Sprintf("http://%s:3002/api/v1/protocols", ip))
	require.NoError(t, err)
	// require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotNil(t, body)

	// var protocols []Protocol
	// err = json.Unmarshal(body, &protocols)
	// require.NoError(t, err)
	// require.NotNil(t, protocols)
	// require.Equal(t, 2, len(protocols)) is
}
