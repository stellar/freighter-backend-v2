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
	service := NewRPCService(rpcURL, "http://localhost:8001", "http://localhost:8002")

	require.NotNil(t, service)
	assert.IsType(t, &rpcService{}, service)

	// Verify the service has a client
	rpcSvc := service.(*rpcService)
	assert.NotNil(t, rpcSvc.pubnetClient)
	assert.NotNil(t, rpcSvc.testnetClient)
	assert.NotNil(t, rpcSvc.futurenetClient)
}

func TestRPCService_Name(t *testing.T) {
	service := NewRPCService("http://localhost:8000", "http://localhost:8001", "http://localhost:8002")

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

		service := NewRPCService(server.URL, "http://localhost:8001", "http://localhost:8002")

		response, err := service.GetHealth(context.Background(), "PUBLIC")

		require.NoError(t, err)
		assert.Equal(t, "healthy", response.Status)
	})

	t.Run("returns error status when server returns error", func(t *testing.T) {
		// Create a test server that returns an error
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		service := NewRPCService(server.URL, "http://localhost:8001", "http://localhost:8002")

		response, err := service.GetHealth(context.Background(), "PUBLIC")

		require.Error(t, err)
		assert.Equal(t, types.StatusError, response.Status)
	})

	t.Run("returns error status on network failure", func(t *testing.T) {
		// Use an invalid URL to simulate network error
		service := NewRPCService("http://localhost:99999", "http://localhost:8001", "http://localhost:8002")

		response, err := service.GetHealth(context.Background(), "PUBLIC")

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

	t.Run("successfully simulate transaction on pubnet", func(t *testing.T) {
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

		service := NewRPCService(server.URL, "http://localhost:8001", "http://localhost:8002")

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

		scVal, err := service.SimulateTx(context.Background(), tx, "PUBLIC")
		require.NoError(t, err)
		require.NotNil(t, scVal)
	})
	t.Run("successfully simulate transaction on testnet", func(t *testing.T) {
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

		service := NewRPCService("http://localhost:8000", server.URL, "http://localhost:8002")

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

		scVal, err := service.SimulateTx(context.Background(), tx, "TESTNET")
		require.NoError(t, err)
		require.NotNil(t, scVal)
	})

	t.Run("successfully simulate transaction on futurenet", func(t *testing.T) {
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

		service := NewRPCService("http://localhost:8000", "http://localhost:8001", server.URL)

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

		scVal, err := service.SimulateTx(context.Background(), tx, "FUTURENET")
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

		service := NewRPCService(server.URL, "http://localhost:8001", "http://localhost:8002")

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

		scVal, err := service.SimulateTx(context.Background(), tx, "PUBLIC")
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

		service := NewRPCService(server.URL, "http://localhost:8001", "http://localhost:8002")
		resp, err := service.SimulateInvocation(context.Background(), contractId, sourceAccount, "get_metadata", []xdr.ScVal{}, timeout, "PUBLIC")
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, xdr.ScValTypeScvI64, resp.Type)
		assert.Equal(t, val, *resp.I64)
	})
		t.Run("successfully simulates contract invocation on testnet", func(t *testing.T) {
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

			service := NewRPCService("http://localhost:8000", server.URL, "http://localhost:8002")
			resp, err := service.SimulateInvocation(context.Background(), contractId, sourceAccount, "get_metadata", []xdr.ScVal{}, timeout, "TESTNET")
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, xdr.ScValTypeScvI64, resp.Type)
			assert.Equal(t, val, *resp.I64)
		})
		t.Run("successfully simulates contract invocation on futurenet", func(t *testing.T) {
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
	
			service := NewRPCService("http://localhost:8000", "http://localhost:8001", server.URL)
			resp, err := service.SimulateInvocation(context.Background(), contractId, sourceAccount, "get_metadata", []xdr.ScVal{}, timeout, "FUTURENET")
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, xdr.ScValTypeScvI64, resp.Type)
			assert.Equal(t, val, *resp.I64)
		})
}

func TestNewRPCService_GetLedgerEntry(t *testing.T) {
	t.Run("returns response from pubnet", func(t *testing.T) {
		pubnetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := `{"jsonrpc":"2.0","id":1,"result":{"entries":[{"keyJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}},"dataJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN","balance":"212714995806","seq_num":"144373126631784459","num_sub_entries":6,"inflation_dest":null,"flags":2,"home_domain":"centre.io","thresholds":"00020202","signers":[{"key":"GAUKFO2NIYEFO773KJZKLPSPYNQ6M7INPEAIQIJCIH7EEVP2KSVQWGH4","weight":1},{"key":"GAXFRO4MH6FSBJMNECVZJ6R3ZXANI7CFCMVN47IDNRQDHII3J5HTOZGB","weight":1},{"key":"GB73YPMX5R5673G35HHAXYUNRS4R4QZEUHCRT3OUAMSH2NUM6KT4N3KO","weight":1},{"key":"GC6ANHZDMCPKU55BUQIJKI3VOYEMETF7Z46HXQRCNONQXPEXQHCVIAFP","weight":1}],"ext":{"v1":{"liabilities":{"buying":"0","selling":"0"},"ext":{"v2":{"num_sponsored":0,"num_sponsoring":0,"signer_sponsoring_i_ds":[null,null,null,null],"ext":{"v3":{"ext":"v0","seq_ledger":57697123,"seq_time":"1750781116"}}}}}}}},"lastModifiedLedgerSeq":59502123,"extJson":"v0"}],"latestLedger":59504061}}`

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer pubnetServer.Close()

		service := NewRPCService(pubnetServer.URL, "http://localhost:8001", "http://localhost:8002")
		response, err := service.GetLedgerEntry(context.Background(), []string{"foo"}, types.PUBLIC)

		require.NoError(t, err)
		assert.Equal(t, "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", response[0].Account.AccountId)
		assert.Equal(t, "212714995806", response[0].Account.Balance)
		assert.Equal(t, "144373126631784459", response[0].Account.Seq_num)
		assert.Equal(t, uint64(6), response[0].Account.Num_sub_entries)
		assert.Equal(t, "", response[0].Account.Inflation_dest)
		assert.Equal(t, uint64(2), response[0].Account.Flags)
		assert.Equal(t, "centre.io", response[0].Account.HomeDomain)
		assert.Equal(t, "00020202", response[0].Account.Thresholds)
	})
	t.Run("returns response from testnet", func(t *testing.T) {
		testnetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := `{"jsonrpc":"2.0","id":1,"result":{"entries":[{"keyJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}},"dataJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN","balance":"212714995806","seq_num":"144373126631784459","num_sub_entries":6,"inflation_dest":null,"flags":2,"home_domain":"centre.io","thresholds":"00020202","signers":[{"key":"GAUKFO2NIYEFO773KJZKLPSPYNQ6M7INPEAIQIJCIH7EEVP2KSVQWGH4","weight":1},{"key":"GAXFRO4MH6FSBJMNECVZJ6R3ZXANI7CFCMVN47IDNRQDHII3J5HTOZGB","weight":1},{"key":"GB73YPMX5R5673G35HHAXYUNRS4R4QZEUHCRT3OUAMSH2NUM6KT4N3KO","weight":1},{"key":"GC6ANHZDMCPKU55BUQIJKI3VOYEMETF7Z46HXQRCNONQXPEXQHCVIAFP","weight":1}],"ext":{"v1":{"liabilities":{"buying":"0","selling":"0"},"ext":{"v2":{"num_sponsored":0,"num_sponsoring":0,"signer_sponsoring_i_ds":[null,null,null,null],"ext":{"v3":{"ext":"v0","seq_ledger":57697123,"seq_time":"1750781116"}}}}}}}},"lastModifiedLedgerSeq":59502123,"extJson":"v0"}],"latestLedger":59504061}}`

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer testnetServer.Close()

		service := NewRPCService("http://localhost:8000", testnetServer.URL, "http://localhost:8000")
		response, err := service.GetLedgerEntry(context.Background(), []string{"foo"}, types.TESTNET)

		require.NoError(t, err)
		assert.Equal(t, "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", response[0].Account.AccountId)
		assert.Equal(t, "212714995806", response[0].Account.Balance)
		assert.Equal(t, "144373126631784459", response[0].Account.Seq_num)
		assert.Equal(t, uint64(6), response[0].Account.Num_sub_entries)
		assert.Equal(t, "", response[0].Account.Inflation_dest)
		assert.Equal(t, uint64(2), response[0].Account.Flags)
		assert.Equal(t, "centre.io", response[0].Account.HomeDomain)
		assert.Equal(t, "00020202", response[0].Account.Thresholds)
	})
	t.Run("returns response from futurenet", func(t *testing.T) {
		futurenetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := `{"jsonrpc":"2.0","id":1,"result":{"entries":[{"keyJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}},"dataJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN","balance":"212714995806","seq_num":"144373126631784459","num_sub_entries":6,"inflation_dest":null,"flags":2,"home_domain":"centre.io","thresholds":"00020202","signers":[{"key":"GAUKFO2NIYEFO773KJZKLPSPYNQ6M7INPEAIQIJCIH7EEVP2KSVQWGH4","weight":1},{"key":"GAXFRO4MH6FSBJMNECVZJ6R3ZXANI7CFCMVN47IDNRQDHII3J5HTOZGB","weight":1},{"key":"GB73YPMX5R5673G35HHAXYUNRS4R4QZEUHCRT3OUAMSH2NUM6KT4N3KO","weight":1},{"key":"GC6ANHZDMCPKU55BUQIJKI3VOYEMETF7Z46HXQRCNONQXPEXQHCVIAFP","weight":1}],"ext":{"v1":{"liabilities":{"buying":"0","selling":"0"},"ext":{"v2":{"num_sponsored":0,"num_sponsoring":0,"signer_sponsoring_i_ds":[null,null,null,null],"ext":{"v3":{"ext":"v0","seq_ledger":57697123,"seq_time":"1750781116"}}}}}}}},"lastModifiedLedgerSeq":59502123,"extJson":"v0"}],"latestLedger":59504061}}`

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer futurenetServer.Close()

		service := NewRPCService("http://localhost:8000",  "http://localhost:8001",  futurenetServer.URL,)
		response, err := service.GetLedgerEntry(context.Background(), []string{"foo"}, types.FUTURENET)

		require.NoError(t, err)
		assert.Equal(t, "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", response[0].Account.AccountId)
		assert.Equal(t, "212714995806", response[0].Account.Balance)
		assert.Equal(t, "144373126631784459", response[0].Account.Seq_num)
		assert.Equal(t, uint64(6), response[0].Account.Num_sub_entries)
		assert.Equal(t, "", response[0].Account.Inflation_dest)
		assert.Equal(t, uint64(2), response[0].Account.Flags)
		assert.Equal(t, "centre.io", response[0].Account.HomeDomain)
		assert.Equal(t, "00020202", response[0].Account.Thresholds)
	})

	t.Run("returns error on network failure", func(t *testing.T) {
		futurenetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			response := `{"jsonrpc":"2.0","id":1,"result":{"entries":[{"keyJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}},"dataJson":{"account":{"account_id":"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN","balance":"212714995806","seq_num":"144373126631784459","num_sub_entries":6,"inflation_dest":null,"flags":2,"home_domain":"centre.io","thresholds":"00020202","signers":[{"key":"GAUKFO2NIYEFO773KJZKLPSPYNQ6M7INPEAIQIJCIH7EEVP2KSVQWGH4","weight":1},{"key":"GAXFRO4MH6FSBJMNECVZJ6R3ZXANI7CFCMVN47IDNRQDHII3J5HTOZGB","weight":1},{"key":"GB73YPMX5R5673G35HHAXYUNRS4R4QZEUHCRT3OUAMSH2NUM6KT4N3KO","weight":1},{"key":"GC6ANHZDMCPKU55BUQIJKI3VOYEMETF7Z46HXQRCNONQXPEXQHCVIAFP","weight":1}],"ext":{"v1":{"liabilities":{"buying":"0","selling":"0"},"ext":{"v2":{"num_sponsored":0,"num_sponsoring":0,"signer_sponsoring_i_ds":[null,null,null,null],"ext":{"v3":{"ext":"v0","seq_ledger":57697123,"seq_time":"1750781116"}}}}}}}},"lastModifiedLedgerSeq":59502123,"extJson":"v0"}],"latestLedger":59504061}}`

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, response)
		}))
		defer futurenetServer.Close()

		service := NewRPCService("http://localhost:8000",  "http://localhost:8001",  "http://localhost:8002",)
		response, err := service.GetLedgerEntry(context.Background(), []string{"foo"}, types.FUTURENET)

		assert.Nil(t, response)
		assert.Equal(t, "failed to get ledger entries: [-32603] Post \"http://localhost:8002\": dial tcp [::1]:8002: connect: connection refused", err.Error())

	})
}
