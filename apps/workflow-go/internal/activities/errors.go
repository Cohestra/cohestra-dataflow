package activities

import (
	"regexp"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"go.temporal.io/sdk/temporal"
)

// activityError marks connector errors that retrying cannot fix (bad records,
// bad configuration, unknown step types) as non-retryable, keeping the type
// names the workflow retry policy already lists.
func activityError(err error) error {
	if err == nil {
		return nil
	}
	if permanent, ok := connectors.AsPermanent(err); ok {
		return temporal.NewNonRetryableApplicationError(redactText(err.Error()), permanent.Type, err)
	}
	return err
}

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
		value = value[:maxErrorTextLength] + "…"
	}
	return value
}
