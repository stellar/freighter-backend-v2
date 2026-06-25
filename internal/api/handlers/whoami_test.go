package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/auth"
)

func TestWhoami_Authenticated(t *testing.T) {
	h := NewWhoamiHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
	r = r.WithContext(auth.ContextWithUserID(r.Context(), "deadbeef"))
	w := httptest.NewRecorder()

	require.NoError(t, h.Whoami(w, r))

	var resp struct {
		Data WhoamiResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Data.Authenticated)
	assert.Equal(t, "deadbeef", resp.Data.UserID)
}

func TestWhoami_Anonymous(t *testing.T) {
	h := NewWhoamiHandler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
	w := httptest.NewRecorder()

	require.NoError(t, h.Whoami(w, r))

	var resp struct {
		Data WhoamiResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Data.Authenticated)
	assert.Empty(t, resp.Data.UserID)
}
