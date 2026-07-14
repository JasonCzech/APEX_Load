package engine

import "testing"

// TestConditionExprUsesNamespaceDetectsBareReferences locks in that the
// activation-pruning heuristic detects a namespace referenced as a bare
// identifier (e.g. `'promo' in vars`, `size(data) > 0`), not only `vars.` /
// `vars[`. Missing a binding makes cel-go error at eval time, which
// evalConditionExpr swallows to false — silently breaking valid conditions.
func TestConditionExprUsesNamespaceDetectsBareReferences(t *testing.T) {
	cases := []struct {
		expr      string
		namespace string
		want      bool
	}{
		{`'promo' in vars`, "vars", true},
		{`vars ["key"] == "x"`, "vars", true},
		{`size(data) > 0`, "data", true},
		{`'REGION' in env`, "env", true},
		{`vars.coupon == "X"`, "vars", true},
		{`data["rows"].size() > 0`, "data", true},
		{`env.REGION == "us"`, "env", true},
		// Must NOT match a namespace only mentioned as a substring of another
		// identifier.
		{`metadata.x > 0`, "data", false},
		{`myvars == 1`, "vars", false},
		{`environment == "x"`, "env", false},
	}
	for _, tc := range cases {
		if got := conditionExprUsesNamespace(tc.expr, tc.namespace); got != tc.want {
			t.Errorf("conditionExprUsesNamespace(%q, %q) = %v, want %v", tc.expr, tc.namespace, got, tc.want)
		}
	}
}
