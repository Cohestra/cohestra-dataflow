package connectors

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/security"
)

func (r *Runtime) registerHTTP() {
	r.Sources["http.fetch"] = func(ctx context.Context, p SourceParams) (SourceResult, error) {
		manifest := model.ConnectorManifest{ActivityType: "http.fetch", Kind: "source", URL: stringValue(p.Config["url"]), Method: stringValue(p.Config["method"]), RecordsPath: stringValue(p.Config["recordsPath"])}
		if value, ok := p.Config["pagination"].(map[string]interface{}); ok {
			manifest.Pagination = value
		}
		if raw, ok := p.Config["paginationJson"].(string); ok && raw != "" {
			_ = json.Unmarshal([]byte(raw), &manifest.Pagination)
		}
		if value, ok := p.Config["incremental"].(map[string]interface{}); ok {
			manifest.Incremental = value
		}
		if raw, ok := p.Config["incrementalJson"].(string); ok && raw != "" {
			_ = json.Unmarshal([]byte(raw), &manifest.Incremental)
		}
		if value, ok := p.Config["auth"].(map[string]interface{}); ok {
			manifest.Auth = value
		}
		if raw, ok := p.Config["authJson"].(string); ok && raw != "" {
			_ = json.Unmarshal([]byte(raw), &manifest.Auth)
		}
		return r.fetchManifest(ctx, manifest, p)
	}
	r.Handlers["sink.webhook"] = r.webhookSink
	r.Handlers["sink.records"] = r.recordsSink
}

func dig(value interface{}, path string) interface{} {
	current := value
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}
func interpolate(value string, config map[string]interface{}) string {
	for key, item := range config {
		value = strings.ReplaceAll(value, "{"+key+"}", stringValue(item))
	}
	return value
}
func (r *Runtime) fetchManifest(ctx context.Context, m model.ConnectorManifest, p SourceParams) (SourceResult, error) {
	endpoint, err := url.Parse(interpolate(firstString(p.Config["url"], m.URL), p.Config))
	if err != nil {
		return SourceResult{}, err
	}
	if _, err := security.ValidateURL(endpoint.String()); err != nil {
		return SourceResult{}, fmt.Errorf("http.fetch: %w", err)
	}
	query := endpoint.Query()
	if params, ok := p.Config["params"].(map[string]interface{}); ok {
		for key, value := range params {
			query.Set(key, stringValue(value))
		}
	}
	style := stringValue(m.Pagination["style"])
	param := firstString(m.Pagination["param"], map[string]string{"cursor": "cursor", "page": "page", "offset": "offset"}[style])
	limit := int(firstNumber(m.Pagination["limit"], number(p.Config["pageSize"]), 100))
	if p.Ingestion != nil && p.Ingestion.PageSize > 0 {
		limit = p.Ingestion.PageSize
	}
	if style == "cursor" && p.Cursor["next"] != nil {
		query.Set(param, stringValue(p.Cursor["next"]))
	}
	if style == "page" {
		query.Set(param, stringValue(firstNumber(p.Cursor["page"], 1)))
	}
	if style == "offset" {
		query.Set(param, stringValue(firstNumber(p.Cursor["offset"], 0)))
	}
	if limitParam := stringValue(m.Pagination["limitParam"]); limitParam != "" {
		query.Set(limitParam, strconv.Itoa(limit))
	}
	if since := stringValue(m.Incremental["sinceParam"]); since != "" {
		watermark := stringValue(p.Cursor["watermark"])
		if watermark == "" && p.Ingestion != nil && p.Ingestion.Mode == "backfill" {
			watermark = p.Ingestion.BackfillStart
		}
		if watermark == "" {
			initial, err := initialWatermark(m.Incremental["initialWatermark"], time.Now())
			if err != nil {
				return SourceResult{}, err
			}
			watermark = initial
		}
		// No saved or configured watermark: omit the parameter and read the
		// full history once, instead of silently starting an hour ago.
		if watermark != "" {
			query.Set(since, watermark)
		}
	}
	endpoint.RawQuery = query.Encode()
	method := firstString(p.Config["method"], m.Method, "GET")
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return SourceResult{}, err
	}
	request.Header.Set("User-Agent", "DataFlow/1.0")
	for key, value := range m.Headers {
		request.Header.Set(key, value)
	}
	if headers, ok := p.Config["headers"].(map[string]interface{}); ok {
		for key, value := range headers {
			request.Header.Set(key, stringValue(value))
		}
	}
	auth := m.Auth
	if value, ok := p.Config["auth"].(map[string]interface{}); ok {
		auth = value
	}
	switch authType := stringValue(auth["type"]); authType {
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+stringValue(auth["token"]))
	case "header":
		request.Header.Set(firstString(auth["name"], auth["headerName"], "Authorization"), stringValue(auth["value"]))
	case "basic":
		request.SetBasicAuth(stringValue(auth["username"]), stringValue(auth["password"]))
	}
	response, err := r.SafeHTTP.Do(request)
	if err != nil {
		return SourceResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == 429 {
		return SourceResult{}, fmt.Errorf("rate limited")
	}
	if response.StatusCode >= 400 {
		return SourceResult{}, fmt.Errorf("http source %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return SourceResult{}, err
	}
	var decoded interface{}
	if err = json.Unmarshal(body, &decoded); err != nil {
		return SourceResult{}, err
	}
	raw := decoded
	if m.RecordsPath != "" {
		raw = dig(decoded, m.RecordsPath)
	}
	records, ok := raw.([]interface{})
	if !ok {
		records = []interface{}{raw}
	}
	next := cloneMap(p.Cursor)
	hasMore := false
	switch style {
	case "cursor":
		nextCursor := dig(decoded, stringValue(m.Pagination["cursorPath"]))
		next["next"] = nextCursor
		hasMore = nextCursor != nil && stringValue(nextCursor) != ""
	case "page":
		hasMore = len(records) >= limit
		if hasMore {
			next["page"] = int(firstNumber(p.Cursor["page"], 1)) + 1
		} else {
			next["page"] = 1
		}
	case "offset":
		hasMore = len(records) >= limit
		if hasMore {
			next["offset"] = int(firstNumber(p.Cursor["offset"], 0)) + len(records)
		} else {
			next["offset"] = 0
		}
	}
	if path := stringValue(m.Incremental["recordTimestampPath"]); path != "" {
		values := []string{}
		for _, record := range records {
			if value := stringValue(dig(record, path)); value != "" {
				values = append(values, value)
			}
		}
		sort.Strings(values)
		if len(values) > 0 {
			next["watermark"] = values[len(values)-1]
		}
	}
	next["backfillDone"] = !hasMore
	return SourceResult{Records: records, NextCursor: next, HasMore: hasMore}, nil
}

// initialWatermark resolves incremental.initialWatermark for a first run: an
// RFC 3339 timestamp, "now", or "now-<duration>" such as "now-24h". Empty
// means no starting point, so the first run reads the full history.
func initialWatermark(value interface{}, now time.Time) (string, error) {
	text := strings.TrimSpace(stringValue(value))
	if text == "" {
		return "", nil
	}
	if text == "now" {
		return now.UTC().Format(time.RFC3339), nil
	}
	if rest, ok := strings.CutPrefix(text, "now-"); ok {
		duration, err := time.ParseDuration(rest)
		if err != nil || duration < 0 {
			return "", configError(`http.fetch: initialWatermark %q must be an RFC 3339 time, "now" or "now-<duration>"`, text)
		}
		return now.Add(-duration).UTC().Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, text); err != nil {
		return "", configError(`http.fetch: initialWatermark %q must be an RFC 3339 time, "now" or "now-<duration>"`, text)
	}
	return text, nil
}

// idempotencyKey is stable across Temporal retries of one sink step in one
// execution, so receivers can drop a delivery they already processed.
func idempotencyKey(handler HandlerContext, batch int) string {
	sum := sha256.Sum256([]byte(handler.TenantID + "\x00" + handler.ExecutionID + "\x00" + handler.NodeID + "\x00" + strconv.Itoa(batch)))
	return hex.EncodeToString(sum[:16])
}

func (r *Runtime) webhookSink(ctx context.Context, input interface{}, cfg map[string]interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	endpoint := stringValue(cfg["url"])
	secret := stringValue(cfg["secret"])
	if endpoint == "" && cfg["connectionId"] != nil {
		instance, err := r.credential(ctx, stringValue(cfg["connectionId"]))
		if err != nil {
			return nil, nil, err
		}
		extra, _ := instance["extra"].(map[string]interface{})
		secrets, _ := instance["secret_value"].(map[string]interface{})
		endpoint = stringValue(extra["baseUrl"])
		secret = stringValue(secrets["hmacSecret"])
	}
	if endpoint == "" {
		return nil, nil, fmt.Errorf("sink.webhook: URL or HTTP connector instance required")
	}
	if _, err := security.ValidateURL(endpoint); err != nil {
		return nil, nil, fmt.Errorf("sink.webhook: %w", err)
	}
	body, _ := json.Marshal(map[string]interface{}{"records": input})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey(handler, 0))
	if secret != "" {
		// Same scheme DataFlow's own webhook triggers verify: HMAC-SHA256 of
		// "<unix seconds>.<body>", so a captured request cannot be replayed later.
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(timestamp + "."))
		_, _ = mac.Write(body)
		request.Header.Set("X-Signature-Timestamp", timestamp)
		request.Header.Set("X-Signature-SHA256", hex.EncodeToString(mac.Sum(nil)))
	}
	response, err := r.SafeHTTP.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("webhook returned %d", response.StatusCode)
	}
	return nil, nil, nil
}
func (r *Runtime) recordsSink(ctx context.Context, input interface{}, cfg map[string]interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	collection := stringValue(cfg["collection"])
	if collection == "" {
		return nil, nil, fmt.Errorf("sink.records: collection required")
	}
	endpoint := strings.TrimSuffix(r.Config.ClickHouseURL, "/") + "/?database=" + url.QueryEscape(r.Config.ClickHouseDB) + "&query=" + url.QueryEscape("INSERT INTO sink_records FORMAT JSONEachRow")
	var body strings.Builder
	writer := bufio.NewWriter(&body)
	for _, record := range rows {
		encoded, _ := json.Marshal(record)
		dedupKey := sha256.Sum256(encoded)
		line, _ := json.Marshal(map[string]interface{}{"tenant_id": handler.TenantID, "collection": collection, "record": record, "dedup_key": hex.EncodeToString(dedupKey[:]), "ingested_at": time.Now().UTC()})
		_, _ = writer.Write(line)
		_ = writer.WriteByte('\n')
	}
	_ = writer.Flush()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.String()))
	request.SetBasicAuth(r.Config.ClickHouseUser, r.Config.ClickHousePassword)
	response, err := r.HTTP.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		data, _ := io.ReadAll(response.Body)
		return nil, nil, fmt.Errorf("clickhouse insert %d: %s", response.StatusCode, data)
	}
	return nil, nil, nil
}
func firstString(values ...interface{}) string {
	for _, value := range values {
		if text := stringValue(value); text != "" {
			return text
		}
	}
	return ""
}
func firstNumber(values ...interface{}) float64 {
	for _, value := range values {
		if result := number(value); result != 0 {
			return result
		}
	}
	return 0
}
func truthy(value interface{}) bool {
	b, _ := value.(bool)
	return b
}
