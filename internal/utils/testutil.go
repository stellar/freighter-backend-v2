package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/go/xdr"
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

var MockLedgerKeyAccount0 = types.AccountInfo{
	AccountId: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
	HomeDomain: "example.com",
	Balance: "1000000000000000000",
	Seq_num: "1000000000000000000",
	Num_sub_entries: 1000000000000000000,
	Inflation_dest: "1000000000000000000",
	Flags: 1000000000000000000,
	Thresholds: "1000000000000000000",
	Signers: []types.Signer{
		{Key: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", Weight: 1},
	},
	Ext: xdr.LedgerEntryExt{
	},
}

var MockLedgerKeyAccount1 = types.AccountInfo{
	AccountId: "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
	HomeDomain: "example2.com",
	Balance: "1000000000000000000",
	Seq_num: "1000000000000000000",
	Num_sub_entries: 1000000000000000000,
	Inflation_dest: "1000000000000000000",
	Flags: 1000000000000000000,
	Thresholds: "1000000000000000000",
	Signers: []types.Signer{
		{Key: "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5", Weight: 1},
	},
	Ext: xdr.LedgerEntryExt{
	},
}

var MockLedgerKeyAccount2 = types.AccountInfo{
	AccountId: "",
	HomeDomain: "",
	Balance: "",
	Seq_num: "",
	Num_sub_entries: 0,
	Inflation_dest: "",
	Flags: 0,
	Thresholds: "",
	Signers: []types.Signer{
	},
	Ext: xdr.LedgerEntryExt{
	},
}

var MockLedgerKeyAccountsData = struct {
	LedgerKeyAccounts map[string]types.AccountInfo
}{
	LedgerKeyAccounts: map[string]types.AccountInfo{
		"GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF": MockLedgerKeyAccount0,
		"GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5": MockLedgerKeyAccount1,
		"GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX": MockLedgerKeyAccount2,
	},
}

var MockLedgerEntryData = struct {
	LedgerEntry []types.LedgerEntryMap
}{
	LedgerEntry: []types.LedgerEntryMap{
		{
			Account: MockLedgerKeyAccount0,
		},
		{
			Account: MockLedgerKeyAccount1,
		},
	},
}