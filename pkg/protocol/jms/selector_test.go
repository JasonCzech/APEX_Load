package jms

import "testing"

func TestSelectorMatches(t *testing.T) {
	props := map[string]string{
		"region":   "us-east",
		"priority": "7",
		"tier":     "gold",
		"score":    "42.5",
		"flag":     "true",
	}
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"empty matches all", "", true},
		{"string equality", "region = 'us-east'", true},
		{"string inequality", "region <> 'eu-west'", true},
		{"string equality false", "region = 'eu-west'", false},
		{"numeric gt", "priority > 5", true},
		{"numeric gte boundary", "priority >= 7", true},
		{"numeric lt false", "priority < 7", false},
		{"float compare", "score > 42", true},
		{"and true", "region = 'us-east' AND priority > 5", true},
		{"and false", "region = 'us-east' AND priority > 9", false},
		{"or true", "region = 'eu-west' OR tier = 'gold'", true},
		{"not", "NOT tier = 'silver'", true},
		{"parens precedence", "(region = 'eu-west' OR tier = 'gold') AND priority > 5", true},
		{"between", "priority BETWEEN 5 AND 9", true},
		{"between false", "priority BETWEEN 8 AND 9", false},
		{"not between", "priority NOT BETWEEN 8 AND 9", true},
		{"like prefix", "region LIKE 'us-%'", true},
		{"like underscore", "tier LIKE 'gol_'", true},
		{"like no match", "tier LIKE 'sil%'", false},
		{"not like", "tier NOT LIKE 'sil%'", true},
		{"in string", "tier IN ('gold', 'platinum')", true},
		{"in numeric", "priority IN (1, 7, 9)", true},
		{"not in", "tier NOT IN ('silver', 'bronze')", true},
		{"is null absent", "missing IS NULL", true},
		{"is not null present", "region IS NOT NULL", true},
		{"is null present false", "region IS NULL", false},
		{"boolean literal", "flag = TRUE", true},
		// Three-valued logic: comparison on a missing property is UNKNOWN ->
		// top-level UNKNOWN does not select.
		{"unknown not selected", "missing = 'x'", false},
		{"unknown OR true selects", "missing = 'x' OR region = 'us-east'", true},
		{"unknown AND false rejects", "missing = 'x' AND region = 'eu-west'", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel, err := compileSelector(tt.expr)
			if err != nil {
				t.Fatalf("compileSelector(%q): %v", tt.expr, err)
			}
			if got := sel.matches(nil, props); got != tt.want {
				t.Fatalf("selector %q matched=%v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestSelectorResolvesHeadersAndProperties(t *testing.T) {
	headers := map[string]string{"JMSType": "order"}
	props := map[string]string{"vip": "true"}
	sel, err := compileSelector("JMSType = 'order' AND vip = TRUE")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !sel.matches(headers, props) {
		t.Fatal("expected selector to resolve across headers and properties")
	}
}

func TestSelectorParseErrors(t *testing.T) {
	bad := []string{
		"region =",             // missing operand
		"region == 'x'",        // invalid operator
		"region 'x'",           // missing operator
		"(region = 'x'",        // unbalanced paren
		"region LIKE 5",        // LIKE needs string
		"region IN ()",         // empty IN list
		"region @ 'x'",         // bad character
		"'unterminated",        // unterminated string
		"region = 'x' garbage", // trailing tokens
		"IS NULL",              // IS NULL without identifier operand
	}
	for _, expr := range bad {
		t.Run(expr, func(t *testing.T) {
			if _, err := compileSelector(expr); err == nil {
				t.Fatalf("expected compile error for %q", expr)
			}
		})
	}
}

func TestSelectorLikeEscape(t *testing.T) {
	props := map[string]string{"path": "a_b", "pct": "50%done"}
	sel, err := compileSelector(`path LIKE 'a!_b' ESCAPE '!'`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !sel.matches(nil, props) {
		t.Fatal("escaped underscore should match a literal underscore")
	}
	// Without escape, _ is a wildcard and matches any single char.
	sel2, _ := compileSelector(`path LIKE 'a_b'`)
	if !sel2.matches(nil, props) {
		t.Fatal("wildcard underscore should match")
	}
}

func TestSelectorNegativeNumbers(t *testing.T) {
	props := map[string]string{"temp": "-5", "balance": "-100.5", "delta": "3"}
	tests := []struct {
		expr string
		want bool
	}{
		{"temp < -1", true},
		{"temp = -5", true},
		{"temp > -10", true},
		{"balance < -100", true},
		{"temp BETWEEN -10 AND -1", true},
		{"temp BETWEEN -3 AND 0", false},
		{"temp IN (-5, -6)", true},
		{"delta IN (-1, 3)", true},
		{"-5 = temp", true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			sel, err := compileSelector(tt.expr)
			if err != nil {
				t.Fatalf("compile %q: %v", tt.expr, err)
			}
			if got := sel.matches(nil, props); got != tt.want {
				t.Fatalf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestSelectorLikeMatchesNewlines(t *testing.T) {
	props := map[string]string{"v": "foo\nbar"}
	for _, expr := range []string{"v LIKE '%'", "v LIKE 'foo%'", "v LIKE '%bar'", "v LIKE 'foo%bar'"} {
		sel, err := compileSelector(expr)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		if !sel.matches(nil, props) {
			t.Fatalf("%q should match a value with an embedded newline", expr)
		}
	}
	// _ must match a single newline char.
	sel, _ := compileSelector("nl LIKE '_'")
	if !sel.matches(nil, map[string]string{"nl": "\n"}) {
		t.Fatal("'_' should match a single newline")
	}
}
