package enterprisearchive

import (
	"reflect"
	"testing"
)

func TestExtractDatabaseBindsOrdersNamedFallbackDeterministically(t *testing.T) {
	fn := ParsedFunction{
		Args: map[string]string{
			"bindName": "named",
			"param10":  "tenth",
			"param3":   "lowercase-third",
			"Param3":   "uppercase-third",
			"Param2":   "second",
			"param1":   "first",
		},
	}

	got := extractDatabaseBinds(fn, "SELECT * FROM example WHERE a = ?")
	want := []any{"first", "second", "uppercase-third", "lowercase-third", "tenth", "named"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractDatabaseBinds() = %#v, want %#v", got, want)
	}
}

// extractSQLText used to range ParsedFunction.Args directly, so an archive whose
// database call carries two SQL-bearing arguments imported a different statement
// (and therefore a different script) from run to run. The repeat loop is the
// point of the test: a single call cannot distinguish "deterministic" from "got
// lucky", and against the pre-fix implementation this fails within a few
// iterations.
func TestExtractSQLTextIsDeterministicAcrossMultipleSQLArgs(t *testing.T) {
	fn := ParsedFunction{
		Args: map[string]string{
			"SQLStatement": "SELECT a FROM sql_named",
			"QueryText":    "SELECT b FROM query_named",
			"Payload":      "SELECT c FROM unnamed",
		},
		RawArgs: []string{"SELECT d FROM raw_arg"},
	}

	const want = "SELECT b FROM query_named"
	for i := 0; i < 200; i++ {
		if got := extractSQLText(fn); got != want {
			t.Fatalf("extractSQLText() = %q on iteration %d, want %q on every call", got, i, want)
		}
	}
}

// The unnamed-argument tail is ordered too: two args that both parse as SQL and
// neither of which is SQL-named must still resolve to the same statement.
func TestExtractSQLTextIsDeterministicAcrossUnnamedArgs(t *testing.T) {
	fn := ParsedFunction{
		Args: map[string]string{
			"zeta":  "SELECT z FROM zeta",
			"alpha": "SELECT a FROM alpha",
		},
	}

	const want = "SELECT a FROM alpha"
	for i := 0; i < 200; i++ {
		if got := extractSQLText(fn); got != want {
			t.Fatalf("extractSQLText() = %q on iteration %d, want %q on every call", got, i, want)
		}
	}
}

func TestDatabaseBindPosition(t *testing.T) {
	cases := []struct {
		key  string
		want int
		ok   bool
	}{
		{key: "param1", want: 1, ok: true},
		{key: "bind002", want: 2, ok: true},
		{key: "bind", ok: false},
		{key: "param-1", ok: false},
	}
	for _, tc := range cases {
		got, ok := databaseBindPosition(tc.key)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("databaseBindPosition(%q) = (%d, %t), want (%d, %t)", tc.key, got, ok, tc.want, tc.ok)
		}
	}
}
