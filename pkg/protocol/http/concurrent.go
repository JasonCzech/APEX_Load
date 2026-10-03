package http

import (
	"encoding/base64"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// ValidateSharedSessionAction checks every sibling before any group traffic.
// It does not mutate cookies, auth, cache or registered verifications.
func (h *HTTP) ValidateSharedSessionAction(action *protocol.Action) error {
	if !h.SharedSessionConcurrentSafe() {
		return fmt.Errorf("HTTP session is not initialized")
	}
	if err := validateHTTPActionBounds(action); err != nil {
		return err
	}
	if _, _, err := buildActionAuthHeader(action.Auth); err != nil {
		return fmt.Errorf("HTTP action authentication is invalid")
	}
	defaults, occurrences := h.defaultHeaders, h.defaultHeaderList
	if action.ResolvedDefaultHeaders {
		defaults = mergeDefaultHeaders(h.typedDefaultHeaders, action.DefaultHeaders)
		occurrences = action.DefaultHeaderList
	}
	headers, issues := effectiveAuthoredHeaderList(defaults, occurrences, action.Headers, action.HeaderList)
	issues = append(issues, HeaderListIssuesForVersion(headers, h.httpVersion)...)
	if len(issues) != 0 {
		return fmt.Errorf("HTTP headers are invalid")
	}
	req, err := stdhttp.NewRequest(actionHTTPMethod(action), h.resolveURL(action.Target), nil)
	if err != nil || req.URL == nil || req.URL.Host == "" || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return fmt.Errorf("HTTP target is invalid")
	}
	if err := protocol.ValidateTargetURL(req.URL.String(), h.allowedTargetHosts); err != nil {
		return fmt.Errorf("HTTP target is not allowed")
	}
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") {
		if _, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(strings.TrimSpace(string(action.Body))))); err != nil {
			return fmt.Errorf("HTTP base64 body is invalid")
		}
	}
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "windows-1252") {
		if _, err := encodeWindows1252Body(action.Body); err != nil {
			return fmt.Errorf("HTTP body encoding is invalid")
		}
	}
	return nil
}
