package enterprisearchive

import (
	"fmt"
	"go/build/constraint"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// optin_suite_inventory_test.go keeps docs/qa/TEST_PROGRAM.md honest.
//
// Suites excluded from `go test ./...` — by a build tag or by an environment
// variable that gates a t.Skip — run in no automated context. That is a legitimate
// choice for suites needing a broker or a large corpus, but only if the exclusion is
// discoverable. An undocumented gate is indistinguishable from dead test code.
//
// This test derives the inventory from the source instead of restating it: it scans
// every *_test.go in the repository for opt-in gates and fails when one is not
// documented. Adding a new gate therefore forces a doc row (and, for build tags, a
// line in scripts/run-optin-suites.sh so the suite is actually runnable).

const (
	optInSuitesDocPath   = "../../../docs/qa/TEST_PROGRAM.md"
	optInSuitesRunScript = "../../../scripts/run-optin-suites.sh"
	repoRootFromPackage  = "../../.."
)

// platformBuildTags are the constraints that select a per-platform or per-mode
// implementation. One branch of each always compiles, so they are not opt-in gates.
var platformBuildTags = map[string]bool{
	// GOOS
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
	"hurd": true, "illumos": true, "ios": true, "js": true, "linux": true, "nacl": true,
	"netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true,
	"windows": true, "zos": true,
	// GOARCH
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mipsle": true, "ppc64": true,
	"ppc64le": true, "riscv64": true, "s390x": true, "wasm": true,
	// toolchain / mode
	"cgo": true, "gc": true, "gccgo": true, "race": true, "msan": true, "asan": true,
	"unix": true, "boringcrypto": true, "purego": true, "ignore": true,
}

// envNamePattern matches a complete environment-variable name. It is applied to
// whole tokens, so "LR_Scripts" (mixed case) is not mistaken for an env var while
// APEXLOAD_RUN_HA_FAILOVER_SOAK is.
var envNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+$`)

// envTokenSplitter cuts a skip message into identifier-shaped tokens.
var envTokenSplitter = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// filePathPattern strips file names out of a skip message before it is tokenized,
// so a pointer to "docs/qa/TEST_PROGRAM.md" is not read as an env var.
var filePathPattern = regexp.MustCompile(`[A-Za-z0-9_./\\-]+\.(?:md|go|sh|ya?ml|json|txt|js|ts)\b`)

// skipCallPattern locates the start of a Skip/Skipf/Skipln call on any testing
// receiver (t, b, tb, ...). Only the opening parenthesis is matched here; the
// arguments are then balanced by skipCallArguments, because the idiomatic gate
// spells its variable as a format ARGUMENT — `t.Skipf("set %s=1", myEnvConst)` —
// often on a later line. A single-line, literal-only scan sees neither half.
var skipCallPattern = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.Skip(?:f|ln)?\(`)

// goIdentPattern matches Go identifiers inside a skip call's arguments so a gate
// named by constant can be resolved back to its literal value.
var goIdentPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// stringConstPattern matches `name = "value"` declarations — standalone or inside
// a const/var block — which is how every environment gate in this repository
// names itself.
var stringConstPattern = regexp.MustCompile(`(?m)^[ \t]*(?:const|var)?[ \t]*([A-Za-z_][A-Za-z0-9_]*)[ \t]*=[ \t]*"([^"\\]*)"`)

// goVersionTagPattern matches go1.x build constraints, which are toolchain gates.
var goVersionTagPattern = regexp.MustCompile(`^go1\.\d+$`)

type optInInventory struct {
	buildTags map[string][]string // tag -> files requiring it
	envVars   map[string][]string // env var -> files gating on it
}

func collectOptInInventory(t *testing.T) optInInventory {
	t.Helper()
	inventory := optInInventory{
		buildTags: map[string][]string{},
		envVars:   map[string][]string{},
	}
	root, err := filepath.Abs(repoRootFromPackage)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repo root not resolvable from the package directory (%v); inventory guard cannot run", err)
	}

	// Files are grouped by package because a gate spelled `t.Skipf("set %s=1",
	// browserTestsEnv)` names its variable through a constant that may be declared
	// in a sibling file of the same package.
	// content is kept raw for //go:build header parsing; masked is the same text
	// with comment bodies blanked, which is what every Skip/const scan runs on.
	type testFile struct {
		rel     string
		content string
		masked  string
	}
	byPackage := map[string][]testFile{}
	var packages []string
	walkErr := walkRepositoryTestFiles(root, func(rel string, path string, content string) {
		dir := filepath.Dir(path)
		if _, seen := byPackage[dir]; !seen {
			packages = append(packages, dir)
		}
		byPackage[dir] = append(byPackage[dir], testFile{rel: rel, content: content, masked: maskGoComments(content)})
	})
	if walkErr != nil {
		t.Fatalf("walk repository: %v", walkErr)
	}

	for _, pkg := range packages {
		files := byPackage[pkg]
		gated := false
		for _, file := range files {
			for _, tag := range requiredBuildTags(file.content) {
				inventory.buildTags[tag] = append(inventory.buildTags[tag], file.rel)
			}
			if !gated && fileMayContainSkip(file.masked) && skipCallPattern.MatchString(file.masked) {
				gated = true
			}
		}
		if !gated {
			continue
		}
		consts := map[string]string{}
		for _, file := range files {
			for name, value := range stringConstants(file.masked) {
				consts[name] = value
			}
		}
		for _, file := range files {
			for _, name := range skipMessageEnvVars(file.masked, consts) {
				inventory.envVars[name] = append(inventory.envVars[name], file.rel)
			}
		}
	}
	return inventory
}

// walkRepositoryTestFiles visits every *_test.go under root, handing the callback
// the repo-relative path, the absolute path and the file contents.
func walkRepositoryTestFiles(root string, visit func(rel string, path string, content string)) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable trees are not the guard's business
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "coverage":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		visit(filepath.ToSlash(rel), path, string(data))
		return nil
	})
}

// fileMayContainSkip is a cheap literal prefilter. skipCallPattern starts with a
// character class, which costs a full regex scan of every one of the repository's
// ~1200 test files; this substring check rejects almost all of them first.
func fileMayContainSkip(content string) bool {
	return strings.Contains(content, ".Skip")
}

// stringConstants returns the `name = "value"` declarations in a file.
func stringConstants(content string) map[string]string {
	out := map[string]string{}
	for _, match := range stringConstPattern.FindAllStringSubmatch(content, -1) {
		out[match[1]] = match[2]
	}
	return out
}

// requiredBuildTags returns the non-platform tags a file REQUIRES (positive
// occurrences only). A file constrained by "!kafka_real" compiles in the default
// build and is therefore not opt-in.
func requiredBuildTags(content string) []string {
	tags := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:build") {
			if line != "" && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "package") {
				break // past the header
			}
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			continue
		}
		collectPositiveTags(expr, false, tags)
	}
	out := make([]string, 0, len(tags))
	for tag := range tags {
		if platformBuildTags[tag] || goVersionTagPattern.MatchString(tag) {
			continue
		}
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func collectPositiveTags(expr constraint.Expr, negated bool, out map[string]bool) {
	switch node := expr.(type) {
	case *constraint.TagExpr:
		if !negated {
			out[node.Tag] = true
		}
	case *constraint.NotExpr:
		collectPositiveTags(node.X, !negated, out)
	case *constraint.AndExpr:
		collectPositiveTags(node.X, negated, out)
		collectPositiveTags(node.Y, negated, out)
	case *constraint.OrExpr:
		collectPositiveTags(node.X, negated, out)
		collectPositiveTags(node.Y, negated, out)
	}
}

// skipMessageEnvVars extracts the environment-variable names named in Skip
// messages. A skip that names its gate is the only kind a developer can act on, so
// this doubles as pressure to write such messages.
//
// Both spellings count: the name written literally in the message, and the name
// passed as a format argument via a package-level constant — `SKIPF("set %s=1",
// browserTestsEnv)` — which is the form docs/qa/TEST_PROGRAM.md tells authors to
// use and the form every existing gate in this repository actually uses.
//
// content is masked with maskGoComments first, so a Skip call quoted inside a
// comment (including the example above, which lives in this file the scanner walks)
// is not scanned as a real gate and a ")" or an apostrophe inside a comment cannot
// derail the argument scan. Masking is idempotent, so callers that already hold a
// masked view may pass it straight through.
func skipMessageEnvVars(content string, packageConsts map[string]string) []string {
	if !fileMayContainSkip(content) {
		return nil
	}
	content = maskGoComments(content)
	names := map[string]bool{}
	for _, loc := range skipCallPattern.FindAllStringIndex(content, -1) {
		literalText, identText := skipCallArguments(content, loc[1])
		// The literal pass keeps the message text: a gate may be spelled out in
		// it. File names are stripped first so a pointer to a doc is not read as
		// a variable name.
		args := filePathPattern.ReplaceAllString(literalText, " ")
		for _, token := range envTokenSplitter.Split(args, -1) {
			if envNamePattern.MatchString(token) {
				names[token] = true
			}
		}
		// The identifier pass runs over the literal-free view. Resolving words
		// from the message text against the package's constant map invented
		// gates: a package holding `const region = "US_EAST_1"` reported
		// US_EAST_1 as an environment gate purely because a skip message said
		// "no region configured", and the guard then demanded a doc row for it.
		for _, ident := range goIdentPattern.FindAllString(identText, -1) {
			value, ok := packageConsts[ident]
			if ok && envNamePattern.MatchString(value) {
				names[value] = true
			}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// maskGoComments returns content with the bytes of every `//` and `/* */` comment
// replaced by spaces. Newlines are preserved, so offsets, line numbers and the
// newline bail-outs downstream are all unchanged. String and rune literals are
// stepped over, so a "//" inside a message is not mistaken for a comment.
//
// Every Skip and constant scan runs on this view, for three reasons the raw text
// got wrong:
//
//   - a ")" inside a comment in a Skip call's argument list closed the call early,
//     truncating the text before the constant that names the gate, so the gate went
//     undetected — precisely the failure this guard exists to prevent;
//   - an apostrophe inside such a comment put the argument scanner into rune-literal
//     mode, which consumed to the next apostrophe anywhere in the file and reported
//     every ALL_CAPS token after it as an environment gate;
//   - a `Skip` call quoted as an EXAMPLE in a doc comment (this file has two) is not
//     a gate and must not be scanned as one.
//
// //go:build headers are parsed from the raw text, before masking.
func maskGoComments(content string) string {
	out := []byte(content)
	blank := func(start, end int) {
		for index := start; index < end && index < len(out); index++ {
			if out[index] != '\n' {
				out[index] = ' '
			}
		}
	}
	for i := 0; i < len(content); {
		switch {
		case content[i] == '/' && i+1 < len(content) && content[i+1] == '/':
			start := i
			for i < len(content) && content[i] != '\n' {
				i++
			}
			blank(start, i)
		case content[i] == '/' && i+1 < len(content) && content[i+1] == '*':
			start := i
			for i += 2; i < len(content); i++ {
				if content[i] == '*' && i+1 < len(content) && content[i+1] == '/' {
					i += 2
					break
				}
			}
			blank(start, i)
		case content[i] == '"':
			for i++; i < len(content) && content[i] != '"' && content[i] != '\n'; i++ {
				if content[i] == '\\' {
					i++
				}
			}
			i++
		case content[i] == '`':
			for i++; i < len(content) && content[i] != '`'; i++ {
			}
			i++
		case content[i] == '\'':
			// A Go rune literal cannot span a line, so bail at a newline. Without
			// that bail an apostrophe in prose swallowed the rest of the file.
			for i++; i < len(content) && content[i] != '\'' && content[i] != '\n'; i++ {
				if content[i] == '\\' {
					i++
				}
			}
			i++
		default:
			i++
		}
	}
	return string(out)
}

// skipCallArguments returns the argument text of a Skip call whose opening
// parenthesis ends at start, balancing parentheses across lines while stepping over
// string and rune literals so a ")" inside a message does not end the scan early.
// It expects maskGoComments output, so comments are already blanked.
//
// Two views of the same text come back:
//
//   - literalText keeps the message bodies, because a gate may be named literally
//     there.
//   - identText additionally blanks the bodies of string and rune literals, so the
//     identifier pass sees only real Go identifiers. Without that, every ordinary
//     word in a skip message was resolved against the package's constant map: a
//     package holding `const region = "US_EAST_1"` reported US_EAST_1 as an
//     environment gate purely because a message said "no region configured", and
//     the guard then demanded a doc row for something that is not a gate.
//
// The rune branch stops at a newline exactly like the string branch, so an
// unterminated apostrophe cannot consume the remainder of the file and leave depth
// permanently above 0.
func skipCallArguments(content string, start int) (literalText string, identText string) {
	var literals, idents []byte
	// emit appends one source byte to each view, blanking it in the ident view
	// where requested. Newlines survive blanking so line structure stays intact.
	emit := func(b byte, hideFromIdents bool) {
		literals = append(literals, b)
		if hideFromIdents && b != '\n' {
			idents = append(idents, ' ')
			return
		}
		idents = append(idents, b)
	}

	depth := 1
	for i := start; i < len(content); {
		switch {
		case content[i] == '"':
			emit(content[i], true)
			for i++; i < len(content) && content[i] != '"' && content[i] != '\n'; i++ {
				emit(content[i], true)
				if content[i] == '\\' && i+1 < len(content) {
					i++
					emit(content[i], true)
				}
			}
			if i < len(content) && content[i] == '"' {
				emit(content[i], true)
				i++
			}
		case content[i] == '`':
			emit(content[i], true)
			for i++; i < len(content) && content[i] != '`'; i++ {
				emit(content[i], true)
			}
			if i < len(content) {
				emit(content[i], true)
				i++
			}
		case content[i] == '\'':
			emit(content[i], true)
			for i++; i < len(content) && content[i] != '\'' && content[i] != '\n'; i++ {
				emit(content[i], true)
				if content[i] == '\\' && i+1 < len(content) {
					i++
					emit(content[i], true)
				}
			}
			if i < len(content) && content[i] == '\'' {
				emit(content[i], true)
				i++
			}
		case content[i] == '(':
			depth++
			emit(content[i], false)
			i++
		case content[i] == ')':
			depth--
			if depth == 0 {
				return string(literals), string(idents)
			}
			emit(content[i], false)
			i++
		default:
			emit(content[i], false)
			i++
		}
	}
	return string(literals), string(idents)
}

// optInScannerFixture is a synthetic test file exercising the three shapes the
// scanner has to recognise. "SKIPCALL" stands in for "Skip" so this fixture is not
// itself picked up when the guard walks the repository and scans THIS file.
const optInScannerFixture = "package sample\n" +
	"\n" +
	"const sampleGateEnv = \"APEXLOAD_SAMPLE_GATE\"\n" +
	"\n" +
	"const (\n" +
	"\tsampleBenchEnv = \"APEXLOAD_SAMPLE_BENCH\"\n" +
	")\n" +
	"\n" +
	"func requireSample(t *testing.T) {\n" +
	"\tt.SKIPCALLf(\"SKIPPED (no corpus here). To run it:\\n\"+\n" +
	"\t\t\"  %s=1 go test ./...\",\n" +
	"\t\tsampleGateEnv)\n" +
	"}\n" +
	"\n" +
	"func requireBench(b *testing.B) {\n" +
	"\tb.SKIPCALLf(\"set %s=1 to run the sample benchmarks\", sampleBenchEnv)\n" +
	"}\n" +
	"\n" +
	"func requireLiteral(t *testing.T) {\n" +
	"\tt.SKIPCALL(\"set APEXLOAD_SAMPLE_LITERAL=1 (LR_Scripts is not a gate). See docs/SAMPLE_SUITES.md.\")\n" +
	"}\n"

// TestSkipMessageEnvVarsSeesGatesNamedByConstant is the scanner's own falsifiable
// test. The guard used to read only the FIRST LINE of a `t.Skip*` call and only
// accept a literal upper-case token there, so every gate written the way
// docs/qa/TEST_PROGRAM.md recommends — name passed as a format argument — was
// invisible, as was every `b.Skipf` on a benchmark.
func TestSkipMessageEnvVarsSeesGatesNamedByConstant(t *testing.T) {
	content := strings.ReplaceAll(optInScannerFixture, "SKIPCALL", "Skip")
	got := skipMessageEnvVars(content, stringConstants(content))
	want := []string{"APEXLOAD_SAMPLE_BENCH", "APEXLOAD_SAMPLE_GATE", "APEXLOAD_SAMPLE_LITERAL"}
	if !slices.Equal(got, want) {
		t.Fatalf("skipMessageEnvVars = %v, want %v", got, want)
	}
}

// TestSkipMessageEnvVarsSurvivesCommentsAndProse pins the three ways the scanner
// used to get a Skip call wrong. Each fixture is valid, gofmt-stable Go; "SKIPCALL"
// stands in for "Skip" so the fixtures are not themselves collected when the guard
// walks this file.
func TestSkipMessageEnvVarsSurvivesCommentsAndProse(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    []string
		defect  string
	}{
		{
			// A ")" inside a comment in the argument list closed the call early,
			// so the scan stopped before the constant naming the gate.
			name: "comment closing paren does not truncate the call",
			fixture: "package sample\n" +
				"\n" +
				"const gateC = \"APEXLOAD_GATE_C\"\n" +
				"\n" +
				"func requireC(t *testing.T) {\n" +
				"\tt.SKIPCALLf(\"set %s=1\", // see step 3)\n" +
				"\t\tgateC)\n" +
				"}\n",
			want:   []string{"APEXLOAD_GATE_C"},
			defect: "a real gate went undetected, which is the one failure this guard exists to prevent",
		},
		{
			// An apostrophe in a comment put the scanner into rune-literal mode
			// with no newline terminator, so it consumed to the next apostrophe
			// anywhere in the file, never closed the call, and returned the rest
			// of the file as the argument text.
			name: "apostrophe in a comment does not run the scan away to EOF",
			fixture: "package sample\n" +
				"\n" +
				"const gateA = \"APEXLOAD_GATE_A\"\n" +
				"\n" +
				"func requireA(t *testing.T) {\n" +
				"\tt.SKIPCALLf(\"set %s=1\",\n" +
				"\t\t// don't forget to export it\n" +
				"\t\tgateA)\n" +
				"}\n" +
				"\n" +
				"const other = \"SOME_OTHER_NAME\"\n" +
				"\n" +
				"func notAGate() string { return \"NOT_A_GATE_TOKEN\" }\n",
			want:   []string{"APEXLOAD_GATE_A"},
			defect: "every ALL_CAPS token in the rest of the file was reported as an environment gate",
		},
		{
			// Identifier extraction ran over the message text, so an ordinary
			// English word that collides with a package constant was resolved
			// as if it were the gate's name.
			name: "prose inside the message is not resolved as a Go identifier",
			fixture: "package sample\n" +
				"\n" +
				"const region = \"US_EAST_1\"\n" +
				"\n" +
				"func requireRegion(t *testing.T) {\n" +
				"\tt.SKIPCALL(\"no region configured for this suite\")\n" +
				"}\n",
			want:   nil,
			defect: "US_EAST_1 was invented as a gate and the guard demanded a doc row for it",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			content := strings.ReplaceAll(testCase.fixture, "SKIPCALL", "Skip")
			got := skipMessageEnvVars(content, stringConstants(content))
			if len(got) == 0 && len(testCase.want) == 0 {
				return
			}
			if !slices.Equal(got, testCase.want) {
				t.Fatalf("skipMessageEnvVars = %v, want %v — %s", got, testCase.want, testCase.defect)
			}
		})
	}
}

// TestNamesGateRequiresAWholeTokenMatch pins the doc guard's matcher. A bare
// strings.Contains let a new gate ride on a longer documented neighbour's row.
func TestNamesGateRequiresAWholeTokenMatch(t *testing.T) {
	const doc = "| `KAFKA_BROKERS`, `KAFKA_TOPIC` | Live Kafka |\n" +
		"| `jms_servicebus_integration` | Live Service Bus |\n"
	for _, present := range []string{"KAFKA_BROKERS", "KAFKA_TOPIC", "jms_servicebus_integration"} {
		if !namesGate(doc, present) {
			t.Fatalf("namesGate(doc, %q) = false, want true — the documented row must still count", present)
		}
	}
	for _, absent := range []string{"KAFKA_BROKER", "KAFKA_TOPI", "jms_servicebus_int", "MS_BROKERS"} {
		if namesGate(doc, absent) {
			t.Fatalf("namesGate(doc, %q) = true, but %q has no row of its own; it only overlaps a documented neighbour, "+
				"so an undocumented opt-in suite would pass the guard", absent, absent)
		}
	}
}

func readOptInSuitesDoc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(optInSuitesDocPath)
	if err != nil {
		t.Fatalf("read %s: %v — the opt-in suite inventory must exist", optInSuitesDocPath, err)
	}
	return string(data)
}

// TestOptInSuiteInventoryIsDocumented is the guard: every build-tag and
// environment-variable gate found in the repository's tests must appear in
// docs/qa/TEST_PROGRAM.md.
func TestOptInSuiteInventoryIsDocumented(t *testing.T) {
	inventory := collectOptInInventory(t)
	doc := readOptInSuitesDoc(t)

	if len(inventory.buildTags) == 0 && len(inventory.envVars) == 0 {
		t.Fatal("scanned the repository and found no opt-in gates at all — the scanner is broken, not the repository")
	}

	for tag, files := range inventory.buildTags {
		if !namesGate(doc, tag) {
			t.Errorf("build tag %q gates %v but is not documented in %s — an undocumented opt-in suite runs nowhere and nobody knows",
				tag, files, optInSuitesDocPath)
		}
	}
	for name, files := range inventory.envVars {
		if !namesGate(doc, name) {
			t.Errorf("environment gate %q gates %v but is not documented in %s", name, files, optInSuitesDocPath)
		}
	}
}

// namesGate reports whether text names gate as a whole token.
//
// A bare strings.Contains was satisfied by a longer neighbour: a new gate called
// KAFKA_BROKER (singular) passed because the documented KAFKA_BROKERS contains it,
// as would APEXLOAD_BROWSER_TEST against APEXLOAD_BROWSER_TESTS or a build tag
// jms_servicebus_int against jms_servicebus_integration. A suite could then ship
// with no inventory row of its own — the one thing this guard is supposed to make
// impossible — so the match is anchored on non-identifier characters.
func namesGate(text, gate string) bool {
	if gate == "" {
		return false
	}
	pattern := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])` + regexp.QuoteMeta(gate) + `(?:[^A-Za-z0-9_]|$)`)
	return pattern.MatchString(text)
}

// TestOptInSuiteRunScriptCoversEveryBuildTag pins the "discoverable" half: the
// documented tags must also be reachable from one command, otherwise the doc is a
// list of suites nobody can conveniently run.
func TestOptInSuiteRunScriptCoversEveryBuildTag(t *testing.T) {
	inventory := collectOptInInventory(t)
	data, err := os.ReadFile(optInSuitesRunScript)
	if err != nil {
		t.Fatalf("read %s: %v — the opt-in runner must exist", optInSuitesRunScript, err)
	}
	script := string(data)
	for tag, files := range inventory.buildTags {
		if !namesGate(script, tag) {
			t.Errorf("build tag %q gates %v but %s never runs it — add a run_suite line", tag, files, optInSuitesRunScript)
		}
	}
}

// TestCorpusSmokeGateIsAutoDiscovering pins the O57 behaviour change itself: the
// corpus suite must not be a bare "env var unset -> skip". It has to probe known
// locations so a fetched corpus is picked up automatically.
func TestCorpusSmokeGateIsAutoDiscovering(t *testing.T) {
	roots := corpusDefaultRoots()
	if len(roots) < 2 {
		t.Fatalf("corpusDefaultRoots() = %v, want the fetch script's destination plus at least one fallback", roots)
	}
	if !slices.Contains(roots, "/tmp/apexload-lr-scripts-corpus") {
		t.Fatalf("corpusDefaultRoots() = %v, want the scripts/fetch-lr-scripts-corpus.sh default destination", roots)
	}
	// A path with no Scripts directory is not a corpus.
	if corpusRootIsUsable(t.TempDir()) {
		t.Fatal("corpusRootIsUsable accepted an empty directory")
	}
	// Nor is an EMPTY Scripts directory. /tmp/apexload-lr-scripts-corpus is a fixed
	// world-writable path and the fetch script materializes Scripts/ before it is
	// populated, so accepting a bare directory promotes the suite from "skip with
	// instructions" to a mandatory run that then fails the whole package on every
	// subsequent invocation.
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "Scripts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if corpusRootIsUsable(empty) {
		t.Fatalf("corpusRootIsUsable(%q) = true for an empty Scripts directory; an interrupted fetch must skip, not fail the package", empty)
	}
	// Nor is a PARTIAL one. This is the case a threshold of one got wrong: a root
	// holding a single script is not the pinned corpus, and admitting it promoted
	// the suite to a mandatory run that then failed on "discovered 1 script
	// directories" — the very failure the skip exists to avoid. Auto-discovery has
	// to use the same bar the assertion does.
	partial := writeCorpusFixture(t, 1)
	if corpusRootIsUsable(partial) {
		t.Fatalf("corpusRootIsUsable(%q) = true for a root holding 1 script directory; auto-discovery must not admit a corpus that TestPinnedLRScriptsCorpusSmoke will then fail on (it wants %d)",
			partial, corpusMinScriptDirs)
	}
	// The explicit override keeps the weaker admission test, so an operator who
	// named a partial checkout still reaches the suite's own, more informative
	// assertion instead of a silent skip.
	if !corpusRootHoldsAnyScript(partial) {
		t.Fatalf("corpusRootHoldsAnyScript(%q) = false; an explicitly named checkout that holds scripts must resolve, not skip", partial)
	}
	if corpusRootHoldsAnyScript(empty) {
		t.Fatalf("corpusRootHoldsAnyScript(%q) = true for an empty Scripts directory", empty)
	}
	// ...and a root that meets the bar is usable.
	full := writeCorpusFixture(t, corpusMinScriptDirs)
	if !corpusRootIsUsable(full) {
		t.Fatalf("corpusRootIsUsable(%q) = false, want true for a checkout holding %d script directories", full, corpusMinScriptDirs)
	}
}

// writeCorpusFixture builds a checkout whose Scripts directory holds exactly
// scriptDirs script directories.
func writeCorpusFixture(t *testing.T, scriptDirs int) string {
	t.Helper()
	root := t.TempDir()
	for index := 0; index < scriptDirs; index++ {
		dir := filepath.Join(root, "Scripts", fmt.Sprintf("WebHttpHtml%02d", index))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Sample.usr"), []byte("[General]\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}
