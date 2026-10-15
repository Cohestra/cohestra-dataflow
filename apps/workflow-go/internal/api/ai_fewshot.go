package api

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Few-shot planner examples ("runtime training"). A held-out bake-off showed
// category-matched examples lift small local models more than a QLoRA adapter
// trained on a handful of rows, so the planner can prepend two worked examples
// chosen for the request's shape. Enable with AI_FEW_SHOT=true; examples use
// fictional connections that never exist in a tenant, and must stay disjoint
// from tests/ai-evals cases so evaluation remains held out.

type aiFewShotExample struct {
	Category string
	Request  string
	Output   aiFewShotOutput
}

type aiFewShotOutput struct {
	Status        aiPipelineStatus         `json:"status"`
	Reason        string                   `json:"reason"`
	SuggestedName string                   `json:"suggestedName"`
	Questions     []string                 `json:"questions"`
	Assumptions   []string                 `json:"assumptions"`
	Warnings      []string                 `json:"warnings"`
	Trigger       map[string]string        `json:"trigger"`
	Execution     map[string]string        `json:"execution,omitempty"`
	Nodes         []map[string]interface{} `json:"nodes"`
	Edges         []map[string]string      `json:"edges"`
}

// aiFewShotConnections are the fictional instances the examples reference.
var aiFewShotConnections = []aiConnectorInstance{
	{ID: "example-crm-pg", Kind: "credential", Provider: "postgres", Name: "example-crm-pg"},
	{ID: "example-lake", Kind: "credential", Provider: "iceberg", Name: "example-lake"},
	{ID: "example-landing", Kind: "credential", Provider: "s3", Name: "example-landing"},
	{ID: "example-events", Kind: "credential", Provider: "kafka", Name: "example-events"},
	{ID: "example-marts", Kind: "credential", Provider: "clickhouse", Name: "example-marts"},
	{ID: "example-shop", Kind: "credential", Provider: "mysql", Name: "example-shop"},
}

func fsNode(id, label, activityType string, config map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": id, "label": label, "activityType": activityType, "config": config}
}

func fsEdge(source, target string) map[string]string {
	return map[string]string{"source": source, "target": target}
}

func fsReady(name string, nodes []map[string]interface{}, edges []map[string]string) aiFewShotOutput {
	return aiFewShotOutput{Status: aiPipelineReady, SuggestedName: name, Questions: []string{}, Assumptions: []string{}, Warnings: []string{}, Trigger: map[string]string{"type": "manual"}, Nodes: nodes, Edges: edges}
}

var aiFewShotExamples = []aiFewShotExample{
	{
		Category: "linear",
		Request:  "Pull table crm.accounts from PostgreSQL connection example-crm-pg, keep rows where region equals EMEA, drop duplicate account_id keeping the last one, and write through Iceberg connection example-lake to namespace lake table accounts_emea as silver.",
		Output: fsReady("EMEA accounts to lake", []map[string]interface{}{
			fsNode("n1", "CRM accounts", "postgres.fetch", map[string]interface{}{"connectionId": "example-crm-pg", "table": "crm.accounts"}),
			fsNode("n2", "EMEA only", "transform.filter", map[string]interface{}{"predicate": "r.region === 'EMEA'"}),
			fsNode("n3", "Latest per account", "transform.dedupe", map[string]interface{}{"key": "account_id", "keep": "last"}),
			fsNode("n4", "Lake silver", "sink.iceberg", map[string]interface{}{"connectionId": "example-lake", "namespace": "lake", "table": "accounts_emea"}),
		}, []map[string]string{fsEdge("n1", "n2"), fsEdge("n2", "n3"), fsEdge("n3", "n4")}),
	},
	{
		Category: "branching",
		Request:  "Consume Kafka connection example-events topic sensor.readings, then fork: land everything through S3 connection example-landing in bucket landing key sensors/raw.jsonl as bronze, and send rows where temp > 90 through ClickHouse connection example-marts to table gold.hot_sensors as gold.",
		Output: fsReady("Sensor medallion fan-out", []map[string]interface{}{
			fsNode("n1", "Sensor readings", "kafka.fetch", map[string]interface{}{"connectionId": "example-events", "topic": "sensor.readings"}),
			fsNode("n2", "Fork", "flow.fork", map[string]interface{}{}),
			fsNode("n3", "Bronze landing", "sink.s3", map[string]interface{}{"connectionId": "example-landing", "bucket": "landing", "key": "sensors/raw.jsonl", "format": "jsonl", "layer": "bronze"}),
			fsNode("n4", "Hot readings", "transform.filter", map[string]interface{}{"predicate": "r.temp > 90"}),
			fsNode("n5", "Gold mart", "sink.clickhouse", map[string]interface{}{"connectionId": "example-marts", "table": "gold.hot_sensors", "layer": "gold"}),
		}, []map[string]string{fsEdge("n1", "n2"), fsEdge("n2", "n3"), fsEdge("n2", "n4"), fsEdge("n4", "n5")}),
	},
	{
		Category: "branching",
		Request:  "Combine customers from MySQL connection example-shop table shop.customers with tickets from PostgreSQL connection example-crm-pg table crm.tickets by customer_id as an inner join, and load through ClickHouse connection example-marts into table marts.customer_tickets.",
		Output: fsReady("Customer tickets join", []map[string]interface{}{
			fsNode("n1", "Customers", "mysql.fetch", map[string]interface{}{"connectionId": "example-shop", "table": "shop.customers"}),
			fsNode("n2", "Tickets", "postgres.fetch", map[string]interface{}{"connectionId": "example-crm-pg", "table": "crm.tickets"}),
			fsNode("n3", "Join on customer", "flow.merge", map[string]interface{}{"mergeStrategy": "innerJoin", "joinKey": "customer_id"}),
			fsNode("n4", "Marts", "sink.clickhouse", map[string]interface{}{"connectionId": "example-marts", "table": "marts.customer_tickets"}),
		}, []map[string]string{fsEdge("n1", "n3"), fsEdge("n2", "n3"), fsEdge("n3", "n4")}),
	},
	{
		Category: "refine",
		Request: `Current pipeline JSON:
{"nodes":[{"id":"orders_src","activityType":"mysql.fetch","config":{"connectionId":"example-shop","table":"shop.orders"}},{"id":"lake_out","activityType":"sink.iceberg","config":{"connectionId":"example-lake","namespace":"lake","table":"orders"}}],"edges":[{"source":"orders_src","target":"lake_out"}],"trigger":{"type":"manual"}}

Apply this change: Rename column cust to customer_id before the lake sink. Keep everything else.`,
		Output: fsReady("Orders to lake", []map[string]interface{}{
			fsNode("orders_src", "Orders", "mysql.fetch", map[string]interface{}{"connectionId": "example-shop", "table": "shop.orders"}),
			fsNode("n1", "Rename cust", "transform.rename", map[string]interface{}{"mapping": map[string]interface{}{"cust": "customer_id"}}),
			fsNode("lake_out", "Lake", "sink.iceberg", map[string]interface{}{"connectionId": "example-lake", "namespace": "lake", "table": "orders"}),
		}, []map[string]string{fsEdge("orders_src", "n1"), fsEdge("n1", "lake_out")}),
	},
	{
		Category: "clarify",
		Request:  "Move our product catalog into the warehouse every hour.",
		Output: aiFewShotOutput{Status: aiPipelineNeedsInput, Questions: []string{"Which saved connection and table or file holds the product catalog?", "Which warehouse connection and table should receive it?"},
			Assumptions: []string{}, Warnings: []string{}, Trigger: map[string]string{"type": "manual"}, Nodes: []map[string]interface{}{}, Edges: []map[string]string{}},
	},
}

// aiNumCtx is the Ollama context window. Few-shot examples add ~600 prompt
// tokens, so pair AI_FEW_SHOT=true with OLLAMA_NUM_CTX=8192.
func aiNumCtx() int {
	if value, err := strconv.Atoi(strings.TrimSpace(env("OLLAMA_NUM_CTX", "4096"))); err == nil && value >= 2048 {
		return value
	}
	return 4096
}

func aiFewShotEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(env("AI_FEW_SHOT", "false")), "true")
}

// aiFewShotCategories picks two example categories for a request.
func aiFewShotCategories(prompt string) []string {
	lower := strings.ToLower(prompt)
	if strings.Contains(prompt, "Current pipeline JSON:") {
		return []string{"refine", "linear"}
	}
	if containsAny(lower, []string{"fork", "branch", "join", "merge", "combine", " both ", "fan out", "fan-out", "also send", "and also"}) {
		return []string{"branching", "linear"}
	}
	return []string{"linear", "clarify"}
}

// aiFewShotSection renders the selected examples as system-prompt lines.
func aiFewShotSection(prompt string) []string {
	lines := []string{
		`WORKED EXAMPLES: they use fictional example-* connections. Never copy their connections, tables, or fields; always use the AVAILABLE CONNECTOR INSTANCES and the user's own words.`,
	}
	for _, category := range aiFewShotCategories(prompt) {
		for _, example := range aiFewShotExamples {
			if example.Category != category || (category == "branching" && !aiFewShotJoinMatch(prompt, example)) {
				continue
			}
			output, err := json.Marshal(example.Output)
			if err != nil {
				continue
			}
			lines = append(lines, "Example request: "+example.Request, "Example JSON: "+string(output))
			break
		}
	}
	return lines
}

// aiFewShotJoinMatch shows the join example for join/merge wording and the
// fork example otherwise.
func aiFewShotJoinMatch(prompt string, example aiFewShotExample) bool {
	wantsJoin := containsAny(strings.ToLower(prompt), []string{"join", "merge", "combine"})
	isJoin := strings.Contains(example.Request, "join")
	return wantsJoin == isJoin
}
