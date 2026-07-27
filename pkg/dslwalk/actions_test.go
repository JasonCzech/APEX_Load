package dslwalk

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestWalkActionsVisitsEveryPhaseAndNestedBranchInPreOrder(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Init: []dsl.Action{{Name: "init"}},
		Actions: []dsl.Action{
			{
				Name: "parent",
				Control: &dsl.ControlFlow{
					Steps: []dsl.Action{
						{Name: "step-one"},
						{
							Name: "nested",
							Control: &dsl.ControlFlow{
								Else: []dsl.Action{{Name: "deep-else"}},
							},
						},
					},
					Else: []dsl.Action{{Name: "else-one"}},
				},
			},
			{Name: "sibling"},
		},
		Teardown: []dsl.Action{{Name: "teardown"}},
	}

	var got []string
	err := WalkActions(script, func(ref ActionRef) error {
		got = append(got, fmt.Sprintf("%s=%s@%d", ref.Path, ref.Action.Name, ref.Depth))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"init[0]=init@1",
		"actions[0]=parent@1",
		"actions[0].control.steps[0]=step-one@2",
		"actions[0].control.steps[1]=nested@2",
		"actions[0].control.steps[1].control.else[0]=deep-else@3",
		"actions[0].control.else[0]=else-one@2",
		"actions[1]=sibling@1",
		"teardown[0]=teardown@1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk order:\n got %#v\nwant %#v", got, want)
	}
}

func TestWalkActionsSupportsFieldMutation(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Actions: []dsl.Action{{
			Name: "parent",
			Control: &dsl.ControlFlow{
				Steps: []dsl.Action{{Name: "child"}},
			},
		}},
	}
	if err := WalkActions(script, func(ref ActionRef) error {
		ref.Action.Name = ref.Path
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if script.Actions[0].Name != "actions[0]" ||
		script.Actions[0].Control.Steps[0].Name != "actions[0].control.steps[0]" {
		t.Fatalf("field mutations were not retained: %#v", script.Actions)
	}
}

func TestWalkActionsPropagatesVisitorError(t *testing.T) {
	t.Parallel()
	stop := errors.New("stop")
	script := &dsl.Script{Actions: []dsl.Action{{Name: "one"}, {Name: "two"}}}
	var visited int
	err := WalkActions(script, func(ref ActionRef) error {
		visited++
		return stop
	})
	if !errors.Is(err, stop) || visited != 1 {
		t.Fatalf("WalkActions() = %v after %d visits", err, visited)
	}
}

func TestWalkActionsRejectsExcessiveDepth(t *testing.T) {
	t.Parallel()
	root := dsl.Action{Name: "root"}
	current := &root
	for depth := 1; depth <= dsl.MaxScriptActionDepth; depth++ {
		current.Control = &dsl.ControlFlow{Steps: []dsl.Action{{Name: fmt.Sprintf("depth-%d", depth+1)}}}
		current = &current.Control.Steps[0]
	}
	script := &dsl.Script{Actions: []dsl.Action{root}}
	err := WalkActions(script, func(ActionRef) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("WalkActions() error = %v", err)
	}
}

func TestWalkActionsRejectsExcessiveDefinitionCount(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Actions: make([]dsl.Action, dsl.MaxScriptActionDefinitions+1)}
	err := WalkActions(script, func(ActionRef) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "definitions exceed") {
		t.Fatalf("WalkActions() error = %v", err)
	}
}

func TestWalkActionsNilInputsAreNoOps(t *testing.T) {
	t.Parallel()
	if err := WalkActions(nil, func(ActionRef) error { return errors.New("unexpected") }); err != nil {
		t.Fatal(err)
	}
	if err := WalkActions(&dsl.Script{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWalkVisitsActionsAndHTTPSequenceRequestsInDeterministicOrder(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Init: []dsl.Action{{Name: "init"}},
		Actions: []dsl.Action{{
			Name: "control",
			Control: &dsl.ControlFlow{
				Steps: []dsl.Action{{
					Name: "sequence",
					Type: "http_sequence",
					Params: map[string]any{
						"requests": []any{
							map[string]any{"name": "first"},
							map[string]any{"name": "second"},
						},
					},
				}},
				Else: []dsl.Action{{Name: "else"}},
			},
		}},
		Teardown: []dsl.Action{{Name: "teardown"}},
	}

	var got []string
	err := Walk(script, func(ref SurfaceRef) error {
		got = append(got, fmt.Sprintf("%s:%s=%s@%d/%s", ref.Kind, ref.Path, ref.Name(), ref.Depth, ref.OwnerPath))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"action:init[0]=init@1/init[0]",
		"action:actions[0]=control@1/actions[0]",
		"action:actions[0].control.steps[0]=sequence@2/actions[0].control.steps[0]",
		"http_sequence_request:actions[0].control.steps[0].params.requests[0]=first@2/actions[0].control.steps[0]",
		"http_sequence_request:actions[0].control.steps[0].params.requests[1]=second@2/actions[0].control.steps[0]",
		"action:actions[0].control.else[0]=else@2/actions[0].control.else[0]",
		"action:teardown[0]=teardown@1/teardown[0]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk order:\n got %#v\nwant %#v", got, want)
	}
}

func TestWalkSupportsCanonicalAndTypedSequenceRequestShapes(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Actions: []dsl.Action{
			{
				Name: "maps",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":     "map",
						"metadata": map[string]any{"gate": "map"},
					}},
				},
			},
			{
				Name: "typed",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []dsl.Action{{
						Name:     "action",
						Metadata: map[string]any{"gate": "action"},
					}},
				},
			},
		},
	}

	var got []string
	err := Walk(script, func(ref SurfaceRef) error {
		if ref.Kind != HTTPSequenceRequestSurface {
			return nil
		}
		got = append(got, fmt.Sprintf("%s=%v", ref.Name(), ref.Metadata()["gate"]))
		if ref.Action != nil {
			ref.Action.Name = "mutated-action"
		}
		if ref.Request != nil {
			ref.Request["name"] = "mutated-map"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"map=map", "action=action"}) {
		t.Fatalf("sequence surfaces = %#v", got)
	}
	mapRequests := script.Actions[0].Params["requests"].([]map[string]any)
	if got := mapRequests[0]["name"]; got != "mutated-map" {
		t.Fatalf("map request mutation = %#v", got)
	}
	typedRequests := script.Actions[1].Params["requests"].([]dsl.Action)
	if got := typedRequests[0].Name; got != "mutated-action" {
		t.Fatalf("typed request mutation = %q", got)
	}
}

func TestWalkCountsMalformedSequenceEntriesAgainstDefinitionBudget(t *testing.T) {
	t.Parallel()
	requests := make([]any, dsl.MaxScriptActionDefinitions)
	script := &dsl.Script{Actions: []dsl.Action{{
		Name:   "sequence",
		Type:   "http_sequence",
		Params: map[string]any{"requests": requests},
	}}}
	err := Walk(script, func(SurfaceRef) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "action/request definitions exceed") {
		t.Fatalf("Walk() error = %v", err)
	}
}

func TestWalkPropagatesSequenceVisitorError(t *testing.T) {
	t.Parallel()
	stop := errors.New("stop at request")
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "sequence",
		Type: "http_sequence",
		Params: map[string]any{
			"requests": []any{map[string]any{"name": "child"}},
		},
	}}}
	err := Walk(script, func(ref SurfaceRef) error {
		if ref.Kind == HTTPSequenceRequestSurface {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("Walk() error = %v", err)
	}
}

func TestWalkPersistsMetadataMutationForEverySequenceRepresentation(t *testing.T) {
	t.Parallel()
	asPointer := &dsl.Action{Name: "pointer"}
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "sequence",
		Type: "http_sequence",
		Params: map[string]any{
			"requests": []any{
				map[string]any{"name": "map"},
				dsl.Action{Name: "value"},
				asPointer,
			},
		},
	}}}
	if err := Walk(script, func(ref SurfaceRef) error {
		if ref.Kind != HTTPSequenceRequestSurface {
			return nil
		}
		ref.SetMetadata(map[string]any{"visited": ref.Name()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requests := script.Actions[0].Params["requests"].([]any)
	if got := requests[0].(map[string]any)["metadata"].(map[string]any)["visited"]; got != "map" {
		t.Fatalf("map metadata = %#v", got)
	}
	if got := requests[1].(dsl.Action).Metadata["visited"]; got != "value" {
		t.Fatalf("value metadata = %#v", got)
	}
	if got := asPointer.Metadata["visited"]; got != "pointer" {
		t.Fatalf("pointer metadata = %#v", got)
	}
}
