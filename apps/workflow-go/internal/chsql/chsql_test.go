package chsql

import "testing"

func TestLiteralByKind(t *testing.T) {
	cases := []struct {
		kind  string
		value interface{}
		want  string
	}{
		{"number", 42.5, "42.5"},
		{"number", "42", "42"},
		{"number", "12x", "'12x'"}, // invalid number falls back to string
		{"boolean", true, "true"},
		{"boolean", "false", "false"},
		{"date", "2026-07-05T00:00:00Z", "parseDateTimeBestEffort('2026-07-05T00:00:00Z')"},
		{"string", "O'Reilly", `'O\'Reilly'`},
	}
	for _, tc := range cases {
		if got := Literal(tc.kind, tc.value); string(got) != tc.want {
			t.Errorf("Literal(%q, %#v) = %q, want %q", tc.kind, tc.value, got, tc.want)
		}
	}
}

func TestIdentRejectsInjection(t *testing.T) {
	if _, err := Ident("user_id"); err != nil {
		t.Fatalf("valid ident rejected: %v", err)
	}
	for _, bad := range []string{"a b", "x;DROP TABLE t", "`tick`", "1abc", ""} {
		if _, err := Ident(bad); err == nil {
			t.Errorf("Ident(%q) accepted, want error", bad)
		}
	}
}

func TestCompareWhitelistsOps(t *testing.T) {
	if _, err := Compare("a", ">", "1"); err != nil {
		t.Fatalf("valid op rejected: %v", err)
	}
	if _, err := Compare("a", "; DROP", "1"); err == nil {
		t.Fatal("bad op accepted")
	}
}

func TestJSONField(t *testing.T) {
	got, err := JSONField("record", "amount", "number")
	if err != nil || string(got) != "JSONExtract(record,'amount','Float64')" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if _, err := JSONField("record", "a'; DROP", "string"); err == nil {
		t.Fatal("bad path accepted")
	}
}

func TestSelectBuild(t *testing.T) {
	q, err := NewSelect().
		Column(Raw("count()"), "row_count").
		Column("collection", "").
		From("sink_records").Final().
		Where("tenant_id = "+String("t1"), "collection = "+String("c1")).
		GroupBy("collection").
		OrderBy("collection", false).
		Limit(10).Offset(20).
		Settings("max_execution_time=10").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT count() AS `row_count`,collection FROM sink_records FINAL " +
		"WHERE tenant_id = 't1' AND collection = 'c1' GROUP BY collection " +
		"ORDER BY collection ASC LIMIT 10 OFFSET 20 SETTINGS max_execution_time=10"
	if q != want {
		t.Fatalf("got:\n%s\nwant:\n%s", q, want)
	}
}

func TestSelectRequiresColumnAndTable(t *testing.T) {
	if _, err := NewSelect().From("t").Build(); err == nil {
		t.Fatal("no columns accepted")
	}
	if _, err := NewSelect().Column(Raw("1"), "").Build(); err == nil {
		t.Fatal("no table accepted")
	}
}

func TestSelectBadAliasPropagates(t *testing.T) {
	if _, err := NewSelect().Column(Raw("1"), "bad alias").From("t").Build(); err == nil {
		t.Fatal("bad alias accepted")
	}
}

func TestStringEscapesBackslashBeforeQuote(t *testing.T) {
	cases := map[string]string{
		`x\' OR 1=1 --`: `'x\\\' OR 1=1 --'`,
		`a\b`:           `'a\\b'`,
		`\`:             `'\\'`,
		`''`:            `'\'\''`,
	}
	for in, want := range cases {
		if got := string(String(in)); got != want {
			t.Errorf("String(%q) = %s, want %s", in, got, want)
		}
	}
}
