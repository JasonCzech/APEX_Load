package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
)

func TestPinnedLRScriptsCorpusSmoke(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("APEXLOAD_LR_SCRIPTS_CORPUS"))
	if root == "" {
		t.Skip("set APEXLOAD_LR_SCRIPTS_CORPUS to a pinned LR_Scripts checkout to run the corpus smoke test")
	}
	scriptsRoot := corpusScriptsRoot(t, root)
	dirs := discoverCorpusScriptDirs(t, scriptsRoot)
	if len(dirs) < 20 {
		t.Fatalf("discovered %d script directories under %s, want the pinned public corpus", len(dirs), scriptsRoot)
	}

	counts := map[string]int{}
	for _, dir := range dirs {
		dir := dir
		t.Run(strings.TrimPrefix(dir, scriptsRoot+string(filepath.Separator)), func(t *testing.T) {
			archive := zipDirectoryForCorpus(t, dir)
			switch {
			case hasBaseFile(dir, "main.js"):
				result, err := devweb.ConvertZip(archive, devweb.ConvertOptions{})
				if err != nil {
					t.Fatalf("DevWeb ConvertZip returned error: %v", err)
				}
				if result.Script == nil || len(result.Script.Actions) == 0 {
					t.Fatalf("DevWeb result = %#v, want executable actions", result)
				}
				counts["devweb"]++
			case firstFileWithSuffix(dir, ".jmx") != "":
				fileName := firstFileWithSuffix(dir, ".jmx")
				data, err := os.ReadFile(fileName)
				if err != nil {
					t.Fatalf("read JMX fixture: %v", err)
				}
				result, err := jmeter.ConvertJMX(data, jmeter.ConvertOptions{
					FileName:     filepath.Base(fileName),
					SidecarFiles: corpusJMeterSidecarFiles(t, dir),
				})
				if err != nil {
					t.Fatalf("JMeter ConvertJMX returned error: %v", err)
				}
				if len(result.Scripts) == 0 || result.Stats.ConvertedActions == 0 {
					t.Fatalf("JMeter result = %#v, want converted actions", result)
				}
				counts["jmeter"]++
			default:
				result, err := ConvertZip(archive, ConvertOptions{PreserveTransactions: true, IncludeAssertions: true})
				if err != nil {
					t.Fatalf("EnterpriseArchive ConvertZip returned error: %v", err)
				}
				if result.Script == nil && result.ReviewArtifact == nil {
					t.Fatalf("EnterpriseArchive result has neither script nor review artifact: %#v", result)
				}
				if result.DetectedProtocol != nil {
					counts[result.DetectedProtocol.CanonicalSourceProtocol]++
					assertCorpusProtocolPosture(t, dir, result)
				}
			}
		})
	}

	for _, key := range []string{"devweb", "jmeter", "web_http_html", "soap", "sap_web", "sap_gui", "rdp", "rte", "truclient_web", "truclient_mobile_web", "truclient_native_mobile"} {
		if counts[key] == 0 {
			t.Fatalf("corpus coverage counts = %#v, missing %s", counts, key)
		}
	}
}

func corpusScriptsRoot(t *testing.T, root string) string {
	t.Helper()
	candidates := []string{root, filepath.Join(root, "Scripts")}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			if strings.EqualFold(filepath.Base(candidate), "Scripts") {
				return candidate
			}
			if st, err := os.Stat(filepath.Join(candidate, "Scripts")); err == nil && st.IsDir() {
				return filepath.Join(candidate, "Scripts")
			}
		}
	}
	t.Fatalf("could not find Scripts directory under %s", root)
	return ""
}

func discoverCorpusScriptDirs(t *testing.T, scriptsRoot string) []string {
	t.Helper()
	scriptDirs := map[string]struct{}{}
	err := filepath.WalkDir(scriptsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		base := strings.ToLower(entry.Name())
		if base == "main.js" || strings.HasSuffix(base, ".usr") || strings.HasSuffix(base, ".jmx") {
			scriptDirs[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	out := make([]string, 0, len(scriptDirs))
	for dir := range scriptDirs {
		out = append(out, dir)
	}
	sortStrings(out)
	return out
}

func assertCorpusProtocolPosture(t *testing.T, dir string, result *ConvertResult) {
	t.Helper()
	if result == nil || result.DetectedProtocol == nil {
		return
	}
	path := strings.ToLower(filepath.ToSlash(dir))
	assert := func(canonical string, mode ImportMode, scriptExpected bool) {
		if result.DetectedProtocol.CanonicalSourceProtocol != canonical || result.DetectedProtocol.ImportMode != mode {
			t.Fatalf("detected protocol = %#v, want %s/%s", result.DetectedProtocol, canonical, mode)
		}
		if scriptExpected && result.Script == nil {
			t.Fatalf("script is nil for executable %s import", canonical)
		}
		if !scriptExpected && result.ReviewArtifact == nil {
			t.Fatalf("review artifact is nil for %s import", canonical)
		}
	}
	switch {
	case strings.Contains(path, "/sap/") && strings.Contains(path, "sap gui"):
		assert("sap_gui", ImportModeExternalRunnerRequired, false)
	case strings.Contains(path, "/sap/") && strings.Contains(path, "sap web"):
		assert("sap_web", ImportModeExecutable, true)
	case strings.Contains(path, "/rdp/"):
		assert("rdp", ImportModeExternalRunnerRequired, false)
	case strings.Contains(path, "/rte/"):
		assert("rte", ImportModeExternalRunnerRequired, false)
	case strings.Contains(path, "truclient - native mobile"):
		assert("truclient_native_mobile", ImportModeExternalRunnerRequired, false)
	case strings.Contains(path, "truclient - web"):
		assert("truclient_web", ImportModeReviewOnly, false)
	case strings.Contains(path, "truclient - mobile web"):
		assert("truclient_mobile_web", ImportModeExecutable, true)
	case strings.Contains(path, "web services soap"):
		assert("soap", ImportModeExecutable, true)
	}
}

func zipDirectoryForCorpus(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		writer, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	})
	if err != nil {
		t.Fatalf("zip corpus directory: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close corpus zip: %v", err)
	}
	return buf.Bytes()
}

func hasBaseFile(dir string, base string) bool {
	return firstBaseFile(dir, base) != ""
}

func firstBaseFile(dir string, base string) string {
	var found string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || found != "" {
			return err
		}
		if strings.EqualFold(entry.Name(), base) {
			found = path
		}
		return nil
	})
	return found
}

func firstFileWithSuffix(dir string, suffix string) string {
	var found string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || found != "" {
			return err
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), strings.ToLower(suffix)) {
			found = path
		}
		return nil
	})
	return found
}

func corpusJMeterSidecarFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".csv") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		files[entry.Name()] = data
		return nil
	})
	if err != nil {
		t.Fatalf("read JMeter sidecars: %v", err)
	}
	if len(files) == 0 {
		return nil
	}
	return files
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		value := values[i]
		j := i - 1
		for ; j >= 0 && values[j] > value; j-- {
			values[j+1] = values[j]
		}
		values[j+1] = value
	}
}
