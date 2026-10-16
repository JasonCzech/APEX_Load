package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestConvertJavaRuntimeReachabilityDefersStoredCallbacks(t *testing.T) {
	tests := []struct {
		name        string
		actionBody  string
		wantBlocker bool
	}{
		{
			name: "stored block lambda",
			actionBody: `
    Runnable callback = () -> {
      lr.decrypt("deferred-lambda");
    };`,
		},
		{
			name: "stored anonymous method",
			actionBody: `
    Runnable callback = new Runnable() {
      public void run() {
        lr.decrypt("deferred-anonymous-method");
      }
    };`,
		},
		{
			name: "anonymous eager initializer",
			actionBody: `
    Object callback = new Object() {
      {
        lr.decrypt("eager-anonymous-initializer");
      }
    };`,
			wantBlocker: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {` + tc.actionBody + `
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
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			hasDecryptBlocker := strings.Contains(blockers, "lr_decrypt")
			if hasDecryptBlocker != tc.wantBlocker {
				t.Fatalf("runner semantic blockers = %q, want lr_decrypt blocker=%t", blockers, tc.wantBlocker)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityConservativelyTraversesUnknownOverload(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    run(null);
    return 0;
  }
  private void run(Object value) {}
  private void run(String value) {
    lr.decrypt("string-overload");
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
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, unknown overload must retain every same-arity target", blockers)
	}
}

func TestParseJavaFilePreservesVarargsMetadata(t *testing.T) {
	class, err := parseJavaFile("Actions.java", `public class Actions {
  public Actions(int fixed, String... names) {}
  private void helper(Object first, String... rest) {}
  public int action() { return 0; }
}`)
	if err != nil {
		t.Fatalf("parseJavaFile: %v", err)
	}
	if len(class.Constructors) != 1 {
		t.Fatalf("constructors = %#v, want one", class.Constructors)
	}
	constructor := class.Constructors[0]
	if !constructor.Varargs || len(constructor.Parameters) != 2 ||
		!constructor.Parameters[1].Varargs || constructor.Parameters[1].Type != "String" {
		t.Fatalf("constructor varargs metadata = %#v", constructor)
	}
	helpers := class.MethodOverloads["helper"]
	if len(helpers) != 1 || !helpers[0].Varargs ||
		len(helpers[0].Parameters) != 2 || !helpers[0].Parameters[1].Varargs {
		t.Fatalf("helper varargs metadata = %#v", helpers)
	}
}

func TestConvertJavaRuntimeReachabilityTraversesVarargsCalls(t *testing.T) {
	tests := []struct {
		name       string
		actionBody string
		helper     string
	}{
		{
			name:       "expanded helper arguments",
			actionBody: `guarded("one", "two");`,
			helper: `private void guarded(String... values) {
    lr.decrypt("expanded-helper");
  }`,
		},
		{
			name:       "empty helper arguments",
			actionBody: `guarded();`,
			helper: `private void guarded(String... values) {
    lr.decrypt("empty-helper");
  }`,
		},
		{
			name:       "expanded constructor arguments",
			actionBody: `new Helper("one", "two");`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
  ` + tc.helper + `
}`,
			}
			if strings.Contains(tc.name, "constructor") {
				files["Helper.java"] = `import lrapi.lr;
public class Helper {
  public Helper(String... values) {
    lr.decrypt("expanded-constructor");
  }
}`
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
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, varargs target was not traversed", blockers)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityPrefersFixedArityOverVarargs(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    guarded("known-string");
    return 0;
  }
  private void guarded(String value) {}
  private void guarded(Object... values) {
    lr.decrypt("must-not-select-varargs");
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
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, fixed-arity overload must win Java's first applicability phase", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityConservesUnmodeledReferenceConversions(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    guarded("known-string");
    return 0;
  }
  private void guarded(CharSequence value) {
    lr.decrypt("char-sequence-overload");
  }
  private void guarded(Object value) {}
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
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, unmodeled String-to-CharSequence conversion must retain its target", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityUsesVarargsInFixedArityPhase(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    String[] values = new String[]{"one"};
    guarded(values);
    return 0;
  }
  private void guarded(Object value) {}
  private void guarded(String... values) {
    lr.decrypt("array-selects-varargs-declaration");
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
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, String[] must select String... during Java fixed-arity resolution", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityPrefersPrimitiveWideningBeforeBoxing(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    guarded(1);
    return 0;
  }
  private void guarded(long value) {
    lr.decrypt("primitive-widening-overload");
  }
  private void guarded(Integer value) {}
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
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, Java strict widening phase must precede boxing", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityBlocksAmbiguousConstructorDelegation(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public Actions() {
    this(null);
  }
  private Actions(Object value) {}
  private Actions(String value) {
    lr.decrypt("string-constructor");
  }
  public int action() { return 0; }
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
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "resolved to 2 archived candidates") {
		t.Fatalf("runner semantic blockers = %q, ambiguous constructor delegation must fail closed", blockers)
	}
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, every ambiguous constructor candidate must remain review evidence", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityFindsInheritedMethodCalls(t *testing.T) {
	tests := []struct {
		name       string
		actionCall string
	}{
		{name: "implicit inherited receiver", actionCall: "guarded();"},
		{name: "explicit super receiver", actionCall: "super.guarded();"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions extends BaseActions {
  public int action() {
    ` + tc.actionCall + `
    return 0;
  }
}`,
				"BaseActions.java": `import lrapi.lr;
public class BaseActions {
  protected void guarded() {
    lr.decrypt("inherited-method");
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
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, inherited call must reach BaseActions.guarded", blockers)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityInitializesStaticFieldOwner(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    int value = Helper.VALUE;
    return value;
  }
}`,
		"Helper.java": `import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("static-field-owner");
  }
  public static int VALUE = 1;
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
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, Helper.VALUE must initialize Helper", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityInitializesStaticImportedAndExpressionFields(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "field selected through expression",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    Helper helper = null;
    return helper.VALUE;
  }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("expression-static-field-owner");
  }
  public static int VALUE = 1;
}`,
			},
		},
		{
			name: "exact static import",
			files: map[string]string{
				"Actions.java": `import static support.Helper.VALUE;
public class Actions {
  public int action() {
    return VALUE;
  }
}`,
				"support/Helper.java": `package support;
import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("exact-static-import-owner");
  }
  public static int VALUE = 1;
}`,
			},
		},
		{
			name: "wildcard static import",
			files: map[string]string{
				"Actions.java": `import static support.Helper.*;
public class Actions {
  public int action() {
    return VALUE;
  }
}`,
				"support/Helper.java": `package support;
import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("wildcard-static-import-owner");
  }
  public static int VALUE = 1;
}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertJava(
				tc.files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, static field access must initialize its archived owner", blockers)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityInitializesStaticFieldsThroughTypedExpressions(t *testing.T) {
	tests := []struct {
		name        string
		actionSetup string
		access      string
		helper      string
	}{
		{
			name:   "method call receiver",
			access: `helper().VALUE`,
			helper: `private Helper helper() { return null; }`,
		},
		{
			name:   "cast receiver",
			access: `((Helper) null).VALUE`,
		},
		{
			name:        "array element receiver",
			actionSetup: `Helper[] helpers = null;`,
			access:      `helpers[0].VALUE`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    ` + tc.actionSetup + `
    return ` + tc.access + `;
  }
  ` + tc.helper + `
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("typed-expression-static-field-owner");
  }
  public static int VALUE = 1;
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
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, typed receiver must initialize static field owner", blockers)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityDoesNotInitializeCompileTimeConstantOwner(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    return Helper.VALUE.length();
  }
}`,
		"Helper.java": `import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("must-not-run-for-inlined-constant");
  }
  public static final String VALUE = "literal";
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
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, reading an inlined compile-time constant must not initialize Helper", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityRecognizesConstantExpressions(t *testing.T) {
	tests := []struct {
		name       string
		fields     string
		extraFiles map[string]string
	}{
		{
			name: "literal arithmetic parentheses and unary operators",
			fields: `
  public static final int VALUE = -((1 + 2) * 4) + ~3;`,
		},
		{
			name: "String concatenation",
			fields: `
  public static final String VALUE = "prefix-" + (1 + 2);`,
		},
		{
			name: "empty String literal",
			fields: `
  public static final String VALUE = "";`,
		},
		{
			name: "same-class referenced constant",
			fields: `
  private static final int BASE = 40;
  public static final int VALUE = BASE + 2;`,
		},
		{
			name: "multi-declarator referenced constant",
			fields: `
  public static final int BASE = 40, VALUE = BASE + 2;`,
		},
		{
			name: "qualified archived referenced constant",
			fields: `
  public static final int VALUE = Constants.BASE + 2;`,
			extraFiles: map[string]string{
				"Constants.java": `public class Constants {
  public static final int BASE = 40;
}`,
			},
		},
		{
			name: "constant inherited through archived interface",
			fields: `
  public static final int VALUE = Holder.BASE + 2;`,
			extraFiles: map[string]string{
				"Holder.java": `public class Holder implements Constants {}`,
				"Constants.java": `public interface Constants {
  int BASE = 40;
}`,
			},
		},
		{
			name: "primitive constant cast",
			fields: `
  public static final int VALUE = (int) (1L + 2L);`,
		},
		{
			name: "decimal and hexadecimal floating literals",
			fields: `
  public static final double VALUE = 1F + 0x1.fp2;`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    Object value = Helper.VALUE;
    return value.hashCode();
  }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("must-not-run-for-constant-expression");
  }
` + tc.fields + `
}`,
			}
			for name, source := range tc.extraFiles {
				files[name] = source
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
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, constant-variable access must not initialize Helper", blockers)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityFailsClosedForNonconstantExpressions(t *testing.T) {
	tests := []struct {
		name   string
		fields string
	}{
		{
			name: "method invocation",
			fields: `
  public static final int VALUE = calculate();
  private static int calculate() { return 42; }`,
		},
		{
			name: "referenced non-final field",
			fields: `
  private static int BASE = 40;
  public static final int VALUE = BASE + 2;`,
		},
		{
			name: "later nonconstant multi-declarator",
			fields: `
  private static final int BASE = 40, VALUE = calculate();
  private static int calculate() { return BASE + 2; }`,
		},
		{
			name: "comparison operators across declarator comma",
			fields: `
  private static int A = 1 < 2 ? 1 : 0, VALUE = calculate() > 0 ? 1 : 0;
  private static int calculate() { return 42; }`,
		},
		{
			name: "field and invoked method share identifier",
			fields: `
  private static final int calculate = 1;
  public static final int VALUE = calculate();
  private static int calculate() { return 42; }`,
		},
		{
			name: "boxed type is not a constant variable",
			fields: `
  public static final Integer VALUE = 42;`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    return Helper.VALUE;
  }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  static {
    lr.decrypt("must-run-for-nonconstant-expression");
  }
` + tc.fields + `
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
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, ambiguous/nonconstant initializer must initialize Helper", blockers)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityHandlesImplicitInterfaceFields(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantBlocker bool
	}{
		{
			name:  "constant interface field does not initialize owner",
			value: `int VALUE = 42;`,
		},
		{
			name:        "nonconstant interface field initializes owner",
			value:       `int VALUE = guarded();`,
			wantBlocker: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    return Helper.VALUE;
  }
}`,
				"Helper.java": `import lrapi.lr;
public interface Helper {
  ` + tc.value + `
  int OTHER = guarded();
  static int guarded() {
    lr.decrypt("implicit-interface-initialization");
    return 1;
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
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			hasBlocker := strings.Contains(blockers, "lr_decrypt")
			if hasBlocker != tc.wantBlocker {
				t.Fatalf("runner semantic blockers = %q, want interface initialization blocker=%t", blockers, tc.wantBlocker)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityResolvesInheritedInterfaceFields(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantBlocker bool
	}{
		{
			name:  "inherited constant does not initialize interface",
			value: `int VALUE = 42;`,
		},
		{
			name:        "inherited nonconstant initializes declaring interface",
			value:       `int VALUE = guarded();`,
			wantBlocker: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() { return Helper.VALUE; }
}`,
				"Helper.java": `public class Helper implements Constants {}`,
				"Constants.java": `import lrapi.lr;
public interface Constants {
  ` + tc.value + `
  int OTHER = guarded();
  static int guarded() {
    lr.decrypt("inherited-interface-field");
    return 1;
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
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			hasBlocker := strings.Contains(blockers, "lr_decrypt")
			if hasBlocker != tc.wantBlocker {
				t.Fatalf("runner semantic blockers = %q, want inherited interface blocker=%t", blockers, tc.wantBlocker)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityResolvesWildcardImportedInterfaceField(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import static Holder.*;
public class Actions {
  public int action() { return VALUE; }
}`,
		"Holder.java": `public class Holder implements Constants {}`,
		"Constants.java": `import lrapi.lr;
public interface Constants {
  int VALUE = guarded();
  static int guarded() {
    lr.decrypt("wildcard-inherited-interface-field");
    return 1;
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
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, wildcard import must enumerate inherited interface field", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityInitializesOnlyDefaultMethodInterfaces(t *testing.T) {
	tests := []struct {
		name        string
		defaultBody string
		wantBlocker bool
	}{
		{
			name: "plain interface is not initialized with implementing class",
		},
		{
			name:        "default-method interface initializes with implementing class",
			defaultBody: `default void noop() {}`,
			wantBlocker: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    new Helper();
    return 0;
  }
}`,
				"Helper.java": `public class Helper implements Defaults {}`,
				"Defaults.java": `import lrapi.lr;
public interface Defaults {
  int MARKER = guarded();
  ` + tc.defaultBody + `
  static int guarded() {
    lr.decrypt("default-interface-initialization");
    return 1;
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
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			hasBlocker := strings.Contains(blockers, "lr_decrypt")
			if hasBlocker != tc.wantBlocker {
				t.Fatalf("runner semantic blockers = %q, want default-interface blocker=%t", blockers, tc.wantBlocker)
			}
		})
	}
}

func TestConvertJavaRuntimeReachabilityDoesNotInitializeParentOnInterfaceInitialization(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() { return Child.VALUE; }
}`,
		"Child.java": `public interface Child extends ParentDefault {
  int VALUE = childValue();
  static int childValue() { return 1; }
}`,
		"ParentDefault.java": `import lrapi.lr;
public interface ParentDefault {
  int MARKER = guarded();
  default void noop() {}
  static int guarded() {
    lr.decrypt("must-not-initialize-parent-interface");
    return 1;
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
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, initializing an interface must not initialize its parent interfaces", blockers)
	}
}

func TestConvertJavaRuntimeReachabilityInitializesEnumConstantOwner(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "static initializer",
			source: `import lrapi.lr;
public enum Helper {
  VALUE;
  static {
    lr.decrypt("enum-constant-owner");
  }
}`,
		},
		{
			name: "constant arguments and enum constructor",
			source: `import lrapi.lr;
public enum Helper {
  VALUE(guarded());
  Helper(int value) {
    lr.decrypt("enum-constructor");
  }
  private static int guarded() {
    lr.decrypt("enum-argument");
    return 1;
  }
}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    Object value = Helper.VALUE;
    return value.hashCode();
  }
}`,
				"Helper.java": tc.source,
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
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, enum constant active use must initialize arguments/constructor", blockers)
			}
		})
	}
}

func TestConvertJavaConservesEveryLRCallInOneStatement(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    String value = lr.eval_string("{value}") + lr.decrypt("cipher");
    return value.length();
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
	assertCombinedJavaImport(t, result)

	found := map[string]int{}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "lr_eval_string" || mapping.Function == "lr_decrypt" {
			found[mapping.Function]++
		}
	}
	if found["lr_eval_string"] != 1 || found["lr_decrypt"] != 1 {
		t.Fatalf("LR source mappings = %#v, want one mapping for each call", found)
	}
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, later review-only call in the statement was lost", blockers)
	}
	if got := result.Stats.NativeFunctions + result.Stats.ExternalRequiredFunctions +
		result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions; got != result.Stats.TotalFunctions {
		t.Fatalf("support counters = %d, want TotalFunctions=%d", got, result.Stats.TotalFunctions)
	}
}

func TestConvertJavaCountsEveryTransactionCallInOneStatement(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    int status = lr.start_transaction("Combined") + lr.end_transaction("Combined", lr.AUTO);
    return status;
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
	assertCombinedJavaImport(t, result)
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want both transaction calls from the statement", result.Stats)
	}
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, transaction-producing action was not marked", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaRecognizesStaticallyImportedLRCalls(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import static lrapi.lr.start_transaction; import static lrapi.lr.end_transaction;
public class Actions {
  public int action() {
    start_transaction("StaticImport");
    end_transaction("StaticImport", 0);
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
	assertCombinedJavaImport(t, result)
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want statically imported transaction calls", result.Stats)
	}
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, statically imported transaction must suppress the duplicate iteration transaction", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaRecognizesWildcardStaticallyImportedLRCalls(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import static lrapi.lr.*;
public class Actions {
  public int action() {
    start_transaction("WildcardStaticImport");
    end_transaction("WildcardStaticImport", 0);
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
	assertCombinedJavaImport(t, result)
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want wildcard-statically imported transaction calls", result.Stats)
	}
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, wildcard static import must suppress the duplicate iteration transaction", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaRecognizesStaticallyImportedLRReviewCall(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import static lrapi.lr.decrypt;
public class Actions {
  public int action() {
    decrypt("cipher");
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
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, statically imported LR review call was missed", blockers)
	}
}

func TestConvertJavaRejectsStaticallyImportedMethodShadowedByOwner(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import static lrapi.lr.start_transaction;
public class Actions {
  private int start_transaction(String name) { return 0; }
  public int action() {
    start_transaction("OwnerMethod");
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
	assertCombinedJavaImport(t, result)
	if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
		t.Fatalf("stats = %#v, owner method shadows the static import", result.Stats)
	}
	if _, exists := result.Script.Actions[0].Metadata["emits_business_transactions"]; exists {
		t.Fatalf("action metadata = %#v, owner method must not suppress the iteration transaction", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaRejectsShadowedLRValueReceiver(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  private final Recorder lr = new Recorder();
  public int action() {
    lr.start_transaction("NotLoadRunner");
    return 0;
  }
}`,
		"Recorder.java": `public class Recorder {
  public int start_transaction(String name) { return 0; }
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
	if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
		t.Fatalf("stats = %#v, shadowed value receiver is not lrapi.lr", result.Stats)
	}
	if _, exists := result.Script.Actions[0].Metadata["emits_business_transactions"]; exists {
		t.Fatalf("action metadata = %#v, shadowed value receiver must not suppress the iteration transaction", result.Script.Actions[0].Metadata)
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "lr_start_transaction" && mapping.SourceFile == "Actions.java" {
			t.Fatalf("source mapping = %#v, shadowed value receiver was classified as lrapi.lr", mapping)
		}
	}
}

func TestConvertJavaSameLineStatementsRetainLifecycleIdentity(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr; public class Actions { public int init(){ lr.start_transaction("Same"); return 0; } public int action(){ lr.start_transaction("Same"); return 0; } }`,
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
	if result.Stats.Transactions != 2 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want distinct init and action occurrences", result.Stats)
	}
	targets := map[string]int{}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "lr_start_transaction" {
			targets[mapping.TargetPath]++
		}
	}
	if targets["init[0]"] != 1 || targets["actions[0]"] != 1 {
		t.Fatalf("transaction targets = %#v, want separate init and action mappings", targets)
	}
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, action occurrence was aliased with init", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaSameFileSameLineMethodsRetainOwnerIdentity(t *testing.T) {
	tests := []struct {
		name             string
		helperBody       string
		mappedFunction   string
		wantBlocker      string
		wantTransactions int
		wantMarkers      int
	}{
		{
			name:           "review-only helper",
			helperBody:     `lr.decrypt("helper-review");`,
			mappedFunction: "lr_decrypt",
			wantBlocker:    "lr_decrypt",
		},
		{
			name:             "transaction helper",
			helperBody:       `lr.start_transaction("Helper"); lr.end_transaction("Helper", 0);`,
			mappedFunction:   "lr_start_transaction",
			wantTransactions: 1,
			wantMarkers:      2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Both mark() declarations deliberately occupy the same physical
			// line in the same compilation unit. Their declaring type is the
			// only identity component that distinguishes their runtime scopes.
			source := `import lrapi.lr; public class Actions { public int action(){ mark(); new Helper().mark(); return 0; } void mark(){} } class Helper { void mark(){ ` + tc.helperBody + ` } }`
			result, err := ConvertJava(
				map[string]string{"Actions.java": source},
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)

			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if tc.wantBlocker != "" && !strings.Contains(blockers, tc.wantBlocker) {
				t.Fatalf("runner semantic blockers = %q, helper method was merged with Actions.mark", blockers)
			}
			if result.Stats.Transactions != tc.wantTransactions ||
				result.Stats.TransactionMarkers != tc.wantMarkers {
				t.Fatalf(
					"stats = %#v, want transactions/markers %d/%d",
					result.Stats,
					tc.wantTransactions,
					tc.wantMarkers,
				)
			}
			if tc.wantTransactions > 0 {
				if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
					t.Fatalf("action metadata = %#v, helper transaction evidence was merged away", result.Script.Actions[0].Metadata)
				}
			}
			mappings := 0
			for _, mapping := range result.SourceReview.Mappings {
				if mapping.Function == tc.mappedFunction {
					mappings++
				}
			}
			if mappings != 1 {
				t.Fatalf("source mappings for %s = %d, want 1", tc.mappedFunction, mappings)
			}
			if got := result.Stats.NativeFunctions + result.Stats.ExternalRequiredFunctions +
				result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions; got != result.Stats.TotalFunctions {
				t.Fatalf("support counters = %d, want TotalFunctions=%d", got, result.Stats.TotalFunctions)
			}
		})
	}
}

func TestJavaRuntimeReachabilityMemoizesDiamondCallGraph(t *testing.T) {
	var source strings.Builder
	source.WriteString("public class Actions {\n")
	source.WriteString("  public int action() { m29(); return 0; }\n")
	source.WriteString("  private void m0() {}\n")
	source.WriteString("  private void m1() {}\n")
	for index := 2; index <= 29; index++ {
		fmt.Fprintf(&source, "  private void m%d() { m%d(); m%d(); }\n", index, index-1, index-2)
	}
	source.WriteString("}\n")

	program, warnings := parseJavaProgram(map[string]string{"Actions.java": source.String()})
	if len(warnings) != 0 {
		t.Fatalf("parse warnings = %#v", warnings)
	}
	started := time.Now()
	reachability := buildJavaVuserRuntimeReachability(program)
	if elapsed := time.Since(started); elapsed > javaAnalysisTimeBudget(2*time.Second) {
		t.Fatalf("diamond reachability took %s; completed-method memoization is not bounding traversal", elapsed)
	}
	if len(reachability.unresolved) != 0 {
		t.Fatalf("unresolved reachability = %#v", reachability.unresolved)
	}
}

func TestJavaCombinedMappingIndexBoundsLargeUnusedArchive(t *testing.T) {
	const classCount = 4096
	classes := make([]javaClass, 0, classCount)

	action := javaMethod{
		Name:       "action",
		File:       "Actions.java",
		StartLine:  5,
		EndLine:    7,
		Visibility: "public",
		ReturnType: "int",
		Statements: []javaStatement{{
			Kind:      "statement",
			Text:      "return 0;",
			File:      "Actions.java",
			StartLine: 6,
			EndLine:   6,
		}},
	}
	initMethod := javaMethod{
		Name:       "init",
		File:       "Actions.java",
		StartLine:  2,
		EndLine:    4,
		Visibility: "public",
		ReturnType: "int",
		Statements: []javaStatement{{
			Kind:      "statement",
			Text:      "return 0;",
			File:      "Actions.java",
			StartLine: 3,
			EndLine:   3,
		}},
	}
	classes = append(classes, javaClass{
		File: "Actions.java",
		Name: "Actions",
		Fields: []javaStatement{{
			Kind:      "statement",
			Text:      "private int state = 1;",
			File:      "Actions.java",
			StartLine: 1,
			EndLine:   1,
		}},
		Methods: map[string]javaMethod{
			"action": action,
			"init":   initMethod,
		},
		MethodOverloads: map[string][]javaMethod{
			"action": {action},
			"init":   {initMethod},
		},
	})

	for classIndex := 1; classIndex < classCount; classIndex++ {
		name := fmt.Sprintf("Unused%04d", classIndex)
		file := "unused/" + name + ".java"
		class := javaClass{
			File:            file,
			Name:            name,
			Package:         "unused",
			Methods:         map[string]javaMethod{},
			MethodOverloads: map[string][]javaMethod{},
			Fields: []javaStatement{{
				Kind:      "statement",
				Text:      "private int value = 1;",
				File:      file,
				StartLine: 1,
				EndLine:   1,
			}},
		}
		for methodIndex := 0; methodIndex < 3; methodIndex++ {
			methodName := fmt.Sprintf("unused%d", methodIndex)
			line := 3 + methodIndex*3
			method := javaMethod{
				Name:       methodName,
				File:       file,
				StartLine:  line,
				EndLine:    line + 2,
				Visibility: "private",
				ReturnType: "void",
				Statements: []javaStatement{{
					Kind:      "statement",
					Text:      "int local = value;",
					File:      file,
					StartLine: line + 1,
					EndLine:   line + 1,
				}},
			}
			class.Methods[methodName] = method
			class.MethodOverloads[methodName] = []javaMethod{method}
		}
		classes = append(classes, class)
	}
	actionsClass := classes[0]
	classes = append(classes[1:], actionsClass)

	program := javaProgram{Classes: classes, Methods: map[string]javaMethod{
		"action": action,
		"init":   initMethod,
	}}
	started := time.Now()
	reachability := buildJavaVuserRuntimeReachability(program)
	mappingIndex := newJavaCombinedMappingIndex(program)
	entries := javaCombinedMappableEntries(program)
	for _, entry := range entries {
		_ = javaCombinedTargetPathWithIndex(entry, reachability, mappingIndex)
	}
	result := &ConvertResult{}
	appendJavaCombinedSourceMappings(result, program, nil, reachability)
	if elapsed := time.Since(started); elapsed > javaAnalysisTimeBudget(5*time.Second) {
		t.Fatalf("4096-class mapping/index pass took %s; per-entry archive scans are not bounded", elapsed)
	}
	if got, want := len(entries), 3+(classCount-1)*4; got != want {
		t.Fatalf("mappable entries = %d, want %d", got, want)
	}
	if got := len(result.SourceReview.Mappings); got != len(entries) {
		t.Fatalf("source mappings = %d, want %d", got, len(entries))
	}
	if got := mappingIndex.lifecycleScopes[javaRuntimeMethodScopeKey(actionsClass, initMethod)]; got != "init[0]" {
		t.Fatalf("init lifecycle scope target = %q", got)
	}
	if !mappingIndex.constructionScopes[javaRuntimeFieldScopeKey(actionsClass, 0)] {
		t.Fatal("selected Actions field is missing from construction ownership index")
	}
	if mappingIndex.constructionScopes[javaRuntimeFieldScopeKey(classes[0], 0)] {
		t.Fatal("unused class field was incorrectly indexed as selected construction work")
	}
	simpleTypes := javaRuntimeSimpleTypeIndex(program)
	if !simpleTypes["unused4095"] {
		t.Fatal("simple-name index omitted the final archived class")
	}
}

func TestConvertJavaFailsClosedWhenReachableHelperExceedsAnalysisLimit(t *testing.T) {
	helper := `public class Helper {
  public void danger() {}
}
` + strings.Repeat(" ", maxJavaAnalysisFileBytes)
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    new Helper().danger();
    return 0;
  }
}`,
		"Helper.java": helper,
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

	sourceBlockers := strings.Join(
		javaReviewArtifactBlockers(result, javaSourceIntegrityKey),
		"\n",
	)
	if !strings.Contains(sourceBlockers, "governed analysis limit") {
		t.Fatalf("source-integrity blockers = %q, want explicit analysis-limit blocker", sourceBlockers)
	}
	runnerBlockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(runnerBlockers, "incomplete parsed type") {
		t.Fatalf("runner-semantics blockers = %q, placeholder Helper must not resolve as a safe type", runnerBlockers)
	}
	foundHelperReviewMapping := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SourceFile == "Helper.java" &&
			mapping.SupportMode == string(SourceFunctionSupportReviewOnly) {
			foundHelperReviewMapping = true
		}
	}
	if !foundHelperReviewMapping {
		t.Fatalf("source mappings = %#v, want preserved review-only Helper evidence", result.SourceReview.Mappings)
	}
	if got := result.Stats.NativeFunctions + result.Stats.ExternalRequiredFunctions +
		result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions; got != result.Stats.TotalFunctions {
		t.Fatalf("support counters = %d, want TotalFunctions=%d", got, result.Stats.TotalFunctions)
	}
}

func TestJavaOverloadWorkGuardIndexesArityAndMemoizesCallSignatures(t *testing.T) {
	program := mustParseJavaTestProgram(t, map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    choose("one");
    choose("two");
    return 0;
  }
  private void choose(String value) {}
  private void choose(Object value) {}
  private void choose(String first, String... rest) {}
}`,
	})
	guard := newJavaOverloadWorkGuard(program)
	owner, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("no selected Actions class")
	}
	ownerKey := javaEvidenceOwnerKey(owner)
	if got := guard.buckets[javaOverloadWorkBucketKey{
		owner: ownerKey,
		name:  "choose",
		arity: 1,
	}]; got != 2 {
		t.Fatalf("fixed choose/1 bucket = %d, want 2", got)
	}
	if got := guard.buckets[javaOverloadWorkBucketKey{
		owner:   ownerKey,
		name:    "choose",
		arity:   1,
		varargs: true,
	}]; got != 1 {
		t.Fatalf("varargs choose/min-arity-1 bucket = %d, want 1", got)
	}
	analysis := analyzeJavaOverloadResolutionWork(program)
	if analysis.blocker != "" {
		t.Fatalf("normal overload analysis blocker = %q", analysis.blocker)
	}
	if got := analysis.resolutionMemoEntries; got != 1 {
		t.Fatalf("resolution memo entries = %d, want repeated String signature cached once", got)
	}

	result, err := ConvertJava(
		map[string]string{
			"Actions.java": `public class Actions {
  public int action() {
    choose("one");
    return 0;
  }
  private void choose(String value) {}
  private void choose(Object value) {}
}`,
		},
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava normal overloads: %v", err)
	}
	assertCombinedJavaImport(t, result)
	for _, blocker := range javaReviewArtifactBlockers(result, javaRunnerSemanticsKey) {
		if strings.Contains(blocker, "candidate-comparison limit") {
			t.Fatalf("normal overload source was work-limited: %q", blocker)
		}
	}
}

func TestConvertJavaOverloadWorkGuardBoundsCandidateAmplification(t *testing.T) {
	const (
		overloadCount = 2_000
		callCount     = 200
	)
	var source strings.Builder
	source.WriteString(`public class Actions {
  public int action() {
`)
	for call := 0; call < callCount; call++ {
		source.WriteString("    overloaded(null);\n")
	}
	source.WriteString("    return 0;\n  }\n")
	for overload := 0; overload < overloadCount; overload++ {
		fmt.Fprintf(
			&source,
			"  private void overloaded(GeneratedType%04d value) {}\n",
			overload,
		)
	}
	source.WriteString("}\n")

	program := mustParseJavaTestProgram(t, map[string]string{"Actions.java": source.String()})
	started := time.Now()
	analysis := analyzeJavaOverloadResolutionWork(program)
	if elapsed := time.Since(started); elapsed > javaAnalysisTimeBudget(2*time.Second) {
		t.Fatalf("overload work preflight took %s; candidate work was not bounded", elapsed)
	}
	if !strings.Contains(analysis.blocker, "candidate-comparison limit") {
		t.Fatalf("overload work blocker = %q, want deterministic candidate limit", analysis.blocker)
	}
	if analysis.resolutionMemoEntries != 1 {
		t.Fatalf("resolution memo entries = %d, want one repeated call/type signature", analysis.resolutionMemoEntries)
	}
	if analysis.candidateComparisons != javaOverloadCandidateComparisonLimit+1 {
		t.Fatalf(
			"candidate comparisons = %d, want saturated limit marker %d",
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
	if elapsed := time.Since(started); elapsed > javaAnalysisTimeBudget(3*time.Second) {
		t.Fatalf("bounded overload conversion took %s", elapsed)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "candidate-comparison limit") {
		t.Fatalf("runner-semantics blockers = %q, want overload work blocker", blockers)
	}
}

func TestJavaOverloadWorkGuardBoundsInterfaceDispatchCandidates(t *testing.T) {
	const (
		implementorCount = 2_000
		callCount        = 40
	)
	workerMethod := javaMethod{
		Name:           "work",
		File:           "Worker.java",
		Visibility:     "public",
		ReturnType:     "void",
		ParameterCount: 1,
		Parameters: []javaParameter{{
			Type: "Object",
			Name: "value",
		}},
	}
	worker := javaClass{
		File:    "Worker.java",
		Name:    "Worker",
		Kind:    "interface",
		Methods: map[string]javaMethod{"work": workerMethod},
		MethodOverloads: map[string][]javaMethod{
			"work": {workerMethod},
		},
	}
	statements := make([]javaStatement, 0, callCount+1)
	for index := 0; index < callCount; index++ {
		statements = append(statements, javaStatement{
			Kind:      "statement",
			Text:      "worker.work(null);",
			File:      "Actions.java",
			StartLine: index + 3,
			EndLine:   index + 3,
		})
	}
	statements = append(statements, javaStatement{
		Kind:      "statement",
		Text:      "return 0;",
		File:      "Actions.java",
		StartLine: callCount + 3,
		EndLine:   callCount + 3,
	})
	action := javaMethod{
		Name:       "action",
		File:       "Actions.java",
		StartLine:  2,
		EndLine:    callCount + 4,
		Visibility: "public",
		ReturnType: "int",
		Statements: statements,
	}
	actions := javaClass{
		File: "Actions.java",
		Name: "Actions",
		Kind: "class",
		Fields: []javaStatement{{
			Kind:      "statement",
			Text:      "private Worker worker;",
			File:      "Actions.java",
			StartLine: 1,
			EndLine:   1,
		}},
		Methods: map[string]javaMethod{"action": action},
		MethodOverloads: map[string][]javaMethod{
			"action": {action},
		},
	}
	classes := make([]javaClass, 0, implementorCount+2)
	classes = append(classes, actions, worker)
	for index := 0; index < implementorCount; index++ {
		name := fmt.Sprintf("WorkerImpl%04d", index)
		classes = append(classes, javaClass{
			File:            name + ".java",
			Name:            name,
			Kind:            "class",
			Interfaces:      []string{"Worker"},
			Methods:         map[string]javaMethod{},
			MethodOverloads: map[string][]javaMethod{},
		})
	}
	program := javaProgram{
		Classes: classes,
		Methods: map[string]javaMethod{"action": action},
	}

	started := time.Now()
	analysis := analyzeJavaOverloadResolutionWork(program)
	if elapsed := time.Since(started); elapsed > javaAnalysisTimeBudget(2*time.Second) {
		t.Fatalf("interface-dispatch work preflight took %s", elapsed)
	}
	if !strings.Contains(analysis.blocker, "candidate-comparison limit") {
		t.Fatalf(
			"interface dispatch blocker = %q, inherited implementors must count toward candidate work",
			analysis.blocker,
		)
	}
	if analysis.resolutionMemoEntries != 1 {
		t.Fatalf(
			"resolution memo entries = %d, want one repeated interface call/type signature",
			analysis.resolutionMemoEntries,
		)
	}
}
