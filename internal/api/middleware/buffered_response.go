package middleware

import (
	"bytes"
	"net/http"
)

// BufferedResponseWriter wraps http.ResponseWriter to buffer the response
// and delay writing the status code until explicitly flushed
type BufferedResponseWriter struct {
	http.ResponseWriter
	statusCode int
	buffer     *bytes.Buffer
	written    bool
}

// NewBufferedResponseWriter creates a new BufferedResponseWriter
func NewBufferedResponseWriter(w http.ResponseWriter) *BufferedResponseWriter {
	return &BufferedResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		buffer:         &bytes.Buffer{},
		written:        false,
	}
}

// WriteHeader captures the status code but doesn't write it yet
func (b *BufferedResponseWriter) WriteHeader(statusCode int) {
	if !b.written {
		b.statusCode = statusCode
	}
}

// Write captures the response body but doesn't write it yet
func (b *BufferedResponseWriter) Write(data []byte) (int, error) {
	if !b.written {
		return b.buffer.Write(data)
	}
	return b.ResponseWriter.Write(data)
}

// StatusCode returns the captured status code
func (b *BufferedResponseWriter) StatusCode() int {
	return b.statusCode
}

// Body returns the buffered body
func (b *BufferedResponseWriter) Body() []byte {
	return b.buffer.Bytes()
}

// Flush writes the buffered response to the underlying ResponseWriter
func (b *BufferedResponseWriter) Flush() error {
	if !b.written {
		b.ResponseWriter.WriteHeader(b.statusCode)
		_, err := b.ResponseWriter.Write(b.buffer.Bytes())
		b.written = true
		return err
	}
	return nil
}

// Reset clears the buffer and resets status code
func (b *BufferedResponseWriter) Reset() {
	b.statusCode = http.StatusOK
	b.buffer.Reset()
	b.written = false
}
