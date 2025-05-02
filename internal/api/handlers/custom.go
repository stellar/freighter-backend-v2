package handlers

import (
	"errors"
	"net/http"
)

type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// CustomHandler is a wrapper that allows us to process and return errors from different handlers.
func CustomHandler(f HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := f(w, r)
		if err != nil {
			status := http.StatusInternalServerError
			var apiError *Error
			if errors.As(err, &apiError) {
				status = apiError.HttpStatus()
			}
			http.Error(w, err.Error(), status)
		}
	}
}
