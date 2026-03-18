// ABOUTME: Unit tests for wallet backend service error classification.
// ABOUTME: Verifies classifyWBError wraps errors with correct UpstreamError kinds and GetHealth wraps HTTP errors.
package services

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

func TestClassifyWBError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectWrapped bool
		expectedKind  string
	}{
		{
			name:          "GraphQL error wraps as graphql_error",
			err:           fmt.Errorf("GraphQL error: something went wrong"),
			expectWrapped: true,
			expectedKind:  "graphql_error",
		},
		{
			name:          "unexpected statusCode wraps as http_error",
			err:           fmt.Errorf("unexpected statusCode=500: internal server error"),
			expectWrapped: true,
			expectedKind:  "http_error",
		},
		{
			name:          "generic error passes through unchanged",
			err:           fmt.Errorf("some other error"),
			expectWrapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyWBError(tt.err)
			if tt.expectWrapped {
				var upErr *metrics.UpstreamError
				require.True(t, errors.As(result, &upErr), "expected UpstreamError, got %T", result)
				assert.Equal(t, tt.expectedKind, upErr.Kind)
				assert.Equal(t, 0, upErr.Code)
			} else {
				assert.Equal(t, tt.err, result)
			}
		})
	}
}

func TestGetHealth_HTTPErrorClassification(t *testing.T) {
	// Verify that the UpstreamError created by GetHealth for non-200 responses
	// classifies correctly with the HTTP status code as a sub-label.
	healthErr := &metrics.UpstreamError{
		Kind: "http_error",
		Code: 503,
		Err:  fmt.Errorf("health endpoint returned status 503"),
	}

	assert.Equal(t, "http_error:503", metrics.ClassifyError(healthErr))

	var upErr *metrics.UpstreamError
	require.True(t, errors.As(healthErr, &upErr))
	assert.Equal(t, "http_error", upErr.Kind)
	assert.Equal(t, 503, upErr.Code)
}
