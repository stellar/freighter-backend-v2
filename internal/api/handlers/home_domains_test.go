package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetHomeDomains(t *testing.T) {
	t.Run("should return home domains with public key", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockHomeDomainsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewHomeDomainsHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("POST", "/api/v1/home-domains?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetHomeDomains(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data HomeDomainsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		homeDomains := response
		d0 := homeDomains.Data.HomeDomains["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		assert.Equal(t, "example.com", d0)
	})
	t.Run("should return multiple home domains with public keys", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockHomeDomainsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewHomeDomainsHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("POST", "/api/v1/home-domains?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF&public_key=GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetHomeDomains(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data HomeDomainsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		homeDomains := response
		d0 := homeDomains.Data.HomeDomains["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		assert.Equal(t, "example.com", d0)
		d1 := homeDomains.Data.HomeDomains["GAWYJTG6RQFXMSOEF7LHUOSDOUQLAHNQGJO5QULS6FTHCR3HCPZDXJKX"]
		require.NotNil(t, d1)
		assert.Equal(t, "example2.com", d1)
	})

	t.Run("should return a home domain if 1 public key is valid and also errors if 1 public key is invalid", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockHomeDomainsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewHomeDomainsHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("POST", "/api/v1/home-domains?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF&public_key=asdff", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetHomeDomains(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data HomeDomainsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		homeDomains := response
		d0 := homeDomains.Data.HomeDomains["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		assert.Equal(t, "example.com", d0)
		e0 := homeDomains.Data.Error.Error_keys[0]
		require.NotNil(t, e0)
		assert.Equal(t, "asdff", e0.PublicKey)
		assert.Equal(t, "base32 decode failed: illegal base32 data at input byte 5", e0.ErrorMessage)

	})

	t.Run("should dedupe public keys when requesting home domains", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockHomeDomainsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewHomeDomainsHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("POST", "/api/v1/home-domains?public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF&public_key=GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetHomeDomains(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data HomeDomainsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		homeDomains := response
		d0 := homeDomains.Data.HomeDomains["GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"]
		require.NotNil(t, d0)
		assert.Equal(t, "example.com", d0)
	})
	t.Run("should not return home domains if public key has no home domain", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockHomeDomainsData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			GetLedgerEntryOverride: utils.MockLedgerEntryData.LedgerEntry,
		}

		handler := NewHomeDomainsHandler(mockRPC)

		body := ``

		req, _ := http.NewRequest("POST", "/api/v1/home-domains?public_key=GDCSWQBW6GWUPOZ7PJTSSSLL5M5ICJUEIRQZ3TLVNO7PCOOX2PSLISRR", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetHomeDomains(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data HomeDomainsResponse `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		homeDomains := response
		d0 := homeDomains.Data.HomeDomains["GDCSWQBW6GWUPOZ7PJTSSSLL5M5ICJUEIRQZ3TLVNO7PCOOX2PSLISRR"]
		assert.Equal(t, "", d0)
	})

}