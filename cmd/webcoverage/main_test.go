package main

import (
	"os"
	"strings"
	"testing"
)

func TestRenderHasFourSectionsInOrder(t *testing.T) {
	doc := render()
	if doc != render() {
		t.Fatal("render is not deterministic")
	}
	last := -1
	for _, h := range []string{"\n## Web function coverage\n", "\n## Java LR function coverage\n", "\n## TruClient step coverage\n", "\n## Java Vuser import coverage\n"} {
		i := strings.Index(doc, h)
		if i <= last {
			t.Fatalf("section %q missing or out of order", h)
		}
		last = i
	}
	if strings.Count(doc, "\n# ") != 0 || !strings.HasPrefix(doc, "# LoadRunner Coverage\n") {
		t.Fatal("expected exactly one H1 at the top")
	}
}

func TestCommittedDocIsFresh(t *testing.T) {
	got, err := os.ReadFile("../../" + defaultOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render() {
		t.Fatalf("%s is stale; run `go generate ./pkg/convert/enterprisearchive`", defaultOut)
	}
}
