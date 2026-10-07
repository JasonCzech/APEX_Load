package dsl

const (
	FeatureBrowserDocumentEvidenceV2    = "browser_document_evidence_v2"
	FeatureBrowserInteractionEvidenceV1 = "browser_interaction_evidence_v1"
)

// RequiredBrowserRuntimeFeatures admits explicitly requested document and
// interaction capture before execution or traffic-free preparation can
// select an older peer.
func RequiredBrowserRuntimeFeatures(script *Script) []string {
	var features []string
	if BrowserDocumentCaptureRequested(script) {
		features = append(features, FeatureBrowserDocumentEvidenceV2)
	}
	if BrowserInteractionCaptureRequested(script) {
		features = append(features, FeatureBrowserInteractionEvidenceV1)
	}
	return features
}

// BrowserDocumentCaptureRequested reports an explicit document-metric opt-in.
func BrowserDocumentCaptureRequested(script *Script) bool {
	return script != nil && script.Protocol == "browser" && script.Config.Browser.CaptureDocumentMetrics != nil && *script.Config.Browser.CaptureDocumentMetrics
}

// BrowserInteractionCaptureRequested reports an explicit interaction
// evidence opt-in.
func BrowserInteractionCaptureRequested(script *Script) bool {
	return script != nil && script.Protocol == "browser" && script.Config.Browser.CaptureInteractions != nil && *script.Config.Browser.CaptureInteractions
}
