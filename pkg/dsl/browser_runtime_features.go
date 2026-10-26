package dsl

import "strings"

const (
	FeatureBrowserDocumentEvidenceV2    = "browser_document_evidence_v2"
	FeatureBrowserInteractionEvidenceV2 = "browser_interaction_evidence_v2"
	FeatureBrowserInteractionEvidenceV1 = "browser_interaction_evidence_v1"
	// FeatureBrowserNativeHARV1 is native HAR capture, ownership and archive
	// transfer on a platform where native HAR staging and publication are
	// qualified.
	FeatureBrowserNativeHARV1 = "browser_native_har_v1"
	// FeatureBrowserSessionIdentitiesV1 is config.browser.session: observer
	// identities, the session preflight, start gaps and session_lost. A
	// generator without it would run every observer as nobody.
	FeatureBrowserSessionIdentitiesV1 = "browser_session_identities_v1"
	// FeatureBrowserSalesforceJWTV1 is the salesforce_jwt session strategy.
	FeatureBrowserSalesforceJWTV1 = "browser_salesforce_jwt_v1"
)

// RequiredBrowserRuntimeFeatures admits explicitly requested document,
// interaction and native HAR capture, and observer sessions, before execution
// or traffic-free preparation can select an older or unsupported peer.
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
	if BrowserHARCaptureRequested(script) {
		features = append(features, FeatureBrowserNativeHARV1)
	}
	return features
}

// BrowserDocumentCaptureRequested reports an explicit document-metric opt-in.
func BrowserDocumentCaptureRequested(script *Script) bool {
	return isBrowserProtocol(script) && script.Config.Browser.CaptureDocumentMetrics != nil && *script.Config.Browser.CaptureDocumentMetrics
}

// BrowserInteractionCaptureRequested reports an explicit interaction
// evidence opt-in.
func BrowserInteractionCaptureRequested(script *Script) bool {
	return isBrowserProtocol(script) && script.Config.Browser.CaptureInteractions != nil && *script.Config.Browser.CaptureInteractions
}

// EffectiveBrowserHARCapturePolicy resolves a browser script's effective HAR
// capture policy with the same precedence as the engine and browser runtime:
// any non-empty HARCapturePolicy overrides HARCapture, and only on_error,
// on_sla_breach, manual and always enable recording.
func EffectiveBrowserHARCapturePolicy(script *Script) string {
	if !isBrowserProtocol(script) {
		return "off"
	}
	raw := script.Config.Browser.HARCapture
	if script.Config.Browser.HARCapturePolicy != "" {
		raw = script.Config.Browser.HARCapturePolicy
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on_error":
		return "on_error"
	case "on_sla_breach":
		return "on_sla_breach"
	case "manual":
		return "manual"
	case "always":
		return "always"
	default:
		return "off"
	}
}

// BrowserHARCaptureRequested reports an explicit native HAR capture opt-in.
func BrowserHARCaptureRequested(script *Script) bool {
	return EffectiveBrowserHARCapturePolicy(script) != "off"
}

func browserSessionForFeatures(script *Script) *BrowserSessionConfig {
	if !isBrowserProtocol(script) {
		return nil
	}
	return script.Config.Browser.Session
}

func isBrowserProtocol(script *Script) bool {
	return script != nil && strings.EqualFold(strings.TrimSpace(script.Protocol), "browser")
}
