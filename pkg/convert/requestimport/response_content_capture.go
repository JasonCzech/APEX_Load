package requestimport

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// HARResponseContentProvenanceKey qualifies recorder response snapshots. Normal
// HAR content.text is already HTTP-decoded; this extension never requests a
// second wire decode.
const HARResponseContentProvenanceKey = "_apexload_response_content"

const ImportResponseContentFidelityMetadataKey = dsl.ImportResponseContentFidelityMetadataKey

// MaxResponseContentStorageBytes keeps recorder capture and HAR storage
// admission aligned so a complete supported recording can be reimported.
const MaxResponseContentStorageBytes = 16 << 20

// IsResponseContentText admits application text without interpreting payload
// signatures or Content-Encoding. Other exact bytes remain binary source data.
func IsResponseContentText(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	for _, b := range body {
		if (b < 0x20 || b == 0x7f) && b != '\t' && b != '\n' && b != '\r' && b != '\f' {
			return false
		}
	}
	return true
}

// ResponseContentCapture carries only bounded provenance and disposition enums.
// Headers and original bytes remain separate source evidence.
type ResponseContentCapture struct {
	Version          int    `json:"version"`
	Captured         string `json:"captured"`
	Stored           string `json:"stored"`
	State            string `json:"state"`
	ReasonCode       string `json:"reason_code,omitempty"`
	DecodedSizeBytes int    `json:"decoded_size_bytes,omitempty"`
	BodyKind         string `json:"body_kind,omitempty"`
}

func CompleteResponseContent(captured string, size int) ResponseContentCapture {
	return ResponseContentCapture{Version: 1, Captured: captured, Stored: "decoded", State: "complete", DecodedSizeBytes: size, BodyKind: "text"}
}

func UnavailableResponseContent(captured, reason string) ResponseContentCapture {
	return ResponseContentCapture{Version: 1, Captured: captured, Stored: "unavailable", State: "unavailable", ReasonCode: reason}
}

func (capture ResponseContentCapture) Complete() bool {
	return capture.Version == 1 && capture.State == "complete" && capture.Stored == "decoded" && capture.ReasonCode == "" && validResponseContentSource(capture.Captured) && capture.DecodedSizeBytes >= 0 && capture.DecodedSizeBytes <= 64<<20 && (capture.BodyKind == "" || capture.BodyKind == "text" || capture.BodyKind == "binary")
}

func validResponseContentSource(source string) bool {
	return source == "wire" || source == "har_decoded" || source == "legacy_normalized"
}

func validResponseContentReason(reason string) bool {
	switch reason {
	case "", "response_content_unavailable", "response_content_truncated", "unsupported_response_content_encoding", "invalid_response_content_encoding", "response_content_decode_limit", "response_content_decode_canceled", "malformed_response_content_provenance", "response_content_provenance_unavailable", "invalid_har_content_storage", "unsupported_har_content_storage", "har_content_storage_limit":
		return true
	}
	return false
}

// ReviewResponseContentProvenance admits a strict bounded extension. Any
// malformed explicit envelope remains unavailable rather than becoming an
// ordinary successful HAR body.
func ReviewResponseContentProvenance(raw any) ResponseContentCapture {
	malformed := UnavailableResponseContent("unknown", "malformed_response_content_provenance")
	values, ok := raw.(map[string]any)
	if !ok || len(values) < 4 || len(values) > 7 {
		return malformed
	}
	for key := range values {
		switch key {
		case "version", "captured", "stored", "state", "reason_code", "decoded_size_bytes", "body_kind":
		default:
			return malformed
		}
	}
	version, ok := responseContentInt(values["version"])
	if !ok || version != 1 {
		return malformed
	}
	captured, capturedOK := values["captured"].(string)
	stored, storedOK := values["stored"].(string)
	state, stateOK := values["state"].(string)
	if !capturedOK || !storedOK || !stateOK || (!validResponseContentSource(captured) && captured != "unknown") {
		return malformed
	}
	reason := ""
	if rawReason, present := values["reason_code"]; present {
		var reasonOK bool
		reason, reasonOK = rawReason.(string)
		if !reasonOK || !validResponseContentReason(reason) {
			return malformed
		}
	}
	size := 0
	if rawSize, present := values["decoded_size_bytes"]; present {
		var sizeOK bool
		size, sizeOK = responseContentInt(rawSize)
		if !sizeOK || size < 0 || size > 64<<20 {
			return malformed
		}
	}
	kind := ""
	if rawKind, present := values["body_kind"]; present {
		var kindOK bool
		kind, kindOK = rawKind.(string)
		if !kindOK || (kind != "text" && kind != "binary") {
			return malformed
		}
	}
	capture := ResponseContentCapture{Version: 1, Captured: captured, Stored: stored, State: state, ReasonCode: reason, DecodedSizeBytes: size, BodyKind: kind}
	if capture.Complete() {
		return capture
	}
	if stored != "unavailable" || state != "unavailable" || reason == "" || reason == "response_content_unavailable" || size != 0 || kind != "" {
		return malformed
	}
	return capture
}

// ReviewResponseContentMetadata permits spec-optional absence only in a normal
// HAR-derived logical snapshot, never in an explicit recorder envelope.
func ReviewResponseContentMetadata(raw any) ResponseContentCapture {
	if values, ok := raw.(map[string]any); ok && values["captured"] == "har_decoded" && values["reason_code"] == "response_content_unavailable" {
		copy := make(map[string]any, len(values))
		for key, value := range values {
			copy[key] = value
		}
		copy["reason_code"] = "response_content_provenance_unavailable"
		capture := ReviewResponseContentProvenance(copy)
		if capture.ReasonCode == "response_content_provenance_unavailable" {
			capture.ReasonCode = "response_content_unavailable"
			return capture
		}
	}
	return ReviewResponseContentProvenance(raw)
}

// ResponseContentBaselineHeaders returns a normalized logical snapshot without
// altering exact captured source headers. Framing describes wire bytes rather
// than the already-decoded content that correlation compares.
func ResponseContentBaselineHeaders(headers map[string]string, capture ResponseContentCapture) map[string]string {
	if !capture.Complete() || len(headers) == 0 {
		return headers
	}
	normalized := make(map[string]string, len(headers))
	for name, value := range headers {
		switch strings.ToLower(name) {
		case "content-encoding", "content-length", "transfer-encoding":
			continue
		}
		normalized[name] = value
	}
	return normalized
}

func responseContentInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case float64:
		if value >= 0 && value <= 64<<20 && value == float64(int(value)) {
			return int(value), true
		}
	case json.Number:
		value64, err := value.Int64()
		if err == nil && value64 >= 0 && value64 <= 64<<20 {
			return int(value64), true
		}
	}
	return 0, false
}

// ApplyResponseContentCapture never removes a preexisting source gate.
func ApplyResponseContentCapture(action *dsl.Action, capture ResponseContentCapture, source SourceRef) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	metadata := map[string]any{"version": capture.Version, "captured": capture.Captured, "stored": capture.Stored, "state": capture.State}
	if capture.DecodedSizeBytes > 0 {
		metadata["decoded_size_bytes"] = capture.DecodedSizeBytes
	}
	if capture.ReasonCode != "" {
		metadata["reason_code"] = capture.ReasonCode
	}
	if capture.BodyKind != "" {
		metadata["body_kind"] = capture.BodyKind
	}
	action.Metadata["response_content_capture"] = metadata
	if capture.ReasonCode == "" || capture.ReasonCode == "response_content_unavailable" {
		return
	}
	// The durable marker survives draft serialization and requires the same
	// execution gate even when ordinary review flags are edited on reopen.
	if _, exists := action.Metadata[ImportResponseContentFidelityMetadataKey]; !exists {
		action.Metadata[ImportResponseContentFidelityMetadataKey] = map[string]any{"version": 1, "reason_code": capture.ReasonCode, "source_ref": sourceLocation(source)}
	}
	importreview.ApplyToAction(action, []importreview.Finding{{Code: importReviewRequiredBlocker, ReasonCode: capture.ReasonCode, Severity: diag.Blocker, Element: "Response content", Detail: "The captured response content is unavailable for faithful correlation (" + capture.ReasonCode + "); replace the incomplete behavior and remove its source gate before execution.", Source: source.Format, SourceRef: sourceLocation(source), Gate: importreview.GateExecution, Resolution: "replace_behavior_and_remove_gate"}})
}
