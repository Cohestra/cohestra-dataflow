package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.temporal.io/sdk/temporal"
)

func TestActivityErrorMarksPermanentConnectorErrorsNonRetryable(t *testing.T) {
	runtime := &connectors.Runtime{Handlers: map[string]connectors.Handler{}, Sources: map[string]connectors.Source{}}
	_, _, err := runtime.Handle(context.Background(), "transform.missing", nil, nil, connectors.HandlerContext{})
	var appErr *temporal.ApplicationError
	if !errors.As(activityError(err), &appErr) || !appErr.NonRetryable() || appErr.Type() != connectors.UnknownActivityError {
		t.Fatalf("activityError(%v) = %#v", err, appErr)
	}
	transient := fmt.Errorf("dial postgres://app:hunter2@db:5432/x: connection reset by peer")
	if !errors.As(activityError(transient), &appErr) || appErr.NonRetryable() || strings.Contains(appErr.Error(), "hunter2") || errors.Unwrap(appErr) != nil {
		t.Fatalf("transient errors must stay retryable, redacted and without the raw cause: %#v", appErr)
	}
	if activityError(context.Canceled) != context.Canceled {
		t.Fatal("cancellation must pass through unchanged")
	}
	if activityError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}

func TestOversizedPayloadReadIsNonRetryableWithFix(t *testing.T) {
	p := &Payloads{MaxPayloadBytes: 100}
	_, err := p.Read(context.Background(), &model.DataRef{Type: "inline", SizeBytes: 1000}, nil)
	var appErr *temporal.ApplicationError
	if !errors.As(activityError(err), &appErr) || !appErr.NonRetryable() || appErr.Type() != PayloadTooLargeError {
		t.Fatalf("oversized read error = %v", err)
	}
	if !strings.Contains(err.Error(), "MAX_PAYLOAD_BYTES") || !strings.Contains(err.Error(), "pageSize") {
		t.Fatalf("error should name the limit and the fix: %v", err)
	}
}

func TestRedactTextMasksCredentials(t *testing.T) {
	tests := map[string]string{
		`dial postgres://dataflow:s3cr3t@db:5432/app failed`:             `dial postgres://[REDACTED]@db:5432/app failed`,
		`auth failed: password=hunter2 user=bob`:                         `auth failed: password=[REDACTED] user=bob`,
		`request had Authorization: Bearer abc.def.ghi`:                  `request had Authorization: [REDACTED]`,
		`snowflake: {"token":"abc123","code":390100}`:                    `snowflake: {"token":"[REDACTED]","code":390100}`,
		`GET https://api.example.com/v1?api_key=XYZ&page=2 returned 500`: `GET https://api.example.com/v1?api_key=[REDACTED]&page=2 returned 500`,
	}
	for input, want := range tests {
		if got := redactText(input); got != want {
			t.Errorf("redactText(%q)\n got %q\nwant %q", input, got, want)
		}
	}
	if got := redactText(strings.Repeat("x", 5000)); len(got) > maxErrorTextLength+len("…") {
		t.Fatalf("redacted text not capped: %d bytes", len(got))
	}
	if redactError("") != nil {
		t.Fatal("empty error must stay NULL")
	}
}

func TestDedupeHashesReadFromRefOrLegacyInline(t *testing.T) {
	a := &Activities{Payloads: &Payloads{MaxPayloadBytes: 1 << 20}}
	ref, err := a.Payloads.Write(context.Background(), []string{"h1", "h2"}, "tenant", "exec", "node-dedupe-keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	hashes, err := a.dedupeHashes(context.Background(), nil, ref, "")
	if err != nil || strings.Join(hashes, ",") != "h1,h2" {
		t.Fatalf("hashes from ref = %v, %v", hashes, err)
	}
	hashes, err = a.dedupeHashes(context.Background(), []string{"legacy"}, nil, "")
	if err != nil || strings.Join(hashes, ",") != "legacy" {
		t.Fatalf("legacy inline hashes = %v, %v", hashes, err)
	}
}

func TestActivityErrorDoesNotLeakPermanentCause(t *testing.T) {
	err := activityError(&connectors.PermanentError{Type: connectors.InvalidConfigError, Err: errors.New("postgres.fetch: bad dsn postgres://app:hunter2@db/x")})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() || strings.Contains(appErr.Error(), "hunter2") || errors.Unwrap(appErr) != nil {
		t.Fatalf("permanent error leaked its raw cause: %v", err)
	}
}

func TestRedactTextCutsOnRuneBoundary(t *testing.T) {
	value := redactText(strings.Repeat("a", maxErrorTextLength-1) + "é" + strings.Repeat("b", 10))
	if !utf8.ValidString(value) || !strings.HasSuffix(value, "a…") {
		t.Fatalf("truncated text is not valid UTF-8 or cut wrongly: %q", value[len(value)-8:])
	}
}
