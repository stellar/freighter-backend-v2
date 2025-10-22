package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// ErrorResponseWriter is a custom http.ResponseWriter that can be configured to error on Write.
// It embeds httptest.ResponseRecorder to act as a pass-through for most functionality.
type ErrorResponseWriter struct {
	*httptest.ResponseRecorder
	FailWrite bool
}

// NewErrorResponseWriter creates a new ErrorResponseWriter.
func NewErrorResponseWriter(failWrite bool) *ErrorResponseWriter {
	return &ErrorResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		FailWrite:        failWrite,
	}
}

// Write implements the io.Writer interface.
// If FailWrite is true, it returns an error. Otherwise, it calls the embedded recorder's Write.
func (w *ErrorResponseWriter) Write(data []byte) (int, error) {
	if w.FailWrite {
		return 0, errors.New("simulated writer error")
	}
	return w.ResponseRecorder.Write(data)
}

// WriteHeader calls the embedded recorder's WriteHeader.
// This ensures that the 'Code' field in the ResponseRecorder is set.
func (w *ErrorResponseWriter) WriteHeader(statusCode int) {
	w.ResponseRecorder.WriteHeader(statusCode)
}

// Header calls the embedded recorder's Header.
// This is necessary to fulfill the http.ResponseWriter interface.
func (w *ErrorResponseWriter) Header() http.Header {
	return w.ResponseRecorder.Header()
}

type RoundTripperFunc func(req *http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// NewMockHTTPClient returns a client that responds with the given JSON payload
func NewMockHTTPClient(payload any) *http.Client {
	b, _ := json.Marshal(payload)
	return &http.Client{
		Transport: RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(b)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}

var MockTokenData = struct {
	Name        string
	Description string
	URL         string
	Issuer      string
}{
	Name:        "MockNFT",
	Description: "A mock NFT",
	URL:         "https://example.com/image.png",
	Issuer:      "G123",
}

var MockHomeDomainsData = struct {
	HomeDomains map[string]string
}{
	HomeDomains: map[string]string{
		"GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF": "example.com",
		"GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX": "example2.com",
	},
}

var MockLedgerEntryData = struct {
	LedgerEntry []types.LedgerEntryMap
}{
	LedgerEntry: []types.LedgerEntryMap{
		{
			Account: types.AccountInfo{
				AccountId: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
				HomeDomain: "example.com",
			},
		},
		{
			Account: types.AccountInfo{
				AccountId: "GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX",
				HomeDomain: "example2.com",
			},
		},
	},
}