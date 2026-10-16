package enterprisearchive

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestJavaDependencyBundleRemainsInertAndNeverPreviewsBinaryContent(t *testing.T) {
	files := map[string]string{"client.jar": "PK\x03\x04synthetic-private-jar", "Infra/BasicClient.class": "\xca\xfe\xba\xbe\x00\x00\x00\x2eprivate-class", "Infra/jad.exe": "MZ" + strings.Repeat("private-executable", 5), "cache.store": "private-store", "helper.txt": "plain helper text"}
	result := newEnterpriseArchiveReviewResult(DetectedProtocol{ImportMode: ImportModeReviewOnly}, files, nil, nil)
	if result.Script != nil {
		t.Fatal("dependency bundle became executable")
	}
	inventory, ok := result.ReviewArtifact.Metadata[javaDependencyInventoryKey].(javaDependencyInventory)
	if !ok || inventory.Classification != "java_dependency_bundle" || inventory.RuntimeProvisioned || inventory.Counts.JARs != 1 || inventory.Counts.Classes != 1 || inventory.Counts.Executables != 1 || inventory.Counts.OtherBinaryAssets != 1 {
		t.Fatal("incorrect inert dependency accounting")
	}
	if len(result.SourceReview.SourceFiles) != 1 || result.SourceReview.SourceFiles[0].Path != "helper.txt" {
		t.Fatal("binary content became source review")
	}
	assets := result.ReviewArtifact.Metadata[sourceAssetInventoryKey].(sourceAssetInventory)
	if assets.FileCount != 5 || assets.OpaqueAssetCount != 4 || len(assets.Files) != 5 {
		t.Fatal("source asset accounting incomplete")
	}
	raw, _ := json.Marshal(inventory)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("binary content in public evidence")
	}
	for _, file := range inventory.Files {
		if len(file.SHA256) != 64 || file.SizeBytes <= 0 {
			t.Fatal("missing fingerprint")
		}
		if strings.HasSuffix(file.SourcePath, ".class") && (file.MajorVersion == nil || *file.MajorVersion != 46) {
			t.Fatal("class header not accounted")
		}
	}
}

func TestJavaDependencyInventorySurvivesDraftSerializationWithoutRuntimeBinding(t *testing.T) {
	result := &ConvertResult{Script: &dsl.Script{Protocol: "java_vuser"}, ReviewArtifact: &ReviewArtifact{}}
	attachJavaDependencyInventory(result, map[string]string{"client.jar": "PK\x03\x04private-jar", "Actions.java": "class Actions {}", "previous.log": "private-log"})
	raw, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	var saved dsl.Script
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	inventory := saved.Metadata.Import.Preserved[javaDependencyInventoryKey].(map[string]any)
	if inventory["runtime_provisioned"] != false || inventory["classification"] != "supplemental_java_assets" || !reflect.DeepEqual(saved.Config.JavaVuser, dsl.JavaVuserConfig{}) {
		t.Fatal("inventory implied runner/classpath provision")
	}
	if strings.Contains(string(raw), "private-") {
		t.Fatal("raw asset data persisted in inventory")
	}
	assets := saved.Metadata.Import.Preserved[sourceAssetInventoryKey].(map[string]any)
	if assets["file_count"] != float64(3) {
		t.Fatal("supporting source omitted from accounting")
	}
}

func TestUnsupportedSourceProjectIsNotRelabeledAsDependencyOnly(t *testing.T) {
	for _, source := range []string{"script.usr", "Main.java", "Action.c", "default.xml"} {
		result := newEnterpriseArchiveReviewResult(DetectedProtocol{ImportMode: ImportModeUnsupported, CanonicalSourceProtocol: "sap_gui"}, map[string]string{source: "source project evidence", "client.jar": "PK\x03\x04synthetic"}, nil, nil)
		inventory := result.ReviewArtifact.Metadata[javaDependencyInventoryKey].(javaDependencyInventory)
		if inventory.Classification != "supplemental_java_assets" || result.DetectedProtocol.CanonicalSourceProtocol != "sap_gui" {
			t.Fatal("source project relabeled as dependency bundle")
		}
	}
}

func TestConvertZipJavaDependencyInventorySurvivesFinalizationAndDraftSerialization(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr; public class Actions {
   public int init() { return 0; }
   public int action() { lr.output_message("synthetic"); return 0; }
   public int end() { return 0; }
  }`,
		"client.jar":   "PK\x03\x04binary-canary-jar",
		"Helper.class": "\xca\xfe\xba\xbe\x00\x00\x00\x34binary-canary-class",
		"cache.store":  "binary-canary-store",
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "DependencyFixture", files), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" || result.Script.Config.JavaVuser.Enabled {
		t.Fatal("dependency evidence changed the governed Java lane")
	}
	raw, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	var saved dsl.Script
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	preserved := saved.Metadata.Import.Preserved
	inventory, ok := preserved[javaDependencyInventoryKey].(map[string]any)
	if !ok || inventory["runtime_provisioned"] != false || inventory["classification"] != "supplemental_java_assets" {
		t.Fatal("finalized draft lost inert dependency inventory")
	}
	assets, ok := preserved[sourceAssetInventoryKey].(map[string]any)
	if !ok || assets["file_count"] != float64(len(files)+1) || assets["opaque_asset_count"] != float64(3) {
		t.Fatal("finalized draft lost complete asset accounting")
	}
	canonical := preserved["review_artifact"].(map[string]any)["metadata"].(map[string]any)
	if !reflect.DeepEqual(canonical[javaDependencyInventoryKey], inventory) || !reflect.DeepEqual(canonical[sourceAssetInventoryKey], assets) {
		t.Fatal("direct and canonical inventory disagree after finalization")
	}
	for _, source := range result.SourceReview.SourceFiles {
		if strings.Contains(source.Content, "binary-canary") {
			t.Fatal("opaque bytes exposed in completed source review")
		}
	}
	if strings.Contains(string(raw), "binary-canary") {
		t.Fatal("binary bytes exposed in serialized draft")
	}
	if !reflect.DeepEqual(saved.Config.JavaVuser.Classpath, []string{JavaVuserScriptClassesEntry}) {
		t.Fatal("asset inventory provisioned executable classpath")
	}
}
