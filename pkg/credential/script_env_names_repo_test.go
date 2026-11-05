package credential

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var repoEnvReference = regexp.MustCompile(`\{\{\s*env\.([A-Za-z0-9_]+)\s*\}\}`)

// shelteredEnvPlaceholders are placeholders a converter writes for a moment
// and restores before its output (pkg/convert/postman request_pipeline.go).
var shelteredEnvPlaceholders = map[string]bool{
	"POSTMAN_AUTH_PASSWORD_TEMPLATE": true,
	"POSTMAN_AUTH_TOKEN_TEMPLATE":    true,
}

// No bundled example, template or converter writes an {{env.*}} reference a
// script can never read: a credential goes through {{secret.*}}, anything
// else through a readable name (ReadableScriptEnvName).
func TestRepositoryWritesNoUnreadableEnvReferences(t *testing.T) {
	root := filepath.Join("..", "..")
	check := func(path string, lines []string) {
		for index, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, match := range repoEnvReference.FindAllStringSubmatch(line, -1) {
				name := match[1]
				if ScriptEnvNameAlwaysBlocked(name) && !shelteredEnvPlaceholders[name] {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d writes {{env.%s}}, which no script can read; use {{secret.%s}} for a credential or %s", rel, index+1, name, name, ReadableScriptEnvName(name))
				}
			}
		}
	}
	walk := func(dir string, keep func(string) bool) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "static", "node_modules", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !keep(path) {
				return nil
			}
			payload, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			check(path, strings.Split(string(payload), "\n"))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	walk("examples", func(string) bool { return true })
	sourceOnly := func(path string) bool { return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") }
	walk("pkg", sourceOnly)
	walk("cmd", sourceOnly)
	walk(filepath.Join("dashboard", "src"), func(path string) bool {
		return (strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) &&
			!strings.Contains(path, ".test.") && !strings.Contains(path, string(filepath.Separator)+"generated"+string(filepath.Separator))
	})
	walk("deployments", func(path string) bool {
		return strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")
	})
	walk("docs", func(path string) bool { return strings.HasSuffix(path, ".md") })
}
