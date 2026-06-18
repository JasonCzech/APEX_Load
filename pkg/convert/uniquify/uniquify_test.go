package uniquify

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// topLevelNames flattens the names the dashboard validates (Init+Actions+Teardown).
func topLevelNames(script *dsl.Script) []string {
	var names []string
	for _, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for _, a := range phase {
			names = append(names, a.Name)
		}
	}
	return names
}

// assertFoldedUnique fails if any two names collide after lowercasing — the exact
// invariant the dashboard enforces via normalizeTokenName.
func assertFoldedUnique(t *testing.T, names []string) {
	t.Helper()
	seen := map[string]string{}
	for _, name := range names {
		key := strings.ToLower(name)
		if prev, ok := seen[key]; ok {
			t.Fatalf("names collide under case-fold: %q and %q both fold to %q (all: %v)", prev, name, key, names)
		}
		seen[key] = name
	}
}

func TestDeduplicateActionNamesTopLevel(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "identical names",
			input: []string{"c_AuraToken_URL", "c_AuraToken_URL"},
			want:  []string{"c_AuraToken_URL", "c_AuraToken_URL_2"},
		},
		{
			name:  "case variants collide under fold",
			input: []string{"c_AuraToken_URL", "c_AuraToken_url", "C_AURATOKEN_URL"},
			want:  []string{"c_AuraToken_URL", "c_AuraToken_url_2", "C_AURATOKEN_URL_3"},
		},
		{
			name:  "suffix cascade: generated _2 must dodge a literal _2",
			input: []string{"c_AuraToken_URL", "c_AuraToken_URL", "c_AuraToken_URL_2"},
			want:  []string{"c_AuraToken_URL", "c_AuraToken_URL_2", "c_AuraToken_URL_2_2"},
		},
		{
			name:  "literal _2 seen first pushes the generated name to _3",
			input: []string{"login.jsp_2", "login.jsp", "login.jsp"},
			want:  []string{"login.jsp_2", "login.jsp", "login.jsp_3"},
		},
		{
			name:  "already unique is untouched",
			input: []string{"alpha", "beta", "gamma"},
			want:  []string{"alpha", "beta", "gamma"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := &dsl.Script{}
			for _, n := range tt.input {
				script.Actions = append(script.Actions, dsl.Action{Name: n, Type: "GET", Target: "https://example.test/"})
			}
			DeduplicateActionNames(script)
			got := topLevelNames(script)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("index %d: got %q, want %q (all got: %v)", i, got[i], tt.want[i], got)
				}
			}
			assertFoldedUnique(t, got)
		})
	}
}

func TestDeduplicateActionNamesSharedAcrossPhases(t *testing.T) {
	script := &dsl.Script{
		Init:     []dsl.Action{{Name: "c_AuraToken_URL"}},
		Actions:  []dsl.Action{{Name: "c_AuraToken_URL"}},
		Teardown: []dsl.Action{{Name: "C_AURATOKEN_URL"}},
	}
	DeduplicateActionNames(script)
	assertFoldedUnique(t, topLevelNames(script))
	if script.Init[0].Name != "c_AuraToken_URL" {
		t.Fatalf("init name = %q, want first occurrence kept", script.Init[0].Name)
	}
}

func TestDeduplicateActionNamesNestedControlScope(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{
			{
				Name: "source_loop_1",
				Type: "control_loop",
				Control: &dsl.ControlFlow{
					Steps: []dsl.Action{
						{Name: "poll"},
						{Name: "poll"},
					},
					Else: []dsl.Action{
						{Name: "poll"},
					},
				},
			},
			// A top-level "poll" must NOT collide with the nested ones (separate scopes).
			{Name: "poll"},
		},
	}
	renames := DeduplicateActionNames(script)
	steps := script.Actions[0].Control.Steps
	if steps[0].Name != "poll" || steps[1].Name != "poll_2" {
		t.Fatalf("nested step names = %q,%q, want poll,poll_2", steps[0].Name, steps[1].Name)
	}
	// Else shares the block scope with Steps, so it is the third "poll".
	if script.Actions[0].Control.Else[0].Name != "poll_3" {
		t.Fatalf("else name = %q, want poll_3", script.Actions[0].Control.Else[0].Name)
	}
	// Top-level "poll" is in its own (top-level) scope and stays unique there.
	if script.Actions[1].Name != "poll" {
		t.Fatalf("top-level poll = %q, want poll", script.Actions[1].Name)
	}
	if len(renames) == 0 {
		t.Fatal("expected renames to be reported")
	}
}

func TestDeduplicateActionNamesHTTPSequenceChildren(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{
			{
				Name: "Login",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{
						{"name": "c_AuraToken_URL", "type": "GET"},
						{"name": "c_AuraToken_URL", "type": "GET"},
					},
				},
			},
		},
	}
	DeduplicateActionNames(script)
	requests := script.Actions[0].Params["requests"].([]map[string]any)
	if requests[0]["name"] != "c_AuraToken_URL" || requests[1]["name"] != "c_AuraToken_URL_2" {
		t.Fatalf("child names = %v,%v, want c_AuraToken_URL,c_AuraToken_URL_2", requests[0]["name"], requests[1]["name"])
	}
}

func TestDeduplicateActionNamesIdempotent(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{
			{Name: "c_AuraToken_URL"},
			{Name: "c_AuraToken_url"},
		},
	}
	DeduplicateActionNames(script)
	first := topLevelNames(script)
	if renames := DeduplicateActionNames(script); len(renames) != 0 {
		t.Fatalf("second run renamed %v, want none (idempotent)", renames)
	}
	second := topLevelNames(script)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("idempotency broken: %v then %v", first, second)
		}
	}
}

func TestDeduplicateActionNamesNilAndEmpty(t *testing.T) {
	if renames := DeduplicateActionNames(nil); renames != nil {
		t.Fatalf("nil script returned %v, want nil", renames)
	}
	// Empty names are left for the validator; they must not be renamed or panic.
	script := &dsl.Script{Actions: []dsl.Action{{Name: ""}, {Name: ""}}}
	if renames := DeduplicateActionNames(script); len(renames) != 0 {
		t.Fatalf("empty names renamed %v, want none", renames)
	}
}
