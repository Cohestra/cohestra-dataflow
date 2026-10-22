package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"github.com/jmespath/go-jmespath"
)

func expressionEnv(record interface{}, records []interface{}) map[string]interface{} {
	return map[string]interface{}{"r": record, "records": records, "abs": func(v interface{}) float64 { return math.Abs(number(v)) }, "round": func(v interface{}, precision ...interface{}) float64 {
			if len(precision) == 0 {
				return math.Round(number(v))
			}
			scale := math.Pow(10, number(precision[0]))
			return math.Round(number(v)*scale) / scale
		}, "lower": func(v interface{}) string { return strings.ToLower(fmt.Sprint(v)) }, "upper": func(v interface{}) string { return strings.ToUpper(fmt.Sprint(v)) }, "string": fmt.Sprint, "number": number, "length": func(v interface{}) int { return reflect.ValueOf(v).Len() }, "coalesce": func(values ...interface{}) interface{} {
		for _, v := range values {
			if v != nil {
				return v
			}
		}
		return nil
	}, "concat": func(values ...interface{}) string {
		var b strings.Builder
		for _, v := range values {
			b.WriteString(fmt.Sprint(v))
		}
		return b.String()
	}}
}
func normalizeExpression(value string) string {
	value = strings.ReplaceAll(value, "!==", "!=")
	value = strings.ReplaceAll(value, "===", "==")
	value = strings.ReplaceAll(value, "records.length", "len(records)")
	return value
}

// compiledPrograms caches compiled expressions as runners, so a step compiles
// its expression once instead of once per record. expr type-checks against the
// environment, so the key includes the record's dynamic type.
type expressionRunner func(env interface{}) (interface{}, error)

var compiledPrograms = struct {
	sync.Mutex
	runners map[string]expressionRunner
}{runners: map[string]expressionRunner{}}

const maxCompiledPrograms = 1024

func compileExpression(value string, record interface{}, records []interface{}) (expressionRunner, error) {
	normalized := normalizeExpression(value)
	key := fmt.Sprintf("%T\x00%s", record, normalized)
	compiledPrograms.Lock()
	runner := compiledPrograms.runners[key]
	compiledPrograms.Unlock()
	if runner != nil {
		return runner, nil
	}
	program, err := expr.Compile(normalized, expr.Env(expressionEnv(record, records)))
	if err != nil {
		return nil, err
	}
	runner = func(env interface{}) (interface{}, error) { return expr.Run(program, env) }
	compiledPrograms.Lock()
	if len(compiledPrograms.runners) >= maxCompiledPrograms {
		compiledPrograms.runners = map[string]expressionRunner{}
	}
	compiledPrograms.runners[key] = runner
	compiledPrograms.Unlock()
	return runner, nil
}

func evalExpression(value string, record interface{}, records []interface{}) (interface{}, error) {
	runner, err := compileExpression(value, record, records)
	if err != nil {
		return nil, err
	}
	return runner(expressionEnv(record, records))
}
func EvaluatePredicate(value string, data interface{}) (bool, error) {
	records, ok := data.([]interface{})
	if !ok {
		records = []interface{}{data}
	}
	var record interface{}
	if len(records) > 0 {
		record = records[0]
	}
	result, err := evalExpression(value, record, records)
	if err != nil {
		return false, err
	}
	truth, ok := result.(bool)
	return truth, func() error {
		if !ok {
			return fmt.Errorf("predicate did not return boolean")
		}
		return nil
	}()
}

func splitProjection(value string) []string {
	parts := []string{}
	start, depth := 0, 0
	quote := rune(0)
	runes := []rune(value)
	for i, ch := range runes {
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
		} else if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
		} else if ch == ',' && depth == 0 {
			parts = append(parts, strings.TrimSpace(string(runes[start:i])))
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(string(runes[start:])))
	return parts
}
func evalMap(value string, record interface{}) (interface{}, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "(")
	trimmed = strings.TrimSuffix(trimmed, ")")
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		body := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		out := map[string]interface{}{}
		for _, field := range splitProjection(body) {
			index := strings.Index(field, ":")
			if index < 1 {
				return nil, fmt.Errorf("invalid map projection field %q", field)
			}
			key := strings.Trim(strings.TrimSpace(field[:index]), "'\"")
			if !regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$-]*$`).MatchString(key) {
				return nil, fmt.Errorf("invalid projection key %q", key)
			}
			value, err := evalExpression(strings.TrimSpace(field[index+1:]), record, nil)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		return out, nil
	}
	return evalExpression(trimmed, record, nil)
}

func (r *Runtime) registerTransforms() {
	r.Handlers["transform.map"] = func(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
		rows, err := records(input)
		if err != nil {
			return nil, nil, err
		}
		out := make([]interface{}, 0, len(rows))
		for _, row := range rows {
			value, err := evalMap(stringValue(cfg["expression"]), row)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, value)
		}
		return out, nil, nil
	}
	r.Handlers["transform.filter"] = func(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
		rows, err := records(input)
		if err != nil {
			return nil, nil, err
		}
		out := []interface{}{}
		for _, row := range rows {
			ok, err := EvaluatePredicate(stringValue(cfg["predicate"]), row)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				out = append(out, row)
			}
		}
		return out, nil, nil
	}
	r.Handlers["transform.formula"] = func(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
		rows, err := records(input)
		if err != nil {
			return nil, nil, err
		}
		out := []interface{}{}
		for _, raw := range rows {
			row, ok := raw.(map[string]interface{})
			if !ok {
				return nil, nil, schemaError("formula input must be objects")
			}
			value, err := evalExpression(stringValue(cfg["expression"]), row, nil)
			if err != nil {
				return nil, nil, err
			}
			copy := cloneMap(row)
			copy[stringValue(cfg["outputField"])] = value
			out = append(out, copy)
		}
		return out, nil, nil
	}
	r.Handlers["transform.select"] = func(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
		rows, err := records(input)
		if err != nil {
			return nil, nil, err
		}
		out := []interface{}{}
		for _, row := range rows {
			value, err := jmespath.Search(stringValue(cfg["expression"]), row)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, value)
		}
		return out, nil, nil
	}
	r.Handlers["transform.rename"] = renameHandler
	r.Handlers["transform.dedupe"] = dedupeHandler
	r.Handlers["transform.flatten"] = flattenHandler
	r.Handlers["transform.parse"] = parseHandler
	r.Handlers["transform.contract"] = contractHandler
}
func records(input interface{}) ([]interface{}, error) {
	rows, ok := input.([]interface{})
	if !ok {
		return nil, schemaError("input must be an array")
	}
	return rows, nil
}

// objectRow returns row i as an object, or a SchemaValidationError naming the
// step, instead of panicking on a scalar or array record.
func objectRow(raw interface{}, step string, i int) (map[string]interface{}, error) {
	row, ok := raw.(map[string]interface{})
	if !ok {
		return nil, schemaError("%s: record %d must be an object", step, i+1)
	}
	return row, nil
}
func cloneMap(value map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for key, item := range value {
		out[key] = item
	}
	return out
}
func renameHandler(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	mapping := map[string]interface{}{}
	switch value := cfg["mapping"].(type) {
	case string:
		if json.Unmarshal([]byte(value), &mapping) != nil {
			return nil, nil, configError("transform.rename: mapping must be a JSON object")
		}
	case map[string]interface{}:
		mapping = value
	default:
		return nil, nil, configError("transform.rename: mapping must be a JSON object")
	}
	out := []interface{}{}
	for i, raw := range rows {
		row, err := objectRow(raw, "transform.rename", i)
		if err != nil {
			return nil, nil, err
		}
		copy := cloneMap(row)
		for from, to := range mapping {
			copy[stringValue(to)] = row[from]
		}
		out = append(out, copy)
	}
	return out, nil, nil
}
func keyFields(value interface{}) []string {
	if list, ok := value.([]interface{}); ok {
		out := []string{}
		for _, v := range list {
			out = append(out, strings.TrimSpace(stringValue(v)))
		}
		return out
	}
	parts := strings.Split(stringValue(value), ",")
	out := []string{}
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func DedupeKeyFields(value interface{}) []string { return keyFields(value) }
func dedupeHandler(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	keys := keyFields(cfg["key"])
	if len(keys) == 0 {
		return rows, nil, nil
	}
	byKey := map[string]interface{}{}
	order := []string{}
	for _, raw := range rows {
		row, _ := raw.(map[string]interface{})
		values := []interface{}{}
		for _, key := range keys {
			values = append(values, row[key])
		}
		body, _ := json.Marshal(values)
		hash := string(body)
		if _, ok := byKey[hash]; !ok {
			order = append(order, hash)
		}
		if cfg["keep"] == "last" || byKey[hash] == nil {
			byKey[hash] = raw
		}
	}
	out := []interface{}{}
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out, nil, nil
}
func flattenValue(value map[string]interface{}, delimiter string, maxDepth int, arrayPolicy, prefix string, depth int, out map[string]interface{}) {
	for key, item := range value {
		name := key
		if prefix != "" {
			name = prefix + delimiter + key
		}
		switch item := item.(type) {
		case map[string]interface{}:
			if depth >= maxDepth {
				out[name] = item
			} else {
				flattenValue(item, delimiter, maxDepth, arrayPolicy, name, depth+1, out)
			}
		case []interface{}:
			if arrayPolicy == "stringify" {
				body, _ := json.Marshal(item)
				out[name] = string(body)
			} else if arrayPolicy == "keep" || depth >= maxDepth {
				out[name] = item
			} else {
				for i, element := range item {
					indexed := name + delimiter + strconv.Itoa(i)
					if child, ok := element.(map[string]interface{}); ok {
						flattenValue(child, delimiter, maxDepth, arrayPolicy, indexed, depth+1, out)
					} else {
						out[indexed] = element
					}
				}
			}
		default:
			out[name] = item
		}
	}
}
func flattenHandler(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	delimiter := stringValue(cfg["delimiter"])
	if delimiter == "" {
		delimiter = "."
	}
	max := int(number(cfg["maxDepth"]))
	if max == 0 {
		max = 10
	}
	policy := stringValue(cfg["arrayPolicy"])
	if policy == "" {
		policy = "index"
	}
	out := []interface{}{}
	for i, raw := range rows {
		row, err := objectRow(raw, "transform.flatten", i)
		if err != nil {
			return nil, nil, err
		}
		result := map[string]interface{}{}
		flattenValue(row, delimiter, max, policy, "", 0, result)
		out = append(out, result)
	}
	return out, nil, nil
}
func parseHandler(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	fields := keyFields(cfg["fields"])
	mode := stringValue(cfg["onError"])
	out := []interface{}{}
	for i, raw := range rows {
		object, err := objectRow(raw, "transform.parse", i)
		if err != nil {
			return nil, nil, err
		}
		row := cloneMap(object)
		for _, field := range fields {
			value, ok := row[field].(string)
			if !ok {
				continue
			}
			var parsed interface{}
			if err := json.Unmarshal([]byte(value), &parsed); err != nil {
				if mode == "fail" {
					return nil, nil, schemaError("transform.parse: field %q is not valid JSON", field)
				}
				if mode == "null" {
					row[field] = nil
				}
			} else {
				row[field] = parsed
			}
		}
		out = append(out, row)
	}
	return out, nil, nil
}
func contractHandler(_ context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	schema := map[string]interface{}{}
	switch value := cfg["schemaJson"].(type) {
	case string:
		err = json.Unmarshal([]byte(value), &schema)
	case map[string]interface{}:
		schema = value
	default:
		err = fmt.Errorf("schemaJson must be object")
	}
	if err != nil {
		return nil, nil, configError("transform.contract: schemaJson must be a JSON object")
	}
	valid := []interface{}{}
	rejected := 0
	for i, raw := range rows {
		row, ok := raw.(map[string]interface{})
		if !ok {
			// A non-object record fails every field check; treat it as a violation.
			row = nil
		}
		problems := []string{}
		if !ok {
			problems = append(problems, "record must be an object")
		}
		for field, specValue := range schema {
			spec := stringValue(specValue)
			optional := strings.HasSuffix(spec, "?")
			kind := strings.TrimSuffix(spec, "?")
			value, exists := row[field]
			if !exists || value == nil {
				if !optional {
					problems = append(problems, field+" is required")
				}
				continue
			}
			if !contractMatches(value, kind) {
				problems = append(problems, field+" must be "+kind)
			}
		}
		if len(problems) > 0 {
			rejected++
			if cfg["onViolation"] == "fail" || cfg["onViolation"] == nil {
				return nil, nil, schemaError("transform.contract: row %d: %s", i+1, strings.Join(problems, "; "))
			}
		} else {
			valid = append(valid, row)
		}
	}
	status := "passed"
	if rejected > 0 {
		status = "warning"
	}
	return valid, map[string]interface{}{"qualityStatus": status, "passedCount": len(valid), "failedCount": rejected}, nil
}
func contractMatches(value interface{}, kind string) bool {
	switch kind {
	case "any":
		return true
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case float64, int, int64, json.Number:
			return true
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "date":
		_, err := time.Parse(time.RFC3339, stringValue(value))
		return err == nil
	}
	return false
}
func number(value interface{}) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		n, _ := value.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(value, 64)
		return n
	}
	return 0
}
func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func DedupeHash(record map[string]interface{}, keys []string) string {
	values := []interface{}{}
	for _, key := range keys {
		values = append(values, record[key])
	}
	body, _ := json.Marshal(values)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func MergeArrays(strategy string, arrays [][]interface{}, joinKey string) ([]interface{}, error) {
	if strategy == "" {
		strategy = "concat"
	}
	if strategy == "concat" {
		out := []interface{}{}
		for _, array := range arrays {
			out = append(out, array...)
		}
		return out, nil
	}
	if strategy == "union" {
		seen := map[string]bool{}
		out := []interface{}{}
		for _, array := range arrays {
			for _, row := range array {
				body, _ := json.Marshal(row)
				key := string(body)
				if !seen[key] {
					seen[key] = true
					out = append(out, row)
				}
			}
		}
		return out, nil
	}
	if strategy == "appendWithSourceTag" {
		out := []interface{}{}
		for i, array := range arrays {
			for j, raw := range array {
				object, err := objectRow(raw, "merge", j)
				if err != nil {
					return nil, err
				}
				row := cloneMap(object)
				row["_source"] = i
				out = append(out, row)
			}
		}
		return out, nil
	}
	if joinKey == "" {
		return nil, configError("merge strategy %q requires a joinKey", strategy)
	}
	a, b := []interface{}{}, []interface{}{}
	if len(arrays) > 0 {
		a = arrays[0]
	}
	if len(arrays) > 1 {
		b = arrays[1]
	}
	index := func(rows []interface{}) (map[string]map[string]interface{}, error) {
		out := map[string]map[string]interface{}{}
		for i, raw := range rows {
			row, err := objectRow(raw, "merge", i)
			if err != nil {
				return nil, err
			}
			out[stringValue(row[joinKey])] = row
		}
		return out, nil
	}
	ai, err := index(a)
	if err != nil {
		return nil, err
	}
	bi, err := index(b)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	if strategy == "outerJoin" {
		for key := range ai {
			keys = append(keys, key)
		}
		for key := range bi {
			if ai[key] == nil {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
	} else {
		for _, raw := range a {
			keys = append(keys, stringValue(raw.(map[string]interface{})[joinKey])) // index(a) checked every row
		}
	}
	out := []interface{}{}
	for _, key := range keys {
		left, right := ai[key], bi[key]
		if strategy == "innerJoin" && right == nil {
			continue
		}
		row := map[string]interface{}{}
		for k, v := range left {
			row[k] = v
		}
		for k, v := range right {
			row[k] = v
		}
		out = append(out, row)
	}
	return out, nil
}
