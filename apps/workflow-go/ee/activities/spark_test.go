// Source-available under the Elastic License 2.0. See ee/LICENSE.

package activities

import "testing"

func TestSparkApplicationOwnedBy(t *testing.T) {
	body := []byte(`{"metadata":{"name":"dataflow-spark-1","labels":{"dataflow.io/execution-id":"exec-1"}}}`)
	if !sparkApplicationOwnedBy(body, "exec-1") {
		t.Fatal("application created for this execution must be reused on retry")
	}
	if sparkApplicationOwnedBy(body, "exec-2") || sparkApplicationOwnedBy([]byte(`{}`), "exec-1") || sparkApplicationOwnedBy(body, "") {
		t.Fatal("applications for other executions must not be attached")
	}
}
