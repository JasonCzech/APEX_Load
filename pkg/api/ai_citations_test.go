package api

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestAICitedRepositoryPathsExist keeps the AI responses' source citations
// pointing at files that exist: a citation to a removed file is evidence an
// agent cannot follow.
func TestAICitedRepositoryPathsExist(t *testing.T) {
	sources, err := filepath.Glob("ai_*.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("glob AI sources: %v (%d files)", err, len(sources))
	}
	cited := regexp.MustCompile(`"(pkg/[^"]+\.go)"`)
	for _, source := range sources {
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range cited.FindAllSubmatch(body, -1) {
			path := string(match[1])
			if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
				t.Errorf("%s cites %s, which does not exist", source, path)
			}
		}
	}
}
