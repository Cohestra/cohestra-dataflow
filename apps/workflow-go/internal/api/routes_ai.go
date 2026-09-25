package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
)

var codedCatalog = []model.CatalogEntry{
	{ActivityType: "zendesk.fetch", NodeType: "source", Label: "Zendesk"}, {ActivityType: "gsheets.fetch", NodeType: "source", Label: "Google Sheets"}, {ActivityType: "gdrive.fetch", NodeType: "source", Label: "Google Drive"}, {ActivityType: "excel.fetch", NodeType: "source", Label: "Microsoft Excel"}, {ActivityType: "http.fetch", NodeType: "source", Label: "Custom API"}, {ActivityType: "postgres.fetch", NodeType: "source", Label: "PostgreSQL"}, {ActivityType: "mysql.fetch", NodeType: "source", Label: "MySQL"}, {ActivityType: "mongodb.fetch", NodeType: "source", Label: "MongoDB"}, {ActivityType: "s3.fetch", NodeType: "source", Label: "Amazon S3"}, {ActivityType: "kafka.fetch", NodeType: "source", Label: "Kafka"}, {ActivityType: "sftp.fetch", NodeType: "source", Label: "SFTP"}, {ActivityType: "snowflake.fetch", NodeType: "source", Label: "Snowflake"}, {ActivityType: "iceberg.fetch", NodeType: "source", Label: "Apache Iceberg"},
	{ActivityType: "transform.map", NodeType: "transform", Label: "Map"}, {ActivityType: "transform.filter", NodeType: "transform", Label: "Filter"}, {ActivityType: "transform.formula", NodeType: "transform", Label: "Formula"}, {ActivityType: "transform.select", NodeType: "transform", Label: "JMESPath select"}, {ActivityType: "transform.rename", NodeType: "transform", Label: "Rename"}, {ActivityType: "transform.dedupe", NodeType: "transform", Label: "Dedupe"}, {ActivityType: "transform.flatten", NodeType: "transform", Label: "Flatten"}, {ActivityType: "transform.parse", NodeType: "transform", Label: "Parse JSON"}, {ActivityType: "transform.contract", NodeType: "transform", Label: "Data contract"},
	{ActivityType: "flow.fork", NodeType: "fork", Label: "Fork"}, {ActivityType: "flow.merge", NodeType: "merge", Label: "Merge"}, {ActivityType: "sink.postgres", NodeType: "sink", Label: "Postgres"}, {ActivityType: "sink.clickhouse", NodeType: "sink", Label: "ClickHouse"}, {ActivityType: "sink.mysql", NodeType: "sink", Label: "MySQL"}, {ActivityType: "sink.mongodb", NodeType: "sink", Label: "MongoDB"}, {ActivityType: "sink.s3", NodeType: "sink", Label: "Amazon S3"}, {ActivityType: "sink.kafka", NodeType: "sink", Label: "Kafka"}, {ActivityType: "sink.sftp", NodeType: "sink", Label: "SFTP"}, {ActivityType: "sink.snowflake", NodeType: "sink", Label: "Snowflake"}, {ActivityType: "sink.iceberg", NodeType: "sink", Label: "Apache Iceberg"}, {ActivityType: "sink.gsheets", NodeType: "sink", Label: "Google Sheets"}, {ActivityType: "sink.webhook", NodeType: "sink", Label: "Webhook"}, {ActivityType: "sink.records", NodeType: "sink", Label: "DataFlow store"},
}

type aiPipelineStatus string

const (
	aiPipelineReady      aiPipelineStatus = "ready"
	aiPipelineNeedsInput aiPipelineStatus = "needs_input"
	aiPipelineRejected   aiPipelineStatus = "rejected"
)

type aiPipelineResponse struct {
	Status        aiPipelineStatus       `json:"status"`
	Reason        string                 `json:"reason"`
	SuggestedName string                 `json:"suggestedName"`
	Questions     []string               `json:"questions"`
	Assumptions   []string               `json:"assumptions"`
	Warnings      []string               `json:"warnings"`
	Trigger       model.Trigger          `json:"trigger"`
	Execution     *model.ExecutionConfig `json:"execution"`
	Nodes         []struct {
		ID           json.RawMessage        `json:"id"`
		Label        string                 `json:"label"`
		ActivityType string                 `json:"activityType"`
		Config       map[string]interface{} `json:"config"`
		Ingestion    *model.IngestionConfig `json:"ingestion"`
	} `json:"nodes"`
	Edges []json.RawMessage `json:"edges"`
}

type aiConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiRequestContext struct {
	Mermaid  string                  `json:"mermaid"`
	Messages []aiConversationMessage `json:"messages"`
}

type aiConnectorInstance struct {
	ID, Kind, Provider, Name string
}

var errInvalidAIJSON = errors.New("invalid AI JSON")

func aiPipelineJSONSchema(activityTypes []string) map[string]interface{} {
	stringArray := map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "reason", "suggestedName", "questions", "assumptions", "warnings", "trigger", "nodes", "edges"},
		"properties": map[string]interface{}{
			"status":        map[string]interface{}{"type": "string", "enum": []string{string(aiPipelineReady), string(aiPipelineNeedsInput), string(aiPipelineRejected)}},
			"reason":        map[string]interface{}{"type": "string"},
			"suggestedName": map[string]interface{}{"type": "string"},
			"questions":     stringArray,
			"assumptions":   stringArray,
			"warnings":      stringArray,
			"trigger": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"type"},
				"properties": map[string]interface{}{
					"type":     map[string]interface{}{"type": "string"},
					"schedule": map[string]interface{}{"type": "string"},
					"path":     map[string]interface{}{"type": "string"},
					"topic":    map[string]interface{}{"type": "string"},
					"assetUrn": map[string]interface{}{"type": "string"},
				},
			},
			"execution": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"engine":       map[string]interface{}{"type": "string", "enum": []string{"workflow", "stream-direct", "spark-sql", "flink-sql"}},
					"transformSql": map[string]interface{}{"type": "string"},
				},
			},
			"nodes": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "label", "activityType", "config"},
					"properties": map[string]interface{}{
						"id":           map[string]interface{}{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_-]*$", "maxLength": 128},
						"label":        map[string]interface{}{"type": "string"},
						"activityType": map[string]interface{}{"type": "string", "enum": activityTypes},
						"config":       map[string]interface{}{"type": "object"},
						"ingestion": map[string]interface{}{
							"type":                 "object",
							"additionalProperties": false,
							"required":             []string{"mode"},
							"properties": map[string]interface{}{
								"mode":          map[string]interface{}{"type": "string", "enum": []string{"incremental", "backfill", "realtime"}},
								"backfillStart": map[string]interface{}{"type": "string"},
								"backfillEnd":   map[string]interface{}{"type": "string"},
								"stateKey":      map[string]interface{}{"type": "string"},
								"pageSize":      map[string]interface{}{"type": "integer", "minimum": 1},
							},
						},
					},
				},
			},
			"edges": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"source", "target"},
					"properties": map[string]interface{}{
						"source":    map[string]interface{}{"type": "string"},
						"target":    map[string]interface{}{"type": "string"},
						"condition": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}
}

func (s *Server) registerAI(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai/generate", handle(s.aiGenerate))
	mux.HandleFunc("POST /api/ai/refine", handle(s.aiRefine))
}
func (s *Server) aiGenerate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Prompt   string                  `json:"prompt"`
		Mermaid  string                  `json:"mermaid"`
		Messages []aiConversationMessage `json:"messages"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if strings.TrimSpace(body.Prompt) == "" {
		return badRequest(ErrInvalidRequest, "prompt required")
	}
	if len([]rune(body.Prompt)) > maxAIUserRunes {
		return badRequest(ErrInvalidRequest, "prompt is too large")
	}
	if reason, rejected := rejectedAIIntent(body.Prompt); rejected {
		jsonResponse(w, http.StatusOK, aiRejectedResult(reason, 0))
		return nil
	}
	modelPrompt, err := contextualAIPrompt(body.Prompt, aiRequestContext{Mermaid: body.Mermaid, Messages: body.Messages})
	if err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	result, err := s.buildPipeline(r, modelPrompt)
	if err != nil {
		return &HTTPError{Status: http.StatusUnprocessableEntity, Message: "could not generate a valid pipeline: " + err.Error()}
	}
	jsonResponse(w, http.StatusOK, result)
	return nil
}
func (s *Server) aiRefine(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Prompt     string                  `json:"prompt"`
		Definition interface{}             `json:"definition"`
		Mermaid    string                  `json:"mermaid"`
		Messages   []aiConversationMessage `json:"messages"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if strings.TrimSpace(body.Prompt) == "" {
		return badRequest(ErrInvalidRequest, "prompt required")
	}
	if len([]rune(body.Prompt)) > maxAIUserRunes {
		return badRequest(ErrInvalidRequest, "prompt is too large")
	}
	if reason, rejected := rejectedAIIntent(body.Prompt); rejected {
		jsonResponse(w, http.StatusOK, aiRejectedResult(reason, 0))
		return nil
	}
	prompt := body.Prompt
	if body.Definition != nil {
		seed, err := json.Marshal(redactAIValue(body.Definition))
		if err != nil {
			return badRequest(ErrInvalidRequest, "definition must be valid JSON")
		}
		if len(seed) > maxAIDefinition {
			return badRequest(ErrInvalidRequest, "definition is too large")
		}
		prompt = "Current pipeline JSON:\n" + string(seed) + "\n\nApply this change: " + prompt
	}
	modelPrompt, err := contextualAIPrompt(prompt, aiRequestContext{Mermaid: body.Mermaid, Messages: body.Messages})
	if err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	result, err := s.buildPipeline(r, modelPrompt)
	if err != nil {
		return &HTTPError{Status: http.StatusUnprocessableEntity, Message: "could not refine pipeline: " + err.Error()}
	}
	jsonResponse(w, http.StatusOK, result)
	return nil
}
func (s *Server) buildPipeline(r *http.Request, prompt string) (map[string]interface{}, error) {
	catalog := append([]model.CatalogEntry{}, codedCatalog...)
	if s.Connectors != nil {
		catalog = append(catalog, s.Connectors.Catalog()...)
	}
	for i := range catalog {
		if _, exists := codedAIFields[catalog[i].ActivityType]; exists {
			catalog[i].Fields = mergeCatalogFields(codedFields(catalog[i].ActivityType), catalog[i].Fields)
		}
	}
	sort.SliceStable(catalog, func(i, j int) bool { return catalog[i].ActivityType < catalog[j].ActivityType })
	lines := []string{
		"You design data pipelines as a directed acyclic graph (DAG).",
		"Use only these activity types:",
	}
	byType := map[string]model.CatalogEntry{}
	activityTypes := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		fields := make([]string, 0, len(entry.Fields))
		for _, field := range entry.Fields {
			suffix := ""
			if field.Required {
				suffix = " (required)"
			}
			fields = append(fields, field.Key+suffix)
		}
		line := fmt.Sprintf("- %s (%s) %s", entry.ActivityType, entry.NodeType, entry.Label)
		if len(fields) > 0 {
			line += "; config: " + strings.Join(fields, ", ")
		}
		lines = append(lines, line)
		if _, exists := byType[entry.ActivityType]; !exists {
			activityTypes = append(activityTypes, entry.ActivityType)
		}
		byType[entry.ActivityType] = entry
	}
	instances := []aiConnectorInstance{}
	if s.DB != nil {
		rows, err := tenantQueryRows(r.Context(), s.DB, tenantFrom(r).TenantID, `SELECT id,kind,provider,provider_account_email FROM connector_instances ORDER BY provider,provider_account_email,id`)
		if err != nil {
			return nil, fmt.Errorf("load tenant connector instances: %w", err)
		}
		if len(rows) > 0 {
			lines = append(lines, `AVAILABLE CONNECTOR INSTANCES: use config.connectionId when the user names one of these connections.`)
			for _, row := range rows {
				instance := aiConnectorInstance{ID: stringValue(row["id"]), Kind: stringValue(row["kind"]), Provider: stringValue(row["provider"]), Name: stringValue(row["provider_account_email"])}
				instances = append(instances, instance)
				lines = append(lines, fmt.Sprintf("- provider=%s kind=%s name=%q id=%q", instance.Provider, instance.Kind, instance.Name, instance.ID))
			}
		}
	}
	if len(instances) == 0 {
		lines = append(lines, `AVAILABLE CONNECTOR INSTANCES: none. Return needs_input for activities that require a saved connection.`)
	}
	lines = append(lines,
		`ROLE: You are a strict data-pipeline planner for non-expert users. Translate intent into the smallest executable catalog DAG.`,
		`USER BEHAVIOR: Users describe business intent, not node names. Infer source/transform/sink roles from verbs and destinations, but do not invent missing credentials or external resources.`,
		`SYSTEM RULES:`,
		`- New node "id" values should be short lowercase alphanumeric strings like n1, n2, n3. When refining, preserve existing safe IDs, including hyphens and underscores.`,
		`- Every edge "source" and "target" MUST exactly match one of the node "id" values you defined above.`,
		`- A pipeline starts with source nodes, applies transforms in request order, and ends with sink nodes.`,
		`- Plan as a symbolic operator chain first; do not generate code. Each natural-language action must map to one catalog activityType.`,
		`- Ground parameters in the user's words and available schemas/columns when named. Do not invent column names, buckets, paths, URLs, or table names.`,
		`- Keep operator order semantically valid: filtering/renaming/casting happens before downstream aggregations, sinks, and exports that depend on those fields.`,
		`CONNECTOR/SINK INTENT RULES:`,
		`- Verbs like read, fetch, import, ingest, pull, consume, listen, stream from mean a source/fetch connector.`,
		`- Verbs like write, save, export, load, sync to, send, publish, notify, upsert mean a sink connector.`,
		`- For the same system, direction decides the activityType: from S3/Sheets/Drive/Postgres/MySQL/MongoDB/Kafka/SFTP/Snowflake/Iceberg means the matching *.fetch source; to those systems means sink.* when available.`,
		`- HTTP/API as an input means http.fetch. Webhook as an output/alert/callback means sink.webhook.`,
		`- Never use a source activityType as a destination and never use a sink activityType as an input.`,
		`CONFIG RULES:`,
		`- Copy user-supplied connector parameters into node config using obvious keys from the wording.`,
		`- If the user mentions a listed connector name, put that connector id in config.connectionId on the matching source or sink node.`,
		`- Known config keys: http.fetch uses url and recordsPath; transform.filter uses predicate; transform.dedupe uses key and keep; sink.s3 uses bucket, key, and format.`,
		`- For other connectors/sinks, preserve named fields such as table, database, collection, topic, path, file, sheetId, spreadsheetId, worksheet, schema, warehouse, endpoint, method, headers, format, and mode.`,
		`- When refining, preserve existing nodes, IDs, edges, and config unless the requested change requires modifying them.`,
		`- Always include a "trigger" with at least {"type":"manual"}.`,
		`- When the user explicitly requests an execution engine, set execution.engine to workflow, stream-direct, spark-sql, or flink-sql. Put Spark/Flink SELECT SQL in execution.transformSql instead of a transform node.`,
		`- Use ingestion.mode incremental, backfill, or realtime. CDC is connector config.syncMode=cdc, not an ingestion mode. Preserve existing ingestion modes, named state keys, and backfill boundaries exactly.`,
		`RESPONSE RULES:`,
		`- Return status "needs_input" with one or more targeted questions when an executable pipeline requires a saved connection, table, topic, bucket, path, URL, or other external resource the user did not provide. Do not guess it.`,
		`- Never ask for or return raw credentials or secrets. Ask the user to configure or select a saved connection instead.`,
		`- Return status "ready" only when the DAG can be validated. Put any non-blocking inferences in assumptions and safety concerns in warnings.`,
		`- Return status "rejected" with a non-empty reason and warning for requests that require secret exposure, unsafe/private outbound URLs, or a cyclic graph. Do not return a DAG.`,
		`- For needs_input, return empty suggestedName, nodes, and edges and use a manual trigger.`,
		`- For ready and needs_input, reason must be an empty string.`,
		`Respond with ONLY JSON matching the supplied schema; no markdown or explanation.`,
	)
	buildResult := func(output aiPipelineResponse) (map[string]interface{}, error) {
		questions := append([]string{}, output.Questions...)
		assumptions := append([]string{}, output.Assumptions...)
		warnings := append([]string{}, output.Warnings...)
		if output.Status == aiPipelineRejected {
			reason := strings.TrimSpace(output.Reason)
			if reason == "" || len(warnings) == 0 {
				return nil, fmt.Errorf("rejected response must include a reason and warning")
			}
			return aiRejectedResult(reason, 0), nil
		}
		if output.Status == aiPipelineNeedsInput {
			if len(questions) == 0 || strings.TrimSpace(questions[0]) == "" {
				return nil, fmt.Errorf("needs_input response must include a question")
			}
			return map[string]interface{}{"status": output.Status, "reason": "", "questions": questions, "assumptions": assumptions, "warnings": warnings}, nil
		}
		if output.Status != aiPipelineReady {
			return nil, fmt.Errorf("response.status must be ready, needs_input, or rejected, got %q", output.Status)
		}
		rawToString := func(raw json.RawMessage) string {
			if len(raw) == 0 {
				return ""
			}
			if raw[0] == '"' {
				var value string
				if json.Unmarshal(raw, &value) == nil {
					return value
				}
			}
			return strings.TrimSpace(string(raw))
		}
		def := model.PipelineDefinition{Name: output.SuggestedName, Trigger: output.Trigger, Execution: output.Execution}
		nodeIDSet := map[string]string{}
		for _, node := range output.Nodes {
			nodeID := rawToString(node.ID)
			if nodeID == "" {
				continue
			}
			entry, ok := byType[node.ActivityType]
			if !ok {
				return nil, fmt.Errorf("unknown activityType %s", node.ActivityType)
			}
			def.Nodes = append(def.Nodes, model.Node{ID: nodeID, Label: node.Label, Type: entry.NodeType, ActivityType: node.ActivityType, Config: node.Config, Ingestion: node.Ingestion})
			nodeIDSet[strings.ToLower(nodeID)] = nodeID
		}
		for i, rawEdge := range output.Edges {
			var src, tgt, condition string
			var obj struct {
				Source    json.RawMessage `json:"source"`
				Target    json.RawMessage `json:"target"`
				Condition string          `json:"condition"`
			}
			if err := json.Unmarshal(rawEdge, &obj); err == nil && len(obj.Source) > 0 {
				src, tgt, condition = rawToString(obj.Source), rawToString(obj.Target), obj.Condition
			} else {
				var edge string
				if json.Unmarshal(rawEdge, &edge) != nil {
					warnings = append(warnings, fmt.Sprintf("dropped edge %d: unrecognised format", i+1))
					continue
				}
				edge = strings.ReplaceAll(strings.ReplaceAll(edge, "→", "->"), " ", "")
				parts := strings.SplitN(edge, "->", 2)
				if len(parts) != 2 {
					parts = strings.SplitN(edge, ",", 2)
				}
				if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
					warnings = append(warnings, fmt.Sprintf("dropped edge %d: cannot parse %q", i+1, edge))
					continue
				}
				src, tgt = parts[0], parts[1]
			}
			if canonical, ok := nodeIDSet[strings.ToLower(src)]; ok {
				src = canonical
			}
			if canonical, ok := nodeIDSet[strings.ToLower(tgt)]; ok {
				tgt = canonical
			}
			if nodeIDSet[strings.ToLower(src)] == "" || nodeIDSet[strings.ToLower(tgt)] == "" {
				warnings = append(warnings, fmt.Sprintf("dropped edge %d: %s→%s (unknown node)", i+1, src, tgt))
				continue
			}
			def.Edges = append(def.Edges, model.Edge{ID: fmt.Sprintf("e%d", i+1), Source: src, Target: tgt, Condition: condition})
		}
		if reason := unsafePipelineReason(def); reason != "" {
			return aiRejectedResult(reason, 0), nil
		}
		if err := validateAIGrounding(def, byType, instances); err != nil {
			return nil, err
		}
		if err := validatePipeline(def); err != nil {
			return nil, err
		}
		mermaidLabel := func(value string) string {
			value = strings.ReplaceAll(value, `\`, `\\`)
			value = strings.ReplaceAll(value, `"`, `\"`)
			return strings.ReplaceAll(value, "\n", " ")
		}
		mermaid := []string{"flowchart LR"}
		for _, node := range def.Nodes {
			mermaid = append(mermaid, fmt.Sprintf(`  %s["%s"]`, node.ID, mermaidLabel(node.Label)))
		}
		for _, edge := range def.Edges {
			mermaid = append(mermaid, fmt.Sprintf("  %s --> %s", edge.Source, edge.Target))
		}
		definition := map[string]interface{}{"nodes": def.Nodes, "edges": def.Edges, "trigger": def.Trigger, "suggestedName": def.Name}
		if def.Execution != nil {
			definition["execution"] = def.Execution
		}
		return map[string]interface{}{
			"status": aiPipelineReady, "reason": "", "mermaid": strings.Join(mermaid, "\n"),
			"definition": definition,
			"questions":  questions, "assumptions": assumptions, "warnings": warnings,
		}, nil
	}

	userPrompt := prompt
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		var output aiPipelineResponse
		if err := s.ollamaJSON(r, strings.Join(lines, "\n"), userPrompt, aiPipelineJSONSchema(activityTypes), &output); err != nil {
			if !errors.Is(err, errInvalidAIJSON) {
				return nil, err
			}
			lastErr = err
		} else if result, err := buildResult(output); err == nil {
			result["metrics"] = map[string]int{"repairCount": attempt}
			return result, nil
		} else {
			lastErr = err
		}
		if attempt == 0 {
			userPrompt = fmt.Sprintf("Your previous response was invalid. Correct it and return only JSON matching the schema.\nValidation error: %s\nOriginal user request: %s", lastErr, prompt)
		}
	}
	return nil, fmt.Errorf("model response invalid after one repair: %w", lastErr)
}

func (s *Server) ollamaJSON(r *http.Request, system, user string, schema map[string]interface{}, target interface{}) error {
	payload, err := json.Marshal(map[string]interface{}{
		"model": env("OLLAMA_MODEL", "llama3.1:8b"), "stream": false, "format": schema,
		"think":    strings.EqualFold(env("OLLAMA_THINK", "false"), "true"),
		"options":  map[string]interface{}{"temperature": 0, "seed": 42, "num_ctx": 4096},
		"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
	})
	if err != nil {
		return fmt.Errorf("AI builder request is invalid")
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimSuffix(env("OLLAMA_URL", "http://ollama:11434"), "/")+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("AI builder request is invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	var response struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	client := *s.HTTP
	// ponytail: CPU-only Ollama on this box runs ~4 tok/s and can take minutes,
	// especially on a cold model load — give it real headroom.
	client.Timeout = 4 * time.Minute
	if err := doJSON(&client, request, &response); err != nil {
		return fmt.Errorf("AI builder is unavailable")
	}
	if err := json.Unmarshal([]byte(response.Message.Content), target); err != nil {
		return fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}
	return nil
}
