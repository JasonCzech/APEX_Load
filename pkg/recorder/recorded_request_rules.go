package recorder

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

// normalizedRecording is a session's requests after the shared
// recorded-traffic rules, plus the review findings each kept request carries
// (keyed by Seq).
type normalizedRecording struct {
	requests []RecordedRequest
	findings map[int][]importreview.Finding
	// integratedAuthHosts are the hosts recorded with an NTLM or Negotiate
	// handshake.
	integratedAuthHosts []string
}

// normalizeRecordedRequests applies the rules every recorded-traffic converter
// shares (pkg/convert/requestimport):
//   - the 401 leg of an NTLM or Negotiate handshake is excluded, because the
//     retry that follows it is the real request;
//   - single-use NTLM/Negotiate Authorization tokens are removed, and the
//     host is listed so the script answers its challenges with
//     negotiate_any auth;
//   - a request whose body hit the capture limit is gated.
//
// The input is not modified.
func normalizeRecordedRequests(requests []RecordedRequest) normalizedRecording {
	out := normalizedRecording{requests: make([]RecordedRequest, len(requests)), findings: map[int][]importreview.Finding{}}
	copy(out.requests, requests)
	for i := range out.requests {
		req := &out.requests[i]
		if req.IsStatic || req.Excluded || len(req.WebSocketMessages) > 0 {
			continue
		}
		if isIntegratedAuthHandshakeLeg(out.requests, i) {
			req.Excluded = true
			continue
		}
		ref := fmt.Sprintf("request-%d", req.Seq)
		if scheme := recordedIntegratedAuthScheme(*req); scheme != "" {
			stripRecordedAuthorization(req)
			out.findings[req.Seq] = append(out.findings[req.Seq], requestimport.IntegratedAuthFinding("recording", ref, scheme))
			if host := requestimport.IntegratedAuthHost(req.URL); host != "" && !containsFold(out.integratedAuthHosts, host) {
				out.integratedAuthHosts = append(out.integratedAuthHosts, host)
			}
		}
		if req.BodyTruncated {
			out.findings[req.Seq] = append(out.findings[req.Seq], requestimport.RecordedBodyTruncatedFinding("recording", ref, req.BodySizeBytes))
		}
	}
	return out
}

// isIntegratedAuthHandshakeLeg reports whether requests[i] is the 401
// challenge of an NTLM/Negotiate handshake that the next meaningful request
// to the same method and URL completes.
func isIntegratedAuthHandshakeLeg(requests []RecordedRequest, i int) bool {
	req := requests[i]
	if !requestimport.IsIntegratedAuthChallenge(req.StatusCode, recordedHeaderValuesFold(req.ResponseHeadersMulti, req.ResponseHeaders, "WWW-Authenticate")) {
		return false
	}
	for j := i + 1; j < len(requests); j++ {
		next := requests[j]
		if next.IsStatic || next.Excluded || len(next.WebSocketMessages) > 0 {
			continue
		}
		return strings.EqualFold(next.Method, req.Method) && next.URL == req.URL && recordedIntegratedAuthScheme(next) != ""
	}
	return false
}

func recordedIntegratedAuthScheme(req RecordedRequest) string {
	for _, headers := range []struct {
		multi  map[string][]string
		single map[string]string
	}{{req.RawHeadersMulti, req.RawHeaders}, {req.HeadersMulti, req.Headers}} {
		for _, value := range recordedHeaderValuesFold(headers.multi, headers.single, "Authorization") {
			if scheme := requestimport.IntegratedAuthScheme(value); scheme != "" {
				return scheme
			}
		}
	}
	return ""
}

func recordedHeaderValuesFold(multi map[string][]string, single map[string]string, name string) []string {
	var values []string
	for key, list := range multi {
		if strings.EqualFold(key, name) {
			values = append(values, list...)
		}
	}
	if len(values) > 0 {
		return values
	}
	for key, value := range single {
		if strings.EqualFold(key, name) {
			values = append(values, value)
		}
	}
	return values
}

// stripRecordedAuthorization removes Authorization from every header view of
// a request, replacing the maps so the caller's session is not modified.
func stripRecordedAuthorization(req *RecordedRequest) {
	req.Headers = withoutHeaderFold(req.Headers, "Authorization")
	req.RawHeaders = withoutHeaderFold(req.RawHeaders, "Authorization")
	req.HeadersMulti = withoutMultiHeaderFold(req.HeadersMulti, "Authorization")
	req.RawHeadersMulti = withoutMultiHeaderFold(req.RawHeadersMulti, "Authorization")
}

func withoutHeaderFold(headers map[string]string, name string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if !strings.EqualFold(key, name) {
			out[key] = value
		}
	}
	return out
}

func withoutMultiHeaderFold(headers map[string][]string, name string) map[string][]string {
	if headers == nil {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			out[key] = values
		}
	}
	return out
}

// isRecordedRedirectStep reports whether a recorded request is a redirect hop
// the browser followed itself, so the generated step must not follow it again.
func isRecordedRedirectStep(req RecordedRequest) bool {
	if req.StatusCode < 300 || req.StatusCode > 399 || req.StatusCode == http.StatusNotModified {
		return false
	}
	for _, value := range recordedHeaderValuesFold(req.ResponseHeadersMulti, req.ResponseHeaders, "Location") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func containsFold(values []string, value string) bool {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return true
		}
	}
	return false
}
