package engine

import "testing"

func TestRuntimeMockNamespacesAreUniqueAndApplied(t *testing.T) {
	first := newRuntimeMockNamespace()
	second := newRuntimeMockNamespace()
	if first == "" || second == "" || first == second {
		t.Fatalf("namespaces = %q and %q, want distinct non-empty values", first, second)
	}
	config := map[string]any{}
	applyRuntimeMockNamespace(config, first)
	if config[runtimeMockNamespaceConfigKey] != first {
		t.Fatalf("runtime namespace = %v, want %q", config[runtimeMockNamespaceConfigKey], first)
	}
}
