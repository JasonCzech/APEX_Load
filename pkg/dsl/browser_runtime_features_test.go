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

func TestBrowserInteractionCaptureIsItsOwnRuntimeFeature(t *testing.T) {
	yes := true
	script := &Script{Protocol: "browser"}
	script.Config.Browser.CaptureInteractions = &yes
	if got := RequiredBrowserRuntimeFeatures(script); len(got) != 1 || got[0] != FeatureBrowserInteractionEvidenceV1 {
		t.Fatalf("features %v", got)
	}
	if BrowserDocumentCaptureRequested(script) || !BrowserInteractionCaptureRequested(script) {
		t.Fatal("interaction capture read as document capture")
	}
	script.Config.Browser.CaptureDocumentMetrics = &yes
	if got := RequiredBrowserRuntimeFeatures(script); len(got) != 2 {
		t.Fatalf("features %v", got)
	}
	script.Protocol = "http"
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 || BrowserInteractionCaptureRequested(script) {
		t.Fatal("non-browser script requires browser features")
	}
}
