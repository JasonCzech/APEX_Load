package dsl

import "testing"

func TestBrowserDocumentRuntimeFeatureIsExplicit(t *testing.T) {
	yes, no := true, false
	script := &Script{Protocol: "browser"}
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 {
		t.Fatal("default capture enabled")
	}
	script.Config.Browser.CaptureDocumentMetrics = &no
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 {
		t.Fatal("explicit false requires capture")
	}
	script.Config.Browser.CaptureDocumentMetrics = &yes
	if f := RequiredBrowserRuntimeFeatures(script); len(f) != 1 || f[0] != FeatureBrowserDocumentEvidenceV2 {
		t.Fatal("explicit capture lacks peer admission")
	}
	script.Protocol = "http"
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 {
		t.Fatal("other protocol acquired browser requirement")
	}
}
