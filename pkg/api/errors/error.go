package errors

import "fmt"

type Error struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func New(status int, code, message string, cause error) *Error {
	return &Error{Status: status, Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }
