// Package mulesoft provides a review-layer analyzer that inspects an imported
// DSL script for MuleSoft / Anypoint signals: Anypoint-hosted endpoints,
// applied API-gateway policies (Client ID Enforcement), and authentication
// requirements (OAuth2 / JWT / basic). It is importer-agnostic — it operates on
// the converted *dsl.Script — so RAML, OpenAPI, Postman, and HAR imports can all
// surface the same MuleSoft review section.
package mulesoft

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Review summarizes the MuleSoft-specific findings for an imported script.
type Review struct {
	Detected       bool                `json:"detected"`
	Domains        []DomainObservation `json:"domains,omitempty"`
	PolicyFindings []PolicyFinding     `json:"policy_findings,omitempty"`
	AuthFindings   []AuthFinding       `json:"auth_findings,omitempty"`
	Blockers       []string            `json:"blockers,omitempty"`
	NextActions    []string            `json:"next_actions,omitempty"`
}

type DomainObservation struct {
	Host  string `json:"host"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// PolicyFinding records an Anypoint API-gateway policy inferred from the script
// (e.g. Client ID Enforcement signalled by client_id/client_secret credentials).
type PolicyFinding struct {
	Policy   string `json:"policy"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence,omitempty"`
}

// AuthFinding records an authentication requirement inferred from the script.
type AuthFinding struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// clientCredentialKeys are header/query/param names that indicate Anypoint
// Client ID Enforcement. Only the Anypoint-specific client_id/client_secret
// names are included — generic api-key headers (x-api-key/apikey) are NOT
// Anypoint-specific and would otherwise false-positive MuleSoft detection on
// ordinary api-key-protected services.
var clientCredentialKeys = map[string]bool{
	"client_id":     true,
	"clientid":      true,
	"client-id":     true,
	"client_secret": true,
	"clientsecret":  true,
	"client-secret": true,
}

// Analyze inspects a converted script for MuleSoft/Anypoint signals.
func Analyze(script *dsl.Script) *Review {
	review := &Review{}
	if script == nil {
		return review
	}

	hostCounts := map[string]int{}
	hostKinds := map[string]string{}
	addHost := func(rawURL string) {
		host := hostOf(rawURL, script.Config.BaseURL)
		if host == "" {
			return
		}
		kind := mulesoftHost(host)
		if kind == "" {
			return
		}
		hostCounts[host]++
		hostKinds[host] = kind
	}
	addHost(script.Config.BaseURL)

	policySeen := map[string]bool{}
	addPolicy := func(policy, detail, evidence string) {
		if policySeen[policy] {
			return
		}
		policySeen[policy] = true
		review.PolicyFindings = append(review.PolicyFindings, PolicyFinding{Policy: policy, Detail: detail, Evidence: evidence})
	}

	authSeen := map[string]bool{}
	addAuth := func(authType, detail string) {
		if authSeen[authType] {
			return
		}
		authSeen[authType] = true
		review.AuthFindings = append(review.AuthFindings, AuthFinding{Type: authType, Detail: detail})
	}

	sawOAuthInit := false
	for _, action := range allActions(script) {
		addHost(action.Target)
		// Header-based signals.
		for name, value := range action.Headers {
			lower := strings.ToLower(strings.TrimSpace(name))
			if clientCredentialKeys[lower] {
				addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement requires client_id/client_secret per request; supply via env/secret placeholders.", "header "+name)
			}
			if lower == "authorization" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ") {
				addAuth("bearer", "Bearer token required on requests.")
			}
			if lower == "authorization" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "basic ") {
				addAuth("basic", "HTTP Basic authentication required on requests.")
			}
		}
		// Query-string signals on the target.
		for key := range queryKeys(action.Target) {
			if clientCredentialKeys[strings.ToLower(key)] {
				addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement client credentials are passed as query parameters; move to env/secret placeholders.", "query "+key)
			}
		}
		// Request-body signals: client credentials carried in a JSON or
		// form-encoded body (common for token exchanges) would otherwise be
		// missed by the header/query checks above.
		if key, ok := bodyCredentialKey(action.Body); ok {
			addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement client credentials are sent in the request body; move to env/secret placeholders.", "body "+key)
		}
		// http_oauth2_token init actions reveal the OAuth grant.
		if strings.EqualFold(action.Type, "http_oauth2_token") {
			sawOAuthInit = true
			grant := paramString(action.Params, "grant_type")
			switch grant {
			case "jwt_bearer":
				addAuth("jwt_bearer", "OAuth2 JWT bearer flow wired via http_oauth2_token.")
			case "", "client_credentials":
				addAuth("oauth2_client_credentials", "OAuth2 client-credentials flow wired via http_oauth2_token.")
			default:
				addAuth("oauth2_"+grant, "OAuth2 "+grant+" flow wired via http_oauth2_token.")
			}
		}
	}

	review.Detected = len(hostCounts) > 0 || len(review.PolicyFindings) > 0

	for host, count := range hostCounts {
		review.Domains = append(review.Domains, DomainObservation{Host: host, Kind: hostKinds[host], Count: count})
	}
	sort.Slice(review.Domains, func(i, j int) bool {
		if review.Domains[i].Count == review.Domains[j].Count {
			return review.Domains[i].Host < review.Domains[j].Host
		}
		return review.Domains[i].Count > review.Domains[j].Count
	})
	sort.Slice(review.PolicyFindings, func(i, j int) bool {
		return review.PolicyFindings[i].Policy < review.PolicyFindings[j].Policy
	})
	sort.Slice(review.AuthFindings, func(i, j int) bool {
		return review.AuthFindings[i].Type < review.AuthFindings[j].Type
	})

	if policySeen["client_id_enforcement"] {
		review.Blockers = append(review.Blockers, "Provide Anypoint client_id/client_secret via env/secret placeholders before promotion; never embed live credentials in the script.")
	}
	if authSeen["bearer"] && !sawOAuthInit {
		review.Blockers = append(review.Blockers, "Bearer tokens are required but no http_oauth2_token init action was found; add one to acquire tokens at runtime.")
	}
	if review.Detected {
		review.NextActions = append(review.NextActions,
			"Confirm Anypoint environment, client app credentials, and applied policies before running the test.",
			"Replay against a non-production Anypoint environment and attach evidence before promotion.",
		)
	}
	return review
}

func allActions(script *dsl.Script) []dsl.Action {
	actions := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	actions = append(actions, script.Init...)
	actions = append(actions, script.Actions...)
	actions = append(actions, script.Teardown...)
	return actions
}

func mulesoftHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case strings.Contains(host, "anypoint.mulesoft.com"):
		return "anypoint"
	case strings.Contains(host, ".cloudhub.io"):
		return "cloudhub"
	case strings.Contains(host, ".cloudhub.eu"):
		return "cloudhub_eu"
	case strings.Contains(host, ".msap.io"):
		return "anypoint_msap"
	case strings.HasSuffix(host, ".mulesoft.com") || host == "mulesoft.com":
		return "mulesoft"
	default:
		return ""
	}
}

func hostOf(rawURL, baseURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if !strings.Contains(rawURL, "://") {
		// Relative target: fall back to the configured base URL host.
		rawURL = strings.TrimSpace(baseURL)
		if rawURL == "" || !strings.Contains(rawURL, "://") {
			return ""
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func queryKeys(rawURL string) map[string]bool {
	keys := map[string]bool{}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return keys
	}
	idx := strings.IndexByte(rawURL, '?')
	if idx < 0 {
		return keys
	}
	values, err := url.ParseQuery(rawURL[idx+1:])
	if err != nil {
		return keys
	}
	for key := range values {
		keys[key] = true
	}
	return keys
}

// bodyCredentialKey reports the first Anypoint client-credential key found in a
// request body. It understands JSON objects (recursively) and form-encoded
// bodies; other content is ignored.
func bodyCredentialKey(body string) (string, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", false
	}
	// JSON object body.
	if strings.HasPrefix(body, "{") {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(body), &decoded); err == nil {
			if key, ok := jsonCredentialKey(decoded); ok {
				return key, true
			}
			return "", false
		}
	}
	// Form-encoded body. Require a key=value shape to avoid matching free-form
	// text that merely contains the substring "client_id".
	if strings.Contains(body, "=") {
		if values, err := url.ParseQuery(body); err == nil {
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if clientCredentialKeys[strings.ToLower(strings.TrimSpace(key))] {
					return key, true
				}
			}
		}
	}
	return "", false
}

// jsonCredentialKey walks a decoded JSON object for a client-credential key,
// visiting keys in sorted order at each level so the reported evidence is stable.
func jsonCredentialKey(node map[string]any) (string, bool) {
	keys := make([]string, 0, len(node))
	for key := range node {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if clientCredentialKeys[strings.ToLower(strings.TrimSpace(key))] {
			return key, true
		}
	}
	for _, key := range keys {
		if nested, ok := node[key].(map[string]any); ok {
			if found, ok := jsonCredentialKey(nested); ok {
				return found, true
			}
		}
	}
	return "", false
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if value, ok := params[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
