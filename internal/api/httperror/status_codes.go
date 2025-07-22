package httperror

import (
	"fmt"
	"net/http"
)

// Common error constructors for consistent status code usage

// BadRequest creates a 400 Bad Request error
func BadRequest(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusBadRequest, nil)
}

// BadRequestf creates a 400 Bad Request error with formatted message
func BadRequestf(format string, args ...interface{}) *HttpError {
	return NewHttpError(fmt.Sprintf(format, args...), nil, http.StatusBadRequest, nil)
}

// Unauthorized creates a 401 Unauthorized error
func Unauthorized(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusUnauthorized, nil)
}

// Forbidden creates a 403 Forbidden error
func Forbidden(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusForbidden, nil)
}

// NotFound creates a 404 Not Found error
func NotFound(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusNotFound, nil)
}

// NotFoundf creates a 404 Not Found error with formatted message
func NotFoundf(format string, args ...interface{}) *HttpError {
	return NewHttpError(fmt.Sprintf(format, args...), nil, http.StatusNotFound, nil)
}

// Conflict creates a 409 Conflict error
func Conflict(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusConflict, nil)
}

// UnprocessableEntity creates a 422 Unprocessable Entity error
func UnprocessableEntity(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusUnprocessableEntity, nil)
}

// TooManyRequests creates a 429 Too Many Requests error
func TooManyRequests(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusTooManyRequests, nil)
}

// InternalServerError creates a 500 Internal Server Error
func InternalServerError(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusInternalServerError, nil)
}

// InternalServerErrorf creates a 500 Internal Server Error with formatted message
func InternalServerErrorf(format string, args ...interface{}) *HttpError {
	return NewHttpError(fmt.Sprintf(format, args...), nil, http.StatusInternalServerError, nil)
}

// ServiceUnavailable creates a 503 Service Unavailable error
func ServiceUnavailable(message string, err error) *HttpError {
	return NewHttpError(message, err, http.StatusServiceUnavailable, nil)
}

// ServiceUnavailablef creates a 503 Service Unavailable error with formatted message
func ServiceUnavailablef(format string, args ...interface{}) *HttpError {
	return NewHttpError(fmt.Sprintf(format, args...), nil, http.StatusServiceUnavailable, nil)
}

// WithExtras adds extra data to any HttpError
func WithExtras(err *HttpError, extras map[string]interface{}) *HttpError {
	err.Extras = extras
	return err
}
