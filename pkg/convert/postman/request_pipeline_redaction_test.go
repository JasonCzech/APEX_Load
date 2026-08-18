package postman

import (
	"fmt"
	"strings"
	"testing"
)

func TestRedactSourceReviewBodiesAppliesOverlappingValuesLongestFirst(t *testing.T) {
	pipeline := &postmanRequestPipeline{sourceReviewBodies: map[string]string{
		"secret":       "[SHORT]",
		"secret-token": "[LONG]",
	}}
	sources := map[string]string{"collection.json": `{"value":"secret-token"}`}

	if !pipeline.redactSourceReviewBodies(sources) {
		t.Fatal("expected source review redaction")
	}
	if got := sources["collection.json"]; got != `{"value":"[LONG]"}` {
		t.Fatalf("redacted source = %q, want longest replacement to win", got)
	}
}

func TestRedactSourceReviewBodiesOmitsSourceWhenWorkBudgetIsExceeded(t *testing.T) {
	bodies := make(map[string]string, 10_000)
	for index := 0; index < 10_000; index++ {
		value := fmt.Sprintf("credential-value-%05d", index)
		bodies[value] = "[protected]"
	}
	pipeline := &postmanRequestPipeline{sourceReviewBodies: bodies}
	sources := map[string]string{"collection.json": strings.Repeat("x", 20_000) + "\n"}

	if !pipeline.redactSourceReviewBodies(sources) {
		t.Fatal("expected fail-closed source omission")
	}
	if got := sources["collection.json"]; !strings.Contains(got, "SOURCE OMITTED") || strings.Contains(got, "credential-value") {
		t.Fatalf("unexpected omitted source: %q", got)
	}
}
