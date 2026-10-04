package requestimport

import (
	"encoding/json"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// HARHTTPVersionProvenanceKey describes the two observed proxy legs. It never
// claims that an intercepted client exchange used the upstream wire protocol.
const HARHTTPVersionProvenanceKey = "_apexload_http_version"

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
	if !present {
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
	version := httpprotocol.NormalizeVersion(value)
	if version != httpprotocol.VersionHTTP11 && version != httpprotocol.VersionHTTP2 {
		return "", "unsupported"
	}
	return version, "available"
}

// ProxyHTTPVersionProvenance returns the only supported recorder provenance
// envelope. Request and response versions remain separate HAR fields.
func ProxyHTTPVersionProvenance() map[string]any {
	return map[string]any{"version": 1, "request_leg": "client_to_proxy", "response_leg": "proxy_to_origin"}
}

func validProxyHTTPVersionProvenance(raw any) bool {
	value, ok := raw.(map[string]any)
	if !ok || len(value) != 3 || value["request_leg"] != "client_to_proxy" || value["response_leg"] != "proxy_to_origin" {
		return false
	}
	switch version := value["version"].(type) {
	case int:
		return version == 1
	case float64:
		return version == 1
	case json.Number:
		return version.String() == "1"
	default:
		return false
	}
}

// ReviewHTTPVersionCapture distinguishes a missing observation from legal DSL
// inheritance. Ordinary HAR uses its request observation; a recorder exchange
// requires supported, equal observations on both explicitly identified legs.
func ReviewHTTPVersionCapture(request any, requestPresent bool, response any, responsePresent bool, provenance any, provenancePresent, legacyRecorder bool) HTTPVersionCapture {
	requestVersion, requestState := capturedHTTPVersion(request, requestPresent)
	responseVersion, responseState := capturedHTTPVersion(response, responsePresent)
	decision := HTTPVersionCapture{RequestState: requestState, ResponseState: responseState, RequestVersion: requestVersion, ResponseVersion: responseVersion, Provenance: "request", Proxy: provenancePresent || legacyRecorder}
	if provenancePresent && !validProxyHTTPVersionProvenance(provenance) {
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

// ApplyHTTPVersionCapture attaches a durable gate without removing preexisting
// source requirements. Its metadata is value-free and safe to retain on reopen.
func ApplyHTTPVersionCapture(action *dsl.Action, capture HTTPVersionCapture, source SourceRef) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	observation := map[string]any{"version": 1, "request_state": capture.RequestState, "response_state": capture.ResponseState, "proxy": capture.Proxy, "provenance": capture.Provenance}
	if capture.RequestVersion == "1.1" || capture.RequestVersion == "2" {
		observation["request_version"] = capture.RequestVersion
	}
	if capture.ResponseVersion == "1.1" || capture.ResponseVersion == "2" {
		observation["response_version"] = capture.ResponseVersion
	}
	action.Metadata["http_version_capture"] = observation
	if capture.ReasonCode == "" {
		return
	}
	detail := "The captured HTTP version is not sufficient to select a faithful replay policy; review the source observation before execution."
	applyRuntimeFidelityMetadata(action, runtimeFidelityProjection{VersionBlocked: true, VersionReason: capture.ReasonCode, VersionSource: source})
	importreview.ApplyToAction(action, []importreview.Finding{{Code: importReviewRequiredBlocker, ReasonCode: capture.ReasonCode, Severity: diag.Blocker, Element: "HTTP version", Detail: detail, Source: source.Format, SourceRef: sourceLocation(source), Gate: importreview.GateExecution, Resolution: "replace_behavior_and_remove_gate"}})
}
