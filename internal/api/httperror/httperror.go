package httperror

import (
	"net/http"

	"github.com/stellar/go/support/render/httpjson"
)

type HttpError struct {
	Message    string                 `json:"message"`
	Err        error                  `json:"originalError,omitempty"`
	StatusCode int                    `json:"statusCode"`
	Extras     map[string]interface{} `json:"extras,omitempty"`
}

func (e *HttpError) Unwrap() error {
	return e.Err
}

func (e *HttpError) Error() string {
	return e.Message
}

func (e *HttpError) HttpStatus() int {
	return e.StatusCode
}

func (e *HttpError) Render(w http.ResponseWriter) {
	httpjson.RenderStatus(w, e.StatusCode, e, httpjson.JSON)
}

func NewHttpError(message string, err error, statusCode int, extras map[string]interface{}) *HttpError {
	return &HttpError{
		Message:    message,
		Err:        err,
		StatusCode: statusCode,
		Extras:     extras,
	}
}
