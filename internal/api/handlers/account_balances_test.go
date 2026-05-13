package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestGetAccountBalances(t *testing.T) {
	t.Run("should return balances for valid addresses", func(t *testing.T) {
		t.Parallel()

		mockBalances := []map[string]interface{}{
			{
				"address": "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"balances": []map[string]interface{}{
					{
						"balance":   "100.0000000",
						"tokenId":   "native",
						"tokenType": "NATIVE",
					},
				},
			},
		}

		mockService := &utils.MockWalletBackendService{
			GetBalancesOverride: mockBalances,
		}

		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": ["GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data interface{} `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.NotNil(t, response.Data)
	})

	t.Run("should return balances for multiple addresses", func(t *testing.T) {
		t.Parallel()

		mockBalances := []map[string]interface{}{
			{
				"address": "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"balances": []map[string]interface{}{
					{
						"balance":   "100.0000000",
						"tokenId":   "native",
						"tokenType": "NATIVE",
					},
				},
			},
			{
				"address": "GBKWMR7TJ7BBICOOXRY2SWXKCWPTOHZPI6MP4LNNE5A73VP3WADGG3CH",
				"balances": []map[string]interface{}{
					{
						"balance":   "200.0000000",
						"tokenId":   "native",
						"tokenType": "NATIVE",
					},
				},
			},
		}

		mockService := &utils.MockWalletBackendService{
			GetBalancesOverride: mockBalances,
		}

		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": [
				"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"GBKWMR7TJ7BBICOOXRY2SWXKCWPTOHZPI6MP4LNNE5A73VP3WADGG3CH"
			]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("should return error for invalid network", func(t *testing.T) {
		t.Parallel()

		mockService := &utils.MockWalletBackendService{}
		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": ["GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=INVALID", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.Error(t, err)
		assert.EqualError(t, err, "invalid network: network must be PUBLIC, TESTNET or FUTURENET")
	})

	t.Run("should return error for empty addresses array", func(t *testing.T) {
		t.Parallel()

		mockService := &utils.MockWalletBackendService{}
		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": []
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.Error(t, err)
		assert.EqualError(t, err, "addresses array cannot be empty")
	})

	t.Run("should return error for invalid JSON", func(t *testing.T) {
		t.Parallel()

		mockService := &utils.MockWalletBackendService{}
		handler := NewAccountBalancesHandler(mockService, 100)

		body := `invalid json`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JSON")
	})

	t.Run("should return error for invalid Stellar address", func(t *testing.T) {
		t.Parallel()

		mockService := &utils.MockWalletBackendService{}
		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": ["invalid-address"]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Stellar address")
	})

	t.Run("should return error when wallet backend service fails", func(t *testing.T) {
		t.Parallel()

		mockService := &utils.MockWalletBackendService{
			GetBalancesError: errors.New("wallet backend error"),
		}

		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": ["GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to get account balances")
	})

	t.Run("should work with TESTNET network", func(t *testing.T) {
		t.Parallel()

		mockBalances := []map[string]interface{}{
			{
				"address": "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"balances": []map[string]interface{}{
					{
						"balance":   "100.0000000",
						"tokenId":   "native",
						"tokenType": "NATIVE",
					},
				},
			},
		}

		mockService := &utils.MockWalletBackendService{
			GetBalancesOverride: mockBalances,
		}

		handler := NewAccountBalancesHandler(mockService, 100)

		body := `{
			"addresses": ["GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=TESTNET", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("should return error when too many addresses are provided", func(t *testing.T) {
		t.Parallel()

		mockService := &utils.MockWalletBackendService{}
		handler := NewAccountBalancesHandler(mockService, 1) // Set max to 1 address

		body := `{
			"addresses": [
				"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"GBKWMR7TJ7BBICOOXRY2SWXKCWPTOHZPI6MP4LNNE5A73VP3WADGG3CH"
			]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many addresses: maximum is 1, got 2")
	})

	t.Run("should allow unlimited addresses when max is 0", func(t *testing.T) {
		t.Parallel()

		mockBalances := []map[string]interface{}{
			{
				"address":  "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"balances": []map[string]interface{}{},
			},
			{
				"address":  "GBKWMR7TJ7BBICOOXRY2SWXKCWPTOHZPI6MP4LNNE5A73VP3WADGG3CH",
				"balances": []map[string]interface{}{},
			},
		}

		mockService := &utils.MockWalletBackendService{
			GetBalancesOverride: mockBalances,
		}
		handler := NewAccountBalancesHandler(mockService, 0) // No limit

		body := `{
			"addresses": [
				"GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF",
				"GBKWMR7TJ7BBICOOXRY2SWXKCWPTOHZPI6MP4LNNE5A73VP3WADGG3CH"
			]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/accounts/balances?network=PUBLIC", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetAccountBalances(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
}
