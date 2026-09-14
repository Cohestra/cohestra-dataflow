package dispatchers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/security"
	"github.com/redis/go-redis/v9"
)

// alertHTTPClient is shared by every alert notification dispatch: the
// destination URL comes from a tenant-configured HTTP connector, so it
// must go through the SSRF-safe client (https-only, private/metadata
// denylist re-checked at dial time).
var alertHTTPClient = security.NewHTTPClient(5 * time.Second)

// lineageHTTPClient serves OpenLineage dispatch, whose destination is an
// operator-configured OPENLINEAGE_URL (not tenant/user-supplied), so it is
// intentionally not routed through the SSRF-safe client — that endpoint
// may legitimately be an internal collector.
var lineageHTTPClient = &http.Client{Timeout: 5 * time.Second}

type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func Start(parent context.Context, db *database.DB, runtime *connectors.Runtime, cfg config.Config) (*Group, error) {
	ctx, cancel := context.WithCancel(parent)
	group := &Group{ctx: ctx, cancel: cancel}
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		cancel()
		return nil, err
	}
	redisClient := redis.NewClient(redisOptions)
	group.run(ctx, time.Second, func(ctx context.Context) error { return dispatchEvents(ctx, db, redisClient) })
	group.run(ctx, 2*time.Second, func(ctx context.Context) error { return dispatchAlerts(ctx, db, runtime) })
	if cfg.OpenLineageURL != "" {
		endpoint, err := lineageEndpoint(cfg.OpenLineageURL)
		if err != nil {
			cancel()
			redisClient.Close()
			return nil, err
		}
		group.run(ctx, 2*time.Second, func(ctx context.Context) error { return dispatchLineage(ctx, db, endpoint, cfg.OpenLineageAPIKey) })
	}
	group.wg.Add(1)
	go func() { defer group.wg.Done(); <-ctx.Done(); _ = redisClient.Close() }()
	return group, nil
}

func (g *Group) run(ctx context.Context, interval time.Duration, fn func(context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("dispatcher failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (g *Group) Stop() { g.cancel(); g.wg.Wait() }

func dispatchEvents(ctx context.Context, db *database.DB, client *redis.Client) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,environment,event_id,topic,payload FROM pipeline_event_outbox WHERE published_at IS NULL ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 20`)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		return err
	}
	type event struct {
		id, tenant, environment, eventID, topic string
		payload                                 []byte
	}
	events := []event{}
	for rows.Next() {
		var value event
		if err = rows.Scan(&value.id, &value.tenant, &value.environment, &value.eventID, &value.topic, &value.payload); err != nil {
			rows.Close()
			return err
		}
		events = append(events, value)
	}
	rows.Close()
	for _, value := range events {
		err = client.XAdd(ctx, &redis.XAddArgs{Stream: "dataflow:pipeline-events", Values: map[string]interface{}{"tenantId": value.tenant, "environment": value.environment, "eventId": value.eventID, "topic": value.topic, "payload": string(value.payload)}}).Err()
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE pipeline_event_outbox SET published_at=now(),attempts=attempts+1,last_error=NULL WHERE id=$1`, value.id)
		} else {
			_, _ = tx.Exec(ctx, `UPDATE pipeline_event_outbox SET attempts=attempts+1,last_error=$2 WHERE id=$1`, value.id, truncate(err.Error()))
		}
	}
	return tx.Commit(ctx)
}

func retryDelay(attempt int, base int, capSeconds int) int {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 9 {
		shift = 9
	}
	delay := base * (1 << shift)
	if delay > capSeconds {
		return capSeconds
	}
	return delay
}
func truncate(value string) string {
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

func dispatchAlerts(ctx context.Context, db *database.DB, runtime *connectors.Runtime) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,alert_id,connection_id,payload,attempts FROM pipeline_alert_notification_outbox WHERE sent_at IS NULL AND attempts<10 AND next_attempt_at<=now() ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 5`)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		return err
	}
	type alert struct {
		id, tenantID, alertID, connectionID string
		payload                             []byte
		attempts                            int
	}
	values := []alert{}
	for rows.Next() {
		var value alert
		if err = rows.Scan(&value.id, &value.tenantID, &value.alertID, &value.connectionID, &value.payload, &value.attempts); err != nil {
			rows.Close()
			return err
		}
		values = append(values, value)
	}
	rows.Close()
	for _, value := range values {
		endpoint, apiKey, sendErr := runtime.AlertDestination(ctx, value.tenantID, value.connectionID)
		if sendErr == nil {
			headers := map[string]string{"X-DataFlow-Alert-ID": value.alertID}
			if apiKey != "" {
				headers["Authorization"] = "Bearer " + apiKey
			}
			sendErr = postJSON(ctx, alertHTTPClient, endpoint, value.payload, headers)
		}
		if sendErr == nil {
			_, err = tx.Exec(ctx, `UPDATE pipeline_alert_notification_outbox SET sent_at=now(),attempts=attempts+1,last_error=NULL WHERE id=$1`, value.id)
		} else {
			attempts := value.attempts + 1
			_, err = tx.Exec(ctx, `UPDATE pipeline_alert_notification_outbox SET attempts=$2,last_error=$3,next_attempt_at=now()+make_interval(secs=>$4) WHERE id=$1`, value.id, attempts, truncate(sendErr.Error()), retryDelay(attempts, 30, 3600))
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func lineageEndpoint(base string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("OPENLINEAGE_URL must use http or https")
	}
	if !strings.HasSuffix(parsed.Path, "/api/v1/lineage") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/api/v1/lineage"
	}
	return parsed.String(), nil
}

func dispatchLineage(ctx context.Context, db *database.DB, endpoint, apiKey string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,payload,attempts FROM openlineage_outbox WHERE sent_at IS NULL AND attempts<10 AND next_attempt_at<=now() ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 20`)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		return err
	}
	type item struct {
		id       string
		payload  []byte
		attempts int
	}
	values := []item{}
	for rows.Next() {
		var value item
		if err = rows.Scan(&value.id, &value.payload, &value.attempts); err != nil {
			rows.Close()
			return err
		}
		values = append(values, value)
	}
	rows.Close()
	for _, value := range values {
		headers := map[string]string{}
		if apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
		sendErr := postJSON(ctx, lineageHTTPClient, endpoint, value.payload, headers)
		if sendErr == nil {
			_, err = tx.Exec(ctx, `UPDATE openlineage_outbox SET sent_at=now(),attempts=attempts+1,last_error=NULL WHERE id=$1`, value.id)
		} else {
			attempts := value.attempts + 1
			_, err = tx.Exec(ctx, `UPDATE openlineage_outbox SET attempts=$2,last_error=$3,next_attempt_at=now()+make_interval(secs=>$4) WHERE id=$1`, value.id, attempts, truncate(sendErr.Error()), retryDelay(attempts, 10, 3600))
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, payload []byte, headers map[string]string) error {
	if !json.Valid(payload) {
		return fmt.Errorf("outbox payload is not JSON")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
