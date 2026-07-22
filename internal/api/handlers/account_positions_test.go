// ABOUTME: Handler tests for GET /api/v1/accounts/{address}/positions:
// ABOUTME: validation, error translation, and the success envelope.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const positionsTestAddress = "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552"

func servePositions(t *testing.T, svc types.PositionsService, target string) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewAccountPositionsHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{address}/positions", CustomHandler(handler.GetAccountPositions))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestGetAccountPositionsSuccess(t *testing.T) {
	total := 3619.27
	svc := &utils.MockPositionsService{
		GetAccountPositionsResult: &types.AccountPositions{
			TotalValueUSD: &total,
			Positions: []types.PoolPosition{{
				Protocol: "blend",
				ID:       "CCEBVDYMCCECIVWVOJSKUNLTVDIRLTRUCVZDVLKXKQZWSCF3DVQGJVIX",
			}},
		},
	}

	rec := servePositions(t, svc, "/api/v1/accounts/"+positionsTestAddress+"/positions?network=TESTNET")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data types.AccountPositions `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Data.TotalValueUSD)
	assert.InDelta(t, 3619.27, *body.Data.TotalValueUSD, 1e-9)
	require.Len(t, body.Data.Positions, 1)
	assert.Equal(t, "blend", body.Data.Positions[0].Protocol)
}

func TestGetAccountPositionsValidation(t *testing.T) {
	svc := &utils.MockPositionsService{}

	t.Run("invalid network", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/"+positionsTestAddress+"/positions?network=DOGENET")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing network", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/"+positionsTestAddress+"/positions")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid address", func(t *testing.T) {
		rec := servePositions(t, svc, "/api/v1/accounts/not-an-address/positions?network=TESTNET")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestGetAccountPositionsErrorTranslation(t *testing.T) {
	t.Run("upstream error maps to 502", func(t *testing.T) {
		svc := &utils.MockPositionsService{
			GetAccountPositionsError: &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("boom")},
		}
		rec := servePositions(t, svc, "/api/v1/accounts/"+positionsTestAddress+"/positions?network=TESTNET")
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})

	t.Run("unclassified error maps to 500", func(t *testing.T) {
		svc := &utils.MockPositionsService{GetAccountPositionsError: errors.New("wat")}
		rec := servePositions(t, svc, "/api/v1/accounts/"+positionsTestAddress+"/positions?network=TESTNET")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestGetAccountPositionsEmptyIs200(t *testing.T) {
	rec := servePositions(t, &utils.MockPositionsService{}, "/api/v1/accounts/"+positionsTestAddress+"/positions?network=TESTNET")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"positions":[]`)
}
