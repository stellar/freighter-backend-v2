// ABOUTME: Unit tests for the account-history handlers and shared translation helpers.
// ABOUTME: Drives handlers with MockWalletBackendService to assert HTTP status, body shape, and error mapping.
package handlers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/wallet-backend/pkg/wbclient"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

func TestTranslateServiceError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"account not found -> 404", wbclient.ErrAccountNotFound, http.StatusNotFound},
		{"ctx deadline -> 504", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"ctx canceled -> 504", context.Canceled, http.StatusGatewayTimeout},
		{"graphql_error -> 502", &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("schema bug")}, http.StatusBadGateway},
		{"http_error -> 502", &metrics.UpstreamError{Kind: "http_error", Code: 503, Err: errors.New("upstream down")}, http.StatusBadGateway},
		{"url.Error -> 502", &url.Error{Op: "Post", URL: "http://wb/graphql", Err: errors.New("dial tcp: connection refused")}, http.StatusBadGateway},
		{"net.OpError -> 502", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, http.StatusBadGateway},
		{"generic -> 500", errors.New("anything else"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := translateServiceError(ctx, tt.err, "test resource", "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF", "PUBLIC")
			require.NotNil(t, h)
			assert.Equal(t, tt.wantStatus, h.StatusCode)
		})
	}
}
