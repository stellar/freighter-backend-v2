package httperror

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusCodeHelpers(t *testing.T) {
	baseErr := errors.New("base error")

	tests := []struct {
		name           string
		errorFunc      func() *HttpError
		expectedStatus int
		expectedMsg    string
	}{
		{
			name: "BadRequest",
			errorFunc: func() *HttpError {
				return BadRequest("bad request", baseErr)
			},
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "bad request",
		},
		{
			name: "BadRequestf",
			errorFunc: func() *HttpError {
				return BadRequestf("bad request: %s", "invalid input")
			},
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "bad request: invalid input",
		},
		{
			name: "Unauthorized",
			errorFunc: func() *HttpError {
				return Unauthorized("unauthorized", baseErr)
			},
			expectedStatus: http.StatusUnauthorized,
			expectedMsg:    "unauthorized",
		},
		{
			name: "Forbidden",
			errorFunc: func() *HttpError {
				return Forbidden("forbidden", baseErr)
			},
			expectedStatus: http.StatusForbidden,
			expectedMsg:    "forbidden",
		},
		{
			name: "NotFound",
			errorFunc: func() *HttpError {
				return NotFound("not found", baseErr)
			},
			expectedStatus: http.StatusNotFound,
			expectedMsg:    "not found",
		},
		{
			name: "NotFoundf",
			errorFunc: func() *HttpError {
				return NotFoundf("resource %s not found", "user")
			},
			expectedStatus: http.StatusNotFound,
			expectedMsg:    "resource user not found",
		},
		{
			name: "Conflict",
			errorFunc: func() *HttpError {
				return Conflict("conflict", baseErr)
			},
			expectedStatus: http.StatusConflict,
			expectedMsg:    "conflict",
		},
		{
			name: "UnprocessableEntity",
			errorFunc: func() *HttpError {
				return UnprocessableEntity("unprocessable", baseErr)
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedMsg:    "unprocessable",
		},
		{
			name: "TooManyRequests",
			errorFunc: func() *HttpError {
				return TooManyRequests("rate limited", baseErr)
			},
			expectedStatus: http.StatusTooManyRequests,
			expectedMsg:    "rate limited",
		},
		{
			name: "InternalServerError",
			errorFunc: func() *HttpError {
				return InternalServerError("server error", baseErr)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedMsg:    "server error",
		},
		{
			name: "InternalServerErrorf",
			errorFunc: func() *HttpError {
				return InternalServerErrorf("server error: %s", "database failed")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedMsg:    "server error: database failed",
		},
		{
			name: "ServiceUnavailable",
			errorFunc: func() *HttpError {
				return ServiceUnavailable("service unavailable", baseErr)
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedMsg:    "service unavailable",
		},
		{
			name: "ServiceUnavailablef",
			errorFunc: func() *HttpError {
				return ServiceUnavailablef("service %s is down", "database")
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedMsg:    "service database is down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.errorFunc()
			assert.Equal(t, tt.expectedStatus, err.StatusCode)
			assert.Equal(t, tt.expectedMsg, err.Message)
		})
	}
}

func TestWithExtras(t *testing.T) {
	err := BadRequest("bad request", nil)
	extras := map[string]interface{}{
		"field": "email",
		"value": "invalid",
	}

	errWithExtras := WithExtras(err, extras)
	assert.Equal(t, extras, errWithExtras.Extras)
	assert.Equal(t, err.StatusCode, errWithExtras.StatusCode)
	assert.Equal(t, err.Message, errWithExtras.Message)
}
