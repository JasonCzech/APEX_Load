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
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
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

// Client credential names are shared by OAuth providers. They imply a possible
// Anypoint policy only on a separately verified MuleSoft destination.
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

	definedVars := definedScriptVars(script)
	sawOAuthInit := false
	sawUnresolvedBearer := false
	inspectHeaderPair := func(actionMuleSoftHost bool, name, value string) {
		lower := strings.ToLower(strings.TrimSpace(name))
		if actionMuleSoftHost && clientCredentialKeys[lower] {
			addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement detected via client_id/client_secret request headers.", "header "+name)
		}
		trimmedVal := strings.TrimSpace(value)
		lowerVal := strings.ToLower(trimmedVal)
		if actionMuleSoftHost && lower == "authorization" && (lowerVal == "bearer" || strings.HasPrefix(lowerVal, "bearer ")) {
			addAuth("bearer", "Bearer token required on requests.")
			tokenPart := strings.TrimSpace(trimmedVal[len("bearer"):])
			if hasUnboundTemplateVar(tokenPart, definedVars) {
				sawUnresolvedBearer = true
			}
		}
		if actionMuleSoftHost && lower == "authorization" && strings.HasPrefix(lowerVal, "basic ") {
			addAuth("basic", "HTTP Basic authentication required on requests.")
			addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement may be using Basic Authorization as its credential origin; confirm the applied policy before replay.", "basic authorization")
		}
	}

	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		target := ref.Target().Value
		addHost(target)
		actionMuleSoftHost := mulesoftHost(hostOf(target, script.Config.BaseURL)) != ""

		hdrs := ref.Headers()
		shadowedByList := map[string]bool{}
		authoredAuthHeader := false
		visitListEntry := func(entry dsl.HeaderEntry) {
			canon := strings.ToLower(strings.TrimSpace(entry.Name))
			if canon != "" {
				shadowedByList[canon] = true
			}
			if canon == "authorization" {
				authoredAuthHeader = true
			}
			if entry.Enabled != nil && !*entry.Enabled {
				return
			}
			inspectHeaderPair(actionMuleSoftHost, entry.Name, entry.Value)
		}
		switch list := hdrs.List.(type) {
		case dsl.HeaderList:
			for _, entry := range list {
				visitListEntry(entry)
			}
		case []dsl.HeaderEntry:
			for _, entry := range list {
				visitListEntry(entry)
			}
		case []any:
			for _, item := range list {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				name, _ := entry["name"].(string)
				val, _ := entry["value"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				he := dsl.HeaderEntry{Name: name, Value: val}
				if enabled, ok := entry["enabled"].(bool); ok {
					he.Enabled = &enabled
				}
				visitListEntry(he)
			}
		}
		var headerMap map[string]string
		switch typed := hdrs.Map.(type) {
		case map[string]string:
			headerMap = typed
		case map[string]any:
			headerMap = make(map[string]string, len(typed))
			for k, v := range typed {
				if text, ok := v.(string); ok {
					headerMap[k] = text
				}
			}
		}
		if len(headerMap) > 0 {
			names := make([]string, 0, len(headerMap))
			for name := range headerMap {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				canon := strings.ToLower(strings.TrimSpace(name))
				if shadowedByList[canon] {
					continue
				}
				if canon == "authorization" {
					authoredAuthHeader = true
				}
				inspectHeaderPair(actionMuleSoftHost, name, headerMap[name])
			}
		}

		for _, key := range queryKeys(target) {
			if actionMuleSoftHost && clientCredentialKeys[strings.ToLower(key)] {
				addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement client credentials detected in query parameters.", "query "+key)
			}
		}
		if key, ok := bodyCredentialKey(ref.Body().Value); ok && actionMuleSoftHost {
			addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement client credentials detected in the request body.", "body "+key)
		}

		var surfaceAuth *dsl.AuthConfig
		if ref.Kind == dslwalk.ActionSurface && ref.Action != nil {
			surfaceAuth = ref.Action.Auth
		} else if ref.Request != nil {
			switch raw := ref.Request["auth"].(type) {
			case *dsl.AuthConfig:
				surfaceAuth = raw
			case dsl.AuthConfig:
				copied := raw
				surfaceAuth = &copied
			case map[string]any:
				typ, _ := raw["type"].(string)
				tok, _ := raw["token"].(string)
				surfaceAuth = &dsl.AuthConfig{Type: typ, Token: tok}
			}
		}
		if actionMuleSoftHost && surfaceAuth != nil && !authoredAuthHeader {
			switch strings.ToLower(strings.TrimSpace(surfaceAuth.Type)) {
			case "bearer":
				addAuth("bearer", "Bearer token required on requests.")
				if hasUnboundTemplateVar(strings.TrimSpace(surfaceAuth.Token), definedVars) {
					sawUnresolvedBearer = true
				}
			case "basic":
				addAuth("basic", "HTTP Basic authentication required on requests.")
				addPolicy("client_id_enforcement", "Anypoint Client ID Enforcement may be using Basic Authorization as its credential origin; confirm the applied policy before replay.", "basic authorization")
			}
		}

		if ref.Kind == dslwalk.ActionSurface && ref.Action != nil {
			action := ref.Action
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
		return nil
	})

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

	if review.Detected && sawUnresolvedBearer && !sawOAuthInit {
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

var dslTemplateVarPattern = regexp.MustCompile(`\{\{\s*(var|param)\.([A-Za-z0-9_.-]+)\s*\}\}`)

func definedScriptVars(script *dsl.Script) map[string]bool {
	defined := map[string]bool{}
	if script == nil {
		return defined
	}
	for _, p := range script.Parameters {
		name := strings.TrimSpace(p.Name)
		if name != "" && parameterProvidesValue(p) {
			defined[name] = true
		}
	}
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action == nil {
			return nil
		}
		for _, ext := range ref.Action.Extractors {
			name := strings.TrimSpace(ext.Name)
			if name == "" {
				continue
			}
			defined[name] = true
			if strings.EqualFold(strings.TrimSpace(ext.Mode), "all") {
				defined[name+"_1"] = true
				defined[name+"_count"] = true
			}
		}
		if strings.EqualFold(ref.Action.Type, "set_var") {
			name := paramString(ref.Action.Params, "name")
			if name != "" && paramString(ref.Action.Params, "value") != "" {
				defined[name] = true
			}
		}
		return nil
	})
	return defined
}

func parameterProvidesValue(p dsl.Parameter) bool {
	if strings.TrimSpace(p.DataSource) != "" {
		return true
	}
	for _, v := range p.Values {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "uuid", "random_string", "random_number", "counter", "timestamp", "date_time", "current_iteration", "vuser_id", "host", "time":
		return true
	default:
		return false
	}
}

func hasUnboundTemplateVar(token string, defined map[string]bool) bool {
	if strings.TrimSpace(token) == "" {
		return true
	}
	matches := dslTemplateVarPattern.FindAllStringSubmatch(token, -1)
	for _, match := range matches {
		if len(match) >= 3 && !defined[match[2]] {
			return true
		}
	}
	return false
}

func mulesoftHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case host == "anypoint.mulesoft.com" || strings.HasSuffix(host, ".anypoint.mulesoft.com"):
		return "anypoint"
	case strings.HasSuffix(host, ".cloudhub.io"):
		return "cloudhub"
	case strings.HasSuffix(host, ".cloudhub.eu"):
		return "cloudhub_eu"
	case strings.HasSuffix(host, ".msap.io"):
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
	if !strings.Contains(rawURL, "://") && !strings.HasPrefix(rawURL, "//") {
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

func queryKeys(rawURL string) []string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	idx := strings.IndexByte(rawURL, '?')
	if idx < 0 {
		return nil
	}
	values, err := url.ParseQuery(rawURL[idx+1:])
	if err != nil || len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
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
	// JSON object/array body.
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		var decoded any
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

// jsonCredentialKey walks decoded JSON for a client-credential key,
// visiting keys in sorted order at each level so the reported evidence is stable.
func jsonCredentialKey(node any) (string, bool) {
	mapping, ok := node.(map[string]any)
	if !ok {
		items, ok := node.([]any)
		if !ok {
			return "", false
		}
		for _, item := range items {
			if found, ok := jsonCredentialKey(item); ok {
				return found, true
			}
		}
		return "", false
	}
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if clientCredentialKeys[strings.ToLower(strings.TrimSpace(key))] {
			return key, true
		}
	}
	for _, key := range keys {
		if found, ok := jsonCredentialKey(mapping[key]); ok {
			return found, true
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
