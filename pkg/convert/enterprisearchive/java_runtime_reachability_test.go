package enterprisearchive

import (
	"strings"
	"testing"
)

func TestConvertJavaBlocksReviewOnlyCallsReachedDuringConstruction(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "static initializer",
			files: map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  static {
    lr.decrypt("static-cipher");
  }
  public int action() { return 0; }
}`,
			},
		},
		{
			name: "instance initializer",
			files: map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  {
    lr.decrypt("instance-cipher");
  }
  public int action() { return 0; }
}`,
			},
		},
		{
			name: "eager field initializer helper",
			files: map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private final String secret = configure();
  private String configure() {
    return lr.decrypt("field-cipher");
  }
  public int action() { return 0; }
}`,
			},
		},
		{
			name: "delegated zero argument constructor",
			files: map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  public Actions() {
    this("delegated");
  }
  private Actions(String value) {
    lr.decrypt(value);
  }
  public int action() { return 0; }
}`,
			},
		},
		{
			name: "constructed helper constructor",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    new Helper();
    return 0;
  }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  public Helper() {
    lr.decrypt("helper-cipher");
  }
}`,
			},
		},
		{
			name: "constructed helper initializer",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    new Helper();
    return 0;
  }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  {
    lr.decrypt("helper-initializer-cipher");
  }
}`,
			},
		},
		{
			name: "archived superclass constructor",
			files: map[string]string{
				"Actions.java": `public class Actions extends BaseActions {
  public int action() { return 0; }
}`,
				"BaseActions.java": `import lrapi.lr;
public class BaseActions {
  public BaseActions() {
    lr.decrypt("base-cipher");
  }
}`,
			},
		},
		{
			name: "archived superclass initializer",
			files: map[string]string{
				"Actions.java": `public class Actions extends BaseActions {
  public int action() { return 0; }
}`,
				"BaseActions.java": `import lrapi.lr;
public class BaseActions {
  {
    lr.decrypt("base-initializer-cipher");
  }
}`,
			},
		},
		{
			name: "explicit superclass constructor delegation",
			files: map[string]string{
				"Actions.java": `public class Actions extends BaseActions {
  public Actions() {
    super("delegated");
  }
  public int action() { return 0; }
}`,
				"BaseActions.java": `import lrapi.lr;
public class BaseActions {
  public BaseActions(String value) {
    lr.decrypt(value);
  }
}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaConstructionReachability", tc.files), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
				t.Fatalf("fidelity report = %#v, reachable construction-time lr.decrypt must block promotion", result.FidelityReport)
			}
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, want reachable lr_decrypt", blockers)
			}
		})
	}
}

func TestConvertJavaBlocksUnresolvedExplicitSuperclass(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions extends vendor.BaseActions {
  public int action() { return 0; }
}`,
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaUnresolvedSuperclass", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, unresolved superclass construction must fail closed", result.FidelityReport)
	}
	blockers := strings.ToLower(strings.Join(result.FidelityReport.Blockers, "\n"))
	if !strings.Contains(blockers, "superclass") || !strings.Contains(blockers, "vendor.baseactions") {
		t.Fatalf("fidelity blockers = %#v, want unresolved superclass blocker", result.FidelityReport.Blockers)
	}
}

func TestConvertJavaAcceptsResolvedExplicitSuperclassConstructor(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions extends BaseActions {
  public Actions() {
    super("configured");
  }
  public int action() { return 0; }
}`,
		"BaseActions.java": `public class BaseActions {
  public BaseActions(String value) {}
}`,
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaResolvedSuperclass", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("fidelity report = %#v, resolved explicit super(String) construction should be promotable", result.FidelityReport)
	}
}

func TestConvertJavaMapsClassInitializersToConstructionPhase(t *testing.T) {
	files := map[string]string{
		"src/Actions.java": `package example;
import lrapi.lr;

public class Actions {
  static {
    lr.log_message("static initialization");
  }
  {
    lr.output_message("instance initialization");
  }
  public int init() { return 0; }
  public int action() { return 0; }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)

	mapped := map[string]SourceReviewMapping{}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "lr_log_message" || mapping.Function == "lr_output_message" {
			mapped[mapping.Function] = mapping
		}
	}
	for function, wantLine := range map[string]int{"lr_log_message": 6, "lr_output_message": 9} {
		mapping, ok := mapped[function]
		if !ok {
			t.Fatalf("source mappings = %#v, missing initializer mapping for %s", result.SourceReview.Mappings, function)
		}
		if mapping.SourceFile != "src/Actions.java" || mapping.StartLine != wantLine || mapping.TargetPath != "init[0]" {
			t.Fatalf("%s mapping = %#v, want exact source location and construction-before-init target", function, mapping)
		}
	}
	if got := result.Stats.NativeFunctions + result.Stats.ExternalRequiredFunctions +
		result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions; got != result.Stats.TotalFunctions {
		t.Fatalf("support counters = %d, want TotalFunctions=%d", got, result.Stats.TotalFunctions)
	}
}

func TestConvertJavaConstructionTransactionsAreAggregateOnly(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  static {
    lr.start_transaction("ClassInitialization");
    lr.end_transaction("ClassInitialization", lr.AUTO);
  }
  public int action() { return 0; }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if _, exists := result.Script.Actions[0].Metadata["emits_business_transactions"]; exists {
		t.Fatalf("action metadata = %#v, one-time construction transaction must not suppress every iteration transaction", result.Script.Actions[0].Metadata)
	}
	if emits, _ := result.ReviewArtifact.Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("review metadata = %#v, construction transaction evidence was lost", result.ReviewArtifact.Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want construction transaction evidence", result.Stats)
	}
}

func TestConvertJavaActionConstructedHelperTransactionSuppressesIterationTransaction(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    new TransactionHelper();
    return 0;
  }
}`,
		"TransactionHelper.java": `import lrapi.lr;
public class TransactionHelper {
  public TransactionHelper() {
    lr.start_transaction("ConstructedEachIteration");
    lr.end_transaction("ConstructedEachIteration", lr.AUTO);
  }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, action-reached helper construction must suppress the duplicate automatic transaction", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want action-reached constructor transaction", result.Stats)
	}
}

func TestConvertJavaTransactionReachabilityUsesImportedHelper(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import live.Helper;
public class Actions {
  private final Helper helper = new Helper();
  public int action() {
    helper.run();
    return 0;
  }
}`,
		"backup/Helper.java": `package backup;
public class Helper {
  public void run() {}
}`,
		"live/Helper.java": `package live;
import lrapi.lr;
public class Helper {
  public void run() {
    lr.start_transaction("ImportedHelper");
    lr.end_transaction("ImportedHelper", lr.AUTO);
  }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, imported helper transaction was not reached", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want imported helper transaction only", result.Stats)
	}
}

func TestConvertJavaTransactionReachabilityInfersLREvalStringReturnType(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    run(lr.eval_string("{value}"));
    return 0;
  }
  private void run(String value) {
    lr.start_transaction("StringOverload");
    lr.end_transaction("StringOverload", lr.AUTO);
  }
  private void run(int value) {}
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, lr.eval_string must resolve the String overload", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want String overload transaction only", result.Stats)
	}
}
