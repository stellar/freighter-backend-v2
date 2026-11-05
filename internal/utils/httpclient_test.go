package utils

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamHTTPClientKeepsDefaultTransportSettings(t *testing.T) {
	tr, ok := NewUpstreamHTTPClient().Transport.(*http.Transport)
	require.True(t, ok)
	def := http.DefaultTransport.(*http.Transport)
	assert.NotNil(t, tr.Proxy, "HTTPS_PROXY/NO_PROXY must still apply")
	assert.NotNil(t, tr.DialContext, "the default dial and keepalive timeouts must still apply")
	assert.Equal(t, def.TLSHandshakeTimeout, tr.TLSHandshakeTimeout)
	assert.Equal(t, 10, tr.MaxIdleConnsPerHost)
	assert.Equal(t, 50, tr.MaxConnsPerHost)
}
