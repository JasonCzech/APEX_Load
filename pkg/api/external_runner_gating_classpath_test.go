package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func javaVuserGroupWithClasspathAsset() TestGroup {
	script := &dsl.Script{
		Protocol: "java_vuser",
		// full_lifecycle avoids the persistent-lifecycle approval branch so the
		// test isolates the classpath-asset conflict.
		Actions: []dsl.Action{{Name: "Actions.action", Type: "java_vuser_run", Params: map[string]any{"method": "full_lifecycle"}}},
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			ClasspathAssets: []dsl.JavaClasspathAsset{{Entry: "app.jar", SHA256: "abc", StorageRef: "file:///assets/app.jar"}},
		}},
	}
	return TestGroup{Name: "java", Protocol: "java_vuser", script: script}
}

func conflictCodes(conflicts []LaunchConflict) map[string]bool {
	out := map[string]bool{}
	for _, c := range conflicts {
		out[c.Code] = true
	}
	return out
}

func TestLaunchConflictClasspathAssetUnresolvedWhenFlagOff(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED", "")
	tr := &TestRunner{}
	conflicts := tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{javaVuserGroupWithClasspathAsset()})
	if !conflictCodes(conflicts)["java_vuser_classpath_asset_unresolved"] {
		t.Fatalf("expected java_vuser_classpath_asset_unresolved with flag off, got %#v", conflicts)
	}
}

func TestLaunchConflictNoAssetConflictWhenFlagOnLocal(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED", "true")
	tr := &TestRunner{} // nil execution -> local backend
	conflicts := tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{javaVuserGroupWithClasspathAsset()})
	codes := conflictCodes(conflicts)
	if codes["java_vuser_classpath_asset_unresolved"] || codes["java_vuser_classpath_asset_distributed_unsupported"] {
		t.Fatalf("no classpath-asset conflict expected with flag on + local runner, got %#v", conflicts)
	}
}

func TestLaunchConflictDistributedAssetsUnsupportedWhenFlagOn(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED", "true")
	tr := &TestRunner{execution: &distributedExecutionBackend{}}
	conflicts := tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{javaVuserGroupWithClasspathAsset()})
	if !conflictCodes(conflicts)["java_vuser_classpath_asset_distributed_unsupported"] {
		t.Fatalf("expected distributed-unsupported conflict, got %#v", conflicts)
	}
}

func TestLaunchConflictNoAssetConflictWithoutAssets(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED", "")
	group := javaVuserGroupWithClasspathAsset()
	group.script.Config.JavaVuser.ClasspathAssets = nil
	tr := &TestRunner{}
	conflicts := tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{group})
	codes := conflictCodes(conflicts)
	if codes["java_vuser_classpath_asset_unresolved"] || codes["java_vuser_classpath_asset_distributed_unsupported"] {
		t.Fatalf("no classpath-asset conflict expected without assets, got %#v", conflicts)
	}
}

func TestLaunchConflictUnboundScriptClassesEntry(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED", "true")
	group := javaVuserGroupWithClasspathAsset()
	group.script.Config.JavaVuser.Classpath = []string{enterprisearchive.JavaVuserScriptClassesEntry, "app.jar"}
	tr := &TestRunner{}
	if !conflictCodes(tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{group}))["java_vuser_script_classes_missing"] {
		t.Fatal("an unbound script classes entry must block the launch")
	}
	group.script.Config.JavaVuser.ClasspathAssets = append(group.script.Config.JavaVuser.ClasspathAssets, dsl.JavaClasspathAsset{Entry: enterprisearchive.JavaVuserScriptClassesEntry, SHA256: "def", StorageRef: "file:///assets/classes.jar"})
	if conflictCodes(tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{group}))["java_vuser_script_classes_missing"] {
		t.Fatal("a bound script classes entry must not block the launch")
	}
	group.script.Config.JavaVuser.Classpath = []string{"app.jar"}
	group.script.Config.JavaVuser.ClasspathAssets = nil
	if conflictCodes(tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, []TestGroup{group}))["java_vuser_script_classes_missing"] {
		t.Fatal("a classpath without the entry must not block the launch")
	}
}
