package httperror

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHttpError(t *testing.T) {
	err := errors.New("root cause")
	extra := map[string]interface{}{"foo": "bar"}
	h := NewHttpError("msg", err, 418, extra)
	require.NotNil(t, h)
	assert.Equal(t, "msg", h.Message)
	assert.Equal(t, err, h.Err)
	assert.Equal(t, 418, h.StatusCode)
	assert.Equal(t, extra, h.Extras)
}

func TestHttpError_ErrorAndUnwrap(t *testing.T) {
	err := errors.New("root cause")
	h := NewHttpError("msg", err, 400, nil)
	assert.Equal(t, "msg", h.Error())
	assert.Equal(t, err, h.Unwrap())
}

func TestHttpError_HttpStatus(t *testing.T) {
	h := NewHttpError("msg", nil, 404, nil)
	assert.Equal(t, 404, h.HttpStatus())
}

func TestHttpError_Render(t *testing.T) {
	h := NewHttpError("msg", nil, 500, map[string]any{"foo": 1})
	rr := httptest.NewRecorder()
	h.Render(rr)
	assert.Equal(t, 500, rr.Code)
	assert.Contains(t, rr.Body.String(), "msg")
	assert.Contains(t, rr.Body.String(), "foo")
}

func TestHttpError_NilExtras(t *testing.T) {
	h := NewHttpError("msg", nil, 200, nil)
	rr := httptest.NewRecorder()
	h.Render(rr)
	assert.Equal(t, 200, rr.Code)
	assert.Contains(t, rr.Body.String(), "msg")
}
