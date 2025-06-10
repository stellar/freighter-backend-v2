package handlers

import (
	"errors"
	"net/http"

	"github.com/stellar/freighter-backend-v2/internal/api/httperror"
	"github.com/stellar/freighter-backend-v2/internal/api/middleware"
)

type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

type HttpResponse struct {
	Data any `json:"data"`
}

// CustomHandler is a wrapper that allows us to process and return errors from different handlers.
// When used with the buffered response writer from logging middleware,
// it can reset the response and send proper error responses.
func CustomHandler(f HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := f(w, r)
		if err != nil {
			if bw, ok := w.(*middleware.BufferedResponseWriter); ok {
				bw.Reset()
			}

			var apiError *httperror.HttpError
			if !errors.As(err, &apiError) {
				apiError = httperror.NewHttpError(err.Error(), err, http.StatusInternalServerError, nil)
			}
			apiError.Render(w)
		}
	}
}
