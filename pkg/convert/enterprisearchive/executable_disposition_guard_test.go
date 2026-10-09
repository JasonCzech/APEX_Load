package enterprisearchive

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// executable_disposition_guard_test.go closes the gap left by the two registry
// completeness suites (TestWebHTTPFunctionRegistryResolvesEveryPublishedFunction
// and TestSourceFunctionRegistryCoversDocumentedFunctions). Both of those end at
// assertSourceReviewSupportMode, which compares the mapping's support_mode to the
// registry entry the mapping was populated from — a tautology. Neither proves the
// substantive half of an `executable` claim: that the function actually produces
// something executable.
//
// This guard is machine-derived: it walks BOTH registries, selects every function
// the registry classifies `executable`, and for each one asserts three falsifiable
// properties of the conversion:
//
//  1. The emitted DSL script differs from the same script converted without the
//     call, once positional metadata (source line numbers) is normalized away. An
//     `executable` function that leaves the runnable artifact otherwise identical
//     executed nothing. The normalization is load-bearing: the converter stamps
//     metadata.source_line on every emitted action, so without it ANY inserted
//     line — a bare comment included — satisfies this check and it can never fail.
//     TestExecutableDispositionGuardRejectsNonEmittingLines pins that.
//  2. It emits at least one source-review mapping that is not a review row —
//     target_kind != "review" and status in {converted, contributor}.
//  3. Every such mapping's target_path resolves to a node that actually exists in
//     the emitted script. A mapping that points at a path the script does not
//     contain is a coverage claim with nothing behind it.
//
// Reclassify a function in the embedded registry (and the expectations table) if it
// stops being executable; do not relax this guard.

// executableGuardAnchorBefore/After bracket the function under test with a real
// request on each side. Several executable functions only reach their executable
// path with a preceding request (web_find attaches its check backwards) or a
// following one (lr_think_time attaches forward, and header/cookie mutations apply
// to the next request); without both, they fall back to preserved review metadata
// and the guard would measure the fallback instead of the claim.
const (
	executableGuardAnchorBefore = "\tweb_url(\"GuardAnchorBefore\", \"URL=https://example.test/before\", LAST);\n"
	executableGuardAnchorAfter  = "\tweb_url(\"GuardAnchorAfter\", \"URL=https://example.test/after\", LAST);\n"
)

// executableGuardPrelude supplies the state a few executable functions need before
// they can do anything observable — you cannot revert an auto header that was never
// added. The prelude is applied to the baseline conversion too, so it never by
// itself accounts for the diff the guard requires.
func executableGuardPrelude(name string) string {
	switch name {
	case "web_revert_auto_header", "web_remove_auto_header", "web_cleanup_auto_headers":
		return "\tweb_add_auto_header(\"X-Env\", \"test\");\n"
	case "web_remove_cookie":
		return "\tweb_add_cookie(\"SessionID=abc; domain=example.test\");\n"
	}
	return ""
}

func convertExecutableGuardSource(t *testing.T, body string) *ConvertResult {
	t.Helper()
	source := "#include \"web_api.h\"\n\nAction()\n{\n" + body + "\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, source)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v\nsource:\n%s", err, source)
	}
	if result == nil || result.Script == nil {
		t.Fatalf("convert produced no script for source:\n%s", source)
	}
	return result
}

// volatileScriptPositionKeys are the positional fields the converter stamps onto
// emitted nodes (sourceFunctionMetadata writes "source_line"; the preserved
// runtime-control entries write "line"). They shift whenever a line is inserted
// anywhere above them, so leaving them in the comparison makes criterion (1) — "the
// emitted script must differ" — true for ANY inserted line, including a comment
// that emits nothing at all. Comparing with them removed is what makes that
// criterion a property of the CALL rather than of its line number.
var volatileScriptPositionKeys = map[string]bool{
	"source_line": true,
	"line":        true,
}

func stripVolatileScriptPositions(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			if volatileScriptPositionKeys[key] {
				continue
			}
			out[key] = stripVolatileScriptPositions(inner)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, inner := range typed {
			out = append(out, stripVolatileScriptPositions(inner))
		}
		return out
	default:
		return value
	}
}

// marshalGuardScript returns two things: a line-number-independent rendering of the
// script used for the "did anything change" comparison, and the decoded script used
// for target_path resolution. json.Marshal sorts map keys, so the first is stable.
func marshalGuardScript(t *testing.T, result *ConvertResult) (string, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal script: %v", err)
	}
	comparable, err := json.Marshal(stripVolatileScriptPositions(decoded))
	if err != nil {
		t.Fatalf("marshal normalized script: %v", err)
	}
	return string(comparable), decoded
}

// scriptPathExists resolves a source-review target_path against the emitted script
// JSON. It understands the path shapes the converter produces: dotted object keys
// ("config.http.auth"), indexed elements ("actions[0].extractors[1]"), the "applies
// to whichever request it lands on" wildcard ("actions[].headers.Cookie"), and map
// keys that themselves contain dots
// ("config.web_runtime.additional_attributes.web_set_option.MaxTotalConnections",
// where the converter stores a single "web_set_option.MaxTotalConnections" key).
func scriptPathExists(root map[string]any, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	return resolveScriptPathSegments([]any{any(root)}, strings.Split(path, "."))
}

func resolveScriptPathSegments(candidates []any, segments []string) bool {
	if len(segments) == 0 {
		return true
	}
	// A map key may itself contain dots, so try consuming an increasing number of
	// segments as one literal key before giving up.
	for take := 1; take <= len(segments); take++ {
		next := stepScriptPath(candidates, strings.Join(segments[:take], "."))
		if len(next) > 0 && resolveScriptPathSegments(next, segments[take:]) {
			return true
		}
	}
	return false
}

func stepScriptPath(candidates []any, segment string) []any {
	key := segment
	index := -1
	wildcard := false
	if open := strings.Index(segment, "["); open >= 0 && strings.HasSuffix(segment, "]") {
		key = segment[:open]
		inner := segment[open+1 : len(segment)-1]
		if inner == "" {
			wildcard = true
		} else {
			parsed, err := strconv.Atoi(inner)
			if err != nil {
				return nil
			}
			index = parsed
		}
	}
	next := make([]any, 0, len(candidates))
	for _, candidate := range candidates {
		object, ok := candidate.(map[string]any)
		if !ok {
			continue
		}
		value, ok := object[key]
		if !ok {
			continue
		}
		switch {
		case wildcard:
			elements, ok := value.([]any)
			if !ok {
				continue
			}
			next = append(next, elements...)
		case index >= 0:
			elements, ok := value.([]any)
			if !ok || index >= len(elements) {
				continue
			}
			next = append(next, elements[index])
		default:
			next = append(next, value)
		}
	}
	return next
}

// executableDispositionViolation returns the empty string when name really does
// produce an executable artifact, or a human-readable violation otherwise. It is a
// value-returning check (rather than a t.Fatalf wall) so the guard's own teeth can
// be tested — see TestExecutableDispositionGuardRejectsReviewOnlyFunctions.
func executableDispositionViolation(t *testing.T, name string, call string) string {
	t.Helper()
	prelude := executableGuardPrelude(name)
	baseline := convertExecutableGuardSource(t, prelude+executableGuardAnchorBefore+executableGuardAnchorAfter)
	baselineJSON, _ := marshalGuardScript(t, baseline)

	converted := convertExecutableGuardSource(t, prelude+executableGuardAnchorBefore+call+executableGuardAnchorAfter)
	convertedJSON, convertedScript := marshalGuardScript(t, converted)

	// (1) The runnable artifact must actually change, ignoring line numbers.
	if convertedJSON == baselineJSON {
		return "the emitted script is identical (modulo source line numbers) to the same script without the call — nothing executable was produced\nscript: " + convertedJSON
	}

	// (2)+(3) The claim must be backed by a non-review mapping whose target exists.
	if converted.SourceReview == nil {
		return "no source review emitted"
	}
	executableMappings := 0
	for _, mapping := range converted.SourceReview.Mappings {
		if mapping.Function != name {
			continue
		}
		switch mapping.Status {
		case "converted", "contributor":
		default:
			// review / review_only / skipped / unsupported / external_required
			continue
		}
		if mapping.TargetKind == "review" {
			continue
		}
		if !scriptPathExists(convertedScript, mapping.TargetPath) {
			return "source-review target_path " + strconv.Quote(mapping.TargetPath) +
				" does not resolve in the emitted script — the executable claim points at nothing\nscript: " + convertedJSON
		}
		executableMappings++
	}
	if executableMappings == 0 {
		return "every source-review mapping it produced is a review row, so no executable target was recorded"
	}
	return ""
}

func assertExecutableDisposition(t *testing.T, def SourceFunctionDefinition, call string) {
	t.Helper()
	if violation := executableDispositionViolation(t, def.Name, call); violation != "" {
		t.Fatalf("%s is classified %s but %s", def.Name, def.SupportMode, violation)
	}
}

// TestExecutableWebFunctionsEmitExecutableArtifacts walks the published Web
// (HTTP/HTML) registry and proves the `executable` half of the coverage matrix.
func TestExecutableWebFunctionsEmitExecutableArtifacts(t *testing.T) {
	checked := 0
	for _, def := range allWebHTTPFunctionDefinitions() {
		if def.SupportMode != SourceFunctionSupportExecutable {
			continue
		}
		checked++
		def := def
		t.Run(def.Name, func(t *testing.T) {
			assertExecutableDisposition(t, def, "\t"+sampleWebFunctionCall(def.Name)+"\n")
		})
	}
	if checked < 40 {
		t.Fatalf("guard walked %d executable web functions, want the full published executable set (>=40) — the registry walk is not wired up", checked)
	}
}

// TestExecutableSourceFunctionsEmitExecutableArtifacts does the same for the
// C/VUser utility baseline registry.
func TestExecutableSourceFunctionsEmitExecutableArtifacts(t *testing.T) {
	checked := 0
	for _, def := range allSourceFunctionDefinitions() {
		if def.SupportMode != SourceFunctionSupportExecutable {
			continue
		}
		checked++
		def := def
		t.Run(def.Name, func(t *testing.T) {
			call := strings.TrimRight(sampleSourceFunctionCall(def), "\n") + "\n"
			assertExecutableDisposition(t, def, call)
		})
	}
	// The floor only proves the registry walk is wired up. The JSON/XML
	// document helpers left the executable set because the runtime does not
	// evaluate their queries.
	if checked < 50 {
		t.Fatalf("guard walked %d executable C/VUser functions, want the documented executable baseline (>=50) — the registry walk is not wired up", checked)
	}
}

// TestExecutableDispositionGuardRejectsNonEmittingLines is criterion (1)'s own
// negative control. Inserting a line that emits nothing — a C comment — must be
// reported as a violation by criterion (1) specifically. Without the positional
// normalization in marshalGuardScript this fails: the inserted line shifts the
// trailing anchor's metadata.source_line, the two scripts differ, and criterion (1)
// becomes unreachable for all of the guard's subtests.
func TestExecutableDispositionGuardRejectsNonEmittingLines(t *testing.T) {
	violation := executableDispositionViolation(t, "web_url", "\t/* this line emits nothing */\n")
	if violation == "" {
		t.Fatal("the guard accepted a source line that emits nothing — criterion (1) has no teeth")
	}
	if !strings.Contains(violation, "nothing executable was produced") {
		t.Fatalf("expected criterion (1) to fire for a comment-only line, got: %s", violation)
	}
}

// TestExecutableDispositionGuardRejectsReviewOnlyFunctions is the guard's negative
// control. A guard that passes for everything proves nothing, so this pins that
// review_only functions — which by definition must NOT produce an executable step —
// are rejected by exactly the check the two suites above apply.
func TestExecutableDispositionGuardRejectsReviewOnlyFunctions(t *testing.T) {
	reviewOnly := []string{"web_set_pac", "web_save_header", "web_util_set_request_url", "web_get_int_property"}
	for _, name := range reviewOnly {
		def, ok := lookupSourceFunction(name)
		if !ok {
			t.Fatalf("%s is not in any registry — fix the control sample", name)
		}
		if def.SupportMode != SourceFunctionSupportReviewOnly {
			t.Fatalf("%s is classified %s, expected review_only for the negative control", name, def.SupportMode)
		}
		if violation := executableDispositionViolation(t, name, "\t"+sampleWebFunctionCall(name)+"\n"); violation == "" {
			t.Fatalf("%s is review_only but the executable guard accepted it — the guard has no teeth", name)
		}
	}
}
