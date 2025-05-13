package utils

import (
	"errors"
	"net/http"
	"net/http/httptest"
)

// ErrorResponseWriter is a custom http.ResponseWriter that can be configured to error on Write.
// It embeds httptest.ResponseRecorder to act as a pass-through for most functionality.
type ErrorResponseWriter struct {
	*httptest.ResponseRecorder
	FailWrite bool
}

// NewErrorResponseWriter creates a new ErrorResponseWriter.
func NewErrorResponseWriter(failWrite bool) *ErrorResponseWriter {
	return &ErrorResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		FailWrite:        failWrite,
	}
}

// Write implements the io.Writer interface.
// If FailWrite is true, it returns an error. Otherwise, it calls the embedded recorder's Write.
func (w *ErrorResponseWriter) Write(data []byte) (int, error) {
	if w.FailWrite {
		return 0, errors.New("simulated writer error")
	}
	return w.ResponseRecorder.Write(data)
}

// WriteHeader calls the embedded recorder's WriteHeader.
// This ensures that the 'Code' field in the ResponseRecorder is set.
func (w *ErrorResponseWriter) WriteHeader(statusCode int) {
	w.ResponseRecorder.WriteHeader(statusCode)
}

// Header calls the embedded recorder's Header.
// This is necessary to fulfill the http.ResponseWriter interface.
func (w *ErrorResponseWriter) Header() http.Header {
	return w.ResponseRecorder.Header()
}
