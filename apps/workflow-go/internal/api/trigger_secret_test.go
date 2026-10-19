package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
)

func TestJSONResponseBlanksTriggerSecrets(t *testing.T) {
	res := httptest.NewRecorder()
	jsonResponse(res, 200, map[string]interface{}{
		"rows": []interface{}{map[string]interface{}{
			"definition": map[string]interface{}{"trigger": map[string]interface{}{"type": "webhook", "path": "orders", "secret": "s3cret-signing-key-123"}},
			"big":        int64(9007199254740993),
		}},
		"trigger":  model.Trigger{Type: "webhook", Path: "orders", Secret: "s3cret-signing-key-123"},
		"apiToken": map[string]interface{}{"secret": "shown-once-token"},
	})
	body := res.Body.String()
	if strings.Contains(body, "s3cret-signing-key-123") {
		t.Fatalf("trigger secret leaked: %s", body)
	}
	if !strings.Contains(body, `"secretSet":true`) {
		t.Fatalf("redacted trigger should say a secret is set: %s", body)
	}
	if !strings.Contains(body, "shown-once-token") {
		t.Fatalf("non-trigger secrets must pass through: %s", body)
	}
	if !strings.Contains(body, "9007199254740993") {
		t.Fatalf("large integers must survive redaction: %s", body)
	}
}

func TestValidateWebhookTrigger(t *testing.T) {
	cases := map[string]struct {
		trigger model.Trigger
		ok      bool
	}{
		"manual":          {model.Trigger{Type: "manual"}, true},
		"good":            {model.Trigger{Type: "webhook", Path: "orders-hook", Secret: "0123456789abcdef0123"}, true},
		"placeholder":     {model.Trigger{Type: "webhook", Path: "orders-hook", Secret: "change-me"}, false},
		"short secret":    {model.Trigger{Type: "webhook", Path: "orders-hook", Secret: "short"}, false},
		"missing secret":  {model.Trigger{Type: "webhook", Path: "orders-hook"}, false},
		"path with slash": {model.Trigger{Type: "webhook", Path: "a/b", Secret: "0123456789abcdef0123"}, false},
		"path too short":  {model.Trigger{Type: "webhook", Path: "ab", Secret: "0123456789abcdef0123"}, false},
	}
	for name, tc := range cases {
		if err := validateWebhookTrigger(tc.trigger); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", name, err, tc.ok)
		}
	}
}
