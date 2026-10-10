package requestimport

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// HARHTTPVersionProvenanceKey carries recorder provenance for an entry's
// HTTP version: version 1 describes the two observed proxy legs, and never
// claims that an intercepted client exchange used the upstream wire protocol;
// version 2 names a browser capture. Provenance is descriptive only: it never
// bypasses validation or clears an unrelated gate.
const HARHTTPVersionProvenanceKey = protocol.HARHTTPVersionProvenanceKey

// Reason codes for HTTP/3 observations.
const (
	// ReasonHTTP3DraftToken: a pre-RFC 9114 token such as h3-29. The leaf
	// keeps its inherited policy and promotion waits for review; moving to
	// final HTTP/3 is an explicit modernization edit.
	ReasonHTTP3DraftToken = "http3_draft_token"
	// CodeCapturedH3 (info): an observed final h3 was mapped by the
	// http_version_mode rules.
	CodeCapturedH3 = "captured_h3"
	// CodeH3NotPreserved (warning): inherit mode dropped an observed h3.
	CodeH3NotPreserved = "h3_not_preserved"
)

// HTTP version modes decide which leaf policy an observed version becomes.
const (
	// HTTPVersionModeUpgrade (default): final h3 becomes auto+h3; HTTP/2 and
	// HTTP/1.1 stay exact.
	HTTPVersionModeUpgrade = "upgrade"
	// HTTPVersionModeExact: final h3 becomes exact 3; HTTP/2 and HTTP/1.1 stay
	// exact.
	HTTPVersionModeExact = "exact"
	// HTTPVersionModeInherit: no observed version becomes a leaf policy.
	HTTPVersionModeInherit = "inherit"
)

// NormalizeHTTPVersionMode accepts an import option value; empty is upgrade.
func NormalizeHTTPVersionMode(raw string) (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(raw)); mode {
	case "":
		return HTTPVersionModeUpgrade, nil
	case HTTPVersionModeUpgrade, HTTPVersionModeExact, HTTPVersionModeInherit:
		return mode, nil
	}
	return "", fmt.Errorf("http_version_mode must be %s, %s or %s", HTTPVersionModeUpgrade, HTTPVersionModeExact, HTTPVersionModeInherit)
}

// HTTPVersionPolicyForCapture returns the leaf policy an admitted observation
// maps to under mode, or "" when the leaf inherits.
func HTTPVersionPolicyForCapture(capture HTTPVersionCapture, mode string) string {
	if capture.Version == "" || mode == HTTPVersionModeInherit {
		return ""
	}
	if capture.Version == httpprotocol.VersionHTTP3 {
		if mode == HTTPVersionModeExact {
			return httpprotocol.VersionHTTP3
		}
		return httpprotocol.VersionAutoH3
	}
	return capture.Version
}

const maxCapturedHTTPVersionBytes = 256

// HTTPVersionCapture is an admission decision, containing only bounded enums
// and supported canonical versions. Arbitrary source values stay in the source
// artifact, rather than becoming diagnostics or executable transport settings.
type HTTPVersionCapture struct {
	Version         string
	ReasonCode      string
	RequestState    string
	ResponseState   string
	RequestVersion  string
	ResponseVersion string
	Provenance      string
	Proxy           bool
}

func capturedHTTPVersion(raw any, present bool) (string, string) {
	if !present || raw == nil {
		return "", "unavailable"
	}
	value, ok := raw.(string)
	if !ok || len(value) > maxCapturedHTTPVersionBytes {
		return "", "malformed"
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "unknown") {
		return "", "unavailable"
	}
	if http3DraftToken(value) {
		return "", "draft"
	}
	version := httpprotocol.NormalizeVersion(value)
	if version != httpprotocol.VersionHTTP11 && version != httpprotocol.VersionHTTP2 && version != httpprotocol.VersionHTTP3 {
		return "", "unsupported"
	}
	return version, "available"
}

// http3DraftToken matches pre-RFC 9114 ALPN tokens (h3-29, h3-Q050, h3-T051).
func http3DraftToken(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "h3-") && len(lower) > len("h3-")
}

// ProxyHTTPVersionProvenance returns the only supported recorder provenance
// envelope. Request and response versions remain separate HAR fields.
func ProxyHTTPVersionProvenance() map[string]any {
	return map[string]any{"version": 1, "request_leg": "client_to_proxy", "response_leg": "proxy_to_origin"}
}

// BrowserHTTPVersionProvenance is the provenance a browser capture writes
// (protocol.BrowserHARHTTPVersionProvenance), with observed given directly
// as h1, h2, h3 or unknown.
func BrowserHTTPVersionProvenance(source, observed string) map[string]any {
	return map[string]any{"version": 2, "source": source, "observed": observed}
}

// browserHTTPVersionProvenance validates a version 2 envelope and returns the
// version its observation names ("" for unknown).
func browserHTTPVersionProvenance(raw any) (string, bool) {
	value, ok := raw.(map[string]any)
	if !ok || len(value) != 3 || !provenanceVersionIs(value["version"], 2) {
		return "", false
	}
	if source := value["source"]; source != protocol.HARProvenanceBrowser && source != protocol.HARProvenanceBrowserRun {
		return "", false
	}
	switch value["observed"] {
	case "h1":
		return httpprotocol.VersionHTTP11, true
	case "h2":
		return httpprotocol.VersionHTTP2, true
	case "h3":
		return httpprotocol.VersionHTTP3, true
	case "unknown":
		return "", true
	}
	return "", false
}

func provenanceVersionIs(raw any, want int) bool {
	switch version := raw.(type) {
	case int:
		return version == want
	case float64:
		return version == float64(want)
	case json.Number:
		return version.String() == fmt.Sprint(want)
	}
	return false
}

func validProxyHTTPVersionProvenance(raw any) bool {
	value, ok := raw.(map[string]any)
	if !ok || len(value) != 3 || value["request_leg"] != "client_to_proxy" || value["response_leg"] != "proxy_to_origin" {
		return false
	}
	return provenanceVersionIs(value["version"], 1)
}

// ReviewHTTPVersionCapture distinguishes a missing observation from legal DSL
// inheritance. Ordinary HAR uses its request observation; a recorder exchange
// requires supported, equal observations on both explicitly identified legs.
// A browser capture's provenance is checked against the entry's own version
// and otherwise only described.
func ReviewHTTPVersionCapture(request any, requestPresent bool, response any, responsePresent bool, provenance any, provenancePresent, legacyRecorder bool) HTTPVersionCapture {
	requestVersion, requestState := capturedHTTPVersion(request, requestPresent)
	responseVersion, responseState := capturedHTTPVersion(response, responsePresent)
	browserObserved, browser := "", false
	if provenancePresent {
		browserObserved, browser = browserHTTPVersionProvenance(provenance)
	}
	decision := HTTPVersionCapture{RequestState: requestState, ResponseState: responseState, RequestVersion: requestVersion, ResponseVersion: responseVersion, Provenance: "request", Proxy: (provenancePresent && !browser) || legacyRecorder}
	if browser {
		decision.Provenance = "browser"
		if browserObserved != "" && requestState == "available" && browserObserved != requestVersion {
			// The envelope contradicts the observation it describes.
			provenancePresent, browser = true, false
		}
	}
	if provenancePresent && !browser && !validProxyHTTPVersionProvenance(provenance) {
		decision.RequestState, decision.ResponseState = "unavailable", "unavailable"
		decision.RequestVersion, decision.ResponseVersion = "", ""
		decision.Provenance = "unavailable"
		decision.ReasonCode = "malformed_http_version_provenance"
		return decision
	}
	if legacyRecorder && !provenancePresent {
		decision.RequestState, decision.ResponseState = "unavailable", "unavailable"
		decision.RequestVersion, decision.ResponseVersion = "", ""
		decision.Provenance = "unavailable"
		decision.ReasonCode = "http_version_unavailable"
		return decision
	}
	if decision.Proxy {
		decision.Provenance = "proxy_legs"
	}
	states := []string{requestState}
	if decision.Proxy {
		states = append(states, responseState)
	}
	for _, state := range states {
		if state == "malformed" {
			decision.ReasonCode = "malformed_http_version"
			return decision
		}
	}
	for _, state := range states {
		if state == "draft" {
			decision.ReasonCode = ReasonHTTP3DraftToken
			return decision
		}
	}
	for _, state := range states {
		if state == "unsupported" {
			decision.ReasonCode = reasonUnsupportedHTTPVersion
			return decision
		}
	}
	for _, state := range states {
		if state == "unavailable" {
			decision.ReasonCode = "http_version_unavailable"
			return decision
		}
	}
	if decision.Proxy && requestVersion != responseVersion {
		decision.ReasonCode = "proxy_http_version_legs_differ"
		return decision
	}
	decision.Version = requestVersion
	return decision
}

// HTTPVersionCaptureQualityOnly reports capture gaps that leave a leaf on its
// inherited HTTP version policy. A missing or "unknown" observation (LoadRunner
// HAR exports), a protocol the native runtime cannot select exactly (HTTP/1.0)
// or a draft HTTP/3 token says nothing about whether the request can replay.
// Malformed values and contradictory recorder provenance still require review.
func HTTPVersionCaptureQualityOnly(reason string) bool {
	return reason == "http_version_unavailable" || reason == reasonUnsupportedHTTPVersion || reason == ReasonHTTP3DraftToken
}

// ApplyHTTPVersionCapture attaches a durable gate, except for capture-quality
// gaps, without removing preexisting source requirements. Its metadata is value-free and safe to retain on reopen.
func ApplyHTTPVersionCapture(action *dsl.Action, capture HTTPVersionCapture, source SourceRef) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	observation := map[string]any{"version": 1, "request_state": capture.RequestState, "response_state": capture.ResponseState, "proxy": capture.Proxy, "provenance": capture.Provenance}
	if supportedObservation(capture.RequestVersion) {
		observation["request_version"] = capture.RequestVersion
	}
	if supportedObservation(capture.ResponseVersion) {
		observation["response_version"] = capture.ResponseVersion
	}
	if HTTPVersionCaptureQualityOnly(capture.ReasonCode) {
		// The leaf keeps its inherited policy; the observation records why no
		// exact version was selected without blocking execution.
		observation["reason_code"] = capture.ReasonCode
	}
	action.Metadata["http_version_capture"] = observation
	if capture.ReasonCode == ReasonHTTP3DraftToken {
		importreview.ApplyToAction(action, []importreview.Finding{{Code: ReasonHTTP3DraftToken, ReasonCode: ReasonHTTP3DraftToken, Severity: diag.Warning, Element: "HTTP version", Detail: "The capture used a draft HTTP/3 version (pre-RFC 9114). The request replays on the inherited policy; choose final HTTP/3 (auto+h3 or 3) explicitly if the target supports it.", Source: source.Format, SourceRef: sourceLocation(source), Gate: importreview.GatePromotion, Resolution: "modernize_http_version"}})
		return
	}
	if capture.ReasonCode == "" || HTTPVersionCaptureQualityOnly(capture.ReasonCode) {
		return
	}
	detail := "The captured HTTP version is not sufficient to select a faithful replay policy; review the source observation before execution."
	applyRuntimeFidelityMetadata(action, runtimeFidelityProjection{VersionBlocked: true, VersionReason: capture.ReasonCode, VersionSource: source})
	importreview.ApplyToAction(action, []importreview.Finding{{Code: importReviewRequiredBlocker, ReasonCode: capture.ReasonCode, Severity: diag.Blocker, Element: "HTTP version", Detail: detail, Source: source.Format, SourceRef: sourceLocation(source), Gate: importreview.GateExecution, Resolution: "replace_behavior_and_remove_gate"}})
}

func supportedObservation(version string) bool {
	return version == httpprotocol.VersionHTTP11 || version == httpprotocol.VersionHTTP2 || version == httpprotocol.VersionHTTP3
}
