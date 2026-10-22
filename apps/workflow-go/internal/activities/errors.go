package activities

import (
	"context"
	"errors"
	"regexp"
	"unicode/utf8"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"go.temporal.io/sdk/temporal"
)

// activityError marks connector errors that retrying cannot fix (bad records,
// bad configuration, unknown step types) as non-retryable, keeping the type
// names the workflow retry policy already lists. Every other connector error
// stays retryable. Both carry only redacted text: the original error is not
// attached as the cause, since Temporal stores and shows the whole chain.
func activityError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *temporal.ApplicationError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &appErr) {
		return err
	}
	if permanent, ok := connectors.AsPermanent(err); ok {
		return temporal.NewNonRetryableApplicationError(redactText(err.Error()), permanent.Type, nil)
	}
	return temporal.NewApplicationError(redactText(err.Error()), ConnectorError)
}

// ConnectorError is the type of retryable connector failures.
const ConnectorError = "ConnectorError"

var (
	urlUserinfo   = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)
	bearerToken   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
	sensitiveText = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|authorization|api[-_]?key|access[-_]?key|private[-_]?key)("?\s*[=:]\s*"?)(?:(?:bearer|basic)\s+)?[^\s,;}"&]+`)
)

const maxErrorTextLength = 2000

// redactText masks credentials that drivers and HTTP clients tend to echo in
// error messages (DSN userinfo, key=value secrets, bearer tokens) and caps the
// length, before the text is stored on node runs and shown to users.
func redactText(value string) string {
	value = urlUserinfo.ReplaceAllString(value, "$1[REDACTED]@")
	value = sensitiveText.ReplaceAllString(value, "$1$2[REDACTED]")
	value = bearerToken.ReplaceAllString(value, "$1 [REDACTED]")
	if len(value) > maxErrorTextLength {
		// Cut on a rune boundary: Postgres rejects invalid UTF-8 in text columns.
		end := maxErrorTextLength
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		value = value[:end] + "…"
	}
	return value
}
