package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// buildJavaVuserFilesArchive builds a Java_protocols .usr archive containing an
// arbitrary set of source files (buildJavaReplayArchive only supports a single
// Actions.java).
func buildJavaVuserFilesArchive(t *testing.T, name string, files map[string]string) []byte {
	t.Helper()
	return buildJavaVuserFilesArchiveWithAction(t, name, "Actions.java", files)
}

func buildJavaVuserFilesArchiveWithAction(t *testing.T, name, actionFile string, files map[string]string) []byte {
	t.Helper()
	return buildJavaVuserFilesArchiveWithActionSection(t, name, "Action="+actionFile+"\n", files)
}

func buildJavaVuserFilesArchiveWithActionSection(t *testing.T, name, actionSection string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, name+"/"+name+".usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
`+actionSection)
	fileNames := make([]string, 0, len(files))
	for fileName := range files {
		fileNames = append(fileNames, fileName)
	}
	sort.Strings(fileNames)
	for _, fileName := range fileNames {
		addJavaZipFile(t, zw, name+"/"+fileName, files[fileName])
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestConvertZipJavaCombinedDraftPreservesLifecyclePhases(t *testing.T) {
	archive := buildJavaReplayArchive(t, "JavaLifecycle", "Java Vuser", `import lrapi.lr;
public class Actions {
  private String ticket;
  public int init() {
    ticket = "created-once";
    return 0;
  }
  public int action() {
    lr.start_transaction("BusinessCall");
    use(ticket);
    lr.end_transaction("BusinessCall", lr.AUTO);
    return 0;
  }
  public int end() {
    close(ticket);
    return 0;
  }
}`)

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	assertJavaLifecycleMethod(t, result.Script.Init, "init")
	assertJavaLifecycleMethod(t, result.Script.Actions, "action")
	assertJavaLifecycleMethod(t, result.Script.Teardown, "end")
	if got := result.Script.Actions[0].Params["event_contract"]; got != "apexload.java_vuser.lrapi_events.v2" {
		t.Fatalf("event_contract = %#v, want v2", got)
	}
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, want transaction-producing marker", result.Script.Actions[0].Metadata)
	}
	if _, exists := result.Script.Init[0].Metadata["emits_business_transactions"]; exists {
		t.Fatalf("init metadata = %#v, transaction marker belongs only to action", result.Script.Init[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want one source transaction and two markers", result.Stats)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues: %v", issues)
	}

	withDeprecatedOption, err := ConvertZip(archive, ConvertOptions{AllowJavaVuserRunner: true})
	if err != nil {
		t.Fatalf("ConvertZip with deprecated option: %v", err)
	}
	assertCombinedJavaImport(t, withDeprecatedOption)
	if len(withDeprecatedOption.Script.Init) != len(result.Script.Init) || len(withDeprecatedOption.Script.Actions) != len(result.Script.Actions) || len(withDeprecatedOption.Script.Teardown) != len(result.Script.Teardown) {
		t.Fatalf("deprecated option changed lifecycle shape: default=%#v option=%#v", result.Script, withDeprecatedOption.Script)
	}
}

func TestConvertZipJavaInitAndEndTransactionsDoNotSuppressIterationTransactions(t *testing.T) {
	archive := buildJavaReplayArchive(t, "JavaLifecycleTransactions", "Java Vuser", `import lrapi.lr;
public class Actions {
  public int init() {
    lr.start_transaction("Setup");
    lr.end_transaction("Setup", lr.AUTO);
    return 0;
  }
  public int action() {
    doWork();
    return 0;
  }
  public int end() {
    lr.start_transaction("Cleanup");
    lr.end_transaction("Cleanup", lr.AUTO);
    return 0;
  }
}`)

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if _, exists := result.Script.Actions[0].Metadata["emits_business_transactions"]; exists {
		t.Fatalf("action metadata = %#v, init/end transactions must not suppress automatic iteration transactions", result.Script.Actions[0].Metadata)
	}
	if emits, _ := result.ReviewArtifact.Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("review metadata = %#v, aggregate lifecycle transaction evidence was lost", result.ReviewArtifact.Metadata)
	}
	if result.Stats.Transactions != 2 || result.Stats.TransactionMarkers != 4 {
		t.Fatalf("stats = %#v, want aggregate init/end transaction evidence", result.Stats)
	}
}

func TestConvertZipJavaTransactionDetectionIgnoresDiagnosticStringLiterals(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JavaTransactionText", "Java Vuser", `public class Actions {
  public int action() {
    System.out.println("lr.start_transaction(\"NotExecuted\")");
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); emits {
		t.Fatalf("action metadata = %#v, diagnostic text must not suppress automatic iteration transactions", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
		t.Fatalf("stats = %#v, diagnostic transaction text must not count as an executed LR call", result.Stats)
	}
}

func TestConvertZipJavaSetTransactionSuppressesAutomaticIterationTransaction(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JavaSetTransaction", "Java Vuser", `import lrapi.lr;
public class Actions {
  public int action() {
    lr.set_transaction("ManualBusinessCall", 1.25, lr.PASS);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, set_transaction must suppress the automatic iteration transaction", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want one completed manual transaction and its start/end event pair", result.Stats)
	}
}

func TestConvertZipJavaBlocksRunnerTypesTheHarnessCannotInstantiate(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantBlocker string
	}{
		{
			name: "parameterized constructor only",
			source: `public class Actions {
  public Actions(String seed) {}
  public int action() { return 0; }
}`,
			wantBlocker: "no public zero-argument constructor",
		},
		{
			name: "private zero argument constructor",
			source: `public class Actions {
  private Actions() {}
  public int action() { return 0; }
}`,
			wantBlocker: "no public zero-argument constructor",
		},
		{
			name: "abstract class",
			source: `public abstract class Actions {
  public int action() { return 0; }
}`,
			wantBlocker: "is abstract",
		},
		{
			name: "interface",
			source: `public interface Actions {
  public static int action() { return 0; }
}`,
			wantBlocker: "is an interface, not a concrete class",
		},
		{
			name: "non public class",
			source: `class Actions {
  public int action() { return 0; }
}`,
			wantBlocker: "is not public",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ConvertZip(buildJavaReplayArchive(t, "JavaInstantiation", "Java Vuser", test.source), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			assertCombinedJavaImport(t, result)
			if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
				t.Fatalf("fidelity report = %#v, want blocked non-instantiable runner type", result.FidelityReport)
			}
			if !strings.Contains(strings.Join(result.FidelityReport.Blockers, "\n"), test.wantBlocker) {
				t.Fatalf("fidelity blockers = %#v, want %q", result.FidelityReport.Blockers, test.wantBlocker)
			}
		})
	}
}

func TestConvertZipJavaAcceptsExplicitPublicZeroArgumentConstructor(t *testing.T) {
	source := `public class Actions {
  public Actions() {}
  public int action() { return 0; }
}`
	result, err := ConvertZip(buildJavaReplayArchive(t, "JavaInstantiation", "Java Vuser", source), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("fidelity report = %#v, want promotable public zero-argument constructor", result.FidelityReport)
	}

	program, warnings := parseJavaProgram(map[string]string{"Actions.java": source})
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	class, ok := javaVuserClass(program)
	if !ok || len(class.Constructors) != 1 {
		t.Fatalf("parsed class = %#v, want one explicit constructor", class)
	}
	if methods := javaMethodsNamed(class, "Actions"); len(methods) != 0 {
		t.Fatalf("constructor was also parsed as a method: %#v", methods)
	}
}

func TestConvertZipJavaUsesFullSourceManifestWithBoundedReviewPreview(t *testing.T) {
	t.Run("file limit omits Java source", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions { public int action() { return 0; } }`,
		}
		for index := 0; index < sourceview.MaxFiles+2; index++ {
			name := fmt.Sprintf("A%02d.java", index)
			files[name] = fmt.Sprintf("public class A%02d {}", index)
		}
		result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaReviewBound", files), ConvertOptions{})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}
		assertCombinedJavaImport(t, result)
		if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable ||
			len(result.FidelityReport.Blockers) != 0 {
			t.Fatalf("fidelity report = %#v, bounded review omissions are not conversion loss", result.FidelityReport)
		}
		if result.SourceReview == nil || len(result.SourceReview.OmittedSourceFiles) == 0 {
			t.Fatalf("source review = %#v, want explicit omitted paths", result.SourceReview)
		}
		assertJavaVuserFullSourceManifest(t, result, files)
	})

	t.Run("file content is truncated", func(t *testing.T) {
		source := `public class Actions { public int action() { return 0; } }` +
			"\n/*" + strings.Repeat("review filler ", sourceview.MaxFileChars/4) + "*/"
		result, err := ConvertZip(buildJavaReplayArchive(t, "JavaReviewTruncated", "Java Vuser", source), ConvertOptions{})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}
		assertCombinedJavaImport(t, result)
		if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable ||
			len(result.FidelityReport.Blockers) != 0 {
			t.Fatalf("fidelity report = %#v, bounded review truncation is not conversion loss", result.FidelityReport)
		}
		truncatedJava := false
		if result.SourceReview != nil {
			for _, sourceFile := range result.SourceReview.SourceFiles {
				if sourceFile.Path == "Actions.java" && sourceFile.Truncated {
					truncatedJava = true
					break
				}
			}
		}
		if !truncatedJava {
			t.Fatal("Actions.java was not explicitly marked as a truncated bounded preview")
		}
		assertJavaVuserFullSourceManifest(t, result, map[string]string{"Actions.java": source})
	})
}

func assertJavaVuserFullSourceManifest(t *testing.T, result *ConvertResult, files map[string]string) {
	t.Helper()
	if result == nil || result.ReviewArtifact == nil || result.ReviewArtifact.Metadata == nil {
		t.Fatalf("review artifact = %#v, want immutable full-source manifest", result)
	}
	manifest, err := DecodeJavaVuserSourceManifest(
		result.ReviewArtifact.Metadata[JavaVuserSourceManifestMetadataKey],
	)
	if err != nil {
		t.Fatalf("DecodeJavaVuserSourceManifest: %v", err)
	}
	want := BuildJavaVuserSourceManifest(files)
	if want == nil {
		t.Fatal("test fixture produced no Java source manifest")
	}
	if manifest.Digest != want.Digest || manifest.FileCount != want.FileCount || manifest.TotalBytes != want.TotalBytes {
		t.Fatalf("source manifest = %#v, want digest/count/bytes from all complete Java sources %#v", manifest, want)
	}
}

func TestConvertZipJavaSourceMappingsDescribePersistentPerVUserState(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JavaPersistentMapping", "Java Vuser", `public class Actions {
  private int iteration;
  public int init() { iteration = 1; return 0; }
  public int action() { iteration++; return 0; }
  public int end() { return iteration; }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatal("combined Java import did not produce source mappings")
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SupportMode != string(SourceFunctionSupportExternalRequired) {
			continue
		}
		lower := strings.ToLower(mapping.Notes)
		if strings.Contains(lower, "does not cross phase") || strings.Contains(lower, "state is not retained") {
			t.Fatalf("mapping still describes one-shot lifecycle state: %#v", mapping)
		}
		if !strings.Contains(lower, "persistent per-vuser actions instance") {
			t.Fatalf("mapping does not describe persistent per-VUser state: %#v", mapping)
		}
	}
}

func TestConvertZipJavaPersistentLifecycleWarningRequiresGovernedCapabilities(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JavaGovernedLifecycle", "Java Vuser", `public class Actions {
  public int action() { return 0; }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	warnings := strings.Join(result.Warnings, "\n")
	for _, required := range []string{
		"governed local or loopback-sidecar runner",
		"qualified process containment",
		protocol.FeatureJavaVuserPersistentLifecycleV1,
		protocol.FeatureJavaVuserSessionFrameTokenV1,
		"stock non-privileged sidecars remain legacy full_lifecycle only",
	} {
		if !strings.Contains(warnings, required) {
			t.Fatalf("warnings = %q, missing governed lifecycle qualifier %q", warnings, required)
		}
	}
}

func TestConvertZipJavaSourceMappingsUseCombinedRunnerLRDispositions(t *testing.T) {
	t.Run("verified continue exit remains executable", func(t *testing.T) {
		result, err := ConvertZip(buildJavaReplayArchive(t, "JavaExitReview", "Java Vuser", `import lrapi.lr;
public class Actions {
  public int action() {
    lr.exit(lr.EXIT_ITERATION_AND_CONTINUE, lr.FAIL);
    return 0;
  }
}`), ConvertOptions{})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}
		assertCombinedJavaImport(t, result)
		assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "lr_exit", SourceFunctionSupportExecutable)
		if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
			t.Fatalf("fidelity report = %#v, verified continue exit should remain promotable", result.FidelityReport)
		}
	})

	t.Run("VUser-wide exit remains review-only", func(t *testing.T) {
		result, err := ConvertZip(buildJavaReplayArchive(t, "JavaExitReview", "Java Vuser", `import lrapi.lr;
public class Actions {
  public int action() {
    lr.exit(lr.EXIT_VUSER, lr.FAIL);
    return 0;
  }
}`), ConvertOptions{})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}
		assertCombinedJavaImport(t, result)
		assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "lr_exit", SourceFunctionSupportReviewOnly)
		if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
			t.Fatalf("fidelity report = %#v, VUser-wide exit must block until engine flow is implemented", result.FidelityReport)
		}
	})

	t.Run("unused review-only helper remains evidence", func(t *testing.T) {
		result, err := ConvertZip(buildJavaReplayArchive(t, "JavaUnusedExitReview", "Java Vuser", `import lrapi.lr;
public class Actions {
  public int action() {
    return 0;
  }
  private void unused() {
    lr.exit(lr.EXIT_VUSER, lr.FAIL);
  }
}`), ConvertOptions{})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}
		assertCombinedJavaImport(t, result)
		assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "lr_exit", SourceFunctionSupportReviewOnly)
		if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
			t.Fatalf("fidelity report = %#v, an unreachable review-only helper should remain review evidence without blocking promotion", result.FidelityReport)
		}
	})
}

func TestConvertZipJavaCombinedDraftPreservesLifecycleMethodAliases(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		init     string
		action   string
		teardown string
	}{
		{
			name: "loadrunner_names",
			source: `public class Actions {
  public int vuser_init() { return 0; }
  public int Action() { return 0; }
  public int vuser_end() { return 0; }
}`,
			init: "vuser_init", action: "Action", teardown: "vuser_end",
		},
		{
			name: "descriptive_names",
			source: `public class Actions {
  public int initialize() { return 0; }
  public int run() { return 0; }
  public int teardown() { return 0; }
}`,
			init: "initialize", action: "run", teardown: "teardown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildJavaReplayArchive(t, "JavaLifecycleAliases", "Java Vuser", tc.source)
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			assertCombinedJavaImport(t, result)
			assertJavaLifecycleMethod(t, result.Script.Init, tc.init)
			assertJavaLifecycleMethod(t, result.Script.Actions, tc.action)
			assertJavaLifecycleMethod(t, result.Script.Teardown, tc.teardown)
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("ValidateScriptIssues: %v", issues)
			}
		})
	}
}

func TestConvertZipJavaProtocolFamiliesAllRemainCombined(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"http", `public class Actions { public int action() { web.url("Home", "URL=https://example.test", web.LAST); return 0; } }`},
		{"jdbc", `import java.sql.*; public class Actions { public int action() throws Exception { PreparedStatement ps = conn.prepareStatement("select 1"); ps.executeQuery(); return 0; } }`},
		{"jms", `import javax.jms.*; public class Actions { public int action() throws Exception { producer.send(session.createTextMessage("hi")); return 0; } }`},
		{"soap", `import javax.xml.soap.*; public class Actions { public int action() throws Exception { connection.call(message, endpoint); return 0; } }`},
		{"mixed", `import java.sql.*; import javax.jms.*; public class Actions { public int action() throws Exception { web.url("Home", "URL=https://example.test", web.LAST); PreparedStatement ps = conn.prepareStatement("select 1"); ps.executeQuery(); producer.send(message); return 0; } }`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertZip(buildJavaReplayArchive(t, "Java_"+tc.name, "Java Vuser", tc.source), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			assertCombinedJavaImport(t, result)
			assertJavaLifecycleMethod(t, result.Script.Actions, "action")
			for _, warning := range append(append([]string(nil), result.Warnings...), result.DetectedProtocol.Warnings...) {
				if strings.Contains(strings.ToLower(warning), "generated as jms dsl") {
					t.Fatalf("stale native-lane warning survived combined import: %q", warning)
				}
			}
			if len(result.Script.Init) != 0 || len(result.Script.Teardown) != 0 {
				t.Fatalf("unexpected optional lifecycle actions: init=%#v teardown=%#v", result.Script.Init, result.Script.Teardown)
			}
		})
	}
}

func TestConvertZipJavaArchiveWinsOverAuxiliaryDevWebMain(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaWithAuxiliaryJS", map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    return 0;
  }
}`,
		"support/main.js": `// Auxiliary package content; this is not the Vuser entry point.
export function helper() { return load.get("https://example.invalid"); }`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.DetectedProtocol == nil ||
		result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" ||
		result.DetectedProtocol.APEXLoadProtocol != "java_vuser" {
		t.Fatalf("detected protocol = %#v, want java_vuser", result.DetectedProtocol)
	}
}

func TestConvertZipDevWebProjectWinsOverAuxiliaryJavaSource(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "DevWebWithAuxiliaryJava/DevWeb.usr", `[General]
Type=DevWeb
`)
	addJavaZipFile(t, zw, "DevWebWithAuxiliaryJava/main.js", `load.action("Browse", async function() {
  new load.WebRequest({ url: "https://example.invalid/orders", method: "GET" }).sendSync();
});`)
	addJavaZipFile(t, zw, "DevWebWithAuxiliaryJava/support/Actions.java", `public class Actions {
  public int action() { return 0; }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.DetectedProtocol == nil ||
		result.DetectedProtocol.CanonicalSourceProtocol != "devweb" ||
		result.Script == nil ||
		result.Script.Protocol != "http" {
		t.Fatalf(
			"result protocol = detected=%#v script=%#v, want authoritative DevWeb routing",
			result.DetectedProtocol,
			result.Script,
		)
	}
	if result.Script.Protocol == "java_vuser" || len(result.Scripts) > 1 {
		t.Fatalf("result = %#v, auxiliary Java source redirected or split the DevWeb project", result)
	}
}

func TestJavaClassifierIgnoresWebHostnameAndUnusedSQLImport(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import java.sql.*;
public class Actions {
  public int action() throws Exception {
    String provider = "t3://dlsapi-opus03.web.att.com:6000";
    InitialContext context = new InitialContext();
    context.lookup(provider);
    return 0;
  }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	profile := classifyJavaArchive(program, nil, files)
	if profile.Surfaces[javaSurfaceHTTP] || profile.Surfaces[javaSurfaceDatabase] {
		t.Fatalf("profile surfaces = %#v, hostname/import must not imply executed HTTP/JDBC", profile.Surfaces)
	}
	if javaLooksLikeHTTP(strings.ToLower(files["Actions.java"])) || javaLooksLikeDatabase(strings.ToLower(files["Actions.java"])) {
		t.Fatal("informational classifiers treated hostname/import text as an executed protocol call")
	}

	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
}

func TestJavaClassifierIgnoresUninvokedConstructorOverload(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import java.sql.Connection;
public class Actions {
  public Actions() {}
  public Actions(Connection connection) throws Exception {
    connection.prepareStatement("select 1").executeQuery();
  }
  public int action() { return 0; }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	profile := classifyJavaArchive(program, nil, files)
	if profile.Surfaces[javaSurfaceDatabase] {
		t.Fatalf("profile surfaces = %#v, an uninvoked constructor overload must not imply executed JDBC", profile.Surfaces)
	}
}

func TestJavaDeclarationDiscoveryIgnoresCommentsAndAnnotationStrings(t *testing.T) {
	files := map[string]string{
		"Actions.java": `/**
 * package decoy.package;
 * This class defines the VUser lifecycle.
 */
package com.acme.load;

@SuppressWarnings("class AnnotationDecoy")
public class Actions {
  public int action() {
    String payload = """
        } class TextBlockDecoy {
        """;
    return 0;
  }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	class, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("selected Java class was not found")
	}
	if class.Name != "Actions" || class.Package != "com.acme.load" {
		t.Fatalf("class = %#v, JavaDoc/annotation data must not replace the declaration", class)
	}

	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if got := result.Script.Actions[0].Target; got != "com.acme.load.Actions" {
		t.Fatalf("runner target = %q, want the declared qualified class", got)
	}
}

func TestJavaDeclarationDiscoveryPrefersFilenameTypeOverEarlierHelpers(t *testing.T) {
	files := map[string]string{
		"Actions.java": `@interface Marker {
}
class AlphabeticalHelper {
  public void warm() {
  }
}
public class Actions {
  public int action() {
    return 0;
  }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	class, ok := javaVuserClass(program)
	if !ok || class.Name != "Actions" {
		t.Fatalf("selected class = %#v, want the top-level type matching Actions.java", class)
	}
	if _, ok := class.Methods["action"]; !ok {
		t.Fatalf("selected methods = %#v, want Actions.action", class.Methods)
	}

	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaMultipleTypes", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("fidelity report = %#v, an earlier helper type must not hide Actions.action", result.FidelityReport)
	}
}

func TestConvertZipJavaUSRActionSourceSelectsAmongDuplicateActionsClasses(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchiveWithAction(t, "JavaDuplicateActions", "src/Actions.java", map[string]string{
		"aaa-backup/Actions.java": `package backup;
public class Actions {
  public int action() {
    web.url("WrongBackupWorkload", "URL=https://backup.example.test", new String[] {});
    return 0;
  }
}`,
		"src/Actions.java": `package live;
public class Actions {
  private int iteration;
  public int init() {
    iteration = 1;
    return 0;
  }
  public int action() {
    iteration++;
    return 0;
  }
  public int end() {
    return iteration;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.Script.Actions[0].Target != "live.Actions" {
		t.Fatalf("runner target = %q, want the .usr-referenced live.Actions instead of the alphabetically earlier backup.Actions", result.Script.Actions[0].Target)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.DisplayName != "Java Vuser" {
		t.Fatalf("detected protocol = %#v, backup class must not replace selected-class review evidence", result.DetectedProtocol)
	}
	for _, signal := range result.DetectedProtocol.Signals {
		if strings.Contains(strings.ToLower(signal), "http api usage") {
			t.Fatalf("signals = %#v, backup Actions.java must not be reported as executed HTTP", result.DetectedProtocol.Signals)
		}
	}
	for _, evidence := range result.DetectedProtocol.RequiredEvidence {
		if strings.Contains(strings.ToLower(evidence), "http replay") {
			t.Fatalf("required evidence = %#v, backup Actions.java must not add HTTP replay requirements", result.DetectedProtocol.RequiredEvidence)
		}
	}
	for _, phase := range []struct {
		name    string
		actions []dsl.Action
		method  string
	}{
		{"init", result.Script.Init, "init"},
		{"action", result.Script.Actions, "action"},
		{"teardown", result.Script.Teardown, "end"},
	} {
		if len(phase.actions) != 1 || phase.actions[0].Type != "java_vuser_run" ||
			phase.actions[0].Target != "live.Actions" || phase.actions[0].Params["method"] != phase.method {
			t.Fatalf("%s actions = %#v, want live.Actions.%s", phase.name, phase.actions, phase.method)
		}
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("fidelity report = %#v, .usr-selected complete lifecycle should be promotable", result.FidelityReport)
	}
}

func TestConvertZipJavaMissingActionMethodIsBlocked(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaMissingAction", map[string]string{
		"Actions.java": `public class Actions {
  public int init() {
    return 0;
  }
  public void helper() {
  }
  public int end() {
    return 0;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, want missing action() to block promotion", result.FidelityReport)
	}
	blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey)
	if !containsString(blockers, javaVuserMissingActionBlocker) {
		t.Fatalf("source integrity blockers = %v, want %q", blockers, javaVuserMissingActionBlocker)
	}
}

func TestConvertZipJavaNestedActionDoesNotSatisfyOuterLifecycle(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaNestedAction", map[string]string{
		"Actions.java": `public class Actions {
  public int init() {
    return 0;
  }
  static class NestedLifecycle {
    public int action() {
      return 0;
    }
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, nested action() cannot run on the outer Actions instance", result.FidelityReport)
	}
	if blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey); !containsString(blockers, javaVuserMissingActionBlocker) {
		t.Fatalf("source integrity blockers = %v, want missing outer action blocker", blockers)
	}
}

func TestConvertZipJavaSingleLineActionIsOwnedByOuterClass(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaSingleLineAction", map[string]string{
		"Actions.java": `public class Actions { public int action() { return 0; } }`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable {
		t.Fatalf("fidelity report = %#v, direct single-line action() should be verified", result.FidelityReport)
	}
	if blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey); len(blockers) != 0 {
		t.Fatalf("source integrity blockers = %v, want none", blockers)
	}
}

func TestConvertJavaFindsTransactionsInReachableQualifiedHelpersOnly(t *testing.T) {
	tests := []struct {
		name       string
		actionCall string
		field      string
	}{
		{
			name:       "instance_field",
			field:      "private final TransactionHelper helper = new TransactionHelper();",
			actionCall: "helper.recordOrder();",
		},
		{
			name:       "static_class_call",
			actionCall: "TransactionHelper.recordOrder();",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  ` + tc.field + `
  public int action() {
    ` + tc.actionCall + `
    return 0;
  }
}`,
				"TransactionHelper.java": `import lrapi.lr;
public class TransactionHelper {
  public static void recordOrder() {
    lr.start_transaction("Reachable");
    lr.end_transaction("Reachable", lr.AUTO);
  }
}`,
				"UnusedHelper.java": `import lrapi.lr;
public class UnusedHelper {
  public static void recordUnused() {
    lr.start_transaction("Uncalled");
    lr.end_transaction("Uncalled", lr.AUTO);
  }
}`,
			}
			result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
				t.Fatalf("action metadata = %#v, want reachable helper transaction marker", result.Script.Actions[0].Metadata)
			}
			if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
				t.Fatalf("stats = %#v, want only the reachable helper transaction", result.Stats)
			}
		})
	}
}

func TestConvertJavaTransactionReachabilityIgnoresUnrelatedMethodVariableShadowing(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  private final RealHelper helper = new RealHelper();

  public int action() {
    helper.record();
    return 0;
  }

  public void configureUnusedPath() {
    TransactionHelper helper = new TransactionHelper();
    helper.record();
  }
}`,
		"RealHelper.java": `public class RealHelper {
  public void record() {
    System.out.println("real action helper");
  }
}`,
		"TransactionHelper.java": `import lrapi.lr;
public class TransactionHelper {
  public void record() {
    lr.start_transaction("Unreachable");
    lr.end_transaction("Unreachable", lr.AUTO);
  }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	selected, ok := javaVuserClass(program)
	if !ok || len(selected.Fields) != 1 || !strings.Contains(selected.Fields[0].Text, "RealHelper helper") {
		t.Fatalf("selected class fields = %#v, want only the direct RealHelper field", selected.Fields)
	}

	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); emits {
		t.Fatalf("action metadata = %#v, unrelated method-local shadow redirected transaction reachability", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
		t.Fatalf("stats = %#v, unreachable TransactionHelper must not produce transaction evidence", result.Stats)
	}
}

func TestConvertJavaTransactionReachabilityRespectsLocalDeclarationScope(t *testing.T) {
	tests := []struct {
		name       string
		actionBody string
	}{
		{
			name: "later_local_does_not_redirect_earlier_field_call",
			actionBody: `helper.record();
    TransactionHelper helper = new TransactionHelper();`,
		},
		{
			name: "nested_local_does_not_escape_its_block",
			actionBody: `if (enabled) {
      TransactionHelper helper = new TransactionHelper();
    }
    helper.record();`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  private final RealHelper helper = new RealHelper();
  private boolean enabled = true;
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
}`,
				"RealHelper.java": `public class RealHelper {
  public void record() {
    System.out.println("real helper");
  }
}`,
				"TransactionHelper.java": `import lrapi.lr;
public class TransactionHelper {
  public void record() {
    lr.start_transaction("OutOfScope");
    lr.end_transaction("OutOfScope", lr.AUTO);
  }
}`,
			}
			result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); emits {
				t.Fatalf("action metadata = %#v, out-of-scope local redirected helper reachability", result.Script.Actions[0].Metadata)
			}
			if result.Stats.Transactions != 0 || result.Stats.TransactionMarkers != 0 {
				t.Fatalf("stats = %#v, out-of-scope local must not produce transaction evidence", result.Stats)
			}
		})
	}
}

func TestParseJavaClassFieldsIncludesBraceInitializersAndExcludesNestedScopes(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  private final Helper plain = new Helper();
  private final Helper anonymous = new Helper() {
    public void nestedOverride() {
      Helper anonymousLocal = new Helper();
    }
  };
  private final Runnable callback = () -> {
    Helper lambdaLocal = new Helper();
  };
  static {
    Helper initializerLocal = new Helper();
  }
  @Configured(value = "test")
  public void annotatedMethod() {
    Helper annotatedMethodLocal = new Helper();
  }
  class Nested {
    private final Helper nestedField = new Helper();
  }
  public int action() {
    Helper methodLocal = new Helper();
    return 0;
  }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	selected, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("no selected Java Vuser class")
	}
	if len(selected.Fields) != 3 {
		t.Fatalf("fields = %#v, want plain, anonymous-class, and lambda fields", selected.Fields)
	}
	fieldSource := ""
	for _, field := range selected.Fields {
		if strings.TrimSpace(field.Text) == ";" {
			t.Fatalf("fields = %#v, brace initializer terminator became a standalone field", selected.Fields)
		}
		fieldSource += "\n" + field.Text
	}
	for _, want := range []string{"Helper plain", "Helper anonymous", "Runnable callback"} {
		if !strings.Contains(fieldSource, want) {
			t.Fatalf("field source = %q, missing %q", fieldSource, want)
		}
	}
	for _, forbidden := range []string{"initializerLocal", "annotatedMethodLocal", "nestedField", "methodLocal"} {
		if strings.Contains(fieldSource, forbidden) {
			t.Fatalf("field source = %q, retained nested-scope declaration %q", fieldSource, forbidden)
		}
	}
}

func TestConvertJavaSourceReviewKeepsDistinctSameLineStatements(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions { public int action() { int first = 1; int second = 2; consume(first + second); consume(first + second); return 0; } }`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	wantMappings := len(javaCombinedMappableStatements(program))
	if wantMappings < 5 {
		t.Fatalf("mappable statements = %#v, want every same-line action statement", javaCombinedMappableStatements(program))
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.SourceReview == nil || len(result.SourceReview.Mappings) != wantMappings {
		t.Fatalf("source mappings = %#v, want %d distinct same-line statements", result.SourceReview, wantMappings)
	}
	if result.Stats.TotalFunctions != wantMappings ||
		result.Stats.ExternalRequiredFunctions != wantMappings {
		t.Fatalf("stats = %#v, want one external-required disposition per same-line statement", result.Stats)
	}
}

func TestConvertJavaTransactionReachabilityUsesCurrentHelperParameters(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    invoke(new TransactionHelper());
    return 0;
  }

  private void invoke(TransactionHelper candidate) {
    candidate.record();
  }
}`,
		"TransactionHelper.java": `import lrapi.lr;
public class TransactionHelper {
  public void record() {
    lr.start_transaction("ParameterHelper");
    lr.end_transaction("ParameterHelper", lr.AUTO);
  }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, want transaction reached through the current helper parameter", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want the parameter-qualified helper transaction", result.Stats)
	}
}

func TestConvertJavaTransactionReachabilityMatchesHelperOverloadArity(t *testing.T) {
	tests := []struct {
		name             string
		actionCall       string
		helperMethods    string
		wantTransactions int
	}{
		{
			name:       "argument_call_ignores_zero_argument_transaction_overload",
			actionCall: "helper(1);",
			helperMethods: `private void helper(int value) {
    System.out.println(value);
  }
  private void helper() {
    lr.start_transaction("WrongOverload");
    lr.end_transaction("WrongOverload", lr.AUTO);
  }`,
			wantTransactions: 0,
		},
		{
			name:       "zero_argument_call_reaches_transaction_overload",
			actionCall: "helper();",
			helperMethods: `private void helper() {
    lr.start_transaction("RightOverload");
    lr.end_transaction("RightOverload", lr.AUTO);
  }
  private void helper(int value) {
    System.out.println(value);
  }`,
			wantTransactions: 1,
		},
		{
			name:       "same_arity_integer_call_ignores_string_transaction_overload",
			actionCall: "helper(1);",
			helperMethods: `private void helper(int value) {
    System.out.println(value);
  }
  private void helper(String value) {
    lr.start_transaction("WrongTypeOverload");
    lr.end_transaction("WrongTypeOverload", lr.AUTO);
  }`,
			wantTransactions: 0,
		},
		{
			name:       "same_arity_string_call_reaches_string_transaction_overload",
			actionCall: `helper("order");`,
			helperMethods: `private void helper(int value) {
    System.out.println(value);
  }
  private void helper(String value) {
    lr.start_transaction("RightTypeOverload");
    lr.end_transaction("RightTypeOverload", lr.AUTO);
  }`,
			wantTransactions: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    ` + tc.actionCall + `
    return 0;
  }
  ` + tc.helperMethods + `
}`,
			}
			result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool)
			if emits != (tc.wantTransactions > 0) {
				t.Fatalf("action metadata = %#v, emits=%v want %v", result.Script.Actions[0].Metadata, emits, tc.wantTransactions > 0)
			}
			if result.Stats.Transactions != tc.wantTransactions ||
				result.Stats.TransactionMarkers != tc.wantTransactions*2 {
				t.Fatalf("stats = %#v, want %d reachable transaction(s)", result.Stats, tc.wantTransactions)
			}
		})
	}
}

func TestConvertZipJavaLifecycleUsesPublicZeroArgumentAliases(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaLifecycleAliases", map[string]string{
		"Actions.java": `public class Actions {
  private int init() {
    return -1;
  }
  public int initialize() {
    return 0;
  }
  public int action(int ignored) {
    return -1;
  }
  public int run() {
    return 0;
  }
  public int end(int ignored) {
    return -1;
  }
  public int teardown() {
    return 0;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	assertJavaLifecycleMethod(t, result.Script.Init, "initialize")
	assertJavaLifecycleMethod(t, result.Script.Actions, "run")
	assertJavaLifecycleMethod(t, result.Script.Teardown, "teardown")
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable || len(result.FidelityReport.Blockers) != 0 {
		t.Fatalf("fidelity report = %#v, valid public zero-argument aliases should be promotable", result.FidelityReport)
	}
}

func TestConvertZipJavaLifecycleBlocksWhenNoPublicZeroArgumentActionExists(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaInvalidActionContract", map[string]string{
		"Actions.java": `public class Actions {
  private int action() {
    return -1;
  }
  public int action(int ignored) {
    return ignored;
  }
  protected int run() {
    return -1;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, invalid lifecycle contract must block promotion", result.FidelityReport)
	}
	if blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey); !containsString(blockers, javaVuserMissingActionBlocker) {
		t.Fatalf("source integrity blockers = %#v, want missing runnable action blocker", blockers)
	}
}

func TestConvertJavaFindsTransactionInReturnedSameClassHelper(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    return recordOrder();
  }
  private int recordOrder() {
    lr.start_transaction("ReturnedHelper");
    lr.end_transaction("ReturnedHelper", lr.AUTO);
    return 0;
  }
}`,
		"UnusedHelper.java": `import lrapi.lr;
public class UnusedHelper {
  public static int recordOrder() {
    lr.start_transaction("Uncalled");
    lr.end_transaction("Uncalled", lr.AUTO);
    return 0;
  }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, want returned helper transaction marker", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want only the reachable same-class helper transaction", result.Stats)
	}
}

func TestConvertJavaFindsTransactionInSameFileTopLevelHelper(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
class TransactionHelper {
  public void recordOrder() {
    lr.start_transaction("SameFileHelper");
    lr.end_transaction("SameFileHelper", lr.AUTO);
  }
}
class UnusedHelper {
  public void recordUnused() {
    lr.start_transaction("Uncalled");
    lr.end_transaction("Uncalled", lr.AUTO);
  }
}
public class Actions {
  private final TransactionHelper helper = new TransactionHelper();
  public int action() {
    helper.recordOrder();
    return 0;
  }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if emits, _ := result.Script.Actions[0].Metadata["emits_business_transactions"].(bool); !emits {
		t.Fatalf("action metadata = %#v, want same-file helper transaction marker", result.Script.Actions[0].Metadata)
	}
	if result.Stats.Transactions != 1 || result.Stats.TransactionMarkers != 2 {
		t.Fatalf("stats = %#v, want only the reachable same-file helper transaction", result.Stats)
	}
}

func TestConvertJavaNormalizesCombinedRunnerIdentityAcrossProtocolFamilies(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "generic_jms",
			source: `import javax.jms.*;
public class Actions {
  public int action() {
    MessageProducer producer = session.createProducer(queue);
    producer.send(message);
    return 0;
  }
}`,
		},
		{
			name: "ibm_mq",
			source: `import javax.jms.*;
import com.ibm.mq.jms.MQQueueConnectionFactory;
public class Actions {
  public int action() {
    MessageProducer producer = session.createProducer(queue);
    producer.send(message);
    return 0;
  }
}`,
		},
		{
			name: "mixed_http_jdbc_jms",
			source: `import javax.jms.*;
import java.sql.*;
public class Actions {
  public int action() {
    web.url("Home", "URL=https://example.test", web.LAST);
    MessageProducer producer = session.createProducer(queue);
    producer.send(message);
    PreparedStatement statement = connection.prepareStatement("select 1");
    statement.executeQuery();
    return 0;
  }
}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"Actions.java": tc.source}
			project := &ProjectConfig{ScriptType: "Java_protocols", ScriptLanguage: "JAVA"}
			detected := DetectProtocol(files, project, nil)
			if len(detected.Signals) == 0 {
				t.Fatal("pre-conversion detection did not retain protocol-family review signals")
			}
			result, err := ConvertJava(files, ConvertOptions{}, detected, project)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			got := result.DetectedProtocol
			if got.DisplayName != "Java Vuser" || got.Bundle != javaVuserRunnerBundle {
				t.Fatalf("detected protocol = %#v, want generic combined-runner identity", got)
			}
			evidence := strings.ToLower(strings.Join(got.RequiredEvidence, "\n"))
			for _, forbidden := range []string{"solace", "dsn", "soapaction", "native adapter", "http replay"} {
				if strings.Contains(evidence, forbidden) {
					t.Fatalf("required evidence = %#v, retained native-family requirement %q", got.RequiredEvidence, forbidden)
				}
			}
			if !strings.Contains(evidence, javaVuserRunnerRequirement) ||
				!strings.Contains(evidence, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
				!strings.Contains(evidence, protocol.FeatureJavaVuserSessionFrameTokenV1) {
				t.Fatalf("required evidence = %#v, want governed persistent Java runner and authenticated frame-token requirements", got.RequiredEvidence)
			}
			if len(got.Signals) == 0 {
				t.Fatal("combined normalization discarded protocol-family review signals")
			}
		})
	}
}

func TestJavaProtocolEvidenceIgnoresUnusedImportsCommentsAndLiterals(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
import java.sql.*;
import javax.jms.*;
import javax.xml.soap.*;

public class Actions {
  public int action() {
    String hostname = "service.web.example";
    String fakeCalls = "producer.send(message); conn.executeQuery(); SOAPMessage";
    MessageProducer unusedProducer = null;
    SOAPMessage unusedSOAPMessage = null;
    // web.url("CommentOnly", "URL=https://example.test", web.LAST);
    /* MessageProducer producer; connection.createSOAPMessage(); */
    return 0;
  }
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	profile := classifyJavaArchive(program, nil, files)
	for _, surface := range []javaSurface{javaSurfaceHTTP, javaSurfaceJMS, javaSurfaceDatabase, javaSurfaceSOAP} {
		if profile.Surfaces[surface] {
			t.Fatalf("profile surfaces = %#v, unused/comment/literal evidence selected %q", profile.Surfaces, surface)
		}
	}

	detected := DetectProtocol(files, &ProjectConfig{ScriptType: "Java Vuser"}, nil)
	if detected.CanonicalSourceProtocol != "java_vuser" {
		t.Fatalf("detected = %#v, want generic Java Vuser without executed protocol evidence", detected)
	}
	for _, signal := range detected.Signals {
		lower := strings.ToLower(signal)
		if strings.Contains(lower, "api usage") || strings.Contains(lower, "jms/solace") {
			t.Fatalf("signal %q reports unused source as executed protocol evidence", signal)
		}
	}
	nonConventionalFiles := map[string]string{
		"Worker.java": strings.Replace(files["Actions.java"], "public class Actions", "public class Worker", 1),
	}
	nonConventional := DetectProtocol(nonConventionalFiles, &ProjectConfig{ScriptType: "Java Vuser"}, nil)
	if nonConventional.CanonicalSourceProtocol != "java_vuser" {
		t.Fatalf("non-conventional detected = %#v, raw Java imports/literals must not select a protocol through the archive fallback", nonConventional)
	}

	result, err := ConvertJava(files, ConvertOptions{}, detected, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
}

func TestConvertJavaLifecycleDiscoveryIsScopedToActionsClass(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    invoke();
    return 0;
  }
}`,
		"LifecycleHelper.java": `import lrapi.lr;
public class LifecycleHelper {
  public int init() { return 0; }
  public int action() {
    lr.start_transaction("WrongClass");
    lr.end_transaction("WrongClass", lr.AUTO);
    return 0;
  }
  public int end() { return 0; }
}`,
	}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if len(result.Script.Init) != 0 || len(result.Script.Teardown) != 0 {
		t.Fatalf("helper lifecycle methods leaked onto Actions target: init=%#v teardown=%#v", result.Script.Init, result.Script.Teardown)
	}
	if _, exists := result.Script.Actions[0].Metadata["emits_business_transactions"]; exists {
		t.Fatalf("helper-class transaction calls leaked onto Actions metadata: %#v", result.Script.Actions[0].Metadata)
	}
}

func TestConvertJavaUnparseableSourceStillReturnsSingletonDraft(t *testing.T) {
	files := map[string]string{"Actions.java": `public class Actions { public int action() { broken();`}
	result, err := ConvertJava(files, ConvertOptions{}, DetectedProtocol{CanonicalSourceProtocol: "java_vuser"}, nil)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	assertJavaLifecycleMethod(t, result.Script.Actions, "action")
	if result.ReviewArtifact == nil || len(result.ReviewArtifact.SourceFiles) == 0 {
		t.Fatalf("unparseable source must remain reviewable: artifact=%#v", result.ReviewArtifact)
	}
	// This fixture yields zero parseable lifecycle methods, so the draft must
	// carry the source-integrity blocker that keeps it from being promotable.
	if _, ok := result.ReviewArtifact.Metadata[javaSourceIntegrityKey]; !ok {
		t.Fatalf("artifact metadata = %#v, want %s blocker for unverified source", result.ReviewArtifact.Metadata, javaSourceIntegrityKey)
	}
}

// Finding: a Java archive with no parseable lifecycle source (missing .java
// files, or source the scanner cannot read at all) previously produced a
// PROMOTABLE runner draft targeting a nonexistent class; every VUser would then
// fail at runtime once a runner pool was enabled. Such archives must be blocked.
func TestConvertZipJavaArchiveWithoutParseableSourceIsBlocked(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"no_java_files", map[string]string{}},
		{"unparseable_single_line", map[string]string{"Actions.java": `public class Actions { public int action() { broken();`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaNoSource", tc.files), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			assertCombinedJavaImport(t, result)
			if result.FidelityReport == nil {
				t.Fatal("no fidelity report attached")
			}
			if result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
				t.Fatalf("promotion status = %q blockers=%v, want blocked draft for archive without parseable Java source", result.FidelityReport.PromotionStatus, result.FidelityReport.Blockers)
			}
			found := false
			for _, blocker := range result.FidelityReport.Blockers {
				if strings.Contains(blocker, "no parseable Java lifecycle method") {
					found = true
				}
			}
			if !found {
				t.Fatalf("blockers = %v, want the unverified-source reason", result.FidelityReport.Blockers)
			}
		})
	}
}

// Finding: any bundled helper/SDK class with a method named init/initialize/
// end/teardown must not be treated as an external lifecycle owner — that
// false-positively blocked promotion of complete imports with impossible
// "merge CacheClient.init into Actions" remediation advice.
func TestConvertZipJavaHelperClassLifecycleNamesDoNotBlockPromotion(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaHelperInit", map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    doWork();
    return 0;
  }
}`,
		"CacheClient.java": `public class CacheClient {
  public int init() {
    return 0;
  }
  public void initialize() {
  }
  public void teardown() {
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.Script.Actions[0].Target != "Actions" {
		t.Fatalf("target = %q, want the Actions class, not the helper", result.Script.Actions[0].Target)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable || len(result.FidelityReport.Blockers) != 0 {
		t.Fatalf("report = %#v, helper class lifecycle-named methods must not block promotion", result.FidelityReport)
	}
	if _, ok := result.ReviewArtifact.Metadata[javaLifecycleBlockersKey]; ok {
		t.Fatalf("artifact metadata = %#v, helper methods must not be recorded as external lifecycle blockers", result.ReviewArtifact.Metadata)
	}
}

// A REAL external lifecycle file (LR-convention vuser_init.java whose init was
// never merged into the selected runner class) must continue to block.
func TestConvertZipJavaExternalVuserInitFileStillBlocksPromotion(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaExternalInit", map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    doWork();
    return 0;
  }
}`,
		"vuser_init.java": `public class vuser_init {
  public int vuser_init() {
    connect();
    return 0;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("report = %#v, want blocked promotion for an unmerged external vuser_init.java", result.FidelityReport)
	}
	found := false
	for _, blocker := range result.FidelityReport.Blockers {
		if strings.Contains(blocker, "vuser_init") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blockers = %v, want the external vuser_init lifecycle blocker", result.FidelityReport.Blockers)
	}
}

func TestConvertZipJavaUSRDeclaredExternalLifecycleFilesBlockPromotion(t *testing.T) {
	archive := buildJavaVuserFilesArchiveWithActionSection(t, "JavaDeclaredLifecycleFiles", `Init=Setup.java
Action=Actions.java
End=Cleanup.java
`, map[string]string{
		"Setup.java": `public class Setup {
  public int init() {
    connect();
    return 0;
  }
}`,
		"Actions.java": `public class Actions {
  public int action() {
    doWork();
    return 0;
  }
}`,
		"Cleanup.java": `public class Cleanup {
  public int end() {
    disconnect();
    return 0;
  }
}`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if len(result.Script.Init) != 0 || len(result.Script.Teardown) != 0 {
		t.Fatalf("init=%#v teardown=%#v, external lifecycle methods cannot run on the selected Actions instance", result.Script.Init, result.Script.Teardown)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, .usr-declared external lifecycle files must block promotion", result.FidelityReport)
	}
	blockers := strings.Join(result.FidelityReport.Blockers, "\n")
	for _, want := range []string{"Setup.init", "Cleanup.end", "merge all lifecycle methods into one Actions class"} {
		if !strings.Contains(blockers, want) {
			t.Fatalf("fidelity blockers = %#v, missing %q", result.FidelityReport.Blockers, want)
		}
	}
	metadata, ok := result.ReviewArtifact.Metadata[javaExternalLifecycleKey].([]map[string]any)
	if !ok || len(metadata) != 2 {
		t.Fatalf("external lifecycle metadata = %#v, want Setup.init and Cleanup.end", result.ReviewArtifact.Metadata[javaExternalLifecycleKey])
	}
}

// Finding: with no class named Actions, the alphabetically-first .java file
// used to become the runner target even when it was a helper, declaring the
// real lifecycle owner "external" and blocking the import. The class owning an
// action-phase lifecycle method must win.
func TestConvertZipJavaActionOwnerPreferredOverAlphabeticalHelper(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaAlphaHelper", map[string]string{
		"AaaHelper.java": `public class AaaHelper {
  public void warm() {
  }
}`,
		"action.java": `public class action {
  public int action() {
    doWork();
    return 0;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 || result.Script.Actions[0].Target != "action" {
		t.Fatalf("actions = %#v, want the lifecycle-owning class selected as runner target", result.Script.Actions)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionPromotable || len(result.FidelityReport.Blockers) != 0 {
		t.Fatalf("report = %#v, alphabetical helper selection must not block a complete import", result.FidelityReport)
	}
}

func TestConvertZipJavaInheritedLifecycleFailsClosedOnActionsTarget(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaInheritedAction", map[string]string{
		"Actions.java": `public class Actions extends BaseActions {
}`,
		"BaseActions.java": `public class BaseActions {
  public int action() {
    return 0;
  }
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	if result.Script.Actions[0].Target != "Actions" {
		t.Fatalf("action target = %q, inherited lifecycle uncertainty must not retarget the draft to BaseActions", result.Script.Actions[0].Target)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, inherited-only lifecycle must fail closed until inheritance is verified", result.FidelityReport)
	}
	found := false
	for _, blocker := range result.FidelityReport.Blockers {
		if strings.Contains(blocker, "does not declare a parseable action lifecycle method") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blockers = %v, want missing verified action lifecycle blocker", result.FidelityReport.Blockers)
	}
}

// Finding: unparsed Java regions were double-counted (review_only at draft
// build AND external_required in the mapping pass) so the fidelity report
// claimed converted == expected despite unreadable source. Unparsed source is
// review evidence, not converted work, and the support counters must reconcile.
func TestConvertZipJavaUnparsedRegionsAreNotReportedConverted(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaUnparsedStats", map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    doWork();
    return 0;
  }
  public int broken() { if (true) {
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)
	stats := result.Stats
	if got := stats.NativeFunctions + stats.ExternalRequiredFunctions + stats.ReviewOnlyFunctions + stats.CustomCallFunctions; got != stats.TotalFunctions {
		t.Fatalf("support counters = %d (native=%d extreq=%d review=%d custom=%d), want them to reconcile with TotalFunctions=%d", got, stats.NativeFunctions, stats.ExternalRequiredFunctions, stats.ReviewOnlyFunctions, stats.CustomCallFunctions, stats.TotalFunctions)
	}
	if stats.ReviewOnlyFunctions < 1 {
		t.Fatalf("stats = %#v, want the unparsed region counted review_only", stats)
	}
	if result.FidelityReport == nil {
		t.Fatal("no fidelity report attached")
	}
	if result.FidelityReport.ConvertedFunctions >= result.FidelityReport.ExpectedFunctions {
		t.Fatalf("fidelity report converted=%d expected=%d, an unparsed region must not be reported as converted", result.FidelityReport.ConvertedFunctions, result.FidelityReport.ExpectedFunctions)
	}
	unparsed := 0
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Status == "review_only" && mapping.SupportMode != string(SourceFunctionSupportReviewOnly) {
			t.Fatalf("mapping = %#v, review_only mappings must carry the review_only support mode", mapping)
		}
		if mapping.SupportMode == string(SourceFunctionSupportReviewOnly) {
			unparsed++
		}
	}
	if unparsed != 1 {
		t.Fatalf("mappings = %#v, want exactly one review_only disposition for the unparsed region", result.SourceReview.Mappings)
	}
	if got, ok := result.ReviewArtifact.Metadata["unparsed_source_regions"].(int); !ok || got != 1 {
		t.Fatalf("artifact metadata = %#v, want unparsed_source_regions=1", result.ReviewArtifact.Metadata)
	}
}

func assertCombinedJavaImport(t *testing.T, result *ConvertResult) {
	t.Helper()
	if result == nil || result.Script == nil {
		t.Fatalf("result = %#v, want Java script", result)
	}
	if len(result.Scripts) != 1 || result.Scripts[0] != result.Script {
		t.Fatalf("scripts = %#v script=%p, want explicit singleton identity", result.Scripts, result.Script)
	}
	if result.Script.Protocol != "java_vuser" {
		t.Fatalf("protocol = %q, want java_vuser", result.Script.Protocol)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" || result.DetectedProtocol.APEXLoadProtocol != "java_vuser" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired || result.DetectedProtocol.RunnerRequirement != javaVuserRunnerRequirement {
		t.Fatalf("detected = %#v, want external-runner java_vuser", result.DetectedProtocol)
	}
}

func assertJavaLifecycleMethod(t *testing.T, actions []dsl.Action, method string) {
	t.Helper()
	if len(actions) != 1 || actions[0].Type != "java_vuser_run" || actions[0].Target != "Actions" || actions[0].Params["method"] != method {
		t.Fatalf("actions = %#v, want one java_vuser_run method=%s", actions, method)
	}
}
