// Package fault defines transport-independent application failures.
package fault

import "errors"

type Kind uint8

const (
	Invalid Kind = iota + 1
	Unauthenticated
	Forbidden
	NotFound
	Conflict
	RateLimited
	Unavailable
	Internal
	Gone
	TooLarge
	UnsupportedMedia
)

// Error separates the public error contract from private diagnostic details.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Cause   error
}

func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

func Wrap(kind Kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func As(err error) (*Error, bool) {
	var target *Error
	ok := errors.As(err, &target)
	return target, ok
}
