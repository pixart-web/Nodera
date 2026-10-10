// Package apierr defines Nodera's normalized error shape (rule 35 /
// docs/API.md): every error returned across a module boundary or an HTTP
// response carries a stable machine-readable code, never a raw vendor or
// driver error string.
package apierr

import "fmt"

type Code string

const (
	CodeValidation      Code = "VALIDATION_ERROR"
	CodeUnauthenticated Code = "UNAUTHENTICATED"
	CodeForbidden       Code = "FORBIDDEN"
	CodeNotFound        Code = "NOT_FOUND"
	CodeConflict        Code = "CONFLICT"
	CodeRateLimited     Code = "RATE_LIMITED"
	CodeInternal        Code = "INTERNAL_ERROR"
	CodeUnavailable     Code = "UNAVAILABLE"
	CodeNotImplemented  Code = "NOT_IMPLEMENTED"
)

// Error is Nodera's normalized error type. HTTPStatus is set by the
// httpserver layer via StatusFor; domain code should not know about HTTP.
type Error struct {
	Code    Code
	Message string
	// Details carries structured, client-safe context (e.g. the offending
	// field). Never put secrets or internal error text in here.
	Details map[string]any
	// Err is the underlying cause, kept for logging/correlation but never
	// serialized to the client — see docs/SECURITY.md on stack trace exposure.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

// WithDetails returns a copy of the error with structured details attached.
func (e *Error) WithDetails(d map[string]any) *Error {
	c := *e
	c.Details = d
	return &c
}

// FieldValidation is a validation error that names the offending field.
func FieldValidation(field, message string) *Error {
	return Validation(message).WithDetails(map[string]any{"field": field})
}

func NotFound(resource string) *Error {
	return New(CodeNotFound, resource+" not found")
}

func Validation(message string) *Error {
	return New(CodeValidation, message)
}

func Forbidden(message string) *Error {
	return New(CodeForbidden, message)
}

func Unauthenticated(message string) *Error {
	return New(CodeUnauthenticated, message)
}

func Conflict(message string) *Error {
	return New(CodeConflict, message)
}

func NotImplemented(message string) *Error {
	return New(CodeNotImplemented, message)
}
