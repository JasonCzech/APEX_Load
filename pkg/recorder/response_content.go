package recorder

import (
	"encoding/base64"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

// effectiveRecordedResponseContent validates persisted snapshots before either
// direct conversion or HAR export. Legacy logical bodies stay explicitly
// unverified; retained wire encoding makes their provenance ambiguous.
func effectiveRecordedResponseContent(req RecordedRequest) requestimport.ResponseContentCapture {
	bodyless := strings.EqualFold(req.Method, "HEAD") || req.StatusCode == 204 || req.StatusCode == 304
	if req.ResponseContentCapture.Version == 0 {
		if req.ResponseBodyTruncated {
			return requestimport.UnavailableResponseContent("unknown", "response_content_truncated")
		}
		if bodyless {
			if req.ResponseBody != "" {
				return requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
			}
			return requestimport.CompleteResponseContent("legacy_normalized", 0)
		}
		for name := range recordedResponseHeaders(req) {
			if strings.EqualFold(name, "Content-Encoding") {
				return requestimport.UnavailableResponseContent("unknown", "response_content_provenance_unavailable")
			}
		}
		decoded, reason := recordedResponseStorage(req.ResponseBody, req.ResponseBodyEncoding)
		if reason != "" {
			return requestimport.UnavailableResponseContent("unknown", reason)
		}
		capture := requestimport.CompleteResponseContent("legacy_normalized", len(decoded))
		if req.ResponseBodyEncoding == "base64" || !requestimport.IsResponseContentText(decoded) {
			capture.BodyKind = "binary"
		}
		return capture
	}
	capture := req.ResponseContentCapture
	values := map[string]any{"version": capture.Version, "captured": capture.Captured, "stored": capture.Stored, "state": capture.State}
	if capture.ReasonCode != "" {
		values["reason_code"] = capture.ReasonCode
	}
	if capture.DecodedSizeBytes != 0 {
		values["decoded_size_bytes"] = capture.DecodedSizeBytes
	}
	if capture.BodyKind != "" {
		values["body_kind"] = capture.BodyKind
	}
	capture = requestimport.ReviewResponseContentProvenance(values)
	if !capture.Complete() {
		return capture
	}
	if req.ResponseBodyTruncated {
		return requestimport.UnavailableResponseContent(capture.Captured, "response_content_truncated")
	}
	decoded, reason := recordedResponseStorage(req.ResponseBody, req.ResponseBodyEncoding)
	if reason != "" || len(decoded) != capture.DecodedSizeBytes || (bodyless && len(decoded) != 0) {
		return requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
	}
	if req.ResponseBodyEncoding == "base64" {
		if capture.BodyKind == "text" {
			return requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
		}
		capture.BodyKind = "binary"
	}
	if !requestimport.IsResponseContentText(decoded) {
		if capture.BodyKind == "text" {
			return requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
		}
		capture.BodyKind = "binary"
	}
	return capture
}

func recordedResponseStorage(body, encoding string) ([]byte, string) {
	if len(body) > ((maxBodyCaptureLimitBytes+2)/3)*4 {
		return nil, "har_content_storage_limit"
	}
	switch encoding {
	case "":
		if len(body) > maxBodyCaptureLimitBytes {
			return nil, "har_content_storage_limit"
		}
		return []byte(body), ""
	case "base64":
		decoded, err := base64.StdEncoding.Strict().DecodeString(body)
		if err != nil {
			return nil, "invalid_har_content_storage"
		}
		if len(decoded) > maxBodyCaptureLimitBytes {
			return nil, "har_content_storage_limit"
		}
		return decoded, ""
	default:
		return nil, "unsupported_har_content_storage"
	}
}

func recordedResponseBodyForBaseline(req RecordedRequest) string {
	capture := effectiveRecordedResponseContent(req)
	if !capture.Complete() || capture.BodyKind == "binary" || req.ResponseBodyEncoding != "" {
		return ""
	}
	return req.ResponseBody
}
