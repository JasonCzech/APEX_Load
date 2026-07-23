package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestJavaEvidenceExplicitImportPrecedesSamePackageType(t *testing.T) {
	files := map[string]string{
		"p/Actions.java": `package p;
import q.Helper;
public class Actions {
  public int action() {
    new Helper().danger();
    return 0;
  }
}`,
		"p/Helper.java": `package p;
public class Helper {
  public void danger() {}
}`,
		"q/Helper.java": `package q;
import lrapi.lr;
public class Helper {
  public void danger() { lr.decrypt("explicit-import"); }
}`,
	}
	program := mustParseJavaTestProgram(t, files)
	actions, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("selected Actions class not found")
	}
	index := newJavaEvidenceIndex(program)
	helper, ok := index.classForType(actions, "Helper")
	if !ok || javaQualifiedClassName(helper) != "q.Helper" {
		t.Fatalf("Helper resolved to %q, want q.Helper", javaQualifiedClassName(helper))
	}

	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, explicit imported helper was not reached", blockers)
	}
}

func TestJavaEvidenceImportScopeDeduplicatesExactImportsAndRejectsConflicts(t *testing.T) {
	program := javaProgram{Classes: []javaClass{
		{File: "p/Actions.java", Name: "Actions", Package: "p"},
		{File: "p/Helper.java", Name: "Helper", Package: "p"},
		{File: "q/Helper.java", Name: "Helper", Package: "q"},
		{File: "r/Helper.java", Name: "Helper", Package: "r"},
	}}
	index := newJavaEvidenceIndex(program)
	owner := program.Classes[0]
	owner.Imports = []string{
		"q.Helper",
		"q.Helper",
		"static q.Helper.run",
		"static q.Helper.run",
	}
	scope := index.importScope(owner)
	if len(scope.explicitTypes["helper"]) != 1 ||
		len(scope.staticExact["run"]) != 1 {
		t.Fatalf(
			"deduplicated import scope = explicit %#v static %#v",
			scope.explicitTypes,
			scope.staticExact,
		)
	}
	helper, ok := index.classForType(owner, "Helper")
	if !ok || javaQualifiedClassName(helper) != "q.Helper" {
		t.Fatalf("duplicate exact import resolved to %q, want q.Helper", javaQualifiedClassName(helper))
	}

	conflicting := owner
	conflicting.File = "p/Conflicting.java"
	conflicting.Imports = []string{"q.Helper", "r.Helper"}
	if helper, ok := index.classForType(conflicting, "Helper"); ok {
		t.Fatalf("conflicting imports resolved to %q, want fail-closed ambiguity", javaQualifiedClassName(helper))
	}
}

func TestConvertJavaExplicitLRImportBeatsSamePackageLRClass(t *testing.T) {
	files := map[string]string{
		"p/Actions.java": `package p;
import lrapi.lr;
public class Actions {
  public int action() {
    lr.start_transaction("ImportedLR");
    lr.end_transaction("ImportedLR", lr.AUTO);
    return 0;
  }
}`,
		"p/lr.java": `package p;
public class lr {}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	emits, _ := result.Script.Actions[0].
		Metadata["emits_business_transactions"].(bool)
	if !emits {
		t.Fatalf("action metadata = %#v, explicit lrapi.lr transaction was not recognized", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaResolvesArbitraryDepthReceiverChains(t *testing.T) {
	t.Run("returned method chain", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions {
  public int action() {
    factory().middle().helper().danger();
    return 0;
  }
  private Factory factory() { return new Factory(); }
}`,
			"Factory.java": `public class Factory {
  public Middle middle() { return new Middle(); }
}`,
			"Middle.java": `public class Middle {
  public Helper helper() { return new Helper(); }
}`,
			"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() { lr.decrypt("deep-returned-chain"); }
}`,
		}
		result, err := ConvertJava(
			files,
			ConvertOptions{},
			DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
			nil,
		)
		if err != nil {
			t.Fatalf("ConvertJava: %v", err)
		}
		blockers := strings.Join(
			javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
			"\n",
		)
		if !strings.Contains(blockers, "lr_decrypt") {
			t.Fatalf("runner semantic blockers = %q, deepest returned receiver was not reached", blockers)
		}
	})

	t.Run("member field chain", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions {
  private Root root = new Root();
  public int action() {
    root.middle.helper.danger();
    return 0;
  }
}`,
			"Root.java": `public class Root {
  public Middle middle = new Middle();
}`,
			"Middle.java": `public class Middle {
  public Helper helper = new Helper();
}`,
			"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() {
    lr.start_transaction("DeepMember");
    lr.end_transaction("DeepMember", lr.AUTO);
  }
}`,
		}
		result, err := ConvertJava(
			files,
			ConvertOptions{},
			DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
			nil,
		)
		if err != nil {
			t.Fatalf("ConvertJava: %v", err)
		}
		emits, _ := result.Script.Actions[0].
			Metadata["emits_business_transactions"].(bool)
		if !emits {
			t.Fatalf("action metadata = %#v, deepest member receiver transaction was not reached", result.Script.Actions[0].Metadata)
		}
	})

	for _, test := range []struct {
		name  string
		files map[string]string
	}{
		{
			name: "method return then fields",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    factory().middle.helper.danger();
    return 0;
  }
  private Factory factory() { return new Factory(); }
}`,
				"Factory.java": `public class Factory {
  public Middle middle = new Middle();
}`,
				"Middle.java": `public class Middle {
  public Helper helper = new Helper();
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() { lr.decrypt("method-then-fields"); }
}`,
			},
		},
		{
			name: "field then method return then field",
			files: map[string]string{
				"Actions.java": `public class Actions {
  private Root root = new Root();
  public int action() {
    root.factory().helper.danger();
    return 0;
  }
}`,
				"Root.java": `public class Root {
  public Container factory() { return new Container(); }
}`,
				"Container.java": `public class Container {
  public Helper helper = new Helper();
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() { lr.decrypt("field-method-field"); }
}`,
			},
		},
		{
			name: "cast around intermediate method result",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    ((Container) factory().middle()).helper.danger();
    return 0;
  }
  private Factory factory() { return new Factory(); }
}`,
				"Factory.java": `public class Factory {
  public Object middle() { return new Container(); }
}`,
				"Container.java": `public class Container {
  public Helper helper = new Helper();
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() { lr.decrypt("cast-intermediate"); }
}`,
			},
		},
		{
			name: "value root shadows qualified archived type",
			files: map[string]string{
				"p/Actions.java": `package p;
public class Actions {
  private q.Root root = new q.Root();
  public int action() {
    root.middle.danger();
    return 0;
  }
}`,
				"q/Root.java": `package q;
public class Root {
  public Helper middle = new Helper();
}`,
				"q/Helper.java": `package q;
import lrapi.lr;
public class Helper {
  public void danger() { lr.decrypt("value-shadow"); }
}`,
				"root/middle.java": `package root;
public class middle {
  public static void danger() {}
}`,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := ConvertJava(
				test.files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, mixed receiver chain was not reached", blockers)
			}
		})
	}
}

func TestConvertJavaReceiverChainLimitFailsClosed(t *testing.T) {
	var expression strings.Builder
	expression.WriteString("node()")
	for index := 0; index < maxJavaReceiverChainLinks+8; index++ {
		expression.WriteString(".next()")
	}
	expression.WriteString(".danger();")
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    ` + expression.String() + `
    return 0;
  }
  private Node node() { return new Node(); }
}`,
		"Node.java": `import lrapi.lr;
public class Node {
  public Node next() { return this; }
  public void danger() { lr.decrypt("after-chain-limit"); }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "receiver-chain-limit") {
		t.Fatalf("runner semantic blockers = %q, want deterministic receiver-chain limit", blockers)
	}

	expression.Reset()
	expression.WriteString("node()")
	for index := 0; index < maxJavaReceiverChainLinks+8; index++ {
		expression.WriteString(".next")
	}
	expression.WriteString(".danger();")
	files["Actions.java"] = `public class Actions {
  public int action() {
    ` + expression.String() + `
    return 0;
  }
  private Node node() { return new Node(); }
}`
	files["Node.java"] = `import lrapi.lr;
public class Node {
  public Node next = this;
  public void danger() { lr.decrypt("after-field-chain-limit"); }
}`
	result, err = ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava field chain: %v", err)
	}
	blockers = strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "receiver-chain-limit") {
		t.Fatalf("field-chain blockers = %q, want deterministic receiver-chain limit", blockers)
	}
}

func TestConvertJavaQualifiedNestedTypeConstructionFailsClosed(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  static class Helper {
    void danger() { lr.decrypt("qualified-nested"); }
  }
  public int action() {
    new Actions.Helper().danger();
    return 0;
  }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "Actions.Helper") ||
		!strings.Contains(blockers, "reachability") {
		t.Fatalf("runner semantic blockers = %q, qualified nested type must fail closed", blockers)
	}
}

func TestConvertJavaNestedReturnedReceiverFailsClosed(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  static class Helper {
    void danger() { lr.decrypt("nested-return"); }
  }
  private Helper helper() { return new Helper(); }
  public int action() {
    helper().danger();
    return 0;
  }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "helper().danger()") ||
		!strings.Contains(blockers, "receiver") {
		t.Fatalf("runner semantic blockers = %q, nested returned receiver must fail closed", blockers)
	}

	files["Actions.java"] = `import lrapi.lr;
public class Actions {
  static class Helper {
    void danger() { lr.decrypt("nested-field"); }
  }
  private Helper helper;
  public int action() {
    helper.danger();
    return 0;
  }
}`
	result, err = ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava nested field: %v", err)
	}
	blockers = strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "helper") ||
		!strings.Contains(blockers, "receiver") {
		t.Fatalf("runner semantic blockers = %q, nested field receiver must fail closed", blockers)
	}
}

func TestJavaEnumMetadataPreflightIsCachedAndLinear(t *testing.T) {
	const fieldCount = 8_000
	var source strings.Builder
	source.WriteString(`import java.lang.annotation.*;
@Target(ElementType.TYPE_USE)
@interface A { int value(); }
public enum Generated {
  VALUE;
`)
	for index := 0; index < fieldCount; index++ {
		fmt.Fprintf(&source, "  int @A(1) [] field%05d;\n", index)
	}
	source.WriteString("  public int marker() { return 1; }\n}\n")
	program := mustParseJavaTestProgram(
		t,
		map[string]string{"Generated.java": source.String()},
	)
	var generated javaClass
	for _, class := range program.Classes {
		if class.Name == "Generated" {
			generated = class
			break
		}
	}
	if !generated.EnumConstantsReady ||
		len(generated.EnumConstantNames) != 1 ||
		!generated.EnumConstantSet["VALUE"] {
		t.Fatalf(
			"enum metadata ready/names/set = %t/%v/%v",
			generated.EnumConstantsReady,
			generated.EnumConstantNames,
			generated.EnumConstantSet,
		)
	}
	started := time.Now()
	analysis := analyzeJavaOverloadResolutionWork(program)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cached enum preflight took %s", elapsed)
	}
	if analysis.blocker != "" {
		t.Fatalf("cached enum preflight blocker = %q", analysis.blocker)
	}
}

func TestConvertJavaEnumReachabilityCachesFieldTypes(t *testing.T) {
	const fieldCount = 750
	var generated strings.Builder
	generated.WriteString(`import java.lang.annotation.*;
@Target(ElementType.TYPE_USE)
@interface A { int value(); }
public enum Actions {
  VALUE;
`)
	for index := 0; index < fieldCount; index++ {
		fmt.Fprintf(&generated, "  int @A(1) [] field%05d;\n", index)
	}
	generated.WriteString(`  public int marker() { return 1; }
  public int action() { return marker(); }
}
`)
	files := map[string]string{
		"Actions.java": generated.String(),
	}
	program := mustParseJavaTestProgram(t, files)
	started := time.Now()
	reachability := buildJavaVuserRuntimeReachability(program)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cached enum reachability took %s", elapsed)
	}
	if builds := len(reachability.evidenceIndex.hierarchyFieldTypes); builds != 1 {
		t.Fatalf(
			"field-type index builds = %d, want one for the selected enum",
			builds,
		)
	}
	if len(reachability.unresolved) != 0 {
		t.Fatalf("enum reachability unresolved = %v", reachability.unresolved)
	}

	started = time.Now()
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("full cached enum conversion took %s", elapsed)
	}
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if strings.Contains(blockers, "candidate-comparison limit") ||
		strings.Contains(blockers, "could not be bound") {
		t.Fatalf("full enum conversion blockers = %q", blockers)
	}
}

func TestJavaMemberReceiverLookupUsesDirectFieldIndexAndWorkCap(t *testing.T) {
	const unrelatedFields = 20_000
	fields := make([]javaStatement, 0, unrelatedFields+1)
	fields = append(fields, javaStatement{Text: "Node next;"})
	for index := 0; index < unrelatedFields; index++ {
		fields = append(fields, javaStatement{
			Text: fmt.Sprintf("int field%05d;", index),
		})
	}
	node := javaClass{
		File:            "Node.java",
		Name:            "Node",
		Package:         "p",
		Fields:          fields,
		Methods:         map[string]javaMethod{},
		MethodOverloads: map[string][]javaMethod{},
	}
	actions := javaClass{
		File:            "Actions.java",
		Name:            "Actions",
		Package:         "p",
		Methods:         map[string]javaMethod{},
		MethodOverloads: map[string][]javaMethod{},
	}
	index := newJavaEvidenceIndex(javaProgram{Classes: []javaClass{actions, node}})
	var qualifier strings.Builder
	qualifier.WriteString("root")
	for hop := 0; hop < maxJavaReceiverChainLinks-1; hop++ {
		qualifier.WriteString(".next")
	}
	started := time.Now()
	resolved, archived, exact, exceeded := index.resolveMemberReceiver(
		actions,
		qualifier.String(),
		map[string]string{"root": "Node"},
	)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("indexed member-chain lookup took %s", elapsed)
	}
	if exceeded || !archived || !exact || resolved.Name != "Node" {
		t.Fatalf(
			"member receiver = %q archived=%t exact=%t exceeded=%t",
			resolved.Name,
			archived,
			exact,
			exceeded,
		)
	}

	tooDeep := qualifier.String() + ".next.next"
	if _, archived, exact, exceeded := index.resolveMemberReceiver(
		actions,
		tooDeep,
		map[string]string{"root": "Node"},
	); !archived || exact || !exceeded {
		t.Fatalf(
			"over-limit member chain archived=%t exact=%t exceeded=%t",
			archived,
			exact,
			exceeded,
		)
	}

	index.totalMemberLookups = maxJavaTotalMemberLookups
	work := 0
	if _, _, exceeded := index.fieldTypeInHierarchy(node, "next", &work); !exceeded {
		t.Fatal("global member-receiver lookup budget was not enforced")
	}
}

func TestConvertJavaCompetingStaticImportCannotForgeLRTransactionMetadata(t *testing.T) {
	tests := []struct {
		name              string
		lrImport          string
		otherImport       string
		wantAmbiguityGate bool
	}{
		{
			name:        "single import takes precedence over LR wildcard",
			lrImport:    "import static lrapi.lr.*;",
			otherImport: "import static support.Other.start_transaction;",
		},
		{
			name:              "competing wildcard owners fail closed",
			lrImport:          "import static lrapi.lr.*;",
			otherImport:       "import static support.Other.*;",
			wantAmbiguityGate: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": test.lrImport + "\n" + test.otherImport + `
public class Actions {
  public int action() {
    start_transaction(5);
    return 0;
  }
}`,
				"support/Other.java": `package support;
public class Other {
  public static int start_transaction(int value) { return value; }
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			if emits, _ := result.Script.Actions[0].
				Metadata["emits_business_transactions"].(bool); emits {
				t.Fatalf(
					"action metadata = %#v, competing archived static method must not be reported as an LR transaction",
					result.Script.Actions[0].Metadata,
				)
			}
			if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
				t.Fatalf(
					"transaction stats = %d/%d, want no invented LR transaction",
					result.Stats.Transactions,
					result.Stats.TransactionMarkers,
				)
			}
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			hasAmbiguityGate := strings.Contains(
				blockers,
				"ambiguous lrapi.lr static-import binding",
			)
			if hasAmbiguityGate != test.wantAmbiguityGate {
				t.Fatalf(
					"runner semantic blockers = %q, ambiguity gate=%t want %t",
					blockers,
					hasAmbiguityGate,
					test.wantAmbiguityGate,
				)
			}
		})
	}
}

func TestConvertJavaArchivedTypeCanShadowLrapiPackageQualifier(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    lrapi.lr.start_transaction(5);
    return 0;
  }
}`,
		"lrapi.java": `public class lrapi {
  public static Helper lr = new Helper();
}`,
		"Helper.java": `public class Helper {
  public int start_transaction(int value) { return value; }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].
		Metadata["emits_business_transactions"].(bool); emits {
		t.Fatalf(
			"action metadata = %#v, archived type lrapi shadows the package qualifier",
			result.Script.Actions[0].Metadata,
		)
	}
	if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
		t.Fatalf(
			"transaction stats = %d/%d, want no invented shim transaction",
			result.Stats.Transactions,
			result.Stats.TransactionMarkers,
		)
	}
	if result.SourceReview != nil {
		for _, mapping := range result.SourceReview.Mappings {
			if mapping.Function == "lr_start_transaction" {
				t.Fatalf("source mapping = %#v, shadowed package was classified as shim LR", mapping)
			}
		}
	}
}

func TestConvertJavaLRWildcardIgnoresNonInvocationSyntax(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "control keyword and local annotation",
			files: map[string]string{
				"Actions.java": `import static lrapi.lr.*;
public class Actions {
  public int action() {
    if (true);
    @SuppressWarnings("unchecked") java.util.List<String> values = null;
    return values == null ? 0 : 1;
  }
}`,
			},
		},
		{
			name: "this constructor invocation",
			files: map[string]string{
				"Actions.java": `import static lrapi.lr.*;
public class Actions {
  public Actions() { this(1); }
  private Actions(int value) {}
  public int action() { return 0; }
}`,
			},
		},
		{
			name: "super constructor invocation",
			files: map[string]string{
				"Actions.java": `import static lrapi.lr.*;
public class Actions extends Base {
  public Actions() { super(); }
  public int action() { return 0; }
}`,
				"Base.java": `public class Base {
  public Base() {}
}`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ConvertJava(
				test.files,
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
			for _, falseCall := range []string{
				"lr_if",
				"lr_suppresswarnings",
				"lr_this",
				"lr_super",
				"ambiguous lrapi.lr static-import binding",
			} {
				if strings.Contains(strings.ToLower(blockers), falseCall) {
					t.Fatalf(
						"runner semantic blockers = %q, Java syntax was classified as %s",
						blockers,
						falseCall,
					)
				}
			}
			if result.SourceReview != nil {
				for _, mapping := range result.SourceReview.Mappings {
					switch strings.ToLower(mapping.Function) {
					case "lr_if", "lr_suppresswarnings", "lr_this", "lr_super":
						t.Fatalf(
							"source mapping = %#v, Java syntax must not become an LR call",
							mapping,
						)
					}
				}
			}
		})
	}
}

func TestJavaOverloadPreflightChargesStaticWildcardImportFanout(t *testing.T) {
	const (
		wildcardOwners = 2048
		distinctCalls  = 1024
	)
	var source strings.Builder
	for index := 0; index < wildcardOwners; index++ {
		fmt.Fprintf(
			&source,
			"import static external.pkg%04d.Owner%04d.*;\n",
			index,
			index,
		)
	}
	source.WriteString("public class Actions {\n  public int action() {\n")
	for index := 0; index < distinctCalls; index++ {
		fmt.Fprintf(&source, "    method%04d();\n", index)
	}
	source.WriteString("    return 0;\n  }\n}\n")

	program := mustParseJavaTestProgram(
		t,
		map[string]string{"Actions.java": source.String()},
	)
	started := time.Now()
	analysis := analyzeJavaOverloadResolutionWork(program)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("static-import fan-out preflight took %s", elapsed)
	}
	if !strings.Contains(analysis.blocker, "candidate-comparison limit") {
		t.Fatalf(
			"static-import fan-out blocker = %q, want candidate-comparison limit",
			analysis.blocker,
		)
	}
	if analysis.candidateComparisons != javaOverloadCandidateComparisonLimit+1 {
		t.Fatalf(
			"candidate comparisons = %d, want saturated marker %d",
			analysis.candidateComparisons,
			javaOverloadCandidateComparisonLimit+1,
		)
	}

	started = time.Now()
	result, err := ConvertJava(
		map[string]string{"Actions.java": source.String()},
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf(
			"guarded static-import conversion repeated fan-out work and took %s",
			elapsed,
		)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "candidate-comparison limit") {
		t.Fatalf("runner semantic blockers = %q, want preflight work-limit blocker", blockers)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatal("guarded conversion did not preserve source-review mappings")
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SupportMode != string(SourceFunctionSupportReviewOnly) {
			t.Fatalf(
				"mapping = %#v, incomplete preflight must not claim analyzed external-runner support",
				mapping,
			)
		}
	}
}
