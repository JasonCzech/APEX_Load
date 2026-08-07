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

// corpusEnvVar is the explicit override for the pinned LR_Scripts corpus location.
const corpusEnvVar = "APEXLOAD_LR_SCRIPTS_CORPUS"

// corpusRelocationEnvVar is the fetch script's destination override
// (scripts/fetch-lr-scripts-corpus.sh). It is not an opt-in switch of its own, but
// it does move the probed location, so the skip message names it and
// docs/OPTIN_TEST_SUITES.md carries a row for it.
const corpusRelocationEnvVar = "LR_SCRIPTS_DEST"

// corpusDefaultRoots are the locations this test discovers a corpus in without an
// APEXLOAD_LR_SCRIPTS_CORPUS override, so a developer who has run
// scripts/fetch-lr-scripts-corpus.sh gets the corpus smoke test for free on the
// next `go test ./pkg/convert/...`. LR_SCRIPTS_DEST is honoured because that is
// where the fetch script put the corpus when it is set.
//
// The first entry is the fetch script's own default destination; keep the two in
// sync (scripts/fetch-lr-scripts-corpus.sh, `dest` default).
func corpusDefaultRoots() []string {
	roots := []string{
		"/tmp/apexload-lr-scripts-corpus",
		filepath.Join("..", "..", "..", ".corpus", "lr-scripts"),
	}
	if dest := strings.TrimSpace(os.Getenv(corpusRelocationEnvVar)); dest != "" {
		roots = append([]string{dest}, roots...)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, "apexload-lr-scripts-corpus"))
	}
	return roots
}

// corpusMinScriptDirs is how many script directories TestPinnedLRScriptsCorpusSmoke
// demands of a corpus. Auto-discovery uses the SAME number, so a root it admits
// cannot fail the assertion below for being too small.
const corpusMinScriptDirs = 20

// corpusRootIsUsable reports whether root holds an LR_Scripts checkout complete
// enough to run the suite: a Scripts directory at or one level below it holding at
// least corpusMinScriptDirs script directories.
//
// The threshold has to match the assertion's, not merely be non-zero. A threshold
// of one turned a half-finished fetch — scripts/fetch-lr-scripts-corpus.sh
// interrupted part-way through `git checkout` — and any directory a co-tenant
// happens to leave at the world-writable /tmp default into a MANDATORY run that
// then t.Fatalf'd with "discovered 1 script directories", so every later
// `go test ./pkg/convert/enterprisearchive` on that machine failed until the tree
// was repaired. A partial checkout is not a corpus; it must fall back to the skip
// that says how to fetch one.
//
// This is the auto-discovery predicate only. An operator who named a path through
// APEXLOAD_LR_SCRIPTS_CORPUS opted in, so that path keeps the weaker
// corpusRootHoldsAnyScript admission test and fails loudly when it is incomplete.
func corpusRootIsUsable(root string) bool {
	return corpusRootScriptDirCount(root) >= corpusMinScriptDirs
}

// corpusRootHoldsAnyScript reports whether root looks like an LR_Scripts checkout
// at all. It is the admission test for the explicit override, where "this is not a
// corpus" must fail rather than skip but "this corpus is incomplete" should still
// reach the suite's own, more informative assertion.
func corpusRootHoldsAnyScript(root string) bool {
	return corpusRootScriptDirCount(root) > 0
}

// corpusRootScriptDirCount returns the largest number of script directories found
// under any of root's candidate Scripts locations, capped at corpusMinScriptDirs so
// the probe stays cheap against the full corpus.
func corpusRootScriptDirCount(root string) int {
	best := 0
	for _, candidate := range corpusScriptsDirCandidates(root) {
		if count := corpusDirScriptDirCount(candidate, corpusMinScriptDirs); count > best {
			best = count
		}
	}
	return best
}

// corpusScriptsDirCandidates lists the places a Scripts directory may sit relative
// to root: root itself when it is already the Scripts directory, one level below,
// or two (a checkout directory containing the repository's own Scripts/Scripts).
func corpusScriptsDirCandidates(root string) []string {
	candidates := []string{filepath.Join(root, "Scripts"), filepath.Join(root, "Scripts", "Scripts")}
	if strings.EqualFold(filepath.Base(root), "Scripts") {
		candidates = append([]string{root}, candidates...)
	}
	return candidates
}

// corpusDirScriptDirCount counts the distinct directories under dir that hold a
// recognizable script file — the same unit discoverCorpusScriptDirs counts, so the
// admission probe and the suite's own assertion cannot disagree about how big a
// corpus is. It stops once limit is reached, so it stays cheap against the full
// corpus.
func corpusDirScriptDirCount(dir string, limit int) int {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0
	}
	scriptDirs := map[string]struct{}{}
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(scriptDirs) >= limit {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		if isCorpusScriptFile(entry.Name()) {
			scriptDirs[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	return len(scriptDirs)
}

// isCorpusScriptFile is the single definition of "this file makes its directory a
// script directory", shared by discovery and by the usability probe so the two can
// never disagree about what a corpus is.
func isCorpusScriptFile(name string) bool {
	base := strings.ToLower(name)
	return base == "main.js" || strings.HasSuffix(base, ".usr") || strings.HasSuffix(base, ".jmx")
}

// resolveCorpusRoot decides whether the corpus smoke test runs.
//
//   - APEXLOAD_LR_SCRIPTS_CORPUS set: it MUST resolve. An operator who pointed the
//     suite at a corpus and got a silent skip learns nothing, so a bad path fails —
//     and so does an incomplete one, via the suite's own assertion.
//   - Unset: the default fetch locations are probed and the suite runs automatically
//     when one of them holds a COMPLETE corpus (corpusMinScriptDirs script
//     directories, the same bar the assertion uses). This is the point of O57 — the
//     suite used to be unconditionally opt-in, so it ran nowhere — without letting
//     an unrelated or half-fetched directory conscript the package into failing.
//   - Nothing found: skip with the exact command that makes it run.
func resolveCorpusRoot(t *testing.T) string {
	t.Helper()
	if root := strings.TrimSpace(os.Getenv(corpusEnvVar)); root != "" {
		if !corpusRootHoldsAnyScript(root) {
			t.Fatalf("%s=%q does not contain an LR_Scripts checkout (no Scripts directory holding any .usr/.jmx/main.js at or below it). "+
				"Fix the path, re-run ./scripts/fetch-lr-scripts-corpus.sh, or unset the variable; a wrong path must not silently skip the only test that runs importers against real archives.", corpusEnvVar, root)
		}
		return root
	}
	for _, candidate := range corpusDefaultRoots() {
		if corpusRootIsUsable(candidate) {
			t.Logf("using auto-discovered LR_Scripts corpus at %s (set %s to override)", candidate, corpusEnvVar)
			return candidate
		}
	}
	t.Skipf("SKIPPED — no LR_Scripts corpus found. This is the only suite that runs the importers against real "+
		"LoadRunner/DevWeb/JMeter archives; while it is skipped, real-archive coverage is UNVERIFIED.\n"+
		"  To run it:   ./scripts/fetch-lr-scripts-corpus.sh   (then re-run this package; no env var needed)\n"+
		"  Or point at an existing checkout: %s=/path/to/LR_Scripts\n"+
		"  %s relocates the fetch script's destination and is probed first when set.\n"+
		"  A probed root counts only when its Scripts directory holds at least %d script directories\n"+
		"  (the same bar this suite asserts), so a half-finished fetch skips here instead of failing the package.\n"+
		"  Probed (in order): %s\n"+
		"  See docs/OPTIN_TEST_SUITES.md.",
		corpusEnvVar, corpusRelocationEnvVar, corpusMinScriptDirs, strings.Join(corpusDefaultRoots(), ", "))
	return ""
}

func TestPinnedLRScriptsCorpusSmoke(t *testing.T) {
	root := resolveCorpusRoot(t)
	scriptsRoot := corpusScriptsRoot(t, root)
	dirs := discoverCorpusScriptDirs(t, scriptsRoot)
	if len(dirs) < corpusMinScriptDirs {
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
				for _, script := range result.effectiveScripts() {
					if script == nil {
						continue
					}
					switch script.Protocol {
					case "jms-solace":
						counts["java_jms_solace"]++
					case "java_vuser":
						counts["java_vuser"]++
					}
				}
			}
		})
	}

	for _, key := range []string{"devweb", "jmeter", "web_http_html", "soap", "sap_web", "sap_gui", "rdp", "rte", "truclient_web", "truclient_mobile_web", "truclient_native_mobile", "java_vuser", "java_jms_solace"} {
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
		if isCorpusScriptFile(entry.Name()) {
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
	case strings.Contains(path, "java") && strings.Contains(path, "solace"):
		if result.Script == nil || result.Script.Protocol != "jms-solace" {
			t.Fatalf("script = %#v, want jms-solace for Java Solace corpus entry", result.Script)
		}
	case strings.Contains(path, "java"):
		if result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" && result.DetectedProtocol.CanonicalSourceProtocol != "java_over_http" && result.DetectedProtocol.CanonicalSourceProtocol != "java_jdbc" {
			t.Fatalf("detected protocol = %#v, want Java Vuser/HTTP/JDBC corpus posture", result.DetectedProtocol)
		}
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
