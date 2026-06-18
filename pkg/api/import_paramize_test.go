package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestBuildImportParameterizationUniquifiesActionNames verifies the shared
// import chokepoint guarantees case-insensitively-unique action names for every
// importer (not just enterprise-archive), so the dashboard never flags false
// "Action name ... is duplicated" warnings.
func TestBuildImportParameterizationUniquifiesActionNames(t *testing.T) {
	script := &dsl.Script{
		Name:     "Case Variant Import",
		Protocol: "http",
		Actions: []dsl.Action{
			{Name: "c_AuraToken_URL", Type: "http_request", Method: "GET", Target: "/a"},
			{Name: "c_AuraToken_url", Type: "http_request", Method: "GET", Target: "/b"},
			{Name: "C_AURATOKEN_URL", Type: "http_request", Method: "GET", Target: "/c"},
		},
	}

	buildImportParameterization(script, nil)

	assertFoldedUniqueActionNames(t, script)
	if script.Actions[0].Name != "c_AuraToken_URL" {
		t.Fatalf("first action = %q, want c_AuraToken_URL kept", script.Actions[0].Name)
	}
}

func TestBuildMultiScriptParameterizationUniquifiesEachScript(t *testing.T) {
	scripts := []*dsl.Script{
		{
			Name:     "Thread Group 1",
			Protocol: "http",
			Actions: []dsl.Action{
				{Name: "Request", Type: "http_request", Method: "GET", Target: "/a"},
				{Name: "request", Type: "http_request", Method: "GET", Target: "/b"},
			},
		},
		{
			Name:     "Thread Group 2",
			Protocol: "http",
			Actions: []dsl.Action{
				{Name: "Step", Type: "http_request", Method: "GET", Target: "/c"},
				{Name: "STEP", Type: "http_request", Method: "GET", Target: "/d"},
			},
		},
	}

	buildMultiScriptParameterization(scripts)

	for _, script := range scripts {
		assertFoldedUniqueActionNames(t, script)
	}
}

func assertFoldedUniqueActionNames(t *testing.T, script *dsl.Script) {
	t.Helper()
	seen := map[string]string{}
	for _, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for _, action := range phase {
			if strings.TrimSpace(action.Name) == "" {
				continue
			}
			key := strings.ToLower(action.Name)
			if prev, ok := seen[key]; ok {
				t.Fatalf("action names collide under case-fold: %q and %q both fold to %q", prev, action.Name, key)
			}
			seen[key] = action.Name
		}
	}
}
