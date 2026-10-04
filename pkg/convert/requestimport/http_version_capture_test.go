package requestimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestReviewHTTPVersionCapture(t *testing.T) {
	for _, tc := range []struct {
		name            string
		request         any
		present         bool
		response        any
		provenance      any
		proxy, legacy   bool
		version, reason string
	}{
		{name: "H1", request: "HTTP/1.1", present: true, version: "1.1"},
		{name: "H2 alias", request: " HTTP/2.0 ", present: true, version: "2"},
		{name: "missing", reason: "http_version_unavailable"},
		{name: "unknown", request: " Unknown ", present: true, reason: "http_version_unavailable"},
		{name: "empty", request: "", present: true, reason: "http_version_unavailable"},
		{name: "numeric", request: 2, present: true, reason: "malformed_http_version"},
		{name: "null", present: true, reason: "http_version_unavailable"},
		{name: "object", request: map[string]any{"secret": "synthetic-canary"}, present: true, reason: "malformed_http_version"},
		{name: "oversized", request: strings.Repeat("x", 257), present: true, reason: "malformed_http_version"},
		{name: "H3", request: "HTTP/3", present: true, reason: "unsupported_http_version"},
		{name: "auto is policy not observation", request: "auto", present: true, reason: "unsupported_http_version"},
		{name: "equal proxy", request: "HTTP/2", present: true, response: "HTTP/2.0", proxy: true, provenance: ProxyHTTPVersionProvenance(), version: "2"},
		{name: "different legs", request: "HTTP/1.1", present: true, response: "HTTP/2", proxy: true, provenance: ProxyHTTPVersionProvenance(), reason: "proxy_http_version_legs_differ"},
		{name: "unknown upstream", request: "HTTP/1.1", present: true, response: "unknown", proxy: true, provenance: ProxyHTTPVersionProvenance(), reason: "http_version_unavailable"},
		{name: "malformed provenance", request: "HTTP/1.1", present: true, response: "HTTP/1.1", proxy: true, provenance: map[string]any{"version": 1, "request_leg": "synthetic-canary"}, reason: "malformed_http_version_provenance"},
		{name: "legacy fabricated recorder H1", request: "HTTP/1.1", present: true, response: "HTTP/1.1", legacy: true, reason: "http_version_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReviewHTTPVersionCapture(tc.request, tc.present, tc.response, tc.response != nil, tc.provenance, tc.proxy, tc.legacy)
			if got.Version != tc.version || got.ReasonCode != tc.reason {
				t.Fatalf("decision=%+v", got)
			}
			action := dsl.Action{}
			ApplyHTTPVersionCapture(&action, got, SourceRef{Format: "har", Pointer: "log.entries[0].request.httpVersion"})
			if gated := action.Metadata["execution_blocker"] != nil; gated != (tc.reason != "" && !HTTPVersionCaptureQualityOnly(tc.reason)) {
				t.Fatalf("reason %q gated=%v", tc.reason, gated)
			}
			encoded, _ := json.Marshal(action.Metadata)
			if strings.Contains(string(encoded), "synthetic-canary") {
				t.Fatal("source payload reached public capture evidence")
			}
		})
	}
}

func TestLeafHTTPVersionObservationDoesNotReplaceAliasDefault(t *testing.T) {
	for _, tc := range []struct{ alias, inherited, observed string }{{"http/1.1", "1.1", "2"}, {"http/2", "2", "1.1"}} {
		script := validBaseScript()
		script.Protocol = tc.alias
		script.Actions = []dsl.Action{{Name: "captured leaf", Type: "GET", Target: "/", Metadata: map[string]any{"http_version": tc.observed}}}
		result, err := FinalizeScript(script, nil, FinalizeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if result.Script.Config.HTTP.Version != "" || dsl.InheritedHTTPVersion(result.Script) != tc.inherited || result.Script.Actions[0].HTTPVersion.Value() != tc.observed || result.Script.Actions[0].Metadata["execution_blocker"] != nil {
			t.Fatal("source observation changed registered alias defaults instead of only its captured leaf")
		}
	}
}

func TestLeafHTTPVersionLegacyExplicitAutoPrecedesAliasAndDeclaration(t *testing.T) {
	for _, alias := range []string{"http/1.1", "http/2"} {
		for _, sourceWide := range []bool{false, true} {
			script := validBaseScript()
			script.Protocol = alias
			script.Config.HTTP.Version = " \t "
			script.Actions = []dsl.Action{{Name: "leaf", Type: "GET", Target: "/"}}
			if !sourceWide {
				script.Actions[0].Metadata = map[string]any{"http_version": "HTTP/1.1"}
			}
			for pass := 0; pass < 3; pass++ {
				var err error
				if sourceWide {
					_, _, err = ApplyRuntimeFidelityReview(script, RuntimeFidelityOptions{DeclaredHTTPVersion: "HTTP/1.1", DeclaredHTTPVersionSource: SourceRef{Format: "loadrunner", Path: "default.cfg", Pointer: "HTTPVersion"}})
					if err != nil {
						t.Fatal(err)
					}
				}
				result, err := FinalizeScript(script, nil, FinalizeOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if result.Script.Config.HTTP.Version == "" || dsl.InheritedHTTPVersion(result.Script) != "" {
					t.Fatal("explicit legacy Auto changed into alias or promoted source mode")
				}
				leaf := result.Script.Actions[0]
				if sourceWide && (leaf.HTTPVersion.IsPresent() || leaf.Metadata["execution_blocker"] != importReviewRequiredBlocker) {
					t.Fatal("source-wide conflict was silently made exact")
				}
				if !sourceWide && (leaf.HTTPVersion.Value() != "1.1" || leaf.Metadata["execution_blocker"] != nil) {
					t.Fatal("supported source leaf was not retained independently")
				}
				encoded, _ := json.Marshal(result.Script)
				script = &dsl.Script{}
				if err := json.Unmarshal(encoded, script); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestLeafHTTPVersionCaptureAndDefaultGatesSurviveReopen(t *testing.T) {
	script := validBaseScript()
	script.Config.HTTP.DefaultHeaders = map[string]string{"Connection": "keep-alive", "X-Default": "shared"}
	for _, version := range []string{"1.1", "2"} {
		override, _ := dsl.NewHTTPVersionOverride(version)
		script.Actions = append(script.Actions, dsl.Action{Name: "leaf", Type: "GET", Target: "/", HTTPVersion: override})
	}
	for pass := 0; pass < 3; pass++ {
		finalized, err := FinalizeScript(script, nil, FinalizeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if finalized.Script.Config.HTTP.DefaultHeaders["Connection"] != "keep-alive" {
			t.Fatal("valid H1 sibling default was removed")
		}
		if finalized.Script.Actions[0].Metadata["execution_blocker"] != nil {
			t.Fatal("valid H1 sibling was gated")
		}
		if finalized.Script.Actions[1].Metadata["execution_blocker"] != importReviewRequiredBlocker {
			t.Fatal("H2 inherited default incompatibility was lost")
		}
		marker := finalized.Script.Actions[1].Metadata[ImportHeaderFidelityMetadataKey].(map[string]any)
		if len(metadataObjectValues(marker[headerFidelityOccurrenceFieldName])) != 1 {
			t.Fatal("repeat finalization duplicated inherited evidence")
		}
		for _, mapping := range finalized.SourceMappings {
			if strings.Contains(mapping.Target, ".omitted_occurrences[") && !strings.HasSuffix(mapping.Target, ".omitted_occurrences[0]") {
				t.Fatalf("repeat review mapping points beyond retained evidence: %s", mapping.Target)
			}
		}
		encoded, err := json.Marshal(finalized.Script)
		if err != nil {
			t.Fatal(err)
		}
		script = &dsl.Script{}
		if err = json.Unmarshal(encoded, script); err != nil {
			t.Fatal(err)
		}
		findings, err := importreview.FindingsFromScript(script)
		if err != nil || len(findings) == 0 {
			t.Fatal("reopen lost gate")
		}
	}
}

func TestLeafHTTPVersionNestedPrecedenceAndDurableGate(t *testing.T) {
	script := validBaseScript()
	script.Protocol = "http/1.1"
	script.Config.HTTP.UserAgent = "discarded-typed-value"
	script.Config.HTTP.DefaultHeaders = map[string]string{"user-agent": "authored-winner", "Connection": "keep-alive"}
	script.Actions = []dsl.Action{{Type: "control_loop", Name: "nested", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{{Type: "GET", Name: "inherited", Target: "/"}}}}}
	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	leaf := result.Script.Actions[0].Control.Steps[0]
	if leaf.HTTPVersion.IsPresent() || leaf.Metadata["execution_blocker"] != nil || result.Script.Config.HTTP.UserAgent != "" || result.Script.Config.HTTP.DefaultHeaders["user-agent"] != "authored-winner" {
		t.Fatalf("protocol alias inheritance or authored header precedence was changed: leaf=%+v config=%+v", leaf, result.Script.Config.HTTP)
	}
	// A missing observation is a capture-quality gap and never gates.
	missing := leaf
	ApplyHTTPVersionCapture(&missing, ReviewHTTPVersionCapture(nil, false, nil, false, nil, false, false), SourceRef{Format: "har", Pointer: "log.entries[0].request.httpVersion"})
	if missing.Metadata["execution_blocker"] != nil {
		t.Fatal("missing HTTP version observation gated execution")
	}
	// A malformed observation keeps its durable gate after a policy is added.
	ApplyHTTPVersionCapture(&leaf, ReviewHTTPVersionCapture(42, true, nil, false, nil, false, false), SourceRef{Format: "har", Pointer: "log.entries[0].request.httpVersion"})
	leaf.HTTPVersion, _ = dsl.NewHTTPVersionOverride("1.1")
	result.Script.Actions[0].Control.Steps[0] = leaf
	reopened, err := FinalizeScript(result.Script, nil, FinalizeOptions{})
	if err != nil || reopened.Script.Actions[0].Control.Steps[0].Metadata["execution_blocker"] != importReviewRequiredBlocker {
		t.Fatal("adding a replay policy erased unresolved source capture gate")
	}
}

func TestLeafHTTPVersionDefaultOrderedShadow(t *testing.T) {
	script := validBaseScript()
	script.Config.HTTP.DefaultHeaders = map[string]string{"Connection": "keep-alive"}
	h2, _ := dsl.NewHTTPVersionOverride("2")
	disabled := false
	script.Actions = []dsl.Action{{Type: "GET", Name: "shadow", Target: "/", HTTPVersion: h2, HeaderList: dsl.HeaderList{{Name: "Connection", Value: "inactive", Enabled: &disabled}}}}
	for pass := 0; pass < 3; pass++ {
		result, err := FinalizeScript(script, nil, FinalizeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		leaf := result.Script.Actions[0]
		if leaf.Metadata["execution_blocker"] != nil || result.Script.Config.HTTP.DefaultHeaders["Connection"] != "keep-alive" || len(leaf.HeaderList) != 1 || leaf.HeaderList[0].Enabled == nil || *leaf.HeaderList[0].Enabled {
			t.Fatal("disabled authoritative leaf header failed to shadow inherited default")
		}
		encoded, _ := json.Marshal(result.Script)
		script = &dsl.Script{}
		if err := json.Unmarshal(encoded, script); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLeafHTTPVersionInvalidMapShapeBecomesDurableDraft(t *testing.T) {
	for _, raw := range []any{nil, "", "HTTP/2", 2, true, map[string]any{"secret": "invalid-policy-canary"}} {
		script := validBaseScript()
		script.Actions = []dsl.Action{{Name: "sequence", Type: "http_sequence", Method: "SEQUENCE", Target: "/", Params: map[string]any{"requests": []any{map[string]any{"name": "leaf", "method": "GET", "type": "GET", "target": "/", "http_version": raw}}}}}
		result, err := FinalizeScript(script, nil, FinalizeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		child := result.Script.Actions[0].Params["requests"].([]any)[0].(map[string]any)
		if _, present := child["http_version"]; present {
			t.Fatal("invalid active scalar survived admission")
		}
		metadata := child["metadata"].(map[string]any)
		if metadata["execution_blocker"] != importReviewRequiredBlocker || metadata[ImportHTTPVersionFidelityMetadataKey].(map[string]any)["reason_code"] != "malformed_http_version" {
			t.Fatal("invalid shape lost durable gate")
		}
		encoded, _ := json.Marshal(result.Script)
		if strings.Contains(string(encoded), "invalid-policy-canary") {
			t.Fatal("malformed source value reached public draft")
		}
	}
}

func TestFinalizeActionExplicitAutoLeaf(t *testing.T) {
	action, _, err := FinalizeAction(EffectiveRequest{Name: "auto", Method: "GET", Target: "/", HTTPVersion: "auto"}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !action.HTTPVersion.IsPresent() || action.HTTPVersion.Value() != "auto" {
		t.Fatal("explicit auto became inheritance")
	}
}
