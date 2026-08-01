// ABOUTME: Handler tests for POST /api/v1/accounts/positions: request
// ABOUTME: validation, error translation, and the success envelope.
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

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const positionsTestAddress = "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552"

func servePositions(t *testing.T, svc types.PositionsService, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewAccountPositionsHandler(svc, 100)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/accounts/positions", CustomHandler(handler.GetAccountsPositions))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)))
	return rec
}

func TestGetAccountsPositionsSuccess(t *testing.T) {
	total := 3619.27
	svc := &utils.MockPositionsService{
		GetAccountsPositionsResult: []*types.AccountPositions{{
			Address:       positionsTestAddress,
			TotalValueUSD: &total,
			Positions: []types.PoolPosition{{
				Protocol: "blend",
				ID:       "CCEBVDYMCCECIVWVOJSKUNLTVDIRLTRUCVZDVLKXKQZWSCF3DVQGJVIX",
			}},
		}},
	}

	rec := servePositions(t, svc, "/api/v1/accounts/positions?network=TESTNET", `{"addresses": ["`+positionsTestAddress+`"]}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data []types.AccountPositions `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, positionsTestAddress, body.Data[0].Address)
	require.NotNil(t, body.Data[0].TotalValueUSD)
	assert.InDelta(t, 3619.27, *body.Data[0].TotalValueUSD, 1e-9)
	require.Len(t, body.Data[0].Positions, 1)
	assert.Equal(t, "blend", body.Data[0].Positions[0].Protocol)
}

func TestGetAccountsPositionsValidation(t *testing.T) {
	svc := &utils.MockPositionsService{}
	valid := `{"addresses": ["` + positionsTestAddress + `"]}`

	t.Run("invalid network", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/positions?network=DOGENET", valid)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing network", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/positions", valid)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("empty addresses", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/positions?network=TESTNET", `{"addresses": []}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid address", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/positions?network=TESTNET", `{"addresses": ["nope"]}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/positions?network=TESTNET", `{`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestGetAccountsPositionsErrorTranslation(t *testing.T) {
	body := `{"addresses": ["` + positionsTestAddress + `"]}`

	t.Run("upstream error maps to 502", func(t *testing.T) {
		svc := &utils.MockPositionsService{
			GetAccountsPositionsError: &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("boom")},
		}
		rec := servePositions(t, svc, "/api/v1/accounts/positions?network=TESTNET", body)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})

	t.Run("unclassified error maps to 500", func(t *testing.T) {
		svc := &utils.MockPositionsService{GetAccountsPositionsError: errors.New("wat")}
		rec := servePositions(t, svc, "/api/v1/accounts/positions?network=TESTNET", body)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestGetAccountsPositionsEmptyIs200(t *testing.T) {
	rec := servePositions(t, &utils.MockPositionsService{}, "/api/v1/accounts/positions?network=TESTNET", `{"addresses": ["`+positionsTestAddress+`"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`)
}
