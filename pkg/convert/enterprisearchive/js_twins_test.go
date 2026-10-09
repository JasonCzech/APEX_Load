package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestJavaScriptTwinsImportLikeTheirCOriginals imports each JavaScript twin
// under testdata/lr_web_parity_js_twins and the C corpus case it mirrors, and
// requires the same steps and settings (WP4.3).
func TestJavaScriptTwinsImportLikeTheirCOriginals(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "lr_web_parity_js_twins", "*"))
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		ran++
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			original := filepath.Join("testdata", "lr_web_parity", name)
			cfg, err := os.ReadFile(filepath.Join(original, "default.cfg"))
			if err != nil {
				t.Fatal(err)
			}
			cResult, err := ConvertZip(zipDirFiles(t, original, nil), ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatal(err)
			}
			jsResult, err := ConvertZip(zipDirFiles(t, dir, map[string][]byte{"default.cfg": cfg}), ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatal(err)
			}
			if jsResult.Script == nil {
				t.Fatalf("the JavaScript twin did not import: %v", jsResult.Warnings)
			}
			if got, want := comparableScript(t, jsResult.Script), comparableScript(t, cResult.Script); got != want {
				t.Fatalf("the JavaScript twin imports differently\njs: %s\nc:  %s", got, want)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no JavaScript twins found")
	}
}

func zipDirFiles(t *testing.T, dir string, extra map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "expected.json" || entry.Name() == "README.md" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = content
	}
	for name, content := range extra {
		files[name] = content
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(files[name])
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// comparableScript is a script's steps and settings without source
// provenance (metadata: file names and line numbers) and with generated
// multipart boundaries normalized.
func comparableScript(t *testing.T, script *dsl.Script) string {
	t.Helper()
	view := map[string]any{
		"init": script.Init, "actions": script.Actions, "teardown": script.Teardown,
		"config": script.Config, "parameters": script.Parameters,
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	var strip func(value any) any
	strip = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			delete(typed, "metadata")
			for key, child := range typed {
				typed[key] = strip(child)
			}
		case []any:
			for index, child := range typed {
				typed[index] = strip(child)
			}
		}
		return value
	}
	normalized, err := json.Marshal(strip(generic))
	if err != nil {
		t.Fatal(err)
	}
	return multipartBoundary.ReplaceAllString(string(normalized), "apexload-BOUNDARY")
}

var multipartBoundary = regexp.MustCompile(`apexload-[0-9a-f]{8,}`)
