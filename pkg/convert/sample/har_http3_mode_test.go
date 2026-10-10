package sample

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// The goldens summarize, per http_version_mode, what each HTTP/3 fixture
// imports as: leaf policies, the retained observation, gates, and the HTTP
// version findings. Regenerate with
// APEXLOAD_UPDATE_HTTP3_HAR_GOLDENS=1 go test ./pkg/convert/sample -run TestHARHTTPVersionModeGoldens
var http3HARFixtures = []string{
	"playwright-chromium-real-scrubbed.har", // a scrubbed real Chromium capture
	"chrome-devtools.har",
	"firefox.har",
	"draft-token.har",
	"loadrunner-unknown.har",
}

type harModeAction struct {
	Name              string         `json:"name"`
	Target            string         `json:"target"`
	HTTPVersion       string         `json:"http_version,omitempty"`
	Captured          map[string]any `json:"captured,omitempty"`
	ExecutionBlockers []string       `json:"execution_blockers,omitempty"`
	ReviewReasons     []string       `json:"review_reasons,omitempty"`
}

type harModeGolden struct {
	ConfigHTTPVersion string          `json:"config_http_version,omitempty"`
	Actions           []harModeAction `json:"actions"`
	Findings          []string        `json:"findings,omitempty"`
}

var http3FindingCodes = map[string]bool{
	requestimport.CodeCapturedH3: true, requestimport.CodeH3NotPreserved: true,
	requestimport.ReasonHTTP3DraftToken: true, OriginAdvertisesH3Code: true,
}

func summarizeHTTP3Import(t *testing.T, content []byte, mode string) harModeGolden {
	t.Helper()
	asyncScan, includeStatic := false, true
	result, err := Convert(ImportRequest{Format: "har", Content: string(content), Options: ConvertOptions{
		HTTPVersionMode: mode, AsyncScan: &asyncScan, IncludeStaticAssets: &includeStatic,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("%s: invalid draft: %v", mode, issues)
	}
	summary := harModeGolden{ConfigHTTPVersion: result.Script.Config.HTTP.Version, Actions: []harModeAction{}}
	for _, action := range result.Script.Actions {
		entry := harModeAction{Name: action.Name, Target: action.Target, HTTPVersion: action.HTTPVersion.Value()}
		if captured, ok := action.Metadata["http_version_capture"].(map[string]any); ok {
			entry.Captured = captured
		}
		entry.ExecutionBlockers = metadataStrings(action.Metadata, "execution_blockers", "execution_blocker")
		entry.ReviewReasons = metadataStrings(action.Metadata, "review_reasons", "review_gate")
		summary.Actions = append(summary.Actions, entry)
	}
	for _, d := range result.Unsupported {
		if http3FindingCodes[d.Code] {
			summary.Findings = append(summary.Findings, fmt.Sprintf("%s %s %s", d.Severity, d.Code, d.Location))
		}
	}
	sort.Strings(summary.Findings)
	return summary
}

func metadataStrings(metadata map[string]any, listKey, singleKey string) []string {
	var out []string
	switch values := metadata[listKey].(type) {
	case []string:
		out = append(out, values...)
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
	}
	if single, ok := metadata[singleKey].(string); ok && single != "" {
		out = append(out, single)
	}
	sort.Strings(out)
	return out
}

func TestHARHTTPVersionModeGoldens(t *testing.T) {
	modes := []string{requestimport.HTTPVersionModeUpgrade, requestimport.HTTPVersionModeExact, requestimport.HTTPVersionModeInherit}
	for _, fixture := range http3HARFixtures {
		t.Run(fixture, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("testdata", "http3", fixture))
			if err != nil {
				t.Fatal(err)
			}
			golden := map[string]harModeGolden{}
			for _, mode := range modes {
				golden[mode] = summarizeHTTP3Import(t, content, mode)
			}
			encoded, err := json.MarshalIndent(golden, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, '\n')
			path := filepath.Join("testdata", "http3", strings.TrimSuffix(fixture, ".har")+".golden.json")
			if os.Getenv("APEXLOAD_UPDATE_HTTP3_HAR_GOLDENS") == "1" {
				if err := os.WriteFile(path, encoded, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (regenerate with APEXLOAD_UPDATE_HTTP3_HAR_GOLDENS=1)", path, err)
			}
			if !bytes.Equal(want, encoded) {
				t.Fatalf("%s is stale; regenerate with APEXLOAD_UPDATE_HTTP3_HAR_GOLDENS=1 and review the diff\n%s", path, encoded)
			}
		})
	}
}

// The rules the goldens record, asserted directly so a regenerated golden
// cannot silently change them.
func TestHARHTTPVersionModeRules(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "http3", "chrome-devtools.har"))
	if err != nil {
		t.Fatal(err)
	}
	byTarget := func(summary harModeGolden) map[string]harModeAction {
		out := map[string]harModeAction{}
		for _, action := range summary.Actions {
			out[action.Target] = action
		}
		return out
	}
	for _, tc := range []struct {
		mode, h3, h2, h1 string
		finding          string
	}{
		{requestimport.HTTPVersionModeUpgrade, "auto+h3", "2", "1.1", "info " + requestimport.CodeCapturedH3},
		{requestimport.HTTPVersionModeExact, "3", "2", "1.1", "info " + requestimport.CodeCapturedH3},
		{requestimport.HTTPVersionModeInherit, "", "", "", "warning " + requestimport.CodeH3NotPreserved},
	} {
		summary := summarizeHTTP3Import(t, content, tc.mode)
		actions := byTarget(summary)
		// app.example.test is the derived base URL, so its targets are relative.
		h3, h2, h1 := actions["/"], actions["https://api.example.test/v1/items"], actions["https://legacy.example.test/status"]
		if h3.HTTPVersion != tc.h3 || h2.HTTPVersion != tc.h2 || h1.HTTPVersion != tc.h1 {
			t.Fatalf("%s: h3=%q h2=%q h1=%q", tc.mode, h3.HTTPVersion, h2.HTTPVersion, h1.HTTPVersion)
		}
		// The observation is kept apart from the policy in every mode.
		if h3.Captured["request_version"] != "3" || h3.Captured["mode"] != tc.mode {
			t.Fatalf("%s: observation %v", tc.mode, h3.Captured)
		}
		// The unrelated malformed observation stays gated in every mode.
		if broken := actions["https://api.example.test/v1/broken"]; len(broken.ExecutionBlockers) == 0 {
			t.Fatalf("%s: the malformed sibling lost its gate: %+v", tc.mode, broken)
		}
		if len(h3.ExecutionBlockers) != 0 || len(h3.ReviewReasons) != 0 {
			t.Fatalf("%s: a captured h3 request was gated: %+v", tc.mode, h3)
		}
		found := false
		for _, finding := range summary.Findings {
			found = found || strings.HasPrefix(finding, tc.finding+" https://app.example.test")
		}
		if !found {
			t.Fatalf("%s: findings %v, want %q for the app origin", tc.mode, summary.Findings, tc.finding)
		}
	}
	if _, err := Convert(ImportRequest{Format: "har", Content: string(content), Options: ConvertOptions{HTTPVersionMode: "assume"}}); err == nil {
		t.Fatal("an unknown http_version_mode was accepted")
	}
}

func TestHARBrowserProvenanceIsDescriptive(t *testing.T) {
	entry := func(version string, provenance any) map[string]any {
		item := map[string]any{
			"request":  map[string]any{"method": "GET", "url": "https://app.example.test/", "httpVersion": version},
			"response": map[string]any{"status": 200, "httpVersion": version},
		}
		if provenance != nil {
			item[requestimport.HARHTTPVersionProvenanceKey] = provenance
		}
		return item
	}
	for _, tc := range []struct {
		name       string
		item       map[string]any
		wantPolicy string
		gated      bool
	}{
		{"browser h3", entry("h3", requestimport.BrowserHTTPVersionProvenance("browser", "h3")), "auto+h3", false},
		{"browser run h2", entry("HTTP/2.0", requestimport.BrowserHTTPVersionProvenance("browser_run", "h2")), "2", false},
		// Provenance cannot vouch for a version the entry does not record.
		{"contradiction", entry("HTTP/2.0", requestimport.BrowserHTTPVersionProvenance("browser", "h3")), "", true},
	} {
		data, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{tc.item}}})
		result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
		if err != nil {
			t.Fatal(err)
		}
		action := result.Script.Actions[0]
		gated := action.Metadata["execution_blocker"] != nil || action.Metadata["execution_blockers"] != nil
		captured, _ := action.Metadata["http_version_capture"].(map[string]any)
		if action.HTTPVersion.Value() != tc.wantPolicy || gated != tc.gated || (!tc.gated && (captured["provenance"] != "browser" || captured["proxy"] != false)) {
			t.Fatalf("%s: policy=%q gated=%v capture=%v", tc.name, action.HTTPVersion.Value(), gated, captured)
		}
		for _, d := range result.Unsupported {
			if d.Severity == diag.Blocker && d.Element == "HTTP version" && !tc.gated {
				t.Fatalf("%s: blocker %+v", tc.name, d)
			}
		}
	}
}

func TestHARExactModeKeepsCleartextHTTP3Importable(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{
		"request":  map[string]any{"method": "GET", "url": "http://plain.example.test/", "httpVersion": "h3"},
		"response": map[string]any{"status": 200, "httpVersion": "h3"},
	}}}})
	result, err := Convert(ImportRequest{Format: "har", Content: string(data), Options: ConvertOptions{HTTPVersionMode: requestimport.HTTPVersionModeExact}})
	if err != nil {
		t.Fatalf("a cleartext h3 entry failed the import: %v", err)
	}
	if action := result.Script.Actions[0]; action.HTTPVersion.IsPresent() || result.Script.Config.HTTP.Version != "" {
		t.Fatalf("exact 3 kept for a cleartext URL: leaf=%v config=%q", action.HTTPVersion, result.Script.Config.HTTP.Version)
	}
	for _, d := range result.Unsupported {
		if d.Code == requestimport.CodeCapturedH3 {
			t.Fatalf("the unmapped entry was reported as mapped: %+v", d)
		}
	}
}
