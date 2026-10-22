package api

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestVerifyWebhookSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	raw := []byte(`{"b":1, "a":2}`) // not Go's canonical form: the raw bytes are signed
	signed := func(timestamp string, secret string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/hooks/x", nil)
		if timestamp != "" {
			req.Header.Set("X-Signature-Timestamp", timestamp)
		}
		req.Header.Set("X-Signature-Sha256", hex.EncodeToString(webhookSignature(secret, timestamp, raw)))
		return req
	}
	fresh := strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)
	stale := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	cases := []struct {
		name        string
		req         *http.Request
		allowLegacy bool
		ok          bool
	}{
		{"timestamped", signed(fresh, "s3cret-signing-key"), false, true},
		{"stale timestamp", signed(stale, "s3cret-signing-key"), false, false},
		{"wrong secret", signed(fresh, "other-key"), false, false},
		{"legacy rejected by default", signed("", "s3cret-signing-key"), false, false},
		{"legacy allowed when enabled", signed("", "s3cret-signing-key"), true, true},
	}
	for _, tc := range cases {
		err := verifyWebhookSignature(tc.req, "s3cret-signing-key", raw, now, tc.allowLegacy)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	// A timestamp cannot be swapped onto another delivery's signature.
	req := signed(fresh, "s3cret-signing-key")
	req.Header.Set("X-Signature-Timestamp", strconv.FormatInt(now.Unix(), 10))
	if verifyWebhookSignature(req, "s3cret-signing-key", raw, now, false) == nil {
		t.Error("signature verified after its timestamp was changed")
	}
	if verifyWebhookSignature(signed(fresh, ""), "", raw, now, false) == nil {
		t.Error("empty secret must never verify")
	}
}
