package connectors

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func rowsWithCursor(values ...int) []interface{} {
	out := make([]interface{}, len(values))
	for i, value := range values {
		out[i] = map[string]interface{}{"updated": value, "id": i + 1}
	}
	return out
}

func TestPlanIncrementalPageQueries(t *testing.T) {
	tests := []struct {
		name    string
		dialect sqlDialect
		key     string
		cursor  map[string]interface{}
		want    string
		args    int
	}{
		{name: "first page", dialect: postgresDialect, cursor: map[string]interface{}{}, want: `SELECT * FROM t ORDER BY "updated" ASC LIMIT $1`, args: 1},
		{name: "unkeyed resume", dialect: postgresDialect, cursor: map[string]interface{}{"value": 5}, want: `SELECT * FROM t WHERE "updated">$1 ORDER BY "updated" ASC LIMIT $2`, args: 2},
		{name: "keyed resume", dialect: postgresDialect, key: "id", cursor: map[string]interface{}{"value": 5, "key": 9}, want: `SELECT * FROM t WHERE ("updated","id")>($1,$2) ORDER BY "updated" ASC,"id" ASC LIMIT $3`, args: 3},
		{name: "keyed after unkeyed checkpoint re-reads the boundary", dialect: postgresDialect, key: "id", cursor: map[string]interface{}{"value": 5}, want: `SELECT * FROM t WHERE "updated">=$1 ORDER BY "updated" ASC,"id" ASC LIMIT $2`, args: 2},
		{name: "mysql keyed", dialect: mysqlDialect, key: "id", cursor: map[string]interface{}{"value": 5, "key": 9}, want: "SELECT * FROM t WHERE (`updated`,`id`)>(?,?) ORDER BY `updated` ASC,`id` ASC LIMIT ?", args: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planIncrementalPage(test.dialect, "*", "t", "updated", test.key, test.cursor, nil, 100)
			if err != nil {
				t.Fatal(err)
			}
			if plan.query != test.want || len(plan.args) != test.args {
				t.Fatalf("query = %s (%d args), want %s (%d args)", plan.query, len(plan.args), test.want, test.args)
			}
		})
	}
	if _, err := planIncrementalPage(postgresDialect, "*", "t", "updated", "id; drop", map[string]interface{}{}, nil, 10); !isPermanent(err, InvalidConfigError) {
		t.Fatalf("invalid keyColumn error = %v", err)
	}
}

func TestPlanIncrementalPageBackfillWindow(t *testing.T) {
	plan, err := planIncrementalPage(postgresDialect, "*", "t", "updated", "", map[string]interface{}{}, &model.IngestionConfig{Mode: "backfill", BackfillStart: "2026-01-01", BackfillEnd: "2026-02-01"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT * FROM t WHERE "updated">=$1 AND "updated"<$2 ORDER BY "updated" ASC LIMIT $3`; plan.query != want {
		t.Fatalf("query = %s", plan.query)
	}
}

func TestFinishIncrementalPageKeepsBoundaryTiesForNextPage(t *testing.T) {
	// Page size 3, fetched 4 rows: the 3rd and 4th rows share cursor value 2,
	// so the page must stop before the 3rd row; "> 1" then re-reads both.
	values, next, hasMore, err := finishIncrementalPage(rowsWithCursor(1, 1, 2, 2), 3, "updated", "", map[string]interface{}{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMore || len(values) != 2 || next["value"] != 1 {
		t.Fatalf("values = %d, next = %#v, hasMore = %v", len(values), next, hasMore)
	}
	// No tie at the boundary: the full page is kept.
	values, next, _, err = finishIncrementalPage(rowsWithCursor(1, 2, 3, 4), 3, "updated", "", map[string]interface{}{}, false)
	if err != nil || len(values) != 3 || next["value"] != 3 {
		t.Fatalf("values = %d, next = %#v, err = %v", len(values), next, err)
	}
	// Last page: nothing to trim.
	values, _, hasMore, err = finishIncrementalPage(rowsWithCursor(7, 7), 3, "updated", "", map[string]interface{}{}, false)
	if err != nil || hasMore || len(values) != 2 {
		t.Fatalf("values = %d, hasMore = %v, err = %v", len(values), hasMore, err)
	}
	// A whole page of one value cannot advance without a key column.
	if _, _, _, err = finishIncrementalPage(rowsWithCursor(5, 5, 5, 5), 3, "updated", "", map[string]interface{}{}, false); !isPermanent(err, InvalidConfigError) || !strings.Contains(err.Error(), "keyColumn") {
		t.Fatalf("all-tie page error = %v", err)
	}
	// Keyed pages are exact and record the key.
	values, next, _, err = finishIncrementalPage(rowsWithCursor(5, 5, 5, 5), 3, "updated", "id", map[string]interface{}{}, true)
	if err != nil || len(values) != 3 || next["value"] != 5 || next["key"] != 3 {
		t.Fatalf("keyed values = %d, next = %#v, err = %v", len(values), next, err)
	}
}

func TestSameCursorValueComparesTimesByInstant(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !sameCursorValue(at, at.In(time.FixedZone("x", 3600))) {
		t.Fatal("equal instants in different zones must tie")
	}
	if sameCursorValue(at, at.Add(time.Nanosecond)) || sameCursorValue(nil, 0) {
		t.Fatal("different values must not tie")
	}
}

func TestMongoPageFilterResumesWithTypedCursor(t *testing.T) {
	id := bson.NewObjectID()
	valueJSON, err := mongoExtJSON(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	idJSON, err := mongoExtJSON(id)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := mongoPageFilter("updatedAt", map[string]interface{}{"valueExtJson": valueJSON, "idExtJson": idJSON})
	if err != nil {
		t.Fatal(err)
	}
	branches, ok := filter["$or"].(bson.A)
	if !ok || len(branches) != 2 {
		t.Fatalf("filter = %#v", filter)
	}
	tie := branches[1].(bson.M)
	if got, ok := tie["_id"].(bson.M)["$gt"].(bson.ObjectID); !ok || got != id {
		t.Fatalf("tie-breaker = %#v, want ObjectID %s", tie["_id"], id.Hex())
	}
	// Legacy checkpoints stored the _id as its hex string.
	legacy, err := mongoPageFilter("_id", map[string]interface{}{"value": id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := legacy["_id"].(bson.M)["$gt"].(bson.ObjectID); !ok || got != id {
		t.Fatalf("legacy _id cursor = %#v", legacy)
	}
}

func TestInitialWatermark(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	tests := map[interface{}]string{
		nil:                    "",
		"":                     "",
		"now":                  "2026-10-19T12:00:00Z",
		"now-24h":              "2026-10-18T12:00:00Z",
		"2026-01-01T00:00:00Z": "2026-01-01T00:00:00Z",
	}
	for input, want := range tests {
		got, err := initialWatermark(input, now)
		if err != nil || got != want {
			t.Fatalf("initialWatermark(%v) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, bad := range []string{"yesterday", "now-1x", "now--1h"} {
		if _, err := initialWatermark(bad, now); !isPermanent(err, InvalidConfigError) {
			t.Fatalf("initialWatermark(%q) error = %v", bad, err)
		}
	}
}

func TestSinkIdempotencyKeysAreStablePerStep(t *testing.T) {
	handler := HandlerContext{TenantID: "t", ExecutionID: "exec-1", NodeID: "sink"}
	if idempotencyKey(handler, 0) != idempotencyKey(handler, 0) {
		t.Fatal("key must be stable across retries")
	}
	other := handler
	other.ExecutionID = "exec-2"
	if idempotencyKey(handler, 0) == idempotencyKey(other, 0) || idempotencyKey(handler, 0) == idempotencyKey(handler, 1) {
		t.Fatal("key must differ per execution and batch")
	}
	records := kafkaSinkRecords("topic", "id", []map[string]interface{}{{"id": "a"}, {"id": "b"}}, handler)
	if len(records) != 2 || string(records[0].Key) != "a" {
		t.Fatalf("records = %#v", records)
	}
	first, second := string(records[0].Headers[0].Value), string(records[1].Headers[0].Value)
	if records[0].Headers[0].Key != kafkaIdempotencyHeader || first == second || !strings.HasPrefix(first, idempotencyKey(handler, 0)) {
		t.Fatalf("headers = %q, %q", first, second)
	}
}

func TestPermanentErrorsAreTyped(t *testing.T) {
	runtime := &Runtime{Handlers: map[string]Handler{}, Sources: map[string]Source{}, Registry: &Registry{Manifests: map[string]model.ConnectorManifest{}}}
	runtime.registerTransforms()
	ctx := context.Background()
	_, _, err := runtime.Handle(ctx, "transform.nope", nil, nil, HandlerContext{})
	if !isPermanent(err, UnknownActivityError) {
		t.Fatalf("unknown activity error = %v", err)
	}
	_, err = runtime.Fetch(ctx, "nope.fetch", SourceParams{})
	if !isPermanent(err, UnknownActivityError) {
		t.Fatalf("unknown source error = %v", err)
	}
	contract := map[string]interface{}{"schemaJson": map[string]interface{}{"id": "number"}}
	// A scalar record used to panic the handler; now it is a contract violation.
	_, _, err = runtime.Handle(ctx, "transform.contract", []interface{}{"not an object"}, contract, HandlerContext{})
	if !isPermanent(err, SchemaValidationError) {
		t.Fatalf("scalar record error = %v", err)
	}
	_, _, err = runtime.Handle(ctx, "transform.contract", []interface{}{map[string]interface{}{"id": "x"}}, contract, HandlerContext{})
	if !isPermanent(err, SchemaValidationError) {
		t.Fatalf("contract violation error = %v", err)
	}
	valid, meta, err := runtime.Handle(ctx, "transform.contract", []interface{}{"scalar", map[string]interface{}{"id": float64(1)}}, map[string]interface{}{"schemaJson": map[string]interface{}{"id": "number"}, "onViolation": "drop"}, HandlerContext{})
	if err != nil || len(valid.([]interface{})) != 1 || meta["failedCount"] != 1 {
		t.Fatalf("drop mode = %#v, %#v, %v", valid, meta, err)
	}
	_, _, err = runtime.Handle(ctx, "transform.contract", []interface{}{}, map[string]interface{}{"schemaJson": "not json"}, HandlerContext{})
	if !isPermanent(err, InvalidConfigError) {
		t.Fatalf("bad schema error = %v", err)
	}
	for _, step := range []string{"transform.rename", "transform.flatten", "transform.parse"} {
		_, _, err = runtime.Handle(ctx, step, []interface{}{42}, map[string]interface{}{"mapping": map[string]interface{}{}}, HandlerContext{})
		if !isPermanent(err, SchemaValidationError) {
			t.Fatalf("%s scalar record error = %v", step, err)
		}
	}
	if _, err = MergeArrays("innerJoin", [][]interface{}{{1}}, ""); !isPermanent(err, InvalidConfigError) {
		t.Fatalf("join without key error = %v", err)
	}
	if _, err = MergeArrays("innerJoin", [][]interface{}{{1}}, "id"); !isPermanent(err, SchemaValidationError) {
		t.Fatalf("join scalar error = %v", err)
	}
}

func TestExpressionsCompileOncePerStep(t *testing.T) {
	compiledPrograms.Lock()
	compiledPrograms.runners = map[string]expressionRunner{}
	compiledPrograms.Unlock()
	for i := 0; i < 50; i++ {
		ok, err := EvaluatePredicate("r.amount > 10", map[string]interface{}{"amount": float64(i)})
		if err != nil || ok != (i > 10) {
			t.Fatalf("row %d: %v, %v", i, ok, err)
		}
	}
	compiledPrograms.Lock()
	defer compiledPrograms.Unlock()
	if len(compiledPrograms.runners) != 1 {
		t.Fatalf("compiled %d programs for one expression", len(compiledPrograms.runners))
	}
}

func isPermanent(err error, kind string) bool {
	var permanentErr *PermanentError
	return errors.As(err, &permanentErr) && permanentErr.Type == kind
}

func TestFinishIncrementalPageMatchesUpperCaseSnowflakeColumns(t *testing.T) {
	rows := []interface{}{}
	for _, v := range []int{1, 2, 3, 4} {
		rows = append(rows, map[string]interface{}{"UPDATED_AT": v})
	}
	values, next, hasMore, err := finishIncrementalPage(rows, 3, "updated_at", "", map[string]interface{}{}, false)
	if err != nil || !hasMore || len(values) != 3 || next["value"] != 3 {
		t.Fatalf("values = %d, next = %#v, hasMore = %v, err = %v", len(values), next, hasMore, err)
	}
	if _, _, _, err = finishIncrementalPage(rows, 3, "modified", "", map[string]interface{}{}, false); !isPermanent(err, InvalidConfigError) || !strings.Contains(err.Error(), "not in the query result") {
		t.Fatalf("missing cursor column error = %v", err)
	}
}
