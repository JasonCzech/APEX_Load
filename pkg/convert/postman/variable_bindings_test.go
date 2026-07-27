package postman

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func TestResolvePostmanVariableBindingsDeepChainIsIterative(t *testing.T) {
	t.Parallel()

	const count = 2048
	values := make(map[string]string, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("node_%04d", index)
		if index+1 == count {
			values[name] = "leaf"
			continue
		}
		values[name] = "{{" + fmt.Sprintf("node_%04d", index+1) + "}}"
	}
	limits := postmanVariableBindingLimits{
		Dependencies: requestimport.BindingDependencyLimits{
			MaxNodes:     count + 1,
			MaxEdges:     count + 1,
			MaxNameBytes: 64,
		},
		MaxResolvedValueBytes: 32,
		MaxResolvedTotalBytes: count * len("leaf"),
	}
	result := resolvePostmanVariableBindingsWithLimits(values, nil, limits)
	if result.ReviewRequired {
		t.Fatal("bounded deep chain unexpectedly required graph review")
	}
	if got := result.RuntimeValues["node_0000"]; got != "leaf" {
		t.Fatalf("deep root = %q, want leaf", got)
	}
	if len(result.RuntimeValues) != count {
		t.Fatalf("resolved bindings = %d, want %d", len(result.RuntimeValues), count)
	}
}

func TestResolvePostmanVariableBindingsFailsClosedAtGraphLimit(t *testing.T) {
	t.Parallel()

	var diagnostics diag.Collector
	result := resolvePostmanVariableBindingsWithLimits(
		map[string]string{"a": "one", "b": "two", "c": "three"},
		&diagnostics,
		postmanVariableBindingLimits{
			Dependencies: requestimport.BindingDependencyLimits{
				MaxNodes:     2,
				MaxEdges:     2,
				MaxNameBytes: 16,
			},
			MaxResolvedValueBytes: 16,
			MaxResolvedTotalBytes: 32,
		},
	)
	if !result.ReviewRequired || !result.OmitSourceReview {
		t.Fatalf("limit disposition = %#v, want review-required source omission", result)
	}
	if len(result.RuntimeValues) != 0 || len(result.SourceValues) != 0 {
		t.Fatalf("partial values escaped graph limit: %#v / %#v", result.RuntimeValues, result.SourceValues)
	}
	items := diagnostics.Items()
	if len(items) != 1 || items[0].Element != "Postman variable dependency limit" {
		t.Fatalf("limit diagnostics = %#v", items)
	}
}

func TestResolvePostmanVariableBindingsBoundsExpansionAndDependents(t *testing.T) {
	t.Parallel()

	var diagnostics diag.Collector
	result := resolvePostmanVariableBindingsWithLimits(
		map[string]string{
			"seed": "12345678",
			"a":    "{{seed}}{{seed}}",
			"b":    "{{a}}{{a}}",
			"c":    "{{b}}",
		},
		&diagnostics,
		postmanVariableBindingLimits{
			Dependencies: requestimport.BindingDependencyLimits{
				MaxNodes:     8,
				MaxEdges:     8,
				MaxNameBytes: 16,
			},
			MaxResolvedValueBytes: 24,
			MaxResolvedTotalBytes: 64,
		},
	)
	if result.ReviewRequired {
		t.Fatal("value expansion limit was confused with graph work exhaustion")
	}
	if result.RuntimeValues["seed"] != "12345678" ||
		result.RuntimeValues["a"] != "1234567812345678" {
		t.Fatalf("bounded prefix = %#v", result.RuntimeValues)
	}
	if _, exists := result.RuntimeValues["b"]; exists {
		t.Fatalf("oversized binding remained runnable: %#v", result.RuntimeValues)
	}
	if _, exists := result.RuntimeValues["c"]; exists {
		t.Fatalf("dependent of oversized binding remained runnable: %#v", result.RuntimeValues)
	}
	if got := fmt.Sprint(diagnostics.Items()); !strings.Contains(got, "Postman variable expansion") {
		t.Fatalf("expansion diagnostic = %s", got)
	}
}

func TestConsumedPostmanCredentialVariableClosureIsQueueBounded(t *testing.T) {
	t.Parallel()

	const count = 4096
	dependencies := make(map[string][]string, count-1)
	for index := 0; index+1 < count; index++ {
		dependencies[fmt.Sprintf("node_%04d", index)] = []string{
			fmt.Sprintf("node_%04d", index+1),
		}
	}
	pipeline := &postmanRequestPipeline{
		sourceDependencies: dependencies,
		reports: []requestimport.Report{{
			Requirements: []credentialpolicy.Requirement{{
				SourceVariableNames: []string{"node_0000"},
			}},
		}},
	}
	names := pipeline.consumedSourceVariableNames()
	if len(names) != count {
		t.Fatalf("credential dependency closure = %d, want %d", len(names), count)
	}
	if names[0] != "node_0000" || names[len(names)-1] != "node_4095" {
		t.Fatalf("credential dependency closure endpoints = %q / %q", names[0], names[len(names)-1])
	}
}
