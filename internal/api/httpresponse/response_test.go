package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testData struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func TestJSON(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		data         interface{}
		expectedBody string
	}{
		{
			name:         "success with struct",
			statusCode:   http.StatusOK,
			data:         testData{Message: "hello", Count: 42},
			expectedBody: `{"message":"hello","count":42}`,
		},
		{
			name:         "success with map",
			statusCode:   http.StatusCreated,
			data:         map[string]string{"key": "value"},
			expectedBody: `{"key":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := JSON(w, tt.statusCode, tt.data)

			require.NoError(t, err)
			assert.Equal(t, tt.statusCode, w.Code)
			// Content-Type is set by ResponseHeader middleware, not here
			assert.Empty(t, w.Header().Get("Content-Type"))

			// Verify JSON output
			var result interface{}
			err = json.Unmarshal(w.Body.Bytes(), &result)
			require.NoError(t, err)
		})
	}
}

func TestOK(t *testing.T) {
	w := httptest.NewRecorder()
	data := testData{Message: "success", Count: 1}

	err := OK(w, data)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Content-Type"))
}

func TestCreated(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"id": "123"}

	err := Created(w, data)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Empty(t, w.Header().Get("Content-Type"))
}

func TestAccepted(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"status": "processing"}

	err := Accepted(w, data)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Empty(t, w.Header().Get("Content-Type"))
}

func TestNoContent(t *testing.T) {
	w := httptest.NewRecorder()

	err := NoContent(w)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestText(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
	}{
		{
			name:       "success message",
			statusCode: http.StatusOK,
			message:    "Operation successful",
		},
		{
			name:       "error message",
			statusCode: http.StatusBadRequest,
			message:    "Invalid input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := Text(w, tt.statusCode, tt.message)

			require.NoError(t, err)
			assert.Equal(t, tt.statusCode, w.Code)
			assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
			assert.Equal(t, tt.message, w.Body.String())
		})
	}
}
