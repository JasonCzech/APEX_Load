package requestimport

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestPrepareMissingFileBindingsWalksLifecycleControlAndSequenceSurfaces(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		GeneratedFrom: "contract",
		Init: []dsl.Action{{
			Name: "init sequence",
			Type: "http_sequence",
			Params: map[string]any{
				"requests": []map[string]any{{
					"name":   "map child",
					"method": "POST",
					"target": "/init",
					"body":   "{{file.init_asset}}",
				}},
			},
		}},
		Actions: []dsl.Action{{
			Name: "control",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Else: []dsl.Action{{
					Name: "typed sequence",
					Type: "http_sequence",
					Params: map[string]any{
						"requests": []dsl.Action{{
							Name:   "typed child",
							Method: "POST",
							Target: "/main",
							Body:   "{{file.main_asset}}",
						}},
					},
				}},
			},
		}},
		Teardown: []dsl.Action{{
			Name:   "teardown",
			Type:   "request",
			Method: "POST",
			Target: "/finish",
			Body:   "{{file.finish_asset}}",
		}},
	}

	findings, err := prepareMissingFileBindings(script)
	if err != nil {
		t.Fatal(err)
	}
	var sourceNames []string
	for _, source := range script.DataSources {
		sourceNames = append(sourceNames, source.Name)
		if source.InlineData != MissingFilePlaceholder {
			t.Fatalf("source %q inline_data = %q", source.Name, source.InlineData)
		}
	}
	if want := []string{"finish_asset", "init_asset", "main_asset"}; !reflect.DeepEqual(sourceNames, want) {
		t.Fatalf("file sources = %#v, want %#v", sourceNames, want)
	}

	gotPaths := make([]string, 0, len(findings))
	for _, finding := range findings {
		gotPaths = append(gotPaths, finding.ActionPath)
		if finding.Code != "import_missing_file_bindings" {
			t.Fatalf("finding = %#v", finding)
		}
	}
	wantPaths := []string{
		"actions[0].control.else[0]",
		"init[0]",
		"teardown[0]",
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("finding paths = %#v, want %#v", gotPaths, wantPaths)
	}
	for path, metadata := range map[string]map[string]any{
		"init":     script.Init[0].Metadata,
		"nested":   script.Actions[0].Control.Else[0].Metadata,
		"teardown": script.Teardown[0].Metadata,
	} {
		if metadata["execution_blocker"] != "import_missing_file_bindings" {
			t.Fatalf("%s metadata = %#v", path, metadata)
		}
	}
}

func TestPrepareMissingFileBindingsDoesNotDoubleCountSequenceChildren(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		GeneratedFrom: "contract",
		Actions: []dsl.Action{{
			Name: "sequence",
			Type: "http_sequence",
			Params: map[string]any{
				"request_label": "{{file.payload}}",
				"requests": []any{
					map[string]any{"name": "one", "target": "{{file.payload}}"},
					map[string]any{"name": "two", "body": "{{file.payload}}"},
				},
			},
		}},
	}

	findings, err := prepareMissingFileBindings(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ActionPath != "actions[0]" {
		t.Fatalf("findings = %#v", findings)
	}
	missing, _ := script.Actions[0].Metadata["missing_file_bindings"].([]string)
	if !reflect.DeepEqual(missing, []string{"payload"}) {
		t.Fatalf("missing_file_bindings = %#v", script.Actions[0].Metadata["missing_file_bindings"])
	}
}
