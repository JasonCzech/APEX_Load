package dsl

const (
	FeatureBrowserDocumentEvidenceV2    = "browser_document_evidence_v2"
	FeatureBrowserInteractionEvidenceV2 = "browser_interaction_evidence_v2"
	FeatureBrowserInteractionEvidenceV1 = "browser_interaction_evidence_v1"
	// FeatureBrowserSessionIdentitiesV1 is config.browser.session: observer
	// identities, the session preflight, start gaps and session_lost. A
	// generator without it would run every observer as nobody.
	FeatureBrowserSessionIdentitiesV1 = "browser_session_identities_v1"
	// FeatureBrowserSalesforceJWTV1 is the salesforce_jwt session strategy.
	FeatureBrowserSalesforceJWTV1 = "browser_salesforce_jwt_v1"
)

// RequiredBrowserRuntimeFeatures admits explicitly requested document and
// interaction capture, and observer sessions, before execution or
// traffic-free preparation can select an older peer.
func RequiredBrowserRuntimeFeatures(script *Script) []string {
	var features []string
	if session := browserSessionForFeatures(script); session != nil {
		features = append(features, FeatureBrowserSessionIdentitiesV1)
		if session.Strategy == BrowserSessionSalesforceJWT {
			features = append(features, FeatureBrowserSalesforceJWTV1)
		}
	}
	if BrowserDocumentCaptureRequested(script) {
		features = append(features, FeatureBrowserDocumentEvidenceV2)
	}
	if BrowserInteractionCaptureRequested(script) {
		features = append(features, FeatureBrowserInteractionEvidenceV2)
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

func browserSessionForFeatures(script *Script) *BrowserSessionConfig {
	if script == nil || script.Protocol != "browser" {
		return nil
	}
	return script.Config.Browser.Session
}
