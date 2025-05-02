package handlers

type Error struct {
	error
	HttpStatusCode int    `json:"status_code"`
}

func (e *Error) Unwrap() error {
	return e.error
}

func (e *Error) Error() string {
	return e.error.Error()
}

func (e *Error) HttpStatus() int {
	return e.HttpStatusCode
}

func WithHttpStatus(err error, status int) *Error {
	return &Error{
		error:          err,
		HttpStatusCode: status,
	}
}
