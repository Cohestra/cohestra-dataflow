package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

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
	transient := fmt.Errorf("connection reset by peer")
	if activityError(transient) != transient {
		t.Fatal("transient errors must stay retryable")
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
