// ABOUTME: Provides standardized HTTP response utilities for successful responses.
// ABOUTME: Centralizes status code setting and JSON encoding for consistency.

package response

import (
	"encoding/json"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/logger"
)

// JSON writes a JSON response with the given status code
func JSON(w http.ResponseWriter, statusCode int, data interface{}) error {
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response", "error", err)
		return err
	}

	return nil
}

// OK writes a 200 OK JSON response
func OK(w http.ResponseWriter, data interface{}) error {
	return JSON(w, http.StatusOK, data)
}

// Created writes a 201 Created JSON response
func Created(w http.ResponseWriter, data interface{}) error {
	return JSON(w, http.StatusCreated, data)
}

// Accepted writes a 202 Accepted JSON response
func Accepted(w http.ResponseWriter, data interface{}) error {
	return JSON(w, http.StatusAccepted, data)
}

// NoContent writes a 204 No Content response
func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Text writes a plain text response with the given status code
func Text(w http.ResponseWriter, statusCode int, message string) error {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)

	if _, err := w.Write([]byte(message)); err != nil {
		logger.Error("Failed to write text response", "error", err)
		return err
	}

	return nil
}
