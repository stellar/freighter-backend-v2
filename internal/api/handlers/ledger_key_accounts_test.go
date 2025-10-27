package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLedgerKeyAccounts(t *testing.T) {
	t.Run("should return ledger key accounts with public key", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockLedgerKeyAccountsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewLedgerKeyAccountHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("GET", "/api/v1/ledger-key/accounts?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetLedgerKeyAccounts(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data LedgerKeyAccountsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		ledgerKeyAccounts := response
		d0 := ledgerKeyAccounts.Data.LedgerKeyAccounts["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		testAccount := types.AccountInfo{
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
		assert.Equal(t, testAccount, d0)
	})
	t.Run("should return multiple ledger key accounts with public keys", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockLedgerKeyAccountsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewLedgerKeyAccountHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("GET", "/api/v1/ledger-key/accounts?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF&public_key=GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetLedgerKeyAccounts(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data LedgerKeyAccountsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		ledgerKeyAccounts := response
		d0 := ledgerKeyAccounts.Data.LedgerKeyAccounts["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		testAccount0 := types.AccountInfo{
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
		assert.Equal(t, testAccount0, d0)
		d1 := ledgerKeyAccounts.Data.LedgerKeyAccounts["GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"]
		require.NotNil(t, d1)
		testAccount1 := types.AccountInfo{
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
		assert.Equal(t, testAccount1, d1)
	})

	t.Run("should return an account if 1 public key is valid and also errors if 1 public key is invalid", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockLedgerKeyAccountsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewLedgerKeyAccountHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("GET", "/api/v1/ledger-key/accounts?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF&public_key=asdff", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetLedgerKeyAccounts(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data LedgerKeyAccountsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		ledgerKeyAccounts := response
		d0 := ledgerKeyAccounts.Data.LedgerKeyAccounts["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		testAccount0 := types.AccountInfo{
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
		assert.Equal(t, testAccount0, d0)
		e0 := ledgerKeyAccounts.Data.Error.ErrorKeys[0]
		require.NotNil(t, e0)
		assert.Equal(t, "asdff", e0.PublicKey)
		assert.Equal(t, "base32 decode failed: illegal base32 data at input byte 5", e0.ErrorMessage)

	})

	t.Run("should dedupe public keys when requesting ledger key accounts", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockLedgerKeyAccountsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewLedgerKeyAccountHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("GET", "/api/v1/ledger-key/accounts?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF&public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetLedgerKeyAccounts(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data LedgerKeyAccountsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		ledgerKeyAccounts := response
		d0 := ledgerKeyAccounts.Data.LedgerKeyAccounts["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		testAccount0 := types.AccountInfo{
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
		assert.Equal(t, testAccount0, d0)
	})
	t.Run("should return empty account if public key doesn't exist", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockLedgerKeyAccountsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewLedgerKeyAccountHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("GET", "/api/v1/ledger-key/accounts?public_key=GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetLedgerKeyAccounts(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data LedgerKeyAccountsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		ledgerKeyAccounts := response
		d0 := ledgerKeyAccounts.Data.LedgerKeyAccounts["GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX"]
		require.NotNil(t, d0)
		testAccount := types.AccountInfo{
			AccountId: "",
			HomeDomain: "",
			Balance: "",
			Seq_num: "",
			Num_sub_entries: 0,
			Inflation_dest: "",
			Flags: 0,
			Thresholds: "",
			Signers: nil,
			Ext: xdr.LedgerEntryExt{
			},
		}
		assert.Equal(t, testAccount, d0)
	})

}