package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

func (s *Server) registerTriggers(mux *http.ServeMux) {
	mux.Handle("POST /api/openlineage", s.rateLimit("openlineage", 120, time.Minute, handle(s.serviceOpenLineage)))
	mux.Handle("POST /api/hooks/{path}", s.rateLimit("webhook-trigger", 120, time.Minute, handle(s.webhookTrigger)))
}

func (s *Server) serviceOpenLineage(w http.ResponseWriter, r *http.Request) error {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "OpenLineage bearer token required"}
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "OpenLineage bearer token required"}
	}
	var tenantID string
	if err := s.DB.Pool.QueryRow(r.Context(), `SELECT resolve_openlineage_tenant($1)`, sha256Hex(token)).Scan(&tenantID); err != nil || tenantID == "" {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "invalid or revoked OpenLineage token"}
	}
	environment := r.URL.Query().Get("environment")
	if environment == "" {
		environment = "prod"
	}
	if environment != "test" && environment != "prod" {
		return badRequest(ErrInvalidRequest, "environment must be test or prod")
	}
	var event map[string]interface{}
	if !decodeJSON(w, r, &event) {
		return nil
	}
	if err := s.storeOpenLineage(r, tenantID, environment, event); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	jsonResponse(w, http.StatusCreated, map[string]bool{"ok": true})
	return nil
}

func (s *Server) storeOpenLineage(r *http.Request, tenantID, environment string, event map[string]interface{}) error {
	job, _ := event["job"].(map[string]interface{})
	run, _ := event["run"].(map[string]interface{})
	if stringValue(job["namespace"]) == "" || stringValue(job["name"]) == "" || stringValue(run["runId"]) == "" {
		return fmt.Errorf("invalid OpenLineage event")
	}
	inputs := event["inputs"]
	if inputs == nil {
		inputs = []interface{}{}
	}
	outputs := event["outputs"]
	if outputs == nil {
		outputs = []interface{}{}
	}
	return s.DB.TenantTx(r.Context(), tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO external_lineage_events (tenant_id,environment,event_type,event_time,run_id,job_namespace,job_name,inputs,outputs,producer,payload) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenantID, environment, event["eventType"], event["eventTime"], run["runId"], job["namespace"], job["name"], inputs, outputs, stringValue(event["producer"]), event)
		return err
	})
}

func (s *Server) webhookTrigger(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.DB.Pool.Query(r.Context(), `SELECT * FROM pipelines WHERE status='active' AND definition->'trigger'->>'type'='webhook' AND definition->'trigger'->>'path'=$1 ORDER BY (environment='prod') DESC`, r.PathValue("path"))
	if err != nil {
		return err
	}
	values, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return notFound(ErrNotFound, "no active pipeline on this hook")
	}
	row := values[0]
	body := map[string]interface{}{}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	canonical, _ := json.Marshal(body)
	definition, _ := json.Marshal(row["definition"])
	var def model.PipelineDefinition
	if err = json.Unmarshal(definition, &def); err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(def.Trigger.Secret))
	_, _ = mac.Write(canonical)
	provided, err := hex.DecodeString(r.Header.Get("X-Signature-Sha256"))
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "bad signature"}
	}
	if err := model.ValidateAgentAdmission(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	tenantID := stringValue(row["tenant_id"])
	environment := model.Environment(stringValue(row["environment"]))
	var payload *model.DataRef
	if len(canonical) <= 4096 {
		payload = &model.DataRef{Type: "inline", Key: base64.StdEncoding.EncodeToString(canonical), TenantID: tenantID, SizeBytes: len(canonical)}
	} else {
		payload, err = s.Payloads.Write(r.Context(), body, tenantID, "webhook", "trigger", nil)
		if err != nil {
			return err
		}
	}
	executionID, err := s.fireExecution(r.Context(), def, stringValue(row["id"]), "webhook", environment, payload, "", "", "")
	if quota, ok := err.(*quotaExceeded); ok {
		jsonResponse(w, http.StatusPaymentRequired, map[string]interface{}{"error": "Quota exceeded", "used": quota.Used, "limit": quota.Limit})
		return nil
	}
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]string{"executionId": executionID})
	return nil
}

func (s *Server) assetEventSubscriber(ctx context.Context) {
	go s.consumePipelineEventStream(ctx)
	pubsub := s.Redis.PSubscribe(ctx, "dataflow:events:*:*:*")
	defer pubsub.Close()
	channel := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-channel:
			if !ok {
				return
			}
			parts := strings.Split(strings.TrimPrefix(message.Channel, "dataflow:events:"), ":")
			if len(parts) < 3 {
				continue
			}
			tenantID, environment, topic := parts[0], parts[1], strings.Join(parts[2:], ":")
			s.deliverEvent(ctx, tenantID, model.Environment(environment), topic, message.Payload, "", false)
		}
	}
}

func (s *Server) deliverEvent(ctx context.Context, tenantID string, environment model.Environment, topic, message, eventID string, allowAsset bool) bool {
	assetURN := ""
	if allowAsset && strings.HasPrefix(topic, "asset.materialized.") {
		assetURN = strings.TrimPrefix(topic, "asset.materialized.")
	}
	rows, err := tenantQueryRows(ctx, s.DB, tenantID, `SELECT * FROM pipelines WHERE status='active' AND environment=$1 AND ((definition->'trigger'->>'type'='event' AND definition->'trigger'->>'topic'=$2) OR ($3<>'' AND definition->'trigger'->>'type'='asset' AND definition->'trigger'->>'assetUrn'=$3))`, environment, topic, assetURN)
	if err != nil {
		return false
	}
	delivered := true
	for _, row := range rows {
		doneKey, lockKey := "", ""
		if eventID != "" {
			doneKey = "dataflow:event-done:" + eventID + ":" + stringValue(row["id"])
			lockKey = "dataflow:event-lock:" + eventID + ":" + stringValue(row["id"])
			if s.Redis.Exists(ctx, doneKey).Val() > 0 {
				continue
			}
			locked, lockErr := s.Redis.SetNX(ctx, lockKey, "1", time.Minute).Result()
			if lockErr != nil || !locked {
				delivered = false
				continue
			}
		}
		body, _ := json.Marshal(row["definition"])
		var def model.PipelineDefinition
		if json.Unmarshal(body, &def) != nil {
			if lockKey != "" {
				_ = s.Redis.Del(ctx, lockKey).Err()
			}
			continue
		}
		payload := &model.DataRef{Type: "inline", Key: base64.StdEncoding.EncodeToString([]byte(message)), TenantID: tenantID, SizeBytes: len(message)}
		triggerType := "event"
		if def.Trigger.Type == "asset" {
			triggerType = "asset"
		}
		_, fireErr := s.fireExecution(ctx, def, stringValue(row["id"]), triggerType, environment, payload, "", "", "")
		if fireErr == nil && doneKey != "" {
			_ = s.Redis.Set(ctx, doneKey, "1", 30*24*time.Hour).Err()
		} else if fireErr != nil {
			var quota *quotaExceeded
			if errors.As(fireErr, &quota) {
				if doneKey != "" {
					_ = s.Redis.Set(ctx, doneKey, "quota-exceeded", 30*24*time.Hour).Err()
				}
			} else {
				delivered = false
			}
		}
		if lockKey != "" {
			_ = s.Redis.Del(ctx, lockKey).Err()
		}
	}
	return delivered
}

func (s *Server) consumePipelineEventStream(ctx context.Context) {
	const stream, group, consumer = "dataflow:pipeline-events", "dataflow-api", "control-plane"
	if err := s.Redis.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		slog.Error("event stream group failed", "error", err)
		return
	}
	pending := true
	for ctx.Err() == nil {
		start := "0"
		block := time.Duration(0)
		if !pending {
			start = ">"
			block = 5 * time.Second
		}
		result, err := s.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{stream, start}, Count: 20, Block: block}).Result()
		if err == redis.Nil {
			pending = false
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("event stream read failed", "error", err)
			time.Sleep(time.Second)
			continue
		}
		retry := false
		count := 0
		for _, batch := range result {
			for _, item := range batch.Messages {
				count++
				field := func(name string) string { return stringValue(item.Values[name]) }
				ok := s.deliverEvent(ctx, field("tenantId"), model.Environment(field("environment")), field("topic"), field("payload"), field("eventId"), true)
				if ok {
					if ackErr := s.Redis.XAck(ctx, stream, group, item.ID).Err(); ackErr == nil {
						_ = s.Redis.XDel(ctx, stream, item.ID).Err()
					}
				} else {
					retry = true
				}
			}
		}
		if count == 0 {
			pending = false
		}
		if retry {
			pending = true
			time.Sleep(time.Second)
		}
	}
}
