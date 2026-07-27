package scrub

import (
	"fmt"
	"testing"
)

func TestStructuredBudgetCountsEmptyContainers(t *testing.T) {
	t.Parallel()

	input := make(map[string]any, maxStructuredScrubEntries*2)
	for index := 0; index < maxStructuredScrubEntries*2; index++ {
		input[fmt.Sprintf("field_%05d", index)] = map[string]any{}
	}

	output, ok := Structured(input, Policy{}).(map[string]any)
	if !ok {
		t.Fatalf("Structured output type = %T", output)
	}
	if len(output) >= len(input) {
		t.Fatalf("empty containers bypassed structured traversal budget: got %d entries", len(output))
	}
	if len(output) > maxStructuredScrubEntries {
		t.Fatalf("structured traversal exceeded entry budget: got %d entries", len(output))
	}
}

func TestStructuredBudgetCountsNestedEmptySlices(t *testing.T) {
	t.Parallel()

	const side = 128
	children := make([]any, side)
	for index := range children {
		children[index] = make([]any, side)
	}

	output, ok := Structured(children, Policy{}).([]any)
	if !ok {
		t.Fatalf("Structured output type = %T", output)
	}
	total := len(output)
	for _, child := range output {
		if values, childOK := child.([]any); childOK {
			total += len(values)
		}
	}
	if total > maxStructuredScrubEntries {
		t.Fatalf("nested empty slices exceeded structured traversal budget: got %d nodes", total)
	}
}
