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
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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

// webhookSignatureMaxAge bounds how old a timestamped signature may be, and
// how long a delivery's signature is remembered to reject replays.
const webhookSignatureMaxAge = 5 * time.Minute

// webhookSignature returns the expected HMAC-SHA256 for a delivery. Senders sign
// "<unix seconds>.<raw body>" and send the seconds in X-Signature-Timestamp;
// with WEBHOOK_LEGACY_SIGNATURES=true a signature over the raw body alone is
// also accepted (no replay window).
func webhookSignature(secret string, timestamp string, raw []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	if timestamp != "" {
		_, _ = mac.Write([]byte(timestamp + "."))
	}
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func verifyWebhookSignature(r *http.Request, secret string, raw []byte, now time.Time, allowLegacy bool) error {
	provided, err := hex.DecodeString(r.Header.Get("X-Signature-Sha256"))
	if err != nil || len(provided) == 0 {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "bad signature"}
	}
	timestamp := strings.TrimSpace(r.Header.Get("X-Signature-Timestamp"))
	if timestamp == "" {
		if !allowLegacy {
			return &HTTPError{Status: http.StatusUnauthorized, Message: "X-Signature-Timestamp is required; sign \"<timestamp>.<body>\""}
		}
	} else {
		seconds, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			return &HTTPError{Status: http.StatusUnauthorized, Message: "bad signature timestamp"}
		}
		if age := now.Sub(time.Unix(seconds, 0)); age > webhookSignatureMaxAge || age < -webhookSignatureMaxAge {
			return &HTTPError{Status: http.StatusUnauthorized, Message: "signature timestamp outside the allowed window"}
		}
	}
	if secret == "" || !hmac.Equal(provided, webhookSignature(secret, timestamp, raw)) {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "bad signature"}
	}
	return nil
}

func (s *Server) webhookTrigger(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return badRequest(ErrInvalidRequest, "request body too large or unreadable")
	}
	body := map[string]interface{}{}
	if err := json.Unmarshal(raw, &body); err != nil {
		return badRequest(ErrInvalidRequest, "invalid JSON body")
	}
	// The resolver runs before a tenant is known; under RLS a plain SELECT on
	// pipelines would see nothing.
	rows, err := s.DB.Pool.Query(r.Context(), `SELECT id,tenant_id,environment,definition FROM resolve_webhook_pipelines($1)`, r.PathValue("path"))
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
	tenantID := stringValue(row["tenant_id"])
	for _, other := range values[1:] {
		if stringValue(other["tenant_id"]) != tenantID {
			return &HTTPError{Status: http.StatusConflict, Message: "webhook path is claimed by more than one workspace"}
		}
	}
	definition, _ := json.Marshal(row["definition"])
	var def model.PipelineDefinition
	if err = json.Unmarshal(definition, &def); err != nil {
		return err
	}
	def.TenantID = tenantID
	if err := verifyWebhookSignature(r, def.Trigger.Secret, raw, time.Now(), os.Getenv("WEBHOOK_LEGACY_SIGNATURES") == "true"); err != nil {
		return err
	}
	if s.Redis != nil {
		key := "dataflow:webhook-seen:" + sha256Hex(stringValue(row["id"])+":"+r.Header.Get("X-Signature-Timestamp")+":"+strings.ToLower(r.Header.Get("X-Signature-Sha256")))
		fresh, err := s.Redis.SetNX(r.Context(), key, "1", 2*webhookSignatureMaxAge).Result()
		if err != nil {
			slog.Warn("webhook replay check unavailable", "error", err)
		} else if !fresh {
			return &HTTPError{Status: http.StatusConflict, Message: "duplicate webhook delivery"}
		}
	}
	if err := model.ValidateAgentAdmission(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	canonical, _ := json.Marshal(body)
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
