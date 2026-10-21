// Package chsql builds ClickHouse SQL from typed parts. Every user-supplied
// value enters a query through Literal/String, every identifier through
// Ident — nothing else is interpolated, which is the whole point.
package chsql

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Expr is a rendered, injection-safe SQL fragment. Construct via the helpers
// in this package only.
type Expr string

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// Ident validates and quotes an identifier (column, alias, JSON path).
func Ident(name string) (Expr, error) {
	if !identPattern.MatchString(name) {
		return "", fmt.Errorf("invalid identifier %q", name)
	}
	return Expr("`" + name + "`"), nil
}

// ClickHouse treats backslash as an escape inside string literals, so it is
// escaped first; doubling quotes alone lets `\'` close the literal.
var stringEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

// String renders a single-quoted, escaped string literal.
func String(v string) Expr {
	return Expr("'" + stringEscaper.Replace(v) + "'")
}

// Literal renders a value typed to the given schema kind, so comparisons
// match the type of the expression they're compared against.
func Literal(kind string, value interface{}) Expr {
	switch kind {
	case "number":
		if f, ok := value.(float64); ok {
			return Expr(strconv.FormatFloat(f, 'f', -1, 64))
		}
		if f, err := strconv.ParseFloat(fmt.Sprint(value), 64); err == nil {
			return Expr(strconv.FormatFloat(f, 'f', -1, 64))
		}
	case "boolean":
		if b, ok := value.(bool); ok {
			return Expr(strconv.FormatBool(b))
		}
		if b, err := strconv.ParseBool(fmt.Sprint(value)); err == nil {
			return Expr(strconv.FormatBool(b))
		}
	case "date":
		return Expr("parseDateTimeBestEffort(" + string(String(fmt.Sprint(value))) + ")")
	}
	return String(fmt.Sprint(value))
}

var comparisonOps = map[string]bool{"=": true, "!=": true, ">": true, "<": true, ">=": true, "<=": true, "LIKE": true}

// Compare renders `left op right` for a whitelisted operator.
func Compare(left Expr, op string, right Expr) (Expr, error) {
	if !comparisonOps[op] {
		return "", fmt.Errorf("invalid operator %q", op)
	}
	return left + " " + Expr(op) + " " + right, nil
}

// In renders `left IN (v1,v2,...)`.
func In(left Expr, values []Expr) Expr {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return left + " IN (" + Expr(strings.Join(parts, ",")) + ")"
}

// Raw marks a trusted, package-internal or constant fragment as an Expr.
// Never pass user input.
func Raw(sql string) Expr { return Expr(sql) }

// JSONField renders typed extraction of a field from a JSON string column.
func JSONField(column Expr, path, kind string) (Expr, error) {
	if !identPattern.MatchString(path) {
		return "", fmt.Errorf("invalid column name %q", path)
	}
	p := String(path)
	switch kind {
	case "number":
		return "JSONExtract(" + column + "," + p + ",'Float64')", nil
	case "boolean":
		return "JSONExtract(" + column + "," + p + ",'Bool')", nil
	case "date":
		return Expr("parseDateTimeBestEffortOrNull(JSONExtractString(" + string(column) + "," + string(p) + "))"), nil
	default:
		return "JSONExtractString(" + column + "," + p + ")", nil
	}
}

// ─── SELECT builder ──────────────────────────────────────────────────────────

type orderBy struct {
	expr Expr
	desc bool
}

type Select struct {
	columns  []Expr
	from     string
	final    bool
	where    []Expr
	groupBy  []Expr
	orderBy  []orderBy
	limit    int
	offset   int
	settings []string
	err      error
}

func NewSelect() *Select { return &Select{limit: -1, offset: -1} }

// Column adds a select expression, optionally aliased.
func (s *Select) Column(expr Expr, alias string) *Select {
	if alias != "" {
		ident, err := Ident(alias)
		if err != nil {
			s.err = err
			return s
		}
		expr = expr + " AS " + ident
	}
	s.columns = append(s.columns, expr)
	return s
}

func (s *Select) From(table string) *Select { s.from = table; return s }
func (s *Select) Final() *Select            { s.final = true; return s }
func (s *Select) Where(conds ...Expr) *Select {
	s.where = append(s.where, conds...)
	return s
}
func (s *Select) GroupBy(exprs ...Expr) *Select {
	s.groupBy = append(s.groupBy, exprs...)
	return s
}
func (s *Select) OrderBy(expr Expr, desc bool) *Select {
	s.orderBy = append(s.orderBy, orderBy{expr, desc})
	return s
}
func (s *Select) Limit(n int) *Select  { s.limit = n; return s }
func (s *Select) Offset(n int) *Select { s.offset = n; return s }

// Settings appends a raw SETTINGS clause fragment (constants only).
func (s *Select) Settings(kv string) *Select { s.settings = append(s.settings, kv); return s }

func (s *Select) Build() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if len(s.columns) == 0 || s.from == "" {
		return "", fmt.Errorf("select needs at least one column and a table")
	}
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, c := range s.columns {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(string(c))
	}
	b.WriteString(" FROM " + s.from)
	if s.final {
		b.WriteString(" FINAL")
	}
	if len(s.where) > 0 {
		parts := make([]string, len(s.where))
		for i, w := range s.where {
			parts[i] = string(w)
		}
		b.WriteString(" WHERE " + strings.Join(parts, " AND "))
	}
	if len(s.groupBy) > 0 {
		parts := make([]string, len(s.groupBy))
		for i, g := range s.groupBy {
			parts[i] = string(g)
		}
		b.WriteString(" GROUP BY " + strings.Join(parts, ","))
	}
	for i, o := range s.orderBy {
		if i == 0 {
			b.WriteString(" ORDER BY ")
		} else {
			b.WriteString(",")
		}
		b.WriteString(string(o.expr))
		if o.desc {
			b.WriteString(" DESC")
		} else {
			b.WriteString(" ASC")
		}
	}
	if s.limit >= 0 {
		b.WriteString(" LIMIT " + strconv.Itoa(s.limit))
	}
	if s.offset >= 0 {
		b.WriteString(" OFFSET " + strconv.Itoa(s.offset))
	}
	if len(s.settings) > 0 {
		b.WriteString(" SETTINGS " + strings.Join(s.settings, ", "))
	}
	return b.String(), nil
}
