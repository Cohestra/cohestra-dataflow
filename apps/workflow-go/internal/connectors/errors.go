package connectors

import (
	"errors"
	"fmt"
)

// Error types that retrying cannot fix. The activity layer turns them into
// non-retryable Temporal application errors with the same type name, so a bad
// record or bad configuration fails the node once instead of five times.
const (
	SchemaValidationError = "SchemaValidationError"
	UnknownActivityError  = "UnknownActivityError"
	InvalidConfigError    = "InvalidConfigError"
)

type PermanentError struct {
	Type string
	Err  error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

func permanent(kind, format string, args ...interface{}) error {
	return &PermanentError{Type: kind, Err: fmt.Errorf(format, args...)}
}

// schemaError reports a record that does not have the shape a step requires.
func schemaError(format string, args ...interface{}) error {
	return permanent(SchemaValidationError, format, args...)
}

// configError reports step configuration that can never succeed as written.
func configError(format string, args ...interface{}) error {
	return permanent(InvalidConfigError, format, args...)
}

// AsPermanent returns the permanent error carried by err, if any.
func AsPermanent(err error) (*PermanentError, bool) {
	var permanentErr *PermanentError
	if errors.As(err, &permanentErr) {
		return permanentErr, true
	}
	return nil, false
}
