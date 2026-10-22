// Source-available under the Elastic License 2.0. See ee/LICENSE.

package activities

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
	sparkengine "github.com/dataflow-poc/workflow-go/ee/spark"
)

type SparkJobParams struct {
	Definition            model.PipelineDefinition `json:"definition"`
	TenantID, ExecutionID string
}
type SparkJobRef struct {
	Name, InputStateKey, InputSnapshot, OutputBefore string
	Noop                                             bool
}

func SubmitSparkJob(ctx context.Context, db *database.DB, rt *connectors.Runtime, p SparkJobParams) (SparkJobRef, error) {
	var source, sink *model.Node
	for i := range p.Definition.Nodes {
		node := &p.Definition.Nodes[i]
		if node.Type == "source" {
			source = node
		}
		if node.Type == "sink" {
			sink = node
		}
	}
	ref := SparkJobRef{}
	previous, current := "", ""
	if source != nil && source.ActivityType == "iceberg.fetch" {
		ref.InputStateKey = "spark:" + p.Definition.ID + ":" + source.ID
		var raw []byte
		_ = db.Pool.QueryRow(ctx, `SELECT cursor FROM connector_state WHERE tenant_id=$1 AND connection_id=$2`, p.TenantID, ref.InputStateKey).Scan(&raw)
		var state map[string]interface{}
		_ = json.Unmarshal(raw, &state)
		previous = fmt.Sprint(state["snapshotId"])
		if previous == "<nil>" {
			previous = ""
		}
		var err error
		current, err = rt.IcebergCurrentSnapshot(ctx, source.Config)
		if err != nil {
			return ref, err
		}
		ref.InputSnapshot = current
		if previous != "" && previous == current {
			ref.Noop = true
			return ref, nil
		}
	}
	if sink != nil && sink.ActivityType == "sink.iceberg" {
		ref.OutputBefore, _ = rt.IcebergCurrentSnapshot(ctx, sink.Config)
	}
	namespace := envValue("SPARK_NAMESPACE", "spark")
	spec, err := sparkengine.BuildApplication(p.Definition, p.ExecutionID, namespace, envValue("SPARK_SQL_IMAGE", "dataflow/spark-sql:latest"), previous, current)
	if err != nil {
		return ref, err
	}
	metadata := spec["metadata"].(map[string]interface{})
	ref.Name = metadata["name"].(string)
	body, _ := json.Marshal(spec)
	collection := "/apis/sparkoperator.k8s.io/v1beta2/namespaces/" + namespace + "/sparkapplications"
	_, err = kubernetesRequest(ctx, http.MethodPost, collection, body)
	var statusErr *kubernetesStatusError
	if errors.As(err, &statusErr) && statusErr.code == http.StatusConflict {
		// The name is derived from the execution, so a retry of this activity
		// after an earlier attempt created the application gets 409. Attach to
		// that application instead of failing the run.
		existing, getErr := kubernetesRequest(ctx, http.MethodGet, collection+"/"+ref.Name, nil)
		if getErr == nil && sparkApplicationOwnedBy(existing, p.ExecutionID) {
			return ref, nil
		}
	}
	return ref, err
}

// sparkApplicationOwnedBy reports whether a SparkApplication was created for
// this execution, using the execution-id label BuildApplication sets.
func sparkApplicationOwnedBy(body []byte, executionID string) bool {
	var app struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &app) != nil || executionID == "" {
		return false
	}
	return app.Metadata.Labels["dataflow.io/execution-id"] == executionID
}

type kubernetesStatusError struct {
	code int
	body []byte
}

func (e *kubernetesStatusError) Error() string {
	return fmt.Sprintf("Kubernetes API returned %d: %s", e.code, e.body)
}

func SparkJobStatus(ctx context.Context, ref SparkJobRef) (map[string]string, error) {
	path := "/apis/sparkoperator.k8s.io/v1beta2/namespaces/" + envValue("SPARK_NAMESPACE", "spark") + "/sparkapplications/" + ref.Name
	body, err := kubernetesRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var value struct {
		Status struct {
			ApplicationState struct{ State, ErrorMessage string } `json:"applicationState"`
		} `json:"status"`
	}
	if err = json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return map[string]string{"state": value.Status.ApplicationState.State, "error": value.Status.ApplicationState.ErrorMessage}, nil
}

func CancelSparkJob(ctx context.Context, ref SparkJobRef) error {
	_, err := kubernetesRequest(ctx, http.MethodDelete, "/apis/sparkoperator.k8s.io/v1beta2/namespaces/"+envValue("SPARK_NAMESPACE", "spark")+"/sparkapplications/"+ref.Name, nil)
	return err
}

type CommitSparkJobParams struct {
	Ref        SparkJobRef              `json:"ref"`
	Definition model.PipelineDefinition `json:"definition"`
	TenantID   string                   `json:"tenantId"`
}

func CommitSparkJob(ctx context.Context, db *database.DB, rt *connectors.Runtime, p CommitSparkJobParams) error {
	for _, node := range p.Definition.Nodes {
		if node.Type == "sink" && node.ActivityType == "sink.iceberg" {
			after, err := rt.IcebergCurrentSnapshot(ctx, node.Config)
			if err != nil {
				return err
			}
			if after == "" || after == p.Ref.OutputBefore {
				return fmt.Errorf("Spark completed without an Iceberg output snapshot")
			}
		}
	}
	if p.Ref.InputStateKey != "" && p.Ref.InputSnapshot != "" {
		_, err := db.Pool.Exec(ctx, `INSERT INTO connector_state(tenant_id,connection_id,cursor,updated_at) VALUES($1,$2,$3,now()) ON CONFLICT(tenant_id,connection_id) DO UPDATE SET cursor=$3,updated_at=now()`, p.TenantID, p.Ref.InputStateKey, map[string]string{"snapshotId": p.Ref.InputSnapshot})
		return err
	}
	return nil
}

func kubernetesRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), envValue("KUBERNETES_SERVICE_PORT_HTTPS", "443")
	if host == "" {
		return nil, fmt.Errorf("Kubernetes API is not configured")
	}
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, err
	}
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	if ca, readErr := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"); readErr == nil {
		pool.AppendCertsFromPEM(ca)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	req, _ := http.NewRequestWithContext(ctx, method, "https://"+host+":"+port+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil, &kubernetesStatusError{code: res.StatusCode, body: payload}
	}
	return payload, nil
}

func envValue(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
