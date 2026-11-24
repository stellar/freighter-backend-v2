// ABOUTME: Unit tests for the simulate-state-changes handler.
// ABOUTME: Drives the handler with MockWalletBackendService to assert status, body shape, and error mapping.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func simulateRequest(target, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
}

func TestSimulateStateChanges_Handler(t *testing.T) {
	t.Parallel()

	t.Run("happy path 200 forwards the XDR and network", func(t *testing.T) {
		t.Parallel()
		opIndex := int32(0)
		var gotXDR, gotAccount, gotNetwork string
		mockSvc := &utils.MockWalletBackendService{
			SimulateStateChangesFunc: func(_ context.Context, transactionXDR, accountAddress, network string) (*types.SimulatedStateChanges, error) {
				gotXDR, gotAccount, gotNetwork = transactionXDR, accountAddress, network
				return &types.SimulatedStateChanges{
					LatestLedger: 123,
					StateChanges: []types.SimulatedStateChange{
						&types.SimulatedBalanceChange{
							SimulatedStateChangeBase: types.SimulatedStateChangeBase{Variant: "BalanceChange", Type: "BALANCE", Reason: "DEBIT", AccountAddress: "GSENDER", OperationIndex: &opIndex},
							TokenID:                  "CUSDC", Amount: "5000000",
						},
					},
					Tokens: []types.SimulatedToken{},
				}, nil
			},
		}
		rr := httptest.NewRecorder()
		err := NewSimulateStateChangesHandler(mockSvc).SimulateStateChanges(rr, simulateRequest("/x?network=TESTNET", `{"transaction_xdr":"AAAA","account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "AAAA", gotXDR)
		assert.Equal(t, "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF", gotAccount)
		assert.Equal(t, types.TESTNET, gotNetwork)
		assert.JSONEq(t, `{"data": {
			"latest_ledger": 123,
			"state_changes": [{"variant": "BalanceChange", "type": "BALANCE", "reason": "DEBIT", "account_address": "GSENDER", "operation_index": 0, "token_id": "CUSDC", "amount": "5000000"}],
			"tokens": []
		}}`, rr.Body.String())
	})

	t.Run("a muxed account_address is resolved to its base account", func(t *testing.T) {
		t.Parallel()
		var base xdr.MuxedAccount
		require.NoError(t, base.SetAddress("GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"))
		muxed := xdr.MuxedAccount{
			Type:     xdr.CryptoKeyTypeKeyTypeMuxedEd25519,
			Med25519: &xdr.MuxedAccountMed25519{Id: 7, Ed25519: *base.Ed25519},
		}
		var gotAccount string
		mockSvc := &utils.MockWalletBackendService{
			SimulateStateChangesFunc: func(_ context.Context, _, accountAddress, _ string) (*types.SimulatedStateChanges, error) {
				gotAccount = accountAddress
				return &types.SimulatedStateChanges{StateChanges: []types.SimulatedStateChange{}, Tokens: []types.SimulatedToken{}}, nil
			},
		}
		body := `{"transaction_xdr":"AAAA","account_address":"` + muxed.Address() + `"}`
		err := NewSimulateStateChangesHandler(mockSvc).SimulateStateChanges(httptest.NewRecorder(), simulateRequest("/x?network=PUBLIC", body))
		require.NoError(t, err)
		assert.Equal(t, "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF", gotAccount)
	})

	t.Run("a contract account_address is accepted as is", func(t *testing.T) {
		t.Parallel()
		const contract = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
		var gotAccount string
		mockSvc := &utils.MockWalletBackendService{
			SimulateStateChangesFunc: func(_ context.Context, _, accountAddress, _ string) (*types.SimulatedStateChanges, error) {
				gotAccount = accountAddress
				return &types.SimulatedStateChanges{StateChanges: []types.SimulatedStateChange{}, Tokens: []types.SimulatedToken{}}, nil
			},
		}
		body := `{"transaction_xdr":"AAAA","account_address":"` + contract + `"}`
		err := NewSimulateStateChangesHandler(mockSvc).SimulateStateChanges(httptest.NewRecorder(), simulateRequest("/x?network=PUBLIC", body))
		require.NoError(t, err)
		assert.Equal(t, contract, gotAccount)
	})

	badRequests := []struct {
		name, target, body string
	}{
		{"missing network", "/x", `{"transaction_xdr":"AAAA","account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`},
		{"unsupported network", "/x?network=FUTURENET", `{"transaction_xdr":"AAAA","account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`},
		{"malformed JSON", "/x?network=PUBLIC", `{`},
		{"missing transaction_xdr", "/x?network=PUBLIC", `{"account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`},
		{"empty transaction_xdr", "/x?network=PUBLIC", `{"transaction_xdr":"","account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`},
		{"missing account_address", "/x?network=PUBLIC", `{"transaction_xdr":"AAAA"}`},
		{"invalid account_address", "/x?network=PUBLIC", `{"transaction_xdr":"AAAA","account_address":"GNOTANADDRESS"}`},
	}
	for _, tc := range badRequests {
		t.Run(tc.name+" is a 400 without calling the service", func(t *testing.T) {
			t.Parallel()
			mockSvc := &utils.MockWalletBackendService{
				SimulateStateChangesFunc: func(context.Context, string, string, string) (*types.SimulatedStateChanges, error) {
					t.Error("service must not be called")
					return nil, nil
				},
			}
			err := NewSimulateStateChangesHandler(mockSvc).SimulateStateChanges(httptest.NewRecorder(), simulateRequest(tc.target, tc.body))
			var herr *httperror.HttpError
			require.True(t, errors.As(err, &herr), "expected HttpError, got %v", err)
			assert.Equal(t, http.StatusBadRequest, herr.StatusCode)
		})
	}

	rejections := []struct {
		code   string
		status int
	}{
		{types.SimulationCodeInvalidTransactionXDR, http.StatusBadRequest},
		{types.SimulationCodeUnsupportedTransaction, http.StatusUnprocessableEntity},
		{types.SimulationCodeSimulationFailed, http.StatusUnprocessableEntity},
	}
	for _, tc := range rejections {
		t.Run(tc.code+" carries its code in extras", func(t *testing.T) {
			t.Parallel()
			mockSvc := &utils.MockWalletBackendService{
				SimulateStateChangesError: &types.SimulationRejectedError{Code: tc.code, Message: "cannot preview"},
			}
			err := NewSimulateStateChangesHandler(mockSvc).SimulateStateChanges(httptest.NewRecorder(), simulateRequest("/x?network=PUBLIC", `{"transaction_xdr":"AAAA","account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`))
			var herr *httperror.HttpError
			require.True(t, errors.As(err, &herr), "expected HttpError, got %v", err)
			assert.Equal(t, tc.status, herr.StatusCode)
			assert.Equal(t, "cannot preview", herr.Message)
			assert.Equal(t, tc.code, herr.Extras["code"])
		})
	}

	upstream := []struct {
		name   string
		err    error
		status int
	}{
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"upstream error", &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("boom")}, http.StatusBadGateway},
		{"unknown error", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range upstream {
		t.Run(tc.name+" maps through translateServiceError", func(t *testing.T) {
			t.Parallel()
			mockSvc := &utils.MockWalletBackendService{SimulateStateChangesError: tc.err}
			err := NewSimulateStateChangesHandler(mockSvc).SimulateStateChanges(httptest.NewRecorder(), simulateRequest("/x?network=PUBLIC", `{"transaction_xdr":"AAAA","account_address":"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"}`))
			var herr *httperror.HttpError
			require.True(t, errors.As(err, &herr), "expected HttpError, got %v", err)
			assert.Equal(t, tc.status, herr.StatusCode)
			assert.Nil(t, herr.Extras)
		})
	}
}
