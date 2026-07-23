package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestJavaCombinedMappableEntriesBoundsVariableScopeSnapshots(t *testing.T) {
	var source strings.Builder
	source.WriteString("public class Actions {\n  public int action() {\n")
	const declarations = 3_000
	for index := 0; index < declarations; index++ {
		fmt.Fprintf(&source, "    String value%04d = \"\";\n", index)
	}
	source.WriteString("    return 0;\n  }\n}\n")

	program := mustParseJavaTestProgram(
		t,
		map[string]string{"Actions.java": source.String()},
	)
	started := time.Now()
	collection := collectJavaCombinedMappableEntries(program)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("bounded mappable-entry collection took %s", elapsed)
	}
	if !strings.Contains(collection.blocker, "variable-scope analysis") {
		t.Fatalf("scope-work blocker = %q, want deterministic variable-scope blocker", collection.blocker)
	}
	if collection.scopeSnapshotBindings > javaVariableScopeSnapshotBindingLimit {
		t.Fatalf(
			"scope snapshot bindings = %d, limit = %d",
			collection.scopeSnapshotBindings,
			javaVariableScopeSnapshotBindingLimit,
		)
	}
	if len(collection.entries) < declarations {
		t.Fatalf(
			"mappable entries = %d, want source evidence retained for %d declarations",
			len(collection.entries),
			declarations,
		)
	}

	result, err := ConvertJava(
		map[string]string{"Actions.java": source.String()},
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "variable-scope analysis") {
		t.Fatalf("runner-semantics blockers = %q, want scope-work blocker", blockers)
	}
	if got := result.Stats.NativeFunctions + result.Stats.ExternalRequiredFunctions +
		result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions; got != result.Stats.TotalFunctions {
		t.Fatalf("support counters = %d, want TotalFunctions=%d", got, result.Stats.TotalFunctions)
	}
}

func TestJavaOverloadWorkGuardCountsEnumConstructorResolution(t *testing.T) {
	const (
		overloads = 2_000
		constants = 40
	)
	var source strings.Builder
	source.WriteString("public enum Generated {\n")
	for index := 0; index < constants; index++ {
		if index > 0 {
			source.WriteString(",\n")
		}
		fmt.Fprintf(&source, "  VALUE_%04d(null)", index)
	}
	source.WriteString(";\n")
	for index := 0; index < overloads; index++ {
		fmt.Fprintf(
			&source,
			"  Generated(GeneratedType%04d value) {}\n",
			index,
		)
	}
	source.WriteString("}\n")

	program, warnings := parseJavaProgram(
		map[string]string{"Generated.java": source.String()},
	)
	for _, warning := range warnings {
		if !strings.Contains(warning, "no Java methods found") {
			t.Fatalf("parse warnings = %v", warnings)
		}
	}
	constructorCount := 0
	if len(program.Classes) > 0 {
		constructorCount = len(program.Classes[0].Constructors)
	}
	if len(program.Classes) != 1 || constructorCount != overloads {
		t.Fatalf(
			"parsed classes/constructors = %d/%d, want 1/%d",
			len(program.Classes),
			constructorCount,
			overloads,
		)
	}
	started := time.Now()
	analysis := analyzeJavaOverloadResolutionWork(program)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("enum overload preflight took %s", elapsed)
	}
	if !strings.Contains(analysis.blocker, "candidate-comparison limit") {
		t.Fatalf("enum overload blocker = %q, want candidate-comparison limit", analysis.blocker)
	}
	if analysis.candidateComparisons != javaOverloadCandidateComparisonLimit+1 {
		t.Fatalf(
			"candidate comparisons = %d, want saturated marker %d",
			analysis.candidateComparisons,
			javaOverloadCandidateComparisonLimit+1,
		)
	}
}

func TestJavaLRBindingCachesBoundMaximumImportScope(t *testing.T) {
	imports := make([]string, 0, maxJavaAnalysisImportCount)
	for index := 0; index < maxJavaAnalysisImportCount-2; index++ {
		imports = append(imports, fmt.Sprintf("example.pkg%04d.Type%04d", index, index))
	}
	imports = append(
		imports,
		"lrapi.*",
		"static lrapi.lr.start_transaction",
	)
	owner := javaClass{
		File:            "Actions.java",
		Name:            "Actions",
		Imports:         imports,
		Methods:         map[string]javaMethod{},
		MethodOverloads: map[string][]javaMethod{},
	}
	index := newJavaEvidenceIndex(javaProgram{Classes: []javaClass{owner}})
	started := time.Now()
	for iteration := 0; iteration < 10_000; iteration++ {
		if parts := javaLRCallPartsBound(
			`lr.end_transaction("x", lr.AUTO);`,
			owner,
			javaMethod{},
			index,
			nil,
		); len(parts) != 1 {
			t.Fatalf("qualified LR parts at iteration %d = %#v, want one", iteration, parts)
		}
		if parts := javaLRCallPartsBound(
			`start_transaction("x");`,
			owner,
			javaMethod{},
			index,
			nil,
		); len(parts) != 1 {
			t.Fatalf("static LR parts at iteration %d = %#v, want one", iteration, parts)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cached maximum-import LR binding took %s", elapsed)
	}
	if len(index.importScopes) != 1 {
		t.Fatalf("import scopes = %d, want one cached source scope", len(index.importScopes))
	}
	scope := index.importScope(owner)
	if !scope.lrSimpleReady || !scope.lrStaticReady {
		t.Fatalf(
			"LR import caches = simple:%t static:%t, want both ready",
			scope.lrSimpleReady,
			scope.lrStaticReady,
		)
	}
	guard := newJavaOverloadWorkGuard(javaProgram{Classes: []javaClass{owner}})
	for iteration := 0; iteration < 10_000; iteration++ {
		if !guard.hasStaticMethodImport(owner, "start_transaction") {
			t.Fatalf("static import lookup %d did not resolve", iteration)
		}
	}
	if len(guard.staticMethodImports) != 1 {
		t.Fatalf(
			"overload static-import caches = %d, want one source file",
			len(guard.staticMethodImports),
		)
	}
}
