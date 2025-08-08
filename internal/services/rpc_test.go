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

func TestNewRPCService(t *testing.T) {
	rpcURL := "http://localhost:8000"
	service := NewRPCService(rpcURL)

	require.NotNil(t, service)
	assert.IsType(t, &rpcService{}, service)

	// Verify the service has a client
	rpcSvc := service.(*rpcService)
	assert.NotNil(t, rpcSvc.client)
}

func TestRPCService_Name(t *testing.T) {
	service := NewRPCService("http://localhost:8000")

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

		service := NewRPCService(server.URL)

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

		service := NewRPCService(server.URL)

		response, err := service.GetHealth(context.Background())

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})

	t.Run("returns error status on network failure", func(t *testing.T) {
		// Use an invalid URL to simulate network error
		service := NewRPCService("http://localhost:99999")

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

		service := NewRPCService(server.URL)

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

		service := NewRPCService(server.URL)

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

func TestRPCService_InvokeContract(t *testing.T) {
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

	t.Run("successfully invokes contract", func(t *testing.T) {
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

		service := NewRPCService(server.URL)
		resp, err := service.InvokeContract(context.Background(), contractId, sourceAccount, nil, timeout)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, xdr.ScValTypeScvI64, resp.Type)
		assert.Equal(t, val, *resp.I64)
	})
}
