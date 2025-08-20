package utils

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewErrorResponseWriter(t *testing.T) {
	w := NewErrorResponseWriter(false)
	require.NotNil(t, w)
	assert.False(t, w.FailWrite)
}

func TestErrorResponseWriter_Write(t *testing.T) {
	w := NewErrorResponseWriter(false)
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", w.Body.String())
}

func TestErrorResponseWriter_Write_Fail(t *testing.T) {
	w := NewErrorResponseWriter(true)
	_, err := w.Write([]byte("fail"))
	assert.Error(t, err)
}

func TestErrorResponseWriter_WriteHeader(t *testing.T) {
	w := NewErrorResponseWriter(false)
	w.WriteHeader(http.StatusTeapot)
	assert.Equal(t, http.StatusTeapot, w.Code)
}

func TestErrorResponseWriter_Header(t *testing.T) {
	w := NewErrorResponseWriter(false)
	h := w.Header()
	require.NotNil(t, h)
	h.Set("X-Test", "value")
	assert.Equal(t, "value", w.Header().Get("X-Test"))
}

func TestMockHTTPClient(t *testing.T) {
	expected := map[string]string{"foo": "bar"}
	client := NewMockHTTPClient(expected)

	resp, err := client.Get("http://example.com")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	var actual map[string]string
	err = json.Unmarshal(body, &actual)
	require.NoError(t, err)

	require.Equal(t, expected, actual)
}
