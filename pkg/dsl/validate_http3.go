package dsl

import (
	"encoding/json"
	"fmt"
	"strings"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// validateHTTP3PolicyIssues rejects settings that an HTTP/3 policy cannot
// honor, on save. The same rules run again after dynamic URLs and runtime
// settings resolve, because a template can still produce an http:// URL.
//
// Exact 3 has no TCP fallback, so it rejects an http:// target, a TLS maximum
// below 1.3, a custom proxy, and integrated (NTLM or Negotiate)
// authentication. auto+h3 keeps those requests on TCP instead. Both policies
// reject network shaping, which wraps TCP connections only and would leave
// QUIC traffic unshaped.
func validateHTTP3PolicyIssues(script *Script) []string {
	if script == nil {
		return nil
	}
	switch ProtocolFamily(script.Protocol) {
	case "http", "soap", "java_http":
	default:
		return nil
	}
	inherited := InheritedHTTPVersion(script)
	httpCfg := script.Config.HTTP
	var issues []string
	exact, quic := false, httpprotocol.VersionUsesQUIC(inherited)
	// configAuth is set when an exact-3 request inherits config.http.auth
	// and its host is within the auth's host scope.
	configAuth := false
	if inherited == httpprotocol.VersionHTTP3 {
		exact = true
		issues = append(issues, http3TargetIssues("config.base_url", script.Config.BaseURL, "")...)
	}
	walkHTTP3RequestLeaves(script, func(label string, leaf http3Leaf) {
		version, err := EffectiveHTTPVersion(leaf.action, inherited)
		if err != nil || !httpprotocol.VersionUsesQUIC(version) {
			return
		}
		quic = true
		if version != httpprotocol.VersionHTTP3 {
			return
		}
		exact = true
		base := script.Config.BaseURL
		if inherited == httpprotocol.VersionHTTP3 {
			base = "" // config.base_url was checked once above
		}
		issues = append(issues, http3TargetIssues(label+".target", leaf.target, base)...)
		switch {
		case leaf.action.Auth == nil:
			configAuth = configAuth || authHostScopeCovers(httpCfg.Auth.Hosts, leaf.target, script.Config.BaseURL)
		case integratedHTTPAuth(leaf.action.Auth.Type):
			issues = append(issues, fmt.Sprintf("%s.auth.type %s cannot run over HTTP/3: integrated authentication stays on TCP; use auto+h3 or another auth type", label, strings.TrimSpace(leaf.action.Auth.Type)))
		}
	})
	if exact {
		if maximum := strings.TrimSpace(httpCfg.TLS.MaxVersion); maximum != "" && maximum != "1.3" {
			issues = append(issues, "config.http.tls.max_version must be 1.3 or unset: HTTP/3 requires TLS 1.3")
		}
		if mode := strings.TrimSpace(httpCfg.ProxyMode); (mode == "" || mode == "custom") && strings.TrimSpace(httpCfg.Proxy.URL) != "" {
			issues = append(issues, "config.http.proxy.url cannot be used with http_version 3: a proxied route stays on TCP (http3_proxy_unsupported); use auto+h3 or remove the proxy")
		}
		if configAuth && integratedHTTPAuth(httpCfg.Auth.Type) {
			issues = append(issues, fmt.Sprintf("config.http.auth.type %s cannot run over HTTP/3: integrated authentication stays on TCP; use auto+h3 or another auth type", strings.TrimSpace(httpCfg.Auth.Type)))
		}
	}
	if quic {
		issues = append(issues, http3ShapingIssues(script.Config.WebRuntime)...)
	}
	return issues
}

// authHostScopeCovers reports whether script-level auth applies to a
// request, using the runtime's host-scope matching. A target whose host
// cannot be resolved before execution is left to the runtime check.
func authHostScopeCovers(hosts []string, target, base string) bool {
	if len(hosts) == 0 {
		return true
	}
	raw := strings.TrimSpace(target)
	if !strings.Contains(raw, "://") {
		raw = strings.TrimSpace(base)
	}
	if strings.Contains(raw, "{{") {
		return false
	}
	allowed, err := httpprotocol.AuthHostScopeAllows(hosts, raw)
	return err == nil && allowed
}

func integratedHTTPAuth(authType string) bool {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "ntlm", "negotiate", "negotiate_any":
		return true
	}
	return false
}

// http3TargetIssues rejects a target that resolves to http://. A relative
// target takes its scheme from base; an unresolved template is checked again
// after resolution.
func http3TargetIssues(label, target, base string) []string {
	value := strings.TrimSpace(target)
	scheme, _, found := strings.Cut(value, "://")
	if !found {
		if base == "" || strings.HasPrefix(value, "{{") {
			return nil
		}
		scheme, _, found = strings.Cut(strings.TrimSpace(base), "://")
		if !found {
			return nil
		}
	}
	if strings.EqualFold(scheme, "http") {
		return []string{label + " must use https:// with http_version 3: HTTP/3 serves only https origins"}
	}
	return nil
}

func http3ShapingIssues(web WebRuntimeConfig) []string {
	shaped := func(n NetworkSimulationConfig) bool {
		return strings.TrimSpace(n.Mode) != "" && httpprotocol.NetworkShapingActive(n.Mode, n.Preset, n.LatencyMs, n.BandwidthKbps, n.JitterMs, n.PacketLossPct)
	}
	var issues []string
	if shaped(web.NetworkSimulation) {
		issues = append(issues, "config.web_runtime.network_simulation cannot be combined with HTTP/3 (auto+h3 or 3): shaping applies to TCP only; use external tc netem for TCP-versus-QUIC comparisons")
	}
	for index, profile := range web.NetworkProfiles {
		if shaped(profile.NetworkSimulationConfig) {
			issues = append(issues, fmt.Sprintf("config.web_runtime.network_profiles[%d] cannot be combined with HTTP/3 (auto+h3 or 3): shaping applies to TCP only", index))
			break
		}
	}
	return issues
}

// http3Leaf is one request surface: an action, or an http_sequence child
// decoded from its params.
type http3Leaf struct {
	action Action
	target string
}

// walkHTTP3RequestLeaves visits every HTTP request surface that can carry a
// version policy: top-level, nested control branches and http_sequence
// children.
func walkHTTP3RequestLeaves(script *Script, visit func(label string, leaf http3Leaf)) {
	type pendingAction struct {
		path   string
		action Action
	}
	var pending []pendingAction
	for index, action := range script.Init {
		pending = append(pending, pendingAction{fmt.Sprintf("init[%d]", index), action})
	}
	for index, action := range script.Actions {
		pending = append(pending, pendingAction{fmt.Sprintf("actions[%d]", index), action})
	}
	for index, action := range script.Teardown {
		pending = append(pending, pendingAction{fmt.Sprintf("teardown[%d]", index), action})
	}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		label := actionLabel(current.path, current.action)
		if current.action.Control != nil {
			for _, branch := range current.action.Control.ActionBranches() {
				for index, nested := range branch.Actions {
					pending = append(pending, pendingAction{fmt.Sprintf("%s.control.%s[%d]", current.path, branch.Path, index), nested})
				}
			}
		}
		if strings.EqualFold(strings.TrimSpace(current.action.Type), "http_sequence") {
			for index, child := range http3SequenceChildren(current.action) {
				visit(fmt.Sprintf("%s.params.requests[%d] %q", label, index, child.action.Name), child)
			}
			continue
		}
		// Engine actions (control containers, cookies, rendezvous...), token
		// fetches and WebSocket steps never use the request version policy.
		if isEngineActionType(current.action.Type) || CanonicalActionType(current.action.Type) == "http_oauth2_token" || isWebSocketStepType(current.action.Type) {
			continue
		}
		visit(label, http3Leaf{action: current.action, target: current.action.Target})
	}
}

// http3SequenceChildren decodes http_sequence children the way the runtime
// does: each is a full action, given as an Action or a JSON object.
func http3SequenceChildren(action Action) []http3Leaf {
	var values []any
	switch typed := action.Params["requests"].(type) {
	case []any:
		values = typed
	case []Action:
		for _, child := range typed {
			values = append(values, child)
		}
	case []map[string]any:
		for _, child := range typed {
			values = append(values, child)
		}
	}
	var children []http3Leaf
	for _, value := range values {
		var child Action
		switch typed := value.(type) {
		case Action:
			child = typed
		default:
			payload, err := json.Marshal(typed)
			if err != nil || json.Unmarshal(payload, &child) != nil {
				continue // the header contract reports malformed children
			}
		}
		children = append(children, http3Leaf{action: child, target: child.Target})
	}
	return children
}
