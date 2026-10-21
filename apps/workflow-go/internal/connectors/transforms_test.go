package connectors

import (
	"fmt"
	"testing"

	"github.com/expr-lang/expr"
)

func TestExpressionAndMergeCompatibility(t *testing.T) {
	ok, err := EvaluatePredicate(`r.amount >= 10 && records.length === 1`, []interface{}{map[string]interface{}{"amount": 12.0}})
	if err != nil || !ok {
		t.Fatalf("predicate: ok=%v err=%v", ok, err)
	}
	merged, err := MergeArrays("innerJoin", [][]interface{}{
		{map[string]interface{}{"id": 1.0, "left": "a"}},
		{map[string]interface{}{"id": 1.0, "right": "b"}},
	}, "id")
	if err != nil || len(merged) != 1 {
		t.Fatalf("merge: %#v err=%v", merged, err)
	}
}

func TestDedupeHashMatchesTypeScriptFixture(t *testing.T) {
	got := DedupeHash(map[string]interface{}{"id": 7.0, "region": "in"}, []string{"id", "region"})
	const want = "6e167cc4b5e61d1f4d91602d0aea729eebf2e4bdd0dfae34fd023c6ccd95fca2"
	if got != want {
		t.Fatalf("hash=%s", got)
	}
}

// The expression cache must not change results: expr type-checks r as a plain
// map, so records with different field types share one compiled program.
func TestCachedExpressionsMatchFreshCompiles(t *testing.T) {
	records := []interface{}{map[string]interface{}{"amount": 1}, map[string]interface{}{"amount": 2.5}, map[string]interface{}{"amount": "1"}, map[string]interface{}{"other": 2}}
	for _, expression := range []string{"r.amount == 1", "r.amount > 1", "r.amount ?? 0"} {
		for _, record := range records {
			cached, cachedErr := evalExpression(expression, record, records)
			program, err := expr.Compile(normalizeExpression(expression), expr.Env(expressionEnv(record, records)))
			if err != nil {
				t.Fatal(err)
			}
			fresh, freshErr := expr.Run(program, expressionEnv(record, records))
			if fmt.Sprint(cached) != fmt.Sprint(fresh) || (cachedErr == nil) != (freshErr == nil) {
				t.Fatalf("%s on %v: cached %v/%v, fresh %v/%v", expression, record, cached, cachedErr, fresh, freshErr)
			}
		}
	}
}
