package dsl

const FeatureBrowserDocumentEvidenceV2 = "browser_document_evidence_v2"

// RequiredBrowserRuntimeFeatures admits explicitly requested document capture
// before execution or traffic-free preparation can select an older peer.
func RequiredBrowserRuntimeFeatures(script *Script) []string {
	if script == nil || script.Protocol != "browser" || script.Config.Browser.CaptureDocumentMetrics == nil || !*script.Config.Browser.CaptureDocumentMetrics {
		return nil
	}
	return []string{FeatureBrowserDocumentEvidenceV2}
}
