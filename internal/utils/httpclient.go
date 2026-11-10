package utils

import (
	"net/http"
	"time"
)

// NewUpstreamHTTPClient is the client for every upstream HTTP dependency.
// Go's default transport keeps two idle connections per host and no cap on
// connections per host, so a burst re-dials and fans out; this pool does not.
// It clones the default transport to keep its proxy, dial and TLS handshake
// settings. The timeout is a backstop: callers bound each request with a
// context.
func NewUpstreamHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 10
	t.MaxConnsPerHost = 50
	t.ResponseHeaderTimeout = 10 * time.Second
	return &http.Client{Timeout: 30 * time.Second, Transport: t}
}
