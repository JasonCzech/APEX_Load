package enterprisearchive

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildJavaVuserSourceManifestBindsEveryCompleteJavaFile(t *testing.T) {
	files := map[string]string{
		"support\\Helper.java": "public class Helper {}",
		"Actions.java":         "public class Actions { public int action() { return 0; } }",
		"default.cfg":          "[General]\n",
	}
	manifest := BuildJavaVuserSourceManifest(files)
	if err := ValidateJavaVuserSourceManifest(manifest); err != nil {
		t.Fatalf("ValidateJavaVuserSourceManifest: %v", err)
	}
	if manifest.FileCount != 2 || manifest.TotalBytes != int64(len(files["support\\Helper.java"])+len(files["Actions.java"])) {
		t.Fatalf("manifest = %#v, want both and only complete Java sources", manifest)
	}
	if manifest.Files[0].Path != "Actions.java" || manifest.Files[1].Path != "support/Helper.java" {
		t.Fatalf("manifest files = %#v, want canonical sorted paths", manifest.Files)
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var ordinaryMap map[string]any
	if err := json.Unmarshal(data, &ordinaryMap); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJavaVuserSourceManifest(ordinaryMap)
	if err != nil {
		t.Fatalf("DecodeJavaVuserSourceManifest(map): %v", err)
	}
	if decoded.Digest != manifest.Digest {
		t.Fatalf("decoded digest = %q, want %q", decoded.Digest, manifest.Digest)
	}
}

func TestValidateJavaVuserSourceManifestFailsClosedOnTampering(t *testing.T) {
	manifest := BuildJavaVuserSourceManifest(map[string]string{
		"Actions.java": "public class Actions { public int action() { return 0; } }",
	})
	manifest.Files[0].SHA256 = "sha256:" + strings.Repeat("0", 64)
	if err := ValidateJavaVuserSourceManifest(manifest); err == nil {
		t.Fatal("ValidateJavaVuserSourceManifest accepted a file digest not covered by the manifest digest")
	}
}

func TestJavaVuserSourceManifestRejectsCanonicalPathAliases(t *testing.T) {
	files := map[string]string{
		"Actions.java":      "public class Actions {}",
		"x/../Actions.java": "public class Replacement {}",
	}
	if manifest := BuildJavaVuserSourceManifest(files); manifest != nil {
		t.Fatalf("BuildJavaVuserSourceManifest accepted canonical path aliases: %#v", manifest)
	}

	manifest := BuildJavaVuserSourceManifest(map[string]string{
		"Actions.java": "public class Actions {}",
	})
	manifest.Files = append(manifest.Files, JavaVuserSourceManifestFile{
		Path:   "x/../Actions.java",
		Bytes:  0,
		SHA256: "sha256:" + strings.Repeat("0", 64),
	})
	manifest.FileCount = len(manifest.Files)
	manifest.Digest = javaVuserSourceManifestDigest(manifest.Files)
	if err := ValidateJavaVuserSourceManifest(manifest); err == nil {
		t.Fatal("ValidateJavaVuserSourceManifest accepted x/../Actions.java alias")
	}
}
