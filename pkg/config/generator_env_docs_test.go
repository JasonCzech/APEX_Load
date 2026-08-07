package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generatorDocFiles are the Markdown files that show a reader how to start a
// generator process. Every generator command in them must set an explicit bind
// address, because APEXLOAD_GENERATORS quick starts point at two loopback
// targets and DefaultGeneratorConfig binds both to the same port.
var generatorDocFiles = []string{"README.md", "QUICK_REFERENCE.md", "DEVELOPMENT.md"}

func TestGeneratorDocsSetListenAddrOnEveryStartCommand(t *testing.T) {
	for _, name := range generatorDocFiles {
		t.Run(name, func(t *testing.T) {
			lines := readDocLines(t, name)
			commands := 0
			for i, line := range lines {
				if !strings.Contains(line, "go run ./cmd/generator") {
					continue
				}
				commands++
				if !strings.Contains(line, "APEXLOAD_GENERATOR_LISTEN_ADDR=") {
					t.Errorf("%s:%d starts a generator without APEXLOAD_GENERATOR_LISTEN_ADDR; a second generator on the same host fails with %q\n\t%s",
						name, i+1, "bind: address already in use", strings.TrimSpace(line))
				}
			}
			if commands == 0 {
				t.Fatalf("%s no longer shows a generator start command; update generatorDocFiles", name)
			}
		})
	}
}

// TestGeneratorDocSnippetsMatchConfigSurface ties the literal env assignments in
// the docs to LoadGeneratorConfigFromEnv, so renaming the variable in code
// breaks the docs test instead of silently stranding the snippets.
func TestGeneratorDocSnippetsMatchConfigSurface(t *testing.T) {
	checked := 0
	for _, name := range generatorDocFiles {
		for i, line := range readDocLines(t, name) {
			for _, assignment := range strings.Fields(line) {
				key, value, ok := strings.Cut(strings.TrimSpace(assignment), "=")
				if !ok || !strings.HasPrefix(key, "APEXLOAD_GENERATOR_LISTEN_ADDR") {
					continue
				}
				cfg := LoadGeneratorConfigFromEnv(func(lookup string) string {
					if lookup == key {
						return value
					}
					return ""
				})
				if cfg.ListenAddr != value {
					t.Errorf("%s:%d sets %s=%s but LoadGeneratorConfigFromEnv resolved ListenAddr=%q", name, i+1, key, value, cfg.ListenAddr)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no APEXLOAD_GENERATOR_LISTEN_ADDR assignment found in the generator docs")
	}
}

func TestGeneratorDocsDocumentDefaultListenAddr(t *testing.T) {
	want := DefaultGeneratorConfig().ListenAddr
	for _, name := range []string{"README.md", "QUICK_REFERENCE.md"} {
		body := strings.Join(readDocLines(t, name), "\n")
		if !strings.Contains(body, "`"+want+"`") {
			t.Errorf("%s does not document the generator default bind address %q", name, want)
		}
	}
}

func readDocLines(t *testing.T, name string) []string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.Split(string(data), "\n")
}
