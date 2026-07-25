package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidateCIScriptAssetsRejectsAnyClasspathAsset(t *testing.T) {
	// User-uploaded classpath JARs are unsupported in inline CI runs regardless
	// of whether they carry a managed reference; reject explicitly.
	for _, asset := range []dsl.JavaClasspathAsset{
		{Entry: "app.jar", SHA256: "abc"},
		{Entry: "app.jar", SHA256: "abc", StorageRef: "file:///assets/app.jar"},
	} {
		script := &dsl.Script{
			Protocol: "java_vuser",
			Config:   dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{ClasspathAssets: []dsl.JavaClasspathAsset{asset}}},
		}
		err := validateCIScriptAssets(script)
		if err == nil || !strings.Contains(err.Error(), "classpath_assets") {
			t.Fatalf("asset %#v: expected classpath_assets rejection, got %v", asset, err)
		}
	}
}

func TestValidateCIScriptAssetsAllowsNoClasspathAssets(t *testing.T) {
	script := &dsl.Script{Protocol: "java_vuser"}
	if err := validateCIScriptAssets(script); err != nil {
		t.Fatalf("a java_vuser script without classpath assets should pass, got %v", err)
	}
}
