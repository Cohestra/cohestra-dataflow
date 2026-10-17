package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/security"
)

const (
	maxAIMermaidRunes = 12_000
	maxAIMessages     = 12
	maxAIMessageRunes = 2_000
	maxAIUserRunes    = 8_000
	maxAIDefinition   = 64 * 1024
	maxAIContextRunes = 6_000
)

func field(key, kind string, options ...string) model.CatalogField {
	values := make([]interface{}, len(options))
	for i, option := range options {
		values[i] = option
	}
	return model.CatalogField{Key: key, Label: key, Type: kind, Options: values}
}

var codedAIFields = map[string][]model.CatalogField{
	"zendesk.fetch":   {field("connectionId", "text"), field("subdomain", "text"), field("resource", "text")},
	"gsheets.fetch":   {field("connectionId", "text"), field("spreadsheetId", "text"), field("range", "text"), field("sheetName", "text"), field("keyColumn", "text")},
	"gdrive.fetch":    {field("connectionId", "text"), field("folderId", "text"), field("query", "text")},
	"excel.fetch":     {field("connectionId", "text"), field("driveId", "text"), field("itemId", "text"), field("sheetName", "text"), field("keyColumn", "text")},
	"http.fetch":      {field("connectionId", "text"), field("url", "text"), field("recordsPath", "text"), field("method", "text"), field("paginationJson", "text"), field("authJson", "text"), field("incrementalJson", "text"), field("pagination", "object"), field("auth", "object"), field("incremental", "object"), field("params", "object"), field("headers", "object"), field("pageSize", "number")},
	"postgres.fetch":  {field("connectionId", "text"), field("table", "text"), field("syncMode", "select", "cursor", "cdc"), field("columns", "text"), field("cursorColumn", "text"), field("cursorType", "select", "date", "number", "string"), field("pageSize", "number"), field("layer", "select", "bronze", "silver", "gold")},
	"mysql.fetch":     {field("connectionId", "text"), field("table", "text"), field("syncMode", "select", "cursor", "cdc"), field("columns", "text"), field("cursorColumn", "text"), field("cursorType", "select", "date", "number", "string"), field("pageSize", "number"), field("layer", "select", "bronze", "silver", "gold")},
	"mongodb.fetch":   {field("connectionId", "text"), field("collection", "text"), field("syncMode", "select", "cursor", "cdc"), field("cursorField", "text"), field("cursorType", "select", "objectId", "string", "number", "date"), field("pageSize", "number"), field("layer", "select", "bronze", "silver", "gold")},
	"s3.fetch":        {field("connectionId", "text"), field("bucket", "text"), field("key", "text"), field("format", "select", "jsonl", "json"), field("layer", "select", "bronze", "silver", "gold")},
	"kafka.fetch":     {field("connectionId", "text"), field("topic", "text"), field("cluster", "text"), field("startPosition", "select", "earliest", "latest"), field("valueFormat", "select", "json", "string"), field("includeMetadata", "boolean"), field("pageSize", "number"), field("layer", "select", "bronze", "silver", "gold")},
	"sftp.fetch":      {field("connectionId", "text"), field("path", "text"), field("format", "select", "jsonl", "json")},
	"snowflake.fetch": {field("connectionId", "text"), field("table", "text"), field("syncMode", "select", "cursor", "changes"), field("cursorColumn", "text"), field("pageSize", "number")},
	"iceberg.fetch":   {field("connectionId", "text"), field("namespace", "text"), field("table", "text"), field("pageSize", "number")},

	"transform.map":      {field("expression", "text")},
	"transform.filter":   {field("predicate", "text")},
	"transform.formula":  {field("outputField", "text"), field("expression", "text")},
	"transform.select":   {field("expression", "text")},
	"transform.rename":   {field("mapping", "json")},
	"transform.dedupe":   {field("key", "text"), field("keep", "select", "first", "last"), field("scope", "select", "run", "pipeline")},
	"transform.flatten":  {field("delimiter", "text"), field("maxDepth", "number"), field("arrayPolicy", "select", "index", "stringify", "keep")},
	"transform.parse":    {field("fields", "text"), field("onError", "select", "skip", "fail", "null")},
	"transform.contract": {field("schemaJson", "text"), field("onViolation", "select", "fail", "drop", "quarantine"), field("allowExtra", "boolean")},
	"flow.fork":          {},
	"flow.merge":         {field("mergeStrategy", "select", "concat", "union", "innerJoin", "leftJoin", "outerJoin", "appendWithSourceTag"), field("joinKey", "text")},

	"sink.postgres":   {field("connectionId", "text"), field("table", "text"), field("writeMode", "select", "upsert", "apply-cdc"), field("conflictKey", "text"), field("layer", "select", "bronze", "silver", "gold")},
	"sink.clickhouse": {field("connectionId", "text"), field("table", "text"), field("layer", "select", "bronze", "silver", "gold")},
	"sink.mysql":      {field("connectionId", "text"), field("table", "text"), field("writeMode", "select", "upsert", "apply-cdc"), field("primaryKey", "text"), field("layer", "select", "bronze", "silver", "gold")},
	"sink.mongodb":    {field("connectionId", "text"), field("collection", "text"), field("keyField", "text"), field("writeMode", "select", "upsert", "apply-cdc"), field("layer", "select", "bronze", "silver", "gold")},
	"sink.s3":         {field("connectionId", "text"), field("bucket", "text"), field("key", "text"), field("format", "select", "jsonl", "json"), field("layer", "select", "bronze", "silver", "gold")},
	"sink.kafka":      {field("connectionId", "text"), field("topic", "text"), field("cluster", "text"), field("keyField", "text"), field("layer", "select", "bronze", "silver", "gold")},
	"sink.sftp":       {field("connectionId", "text"), field("path", "text"), field("format", "select", "jsonl", "json")},
	"sink.snowflake":  {field("connectionId", "text"), field("table", "text")},
	"sink.iceberg":    {field("connectionId", "text"), field("namespace", "text"), field("table", "text")},
	"sink.gsheets":    {field("connectionId", "text"), field("spreadsheetId", "text"), field("sheetName", "text"), field("writeMode", "select", "replace", "append"), field("includeHeader", "boolean")},
	"sink.webhook":    {field("connectionId", "text"), field("url", "text")},
	"sink.records":    {field("collection", "text")},
}

var codedAIRequiredFields = map[string][]string{
	"zendesk.fetch": {"connectionId"}, "gsheets.fetch": {"connectionId", "spreadsheetId"}, "gdrive.fetch": {"connectionId"},
	"excel.fetch": {"connectionId", "driveId", "itemId", "sheetName"}, "http.fetch": {"url"},
	"postgres.fetch": {"connectionId", "table"}, "mysql.fetch": {"connectionId", "table"}, "mongodb.fetch": {"connectionId", "collection"},
	"s3.fetch": {"connectionId", "bucket", "key"}, "kafka.fetch": {"connectionId", "topic"}, "sftp.fetch": {"connectionId", "path"},
	"snowflake.fetch": {"connectionId", "table"}, "iceberg.fetch": {"connectionId", "namespace", "table"},
	"transform.map": {"expression"}, "transform.filter": {"predicate"}, "transform.formula": {"outputField", "expression"},
	"transform.select": {"expression"}, "transform.rename": {"mapping"}, "transform.dedupe": {"key"}, "transform.contract": {"schemaJson"},
	"sink.postgres": {"connectionId", "table"}, "sink.clickhouse": {"connectionId", "table"}, "sink.mysql": {"connectionId", "table"},
	"sink.mongodb": {"connectionId", "collection"}, "sink.s3": {"connectionId", "bucket", "key"}, "sink.kafka": {"connectionId", "topic"},
	"sink.sftp": {"connectionId", "path"}, "sink.snowflake": {"connectionId", "table"}, "sink.iceberg": {"connectionId", "namespace", "table"},
	"sink.gsheets": {"connectionId", "spreadsheetId"}, "sink.records": {"collection"},
}

func codedFields(activityType string) []model.CatalogField {
	fields := append([]model.CatalogField{}, codedAIFields[activityType]...)
	required := map[string]bool{}
	for _, key := range codedAIRequiredFields[activityType] {
		required[key] = true
	}
	for i := range fields {
		fields[i].Required = required[fields[i].Key]
	}
	return fields
}

func mergeCatalogFields(base, override []model.CatalogField) []model.CatalogField {
	merged := append([]model.CatalogField{}, base...)
	positions := map[string]int{}
	for i, field := range merged {
		positions[field.Key] = i
	}
	for _, field := range override {
		if position, exists := positions[field.Key]; exists {
			merged[position] = field
		} else {
			positions[field.Key] = len(merged)
			merged = append(merged, field)
		}
	}
	return merged
}

var (
	aiBearerValue      = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
	aiSecretAssignment = regexp.MustCompile(`(?i)\b(authorization|client[_-]?secret|access[_-]?token|refresh[_-]?token|token|api[_-]?key|password|private[_-]?key)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;}\]]+)`)
	aiURLValue         = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

func aiSecretKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, key)
	if strings.HasPrefix(normalized, "x") {
		normalized = strings.TrimPrefix(normalized, "x")
	}
	return normalized == "authorization" || normalized == "apikey" || normalized == "privatekey" || normalized == "accesskeyid" ||
		normalized == "token" || normalized == "accesstoken" || normalized == "refreshtoken" || normalized == "idtoken" || normalized == "bearertoken" || normalized == "authtoken" ||
		strings.Contains(normalized, "password") || strings.Contains(normalized, "secret")
}

func redactAIValue(value interface{}) interface{} {
	switch value := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(value))
		for key, item := range value {
			if aiSecretKey(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactAIValue(item)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(value))
		for i, item := range value {
			out[i] = redactAIValue(item)
		}
		return out
	case string:
		return redactAIText(value)
	default:
		return value
	}
}

func redactAIText(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var decoded interface{}
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			if encoded, err := json.Marshal(redactAIValue(decoded)); err == nil {
				return string(encoded)
			}
		}
	}
	value = aiURLValue.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User == nil {
			return raw
		}
		parsed.User = url.User("[REDACTED]")
		return parsed.String()
	})
	value = aiBearerValue.ReplaceAllString(value, "$1 [REDACTED]")
	return aiSecretAssignment.ReplaceAllString(value, "$1$2[REDACTED]")
}

func contextualAIPrompt(prompt string, context aiRequestContext) (string, error) {
	parts := []string{}
	messages := context.Messages
	if len(messages) > maxAIMessages {
		return "", fmt.Errorf("AI context has too many messages")
	}
	for _, message := range messages {
		if (message.Role == "user" || message.Role == "assistant") && strings.TrimSpace(message.Content) != "" {
			content := redactAIText(message.Content)
			if len([]rune(content)) > maxAIMessageRunes {
				return "", fmt.Errorf("AI context message is too large")
			}
			parts = append(parts, message.Role+": "+content)
		}
	}
	if len(parts) > 0 {
		parts = append([]string{"CONVERSATION CONTEXT (oldest to newest):"}, parts...)
	}
	if mermaid := strings.TrimSpace(context.Mermaid); mermaid != "" {
		mermaid = redactAIText(mermaid)
		if len([]rune(mermaid)) > maxAIMermaidRunes {
			return "", fmt.Errorf("Mermaid context is too large")
		}
		parts = append(parts, "CURRENT MERMAID:\n"+mermaid)
	}
	parts = append(parts, "LATEST USER REQUEST:\n"+redactAIText(prompt))
	combined := strings.Join(parts, "\n\n")
	if len([]rune(combined)) > maxAIContextRunes {
		return "", fmt.Errorf("AI context is too large")
	}
	return combined, nil
}

func rejectedAIIntent(prompt string) (string, bool) {
	lower := strings.ToLower(prompt)
	secretTerms := []string{"password", "secret", "access token", "refresh token", "api key", "private key"}
	exposureTerms := []string{"show", "expose", "include", "put", "copy", "return", "label", "node config"}
	if containsAny(lower, secretTerms) && containsAny(lower, exposureTerms) {
		return "Requests to expose credentials or secrets in pipeline output are not allowed.", true
	}
	if containsAny(lower, []string{"feeds back", "feed back", "cyclic graph", "circular graph", "loop forever", "back into"}) {
		return "Pipeline cycles are not executable; the graph must remain a DAG.", true
	}
	for _, token := range strings.Fields(prompt) {
		candidate := strings.Trim(token, `"'()[]{}<>,.;`)
		if !strings.HasPrefix(strings.ToLower(candidate), "http://") && !strings.HasPrefix(strings.ToLower(candidate), "https://") {
			continue
		}
		parsed, err := security.ValidateURL(candidate)
		if err != nil {
			return "The requested outbound URL violates the HTTPS-only egress policy.", true
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" || host == "metadata.google.internal" || strings.HasSuffix(host, ".localhost") {
			return "The requested outbound URL targets a private or metadata service.", true
		}
		if ip := net.ParseIP(host); ip != nil && security.IsDenied(ip) {
			return "The requested outbound URL targets a private or metadata address.", true
		}
	}
	return "", false
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func aiRejectedResult(reason string, repairCount int) map[string]interface{} {
	return map[string]interface{}{
		"status": aiPipelineRejected, "reason": reason,
		"questions": []string{}, "assumptions": []string{}, "warnings": []string{reason},
		"metrics": map[string]int{"repairCount": repairCount},
	}
}

func connectorRequirement(activityType string) (provider, kind string) {
	switch activityType {
	case "zendesk.fetch":
		return "zendesk", "oauth"
	case "gsheets.fetch", "gdrive.fetch", "sink.gsheets":
		return "google", "oauth"
	case "excel.fetch":
		return "microsoft", "oauth"
	case "sink.webhook":
		return "http", "credential"
	}
	if strings.HasSuffix(activityType, ".fetch") {
		return strings.TrimSuffix(activityType, ".fetch"), "credential"
	}
	if strings.HasPrefix(activityType, "sink.") {
		return strings.TrimPrefix(activityType, "sink."), "credential"
	}
	return "", ""
}

func validateAIField(nodeID string, field model.CatalogField, value interface{}) error {
	validType := true
	switch field.Type {
	case "text", "string", "textarea", "select", "oauth-picker", "instance-picker":
		_, validType = value.(string)
	case "number":
		switch value.(type) {
		case float64, float32, int, int32, int64, uint, uint32, uint64, json.Number:
		default:
			validType = false
		}
	case "checkbox", "boolean":
		_, validType = value.(bool)
	case "object":
		_, validType = value.(map[string]interface{})
	case "json":
		if _, ok := value.(map[string]interface{}); !ok {
			text, stringValue := value.(string)
			validType = stringValue && json.Valid([]byte(text))
		}
	case "array":
		_, validType = value.([]interface{})
	}
	if !validType {
		return fmt.Errorf("node %s config.%s must have type %s", nodeID, field.Key, field.Type)
	}
	if len(field.Options) > 0 {
		matched := false
		for _, option := range field.Options {
			if fmt.Sprint(option) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("node %s config.%s must be one of %v", nodeID, field.Key, field.Options)
		}
	}
	return nil
}

// aiCommaListFields are text fields the runtime splits on commas. Models often
// send them as JSON arrays; normalizeAIConfig joins those instead of spending
// the repair round on a type error.
var aiCommaListFields = map[string]map[string]bool{
	"transform.parse":  {"fields": true},
	"transform.dedupe": {"key": true},
	"postgres.fetch":   {"columns": true},
	"mysql.fetch":      {"columns": true},
}

func aiCommaListField(activityType, key string) bool { return aiCommaListFields[activityType][key] }

func normalizeAIConfig(node model.Node) {
	normalizeAIRenameMapping(node)
	for key := range aiCommaListFields[node.ActivityType] {
		list, ok := node.Config[key].([]interface{})
		if !ok {
			continue
		}
		parts := make([]string, 0, len(list))
		for _, item := range list {
			text, isString := item.(string)
			if !isString {
				return // leave mixed lists for the type check to reject
			}
			if text = strings.TrimSpace(text); text != "" {
				parts = append(parts, text)
			}
		}
		node.Config[key] = strings.Join(parts, ",")
	}
}

// normalizeAIRenameMapping accepts the "old:new, a->b" shorthand models write
// for transform.rename mapping (a JSON object field). Anything else, including
// valid JSON text, is left for the type check.
func normalizeAIRenameMapping(node model.Node) {
	text, ok := node.Config["mapping"].(string)
	if node.ActivityType != "transform.rename" || !ok || json.Valid([]byte(text)) {
		return
	}
	mapping := map[string]interface{}{}
	for _, pair := range strings.Split(text, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(strings.Replace(pair, "->", ":", 1), ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return
		}
		mapping[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	if len(mapping) > 0 {
		node.Config["mapping"] = mapping
	}
}

func validateAIGrounding(def model.PipelineDefinition, catalog map[string]model.CatalogEntry, instances []aiConnectorInstance) error {
	byID := map[string]aiConnectorInstance{}
	byName := map[string][]aiConnectorInstance{}
	for _, instance := range instances {
		byID[instance.ID] = instance
		byName[instance.Name] = append(byName[instance.Name], instance)
	}
	for _, node := range def.Nodes {
		entry, ok := catalog[node.ActivityType]
		if !ok {
			return fmt.Errorf("unknown activityType %s", node.ActivityType)
		}
		normalizeAIConfig(node)
		fields := map[string]model.CatalogField{}
		for _, field := range entry.Fields {
			fields[field.Key] = field
			value, exists := node.Config[field.Key]
			if field.Required && (!exists || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "") {
				return fmt.Errorf("node %s (%s) requires config.%s", node.ID, node.ActivityType, field.Key)
			}
			if exists && value != nil {
				if err := validateAIField(node.ID, field, value); err != nil {
					return err
				}
			}
		}
		for key := range node.Config {
			if _, allowed := fields[key]; !allowed {
				return fmt.Errorf("node %s (%s) contains unsupported config.%s", node.ID, node.ActivityType, key)
			}
		}
		connectionID := ""
		if value, exists := node.Config["connectionId"]; exists && value != nil {
			connectionID = strings.TrimSpace(stringValue(value))
		}
		webhookURL := ""
		if value, exists := node.Config["url"]; exists && value != nil {
			webhookURL = strings.TrimSpace(stringValue(value))
		}
		if node.ActivityType == "sink.webhook" && connectionID == "" && webhookURL == "" {
			return fmt.Errorf("node %s (sink.webhook) requires config.url or config.connectionId", node.ID)
		}
		provider, kind := connectorRequirement(node.ActivityType)
		if connectionID == "" {
			continue
		}
		instance, exists := byID[connectionID]
		if !exists {
			matches := []aiConnectorInstance{}
			for _, candidate := range byName[connectionID] {
				if (provider == "" || candidate.Provider == provider) && (kind == "" || candidate.Kind == kind) {
					matches = append(matches, candidate)
				}
			}
			if len(matches) > 1 {
				return fmt.Errorf("node %s connector name %q is ambiguous; select a specific saved connection", node.ID, connectionID)
			}
			if len(matches) == 1 {
				instance, exists = matches[0], true
				node.Config["connectionId"] = instance.ID
			}
		}
		if !exists {
			return fmt.Errorf("node %s references unknown tenant connector instance %q", node.ID, connectionID)
		}
		if provider != "" && instance.Provider != provider {
			return fmt.Errorf("node %s (%s) requires provider %s, but connector %q uses %s", node.ID, node.ActivityType, provider, connectionID, instance.Provider)
		}
		if kind != "" && instance.Kind != kind {
			return fmt.Errorf("node %s (%s) requires connector kind %s, but connector %q is %s", node.ID, node.ActivityType, kind, connectionID, instance.Kind)
		}
	}
	return nil
}

func aiSecretPath(value interface{}, path string) string {
	switch value := value.(type) {
	case map[string]interface{}:
		for key, item := range value {
			next := path + "." + key
			if aiSecretKey(key) {
				return next
			}
			if found := aiSecretPath(item, next); found != "" {
				return found
			}
		}
	case []interface{}:
		for i, item := range value {
			if found := aiSecretPath(item, fmt.Sprintf("%s[%d]", path, i)); found != "" {
				return found
			}
		}
	case string:
		trimmed := strings.TrimSpace(value)
		for _, raw := range aiURLValue.FindAllString(value, -1) {
			if parsed, err := url.Parse(raw); err == nil && parsed.User != nil {
				return path
			}
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var decoded interface{}
			if json.Unmarshal([]byte(trimmed), &decoded) == nil {
				return aiSecretPath(decoded, path)
			}
		}
		if aiBearerValue.MatchString(value) || aiSecretAssignment.MatchString(value) || strings.Contains(value, "-----BEGIN PRIVATE KEY-----") {
			return path
		}
	}
	return ""
}

func unsafePipelineReason(def model.PipelineDefinition) string {
	for _, node := range def.Nodes {
		if path := aiSecretPath(node.Config, "config"); path != "" {
			return fmt.Sprintf("Node %s contains raw credential material at %s; use a saved connector instance instead.", node.ID, path)
		}
		for key, value := range node.Config {
			if value == nil {
				continue
			}
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized != "url" && normalized != "endpoint" && normalized != "baseurl" {
				continue
			}
			raw := strings.TrimSpace(stringValue(value))
			if raw == "" {
				continue
			}
			parsed, err := security.ValidateURL(raw)
			if err != nil {
				return fmt.Sprintf("Node %s contains an unsafe outbound URL: %s.", node.ID, err)
			}
			if parsed.User != nil {
				return fmt.Sprintf("Node %s contains credentials in an outbound URL.", node.ID)
			}
			host := strings.ToLower(parsed.Hostname())
			if host == "localhost" || host == "metadata.google.internal" || strings.HasSuffix(host, ".localhost") {
				return fmt.Sprintf("Node %s targets a private or metadata service.", node.ID)
			}
			if ip := net.ParseIP(host); ip != nil && security.IsDenied(ip) {
				return fmt.Sprintf("Node %s targets a private or metadata address.", node.ID)
			}
		}
	}
	return ""
}

// aiGraphShapeError applies the canvas's structural rules (apps/web
// validatePipeline.ts) to a planner proposal, so a graph the editor would
// refuse to save triggers the repair round instead of being returned as ready.
func aiGraphShapeError(def model.PipelineDefinition) error {
	incoming, outgoing := map[string]int{}, map[string]int{}
	parents, children := map[string][]string{}, map[string][]string{}
	for _, edge := range def.Edges {
		outgoing[edge.Source]++
		incoming[edge.Target]++
		parents[edge.Target] = append(parents[edge.Target], edge.Source)
		children[edge.Source] = append(children[edge.Source], edge.Target)
	}
	sources, sinks := 0, 0
	for _, node := range def.Nodes {
		in, out := incoming[node.ID], outgoing[node.ID]
		switch {
		case node.Type == "source" && in > 0:
			return fmt.Errorf("source node %s cannot have incoming edges", node.ID)
		case node.Type == "sink" && out > 0:
			return fmt.Errorf("sink node %s cannot have outgoing edges", node.ID)
		case node.Type == "fork" && out < 2:
			return fmt.Errorf("fork node %s needs at least two outgoing edges", node.ID)
		case node.Type == "merge" && in < 2:
			return fmt.Errorf("merge node %s needs at least two incoming edges", node.ID)
		case in+out == 0:
			return fmt.Errorf("node %s is not connected to the pipeline", node.ID)
		case node.Type != "sink" && out == 0:
			return fmt.Errorf("node %s (%s) has no outgoing edge; connect it to a downstream node or sink%s", node.ID, node.Type, aiInsertHint(def, node.ID, parents, children))
		}
		if node.Type == "source" {
			sources++
		}
		if node.Type == "sink" {
			sinks++
		}
	}
	if sources == 0 || sinks == 0 {
		return fmt.Errorf("pipeline needs at least one source and one sink")
	}
	return nil
}

// aiInsertHint names the edge fix for the two misses seen live when a step X
// is inserted before a sink T: X attached as a P -> X side branch next to
// P -> T, or P -> T dropped so T is left without input.
func aiInsertHint(def model.PipelineDefinition, id string, parents, children map[string][]string) string {
	// The other miss: the old P -> T edge was dropped, leaving T unfed.
	unfed := []string{}
	for _, node := range def.Nodes {
		if node.Type == "sink" && len(parents[node.ID]) == 0 {
			unfed = append(unfed, node.ID)
		}
	}
	if len(unfed) == 1 {
		return fmt.Sprintf(" (add edge %s -> %s so the sink receives its output)", id, unfed[0])
	}
	if len(parents[id]) != 1 {
		return ""
	}
	parent := parents[id][0]
	siblings := []string{}
	for _, child := range children[parent] {
		if child != id {
			siblings = append(siblings, child)
		}
	}
	if len(siblings) != 1 {
		return ""
	}
	target := siblings[0]
	return fmt.Sprintf(" (to place %s before %s, replace edge %s -> %s with %s -> %s and %s -> %s)", id, target, parent, target, parent, id, id, target)
}
