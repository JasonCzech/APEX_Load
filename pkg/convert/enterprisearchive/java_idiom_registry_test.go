package enterprisearchive

import "testing"

func TestJavaStatementMapperRegistryIsOrderedAndTerminal(t *testing.T) {
	if len(javaStatementMapperRegistry) == 0 {
		t.Fatal("Java statement mapper registry is empty")
	}
	seen := map[string]bool{}
	for i, mapper := range javaStatementMapperRegistry {
		if mapper.Name() == "" {
			t.Fatalf("mapper %d has empty name", i)
		}
		if seen[mapper.Name()] {
			t.Fatalf("duplicate Java statement mapper name %q", mapper.Name())
		}
		seen[mapper.Name()] = true
	}
	last := javaStatementMapperRegistry[len(javaStatementMapperRegistry)-1]
	if last.Name() != "java_source_review_fallback" {
		t.Fatalf("last mapper = %q, want terminal source-review fallback", last.Name())
	}
	for _, required := range []string{"java_lr_api", "java_jms_api", "java_named_review", "java_http_owned_by_http_lane", "java_database_owned_by_database_lane"} {
		if !seen[required] {
			t.Fatalf("Java statement mapper registry missing %q", required)
		}
	}
}
