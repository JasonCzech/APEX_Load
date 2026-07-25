package generatorrpc

import (
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// PrepareDistributedScript must strip the controller-local StorageRef (and any
// StagedAsset) from Java Vuser classpath assets before transport, so a generator
// fails closed rather than reading a controller path, and no controller path
// leaks into the assignment.
func TestPrepareDistributedScriptStripsClasspathAssetStorageRef(t *testing.T) {
	script := &dsl.Script{
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			Classpath: []string{"app.jar"},
			ClasspathAssets: []dsl.JavaClasspathAsset{{
				Entry:      "app.jar",
				SHA256:     "abc",
				StorageRef: "file:///controller/secret/app.jar",
			}},
		}},
	}
	content, _, files, err := PrepareDistributedScript(script)
	if err != nil {
		t.Fatalf("PrepareDistributedScript: %v", err)
	}
	if strings.Contains(string(content), "/controller/secret/") {
		t.Fatalf("controller path leaked into transported script: %s", content)
	}
	for _, f := range files {
		if strings.Contains(f.Name, "app.jar") {
			t.Fatalf("classpath asset must not be staged as a DataFile: %q", f.Name)
		}
	}
	var staged dsl.Script
	if err := json.Unmarshal(content, &staged); err != nil {
		t.Fatalf("unmarshal staged: %v", err)
	}
	if len(staged.Config.JavaVuser.ClasspathAssets) != 1 {
		t.Fatalf("expected the asset binding preserved (sans refs), got %#v", staged.Config.JavaVuser.ClasspathAssets)
	}
	if ref := staged.Config.JavaVuser.ClasspathAssets[0].StorageRef; ref != "" {
		t.Fatalf("StorageRef not stripped: %q", ref)
	}
}

// The generator-side validator must reject an assignment whose classpath asset
// still carries a local reference (defense in depth against a hand-crafted or
// refactor-broken assignment).
func TestAssignmentDataFilesRejectsClasspathAssetLocalRef(t *testing.T) {
	script := &dsl.Script{
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			ClasspathAssets: []dsl.JavaClasspathAsset{{Entry: "app.jar", SHA256: "abc", StorageRef: "file:///x/app.jar"}},
		}},
	}
	_, err := assignmentDataFiles(&pb.WorkAssignment{}, script, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "classpath asset") {
		t.Fatalf("expected rejection of inbound classpath-asset local ref, got %v", err)
	}
}
