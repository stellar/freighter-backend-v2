package handlers

import (
	"errors"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
)

type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// CustomHandler is a wrapper that allows us to process and return errors from different handlers.
func CustomHandler(f HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := f(w, r)
		if err != nil {
			var apiError *httperror.HttpError
			if !errors.As(err, &apiError) {
				apiError = httperror.NewHttpError(err.Error(), err, http.StatusInternalServerError, nil)
			}
			apiError.Render(w)
		}
	}
}
