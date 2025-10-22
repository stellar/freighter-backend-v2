package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

var testnetRPCURL = "http://localhost:8100"
var futurenetRPCURL = "http://localhost:8200"

func TestNewRPCService(t *testing.T) {
	rpcURL := "http://localhost:8000"

	service := NewRPCService(rpcURL, testnetRPCURL, futurenetRPCURL)

	require.NotNil(t, service)
	assert.IsType(t, &rpcService{}, service)

	// Verify the service has a client
	rpcSvc := service.(*rpcService)
	assert.NotNil(t, rpcSvc.client)
}

func TestRPCService_Name(t *testing.T) {
	service := NewRPCService("http://localhost:8000", testnetRPCURL, futurenetRPCURL)

	name := service.Name()

	assert.Equal(t, "rpc", name)
}

func TestRPCService_GetHealth(t *testing.T) {
	t.Run("returns healthy status when RPC is available", func(t *testing.T) {
		// Create a test server that responds with a healthy status
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			// Respond with a successful RPC response
			response := `{"jsonrpc":"2.0","id":1,"result":{"status":"healthy"}}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer server.Close()

		service := NewRPCService(server.URL, server.URL, futurenetRPCURL)

		response, err := service.GetHealth(context.Background())

		require.NoError(t, err)
		assert.Equal(t, "healthy", response.Status)
	})

	t.Run("returns error status when server returns error", func(t *testing.T) {
		// Create a test server that returns an error
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		service := NewRPCService(server.URL, testnetRPCURL, futurenetRPCURL)

		response, err := service.GetHealth(context.Background())

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})

	t.Run("returns error status on network failure", func(t *testing.T) {
		// Use an invalid URL to simulate network error
		service := NewRPCService("http://localhost:99999", testnetRPCURL, futurenetRPCURL)

		response, err := service.GetHealth(context.Background())

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})
}

func TestRPCService_SimulateTx(t *testing.T) {
	account := keypair.MustRandom()
	sourceAccount := &txnbuild.SimpleAccount{
		AccountID: account.Address(),
		Sequence:  1,
	}

	t.Run("successfully simulate transaction", func(t *testing.T) {
		validXDR := "AAAAAgAAAAMAAAAB"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := fmt.Sprintf(`{
				"jsonrpc":"2.0",
				"id":1,
				"result":{
					"error":"",
					"results":[{
						"xdr":"%s"
					}]
				}
			}`, validXDR)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer server.Close()

		service := NewRPCService(server.URL, testnetRPCURL, futurenetRPCURL)

		destinationKP := keypair.MustRandom()
		op := txnbuild.Payment{
			Destination: destinationKP.Address(),
			Amount:      "10",
			Asset:       txnbuild.NativeAsset{},
		}

		txParams := txnbuild.TransactionParams{
			SourceAccount:        sourceAccount,
			IncrementSequenceNum: true,
			Operations:           []txnbuild.Operation{&op},
			BaseFee:              txnbuild.MinBaseFee,
			Preconditions: txnbuild.Preconditions{
				TimeBounds: txnbuild.NewTimeout(300),
			},
		}

		tx, err := txnbuild.NewTransaction(txParams)
		require.NoError(t, err)

		scVal, err := service.SimulateTx(context.Background(), tx)
		require.NoError(t, err)
		require.NotNil(t, scVal)
	})

	t.Run("simulate transaction RPC call error", func(t *testing.T) {
		validXDR := "AAAAAgAAAAMAAAAB"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := fmt.Sprintf(`{
				"jsonrpc":"2.0",
				"id":1,
				"result":{
					"error":"error",
					"results":[{
						"xdr":"%s"
					}]
				}
			}`, validXDR)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, response)
		}))
		defer server.Close()

		service := NewRPCService(server.URL, testnetRPCURL, futurenetRPCURL)

		destinationKP := keypair.MustRandom()
		op := txnbuild.Payment{
			Destination: destinationKP.Address(),
			Amount:      "10",
			Asset:       txnbuild.NativeAsset{},
		}

		txParams := txnbuild.TransactionParams{
			SourceAccount:        sourceAccount,
			IncrementSequenceNum: true,
			Operations:           []txnbuild.Operation{&op},
			BaseFee:              txnbuild.MinBaseFee,
			Preconditions: txnbuild.Preconditions{
				TimeBounds: txnbuild.NewTimeout(300),
			},
		}

		tx, err := txnbuild.NewTransaction(txParams)
		require.NoError(t, err)

		scVal, err := service.SimulateTx(context.Background(), tx)
		require.NotNil(t, err)
		assert.Equal(t, (*xdr.ScVal)(nil), scVal)
	})
}

func TestRPCService_SimulateInvocation(t *testing.T) {
	account := keypair.MustRandom()
	sourceAccount := &txnbuild.SimpleAccount{
		AccountID: account.Address(),
		Sequence:  1,
	}
	var contractHash xdr.Hash
	copy(contractHash[:], []byte("12345678901234567890123456789012"))

	contractId := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractHash,
	}
	timeout := txnbuild.NewTimeout(300)

	t.Run("successfully simulates contract invocation", func(t *testing.T) {
		val := xdr.Int64(100)
		validXDR := xdr.ScVal{
			Type: xdr.ScValTypeScvI64,
			I64:  &val,
		}
		b64, err := xdr.MarshalBase64(validXDR)
		if err != nil {
			t.Fatalf("failed to marshal ScVal: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := fmt.Sprintf(`{
				"jsonrpc":"2.0",
				"id":1,
				"result":{
					"error":"",
					"results":[{
						"xdr":"%s"
					}]
				}
			}`, b64)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer server.Close()

		service := NewRPCService(server.URL, testnetRPCURL, futurenetRPCURL)
		resp, err := service.SimulateInvocation(context.Background(), contractId, sourceAccount, "get_metadata", []xdr.ScVal{}, timeout)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, xdr.ScValTypeScvI64, resp.Type)
		assert.Equal(t, val, *resp.I64)
	})
}

func TestNewRPCService_GetLedgerEntry(t *testing.T) {
	t.Run("returns response from correct network", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			// Respond with a successful RPC response
			response := `{"jsonrpc":"2.0","id":8675309,"result":{"entries":[{"keyJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}},"dataJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN","balance":"212714995806","seq_num":"144373126631784459","num_sub_entries":6,"inflation_dest":null,"flags":2,"home_domain":"centre.io","thresholds":"00020202","signers":[{"key":"GAUKFO2NIYEFO773KJZKLPSPYNQ6M7INPEAIQIJCIH7EEVP2KSVQWGH4","weight":1},{"key":"GAXFRO4MH6FSBJMNECVZJ6R3ZXANI7CFCMVN47IDNRQDHII3J5HTOZGB","weight":1},{"key":"GB73YPMX5R5673G35HHAXYUNRS4R4QZEUHCRT3OUAMSH2NUM6KT4N3KO","weight":1},{"key":"GC6ANHZDMCPKU55BUQIJKI3VOYEMETF7Z46HXQRCNONQXPEXQHCVIAFP","weight":1}],"ext":{"v1":{"liabilities":{"buying":"0","selling":"0"},"ext":{"v2":{"num_sponsored":0,"num_sponsoring":0,"signer_sponsoring_i_ds":[null,null,null,null],"ext":{"v3":{"ext":"v0","seq_ledger":57697123,"seq_time":"1750781116"}}}}}}}},"lastModifiedLedgerSeq":59502123,"extJson":"v0"}],"latestLedger":59504061}}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer server.Close()

		service := NewRPCService("http://localhost:8000", server.URL, "http://localhost:8000")
		response, err := service.GetLedgerEntry(context.Background(), []string{"foo"}, types.TESTNET)

		require.NoError(t, err)
		assert.Equal(t, "centre.io", response[0].Account.AccountId)
	})


}
