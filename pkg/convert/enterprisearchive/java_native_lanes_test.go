package enterprisearchive

// Quarantined native-Java-lane orchestration.
//
// ConvertJava (java_import.go) always produces a single combined java_vuser
// runner draft: it never calls the protocol-specific statement/HTTP/JDBC/SOAP
// lanes and never merges lane results. The orchestration below was the old
// executable-lane pipeline. It is retained *only* so the static Java idiom
// analyzers (JMS mapping, statement classification, conservation backfill)
// keep their focused coverage — see java_native_analysis_test.go, which is the
// only caller.
//
// What the quarantine does and does not cover, stated precisely: only the
// orchestration moved here. The lane bodies it drives are still production
// resident and are still compiled into every shipped binary —
// convertJavaStatement and attachJavaTruststoreArtifact (java_import.go),
// javaHTTPParsedActions (java_http_idioms.go), javaDatabaseParsedActions
// (java_jdbc_idioms.go), javaSOAPParsedActions (java_soap_idioms.go),
// newJavaScriptForSurface and classifyJavaArchive (java_classify.go), and the
// statement mapper registry reached only through convertJavaStatement
// (java_idiom_registry.go). Editing one of those bodies changes nothing for any
// user, so each one carries a pointer back to this file saying so.
//
// The guarantee is therefore not "unbuildable" but "uncalled", and it is
// enforced by two tests that cover different halves of it. Neither is redundant,
// because only one of them is independent of how a re-entry is spelled.
//
// TestNoProductionCodeCallsQuarantinedJavaLanes parses every non-test file in
// this package and fails if any of them names a symbol in
// javaQuarantinedLaneEntryPoints. That guard is name-based, and it is therefore
// necessary but NOT sufficient: the retired lanes stay reachable through
// internals that no list entry can cover. javaWebToParsedFunction and
// javaHTTPClientToParsedFunction (java_http_idioms.go) and convertJavaJMSAction
// (java_import.go) are shared with the statement mappers in
// java_idiom_registry.go, so listing them would report those mappers as
// violations. A maintainer who drives collectJavaParsedFunctions with
// javaWebToParsedFunction from inside ConvertJava — or inlines the equivalent
// loop — rebuilds the HTTP lane while naming nothing this scan can see.
//
// TestConvertJavaNeverEntersNativeLanes is what closes that gap and is the
// guarantee that actually holds for every spelling: it drives one archive per
// retired lane (HTTP, JDBC, JMS, SOAP) through the real ConvertJava entry point,
// requires exactly one combined java_vuser runner draft, and fails on any action
// type outside the combined-runner allow-list no matter which symbol produced
// it. TestConvertZipJavaProtocolFamiliesAllRemainCombined
// (java_combined_import_test.go) pins the same shape from ConvertZip.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// javaQuarantinedLaneEntryPoints maps each production-resident entry point of
// the retired executable lane pipeline to the production file that declares it.
// Every name here must still be declared in that file (a rename or move must
// update this list) and must have no reference from any other non-test file.
//
// javaParsedActionsFor and javaSOAPCallToParsedFunction are listed even though
// they are lane internals rather than lane heads: they are the substitutes a
// re-entry would most plausibly reach for (javaHTTPParsedActions is itself just
// a javaParsedActionsFor call with a converter closure), and both are
// production-dead today. classifyJavaArchive and its reachability variant are
// listed for the same reason — re-adding a call would silently put a second full
// lifecycle-reachability pass on every Java import.
//
// The remaining internals — javaWebToParsedFunction,
// javaHTTPClientToParsedFunction, convertJavaJMSAction — cannot be listed,
// because the statement mappers in java_idiom_registry.go call them legitimately
// from inside the dead subgraph; see the file comment above for what covers
// those instead.
var javaQuarantinedLaneEntryPoints = map[string]string{
	"convertJavaStatement":                "java_import.go",
	"attachJavaTruststoreArtifact":        "java_import.go",
	"javaHTTPParsedActions":               "java_http_idioms.go",
	"javaParsedActionsFor":                "java_http_idioms.go",
	"javaDatabaseParsedActions":           "java_jdbc_idioms.go",
	"javaSOAPParsedActions":               "java_soap_idioms.go",
	"javaSOAPCallToParsedFunction":        "java_soap_idioms.go",
	"newJavaScriptForSurface":             "java_classify.go",
	"classifyJavaArchive":                 "java_classify.go",
	"classifyJavaArchiveWithReachability": "java_classify.go",
	"javaStatementMapperRegistry":         "java_idiom_registry.go",
}

// TestNoProductionCodeCallsQuarantinedJavaLanes enforces the name-based half of
// the quarantine that the file comment above describes. The lane bodies stay in
// production files, so "quarantined" can only mean "no production caller"; this
// test is what makes that true for every symbol it can name. One line anywhere
// in a non-test file — for example a call to javaDatabaseParsedActions followed
// by ConvertDatabase — would put an executable lane back on a live import path,
// and that line fails here.
//
// It is deliberately not the only guard. A re-entry that reaches a lane through
// an unlistable internal (see javaQuarantinedLaneEntryPoints) is invisible to
// this scan and is caught by TestConvertJavaNeverEntersNativeLanes instead.
//
// References from inside another quarantined entry point (convertJavaStatement
// recursing, or its use of javaStatementMapperRegistry) stay inside the dead
// subgraph and are allowed; only entries from live production code matter.
func TestNoProductionCodeCallsQuarantinedJavaLanes(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	declaredIn := map[string]string{}
	violations := []string{}
	scanned := 0

	note := func(enclosing string, ident *ast.Ident) {
		if _, guarded := javaQuarantinedLaneEntryPoints[ident.Name]; !guarded {
			return
		}
		position := fset.Position(ident.Pos())
		violations = append(violations, fmt.Sprintf("%s:%d %s references quarantined lane entry point %s",
			position.Filename, position.Line, enclosing, ident.Name))
	}
	record := func(enclosing string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			switch typed := n.(type) {
			case *ast.SelectorExpr:
				// Only the qualified expression can name a package-level symbol;
				// the selector itself is a field or method name.
				ast.Inspect(typed.X, func(inner ast.Node) bool {
					if ident, ok := inner.(*ast.Ident); ok {
						note(enclosing, ident)
					}
					return true
				})
				return false
			case *ast.Ident:
				note(enclosing, typed)
			}
			return true
		})
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		scanned++
		for _, decl := range file.Decls {
			switch typed := decl.(type) {
			case *ast.FuncDecl:
				if _, guarded := javaQuarantinedLaneEntryPoints[typed.Name.Name]; guarded {
					declaredIn[typed.Name.Name] = name
					continue
				}
				record("func "+typed.Name.Name, typed)
			case *ast.GenDecl:
				for _, spec := range typed.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						record("declaration", spec)
						continue
					}
					quarantinedVar := false
					for _, ident := range valueSpec.Names {
						if _, guarded := javaQuarantinedLaneEntryPoints[ident.Name]; guarded {
							declaredIn[ident.Name] = name
							quarantinedVar = true
						}
					}
					if quarantinedVar {
						continue
					}
					for _, value := range valueSpec.Values {
						record("package-level declaration", value)
					}
				}
			}
		}
	}

	// Guard the guard: a cwd surprise or a rename must fail loudly instead of
	// scanning nothing and reporting success.
	if scanned < 20 {
		t.Fatalf("scanned only %d production files in the package directory; the quarantine scan is not looking at the real sources", scanned)
	}
	missing := []string{}
	for symbol, wantFile := range javaQuarantinedLaneEntryPoints {
		gotFile, ok := declaredIn[symbol]
		if !ok {
			missing = append(missing, symbol+" (expected in "+wantFile+")")
			continue
		}
		if gotFile != wantFile {
			missing = append(missing, symbol+" moved from "+wantFile+" to "+gotFile)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("quarantined lane entry points are no longer where this guard expects them, so the scan below proves nothing: %v", missing)
	}
	sort.Strings(violations)
	for _, violation := range violations {
		t.Errorf("production code re-entered a retired native Java lane: %s", violation)
	}
}

// javaCombinedRunnerActionTypes is the complete set of action types the
// production combined runner draft may emit. Every action ConvertJava produces
// comes from javaVuserLifecycleAction (java_import.go), which hardcodes
// java_vuser_run; nothing else on the live Java path appends an action.
//
// This is deliberately an allow-list. The previous deny-list of retired lane
// types rotted silently: it named "http_request" and "soap_request", which no
// converter in this package emits — the HTTP and SOAP lanes route through
// convertAction (action_convert.go), which sets Action.Type from the HTTP verb,
// so they actually emit "GET"/"POST". A deny-list entry that matches nothing
// still reads as coverage; an allow-list cannot rot when a lane renames its
// action type, because the new name is not on it either.
var javaCombinedRunnerActionTypes = map[string]bool{
	"java_vuser_run": true,
}

// TestConvertJavaNeverEntersNativeLanes pins the retirement of the executable
// native Java lanes: production ConvertJava must always return exactly one
// combined java_vuser runner draft, whatever protocol idioms the source uses.
// It is the guard that does not depend on how a re-entry is spelled, so it
// covers the lane internals javaQuarantinedLaneEntryPoints cannot name.
//
// One archive per retired lane, and each case opens with two controls. The
// archive is run through the quarantined lane pipeline and must still emit that
// lane's executable action, which proves the source genuinely exercises the lane
// rather than falling through to a review disposition; and that action type must
// not itself be allow-listed, which proves the production assertion below can
// fail. Without both, a case would pass vacuously the moment its fixture stopped
// matching the lane's idiom detector.
func TestConvertJavaNeverEntersNativeLanes(t *testing.T) {
	tests := []struct {
		name string
		// laneActionType is what the retired lane emits for this archive today,
		// taken from the lane's own output rather than from a guessed name.
		laneActionType string
		source         string
	}{
		{
			name:           "http",
			laneActionType: "GET",
			source: `public class Actions {
  public int action() throws Throwable {
    web.url("Home", "URL=https://example.test/home", web.LAST);
    return 0;
  }
}`,
		},
		{
			name:           "jdbc",
			laneActionType: "db_exec",
			source: `import java.sql.*;
public class Actions {
  public int action() throws Throwable {
    PreparedStatement ps = conn.prepareStatement("insert into audit values (1)");
    ps.executeUpdate();
    return 0;
  }
}`,
		},
		{
			name:           "jms",
			laneActionType: "jms_publish",
			source: `import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    producer.send(session.createTextMessage("hi"));
    return 0;
  }
}`,
		},
		{
			name:           "soap",
			laneActionType: "POST",
			source: `import javax.xml.soap.*;
public class Actions {
  public int action() throws Throwable {
    String endpoint = "https://soap.example.test/services/order";
    String soapEnvelope = "<soapenv:Envelope><soapenv:Body><order>1</order></soapenv:Body></soapenv:Envelope>";
    SOAPMessage message = null;
    SOAPConnection connection = SOAPConnectionFactory.newInstance().createConnection();
    connection.call(message, endpoint);
    return 0;
  }
}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"Actions.java": tc.source}
			detected := DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}

			quarantined, err := convertJavaNativeAnalysisForTest(files, ConvertOptions{}, detected, nil)
			if err != nil {
				t.Fatalf("quarantined lane pipeline: %v", err)
			}
			if _, ok := javaFindAction(quarantined, tc.laneActionType); !ok {
				t.Fatalf("control failed: the quarantined %s lane no longer emits %q for this archive, so the production assertions below can no longer fail; types=%v",
					tc.name, tc.laneActionType, javaActionTypesAcrossScripts(quarantined))
			}
			if javaCombinedRunnerActionTypes[tc.laneActionType] {
				t.Fatalf("control failed: retired %s lane action type %q is itself allow-listed as combined-runner output, so the production assertion below cannot fail",
					tc.name, tc.laneActionType)
			}

			result, err := ConvertJava(files, ConvertOptions{}, detected, nil)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			if got := len(result.effectiveScripts()); got != 1 {
				t.Fatalf("ConvertJava produced %d scripts, want exactly 1 combined runner draft (multiple scripts mean lane merging came back)", got)
			}
			if result.DetectedProtocol == nil {
				t.Fatal("ConvertJava returned no DetectedProtocol")
			}
			if got := result.DetectedProtocol.APEXLoadProtocol; got != "java_vuser" {
				t.Fatalf("DetectedProtocol.APEXLoadProtocol = %q, want %q", got, "java_vuser")
			}
			if got := result.DetectedProtocol.ImportMode; got != ImportModeExternalRunnerRequired {
				t.Fatalf("DetectedProtocol.ImportMode = %q, want %q", got, ImportModeExternalRunnerRequired)
			}
			if got := result.Script.Protocol; got != "java_vuser" {
				t.Fatalf("script protocol = %q, want %q", got, "java_vuser")
			}
			producedTypes := javaActionTypesAcrossScripts(result)
			if len(producedTypes) == 0 {
				t.Fatal("ConvertJava produced no actions at all; an empty draft would satisfy the allow-list below vacuously")
			}
			for _, actionType := range producedTypes {
				if javaCombinedRunnerActionTypes[actionType] {
					continue
				}
				action, _ := javaFindAction(result, actionType)
				t.Fatalf("ConvertJava emitted action type %q (target %q), which is not combined-runner output; the retired %s lane is back on the live import path (all types=%v)",
					actionType, action.Target, tc.name, producedTypes)
			}
		})
	}
}

// javaLeafStatements enumerates the leaf statements the converter actually visits
// — recursing into transparent blocks and treating opaque blocks as a single leaf
// — so conservation can be checked against the real traversal.
func javaLeafStatements(program javaProgram) []javaStatement {
	out := make([]javaStatement, 0)
	for _, phase := range []string{"init", "action", "teardown"} {
		for _, statement := range javaLifecycleStatements(program, phase) {
			out = append(out, javaLeafStatementList(statement)...)
		}
	}
	return out
}

// backfillJavaConservation guarantees the no-silent-drop invariant: every leaf
// statement the converter visits ends with at least one SourceReview disposition.
// Executable lanes (HTTP/JDBC/SOAP) consume some contributor statements (SQL/bind
// assignments, prepareStatement bookkeeping) without emitting their own mapping;
// this records an honest disposition for any such leaf that no lane mapped.
func backfillJavaConservation(result *ConvertResult, program javaProgram) {
	if result == nil {
		return
	}
	if result.SourceReview == nil {
		result.SourceReview = &SourceReview{}
	}
	mapped := map[javaStatementKey]bool{}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SourceFile == "" || mapping.StartLine == 0 {
			continue
		}
		mapped[javaStatementKey{file: mapping.SourceFile, startLine: mapping.StartLine, endLine: mapping.EndLine}] = true
	}
	for _, statement := range javaLeafStatements(program) {
		if javaNormalizeStatementText(statement.Text) == "" {
			continue
		}
		key := javaStatementKey{file: statement.File, startLine: statement.StartLine, endLine: statement.EndLine}
		if mapped[key] {
			continue
		}
		mapped[key] = true
		status, mode, notes := classifyJavaResidueStatement(javaNormalizeStatementText(statement.Text))
		appendSourceReviewMapping(result, SourceReviewMapping{
			SourceFile:  statement.File,
			StartLine:   statement.StartLine,
			EndLine:     statement.EndLine,
			Function:    "java_statement",
			TargetKind:  "disposition",
			Status:      status,
			SupportMode: string(mode),
			Notes:       notes,
		})
	}
}

type javaStatementKey struct {
	file      string
	startLine int
	endLine   int
}

// classifyJavaResidueStatement gives an honest disposition to a leaf statement no
// lane claimed: no-op for inert lines, an executable-contributor note for
// SQL/state plumbing the executable lanes consumed, a named review for known
// no-runtime families, else a generic review.
func classifyJavaResidueStatement(text string) (status string, mode SourceFunctionSupportMode, notes string) {
	switch {
	case isJavaInertStatement(text):
		return "converted", SourceFunctionSupportNativeNoop, "Java statement has no executable replay effect (no-op disposition)."
	case javaStatementLooksLikeDatabase(text) || javaSQLAssignRe.MatchString(text) || javaSetBindRe.MatchString(text):
		return "converted", SourceFunctionSupportExecutable, "Contributor consumed by the executable database lane."
	case javaNamedReviewReason(text) != "":
		return "review_only", SourceFunctionSupportReviewOnly, "Preserved for manual review: " + javaNamedReviewReason(text) + "."
	default:
		return "review_only", SourceFunctionSupportReviewOnly, "Unmapped Java statement preserved for review."
	}
}

func convertJavaStatementLane(files map[string]string, opts ConvertOptions, detected DetectedProtocol, program javaProgram, parseWarnings []string, profile JavaProfile, secondary bool, skipHTTP bool, skipDatabase bool) (*ConvertResult, error) {
	surface := javaSurfaceJMS
	if !profile.Surfaces[javaSurfaceJMS] {
		surface = profile.Primary
	}
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append(append([]string(nil), detected.Warnings...), parseWarnings...),
		Script:           newJavaScriptForSurface(opts, profile, surface),
	}
	if secondary && result.Script != nil {
		result.Script.Name = result.Script.Name + "_review"
	}
	state := &javaImportState{
		env:          newJavaStringEnv(),
		profile:      profile,
		skipHTTP:     skipHTTP,
		skipDatabase: skipDatabase,
	}
	for _, phase := range []string{"init", "action", "teardown"} {
		statements := javaLifecycleStatements(program, phase)
		for _, statement := range statements {
			state.statementCount++
			result.Stats.TotalFunctions++
			if err := convertJavaStatement(result, state, files, phase, statement); err != nil {
				return nil, err
			}
		}
	}
	appendJavaUnparsedRegions(result, state, program)
	attachJavaTruststoreArtifact(result, files)

	if opts.AllowJavaVuserRunner && (state.jmsActions == 0 || state.unrecognizedAction > 0 || state.configReview > 0) {
		return convertJavaVuserRunnerDraft(files, opts, detected, program, result.Warnings, state, nil), nil
	}

	if state.jmsActions == 0 || state.unrecognizedAction > 0 || state.configReview > 0 {
		detected = markDetectedReviewOnly(detected, "")
		if profile.Surfaces[javaSurfaceJMS] {
			detected.APEXLoadProtocol = profile.JMSProtocol
		}
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Operator review of unmapped Java statements", "JMS broker configuration verification")
		result.DetectedProtocol = &detected
		result.ReviewArtifact = NewReviewArtifact(detected, files, nil, map[string]any{
			"reason":                         "partial_java_mapping",
			"jms_actions":                    state.jmsActions,
			"unrecognized_action_statements": state.unrecognizedAction,
			"configuration_review_items":     state.configReview,
			"recognized_lr_actions":          state.recognizedLRActions,
		})
		// Distinguish "every statement classified but no executable protocol
		// target" (a pure lr.* script) from genuinely unmapped statements, so the
		// warning is honest rather than alarming.
		if state.unrecognizedAction == 0 && state.configReview == 0 && state.recognizedLRActions > 0 {
			result.Warnings = append(result.Warnings, "Java Vuser import classified every lr.* statement, but the script has no executable APEX protocol target (no JMS actions); it remains review-only.")
		} else {
			result.Warnings = append(result.Warnings, "Java Vuser import produced a partial JMS draft and remains review-only until unmapped statements are resolved.")
		}
	} else {
		detected.ImportMode = ImportModeExecutable
		detected.APEXLoadProtocol = profile.JMSProtocol
		detected.CanonicalSourceProtocol = "java_vuser"
		result.DetectedProtocol = &detected
	}
	if len(result.Script.Actions) == 0 {
		result.Script.Actions = append(result.Script.Actions, javaSourceReviewAction(javaStatement{
			Text:      "No executable JMS actions were recognized.",
			StartLine: 1,
			EndLine:   1,
		}, "action", "no_jms_actions"))
		result.Stats.ReviewOnlyFunctions++
	}
	return result, nil
}

func appendJavaUnparsedRegions(result *ConvertResult, state *javaImportState, program javaProgram) {
	for _, statement := range program.Unparsed {
		state.statementCount++
		result.Stats.TotalFunctions++
		appendJavaReview(result, state, statement, "action", "unparsed_java_region", true)
	}
}

func mergeJavaLaneResults(lanes []*ConvertResult, detected DetectedProtocol, files map[string]string, profile JavaProfile) *ConvertResult {
	if len(lanes) == 1 {
		lanes[0].Scripts = lanes[0].effectiveScripts()
		return lanes[0]
	}
	merged := &ConvertResult{
		DetectedProtocol: &DetectedProtocol{
			CanonicalSourceProtocol: "java_vuser",
			APEXLoadProtocol:        "multi",
			ImportMode:              ImportModeExecutable,
			Confidence:              detected.Confidence,
			DisplayName:             "Java Vuser Multi-Protocol",
			SourceType:              detected.SourceType,
			Signals:                 detected.Signals,
			Warnings:                detected.Warnings,
			RequiredEvidence:        detected.RequiredEvidence,
		},
		Warnings: append([]string(nil), detected.Warnings...),
		Scripts:  make([]*dsl.Script, 0, len(lanes)),
	}
	for _, lane := range lanes {
		if lane == nil {
			continue
		}
		merged.Warnings = append(merged.Warnings, lane.Warnings...)
		merged.Stats.TotalFunctions += lane.Stats.TotalFunctions
		merged.Stats.ConvertedActions += lane.Stats.ConvertedActions
		merged.Stats.LogicalActions += lane.Stats.LogicalActions
		merged.Stats.RawRequestActions += lane.Stats.RawRequestActions
		merged.Stats.CollapsedRequests += lane.Stats.CollapsedRequests
		merged.Stats.TransactionMarkers += lane.Stats.TransactionMarkers
		merged.Stats.ContributorFunctions += lane.Stats.ContributorFunctions
		merged.Stats.Extractors += lane.Stats.Extractors
		merged.Stats.ThinkTimes += lane.Stats.ThinkTimes
		merged.Stats.Transactions += lane.Stats.Transactions
		merged.Stats.NativeFunctions += lane.Stats.NativeFunctions
		merged.Stats.ReviewOnlyFunctions += lane.Stats.ReviewOnlyFunctions
		merged.Stats.ExternalRequiredFunctions += lane.Stats.ExternalRequiredFunctions
		merged.Stats.CustomCallFunctions += lane.Stats.CustomCallFunctions
		merged.Stats.Skipped += lane.Stats.Skipped
		merged.Stats.UnsupportedFuncs = append(merged.Stats.UnsupportedFuncs, lane.Stats.UnsupportedFuncs...)
		if lane.SourceReview != nil {
			for _, mapping := range lane.SourceReview.Mappings {
				appendSourceReviewMapping(merged, mapping)
			}
		}
		if lane.Script != nil {
			merged.Scripts = append(merged.Scripts, lane.Script)
		}
	}
	if len(merged.Scripts) > 0 {
		merged.Script = merged.Scripts[0]
	}
	if profile.Surfaces[javaSurfaceReview] {
		reviewDetected := *merged.DetectedProtocol
		reviewDetected.ImportMode = ImportModeReviewOnly
		merged.ReviewArtifact = NewReviewArtifact(reviewDetected, files, nil, map[string]any{
			"reason":          "java_named_review_surfaces",
			"review_families": profile.ReviewFamilies,
		})
	}
	merged.Warnings = uniqueSortedStrings(merged.Warnings)
	merged.Stats.UnsupportedFuncs = uniqueSortedStrings(merged.Stats.UnsupportedFuncs)
	return merged
}
