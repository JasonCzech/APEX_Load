package dsl

import (
	"net/url"
	"strings"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// FeatureNativeHTTP3V1 is the generator capability for exact HTTP/3 (policy
// 3). A generator that does not advertise it, including any older generator,
// is treated as unable to run HTTP/3 work.
const FeatureNativeHTTP3V1 = "native_http3_v1"

// FeatureHTTP3UpgradeV1 is the generator capability for the auto+h3 upgrade.
// It is separate from native_http3_v1 so a rolling upgrade never hands
// auto+h3 work to a generator that can run only exact HTTP/3.
const FeatureHTTP3UpgradeV1 = "http3_upgrade_v1"

// ScriptHTTP3Policies reports whether any request resolves to exact HTTP/3
// (3) or to the upgrade (auto+h3) after precedence (request, then script,
// then alias). A script-level policy that every request overrides counts as
// neither.
func ScriptHTTP3Policies(script *Script) (exact, upgrade bool) {
	if script == nil {
		return false, false
	}
	switch ProtocolFamily(script.Protocol) {
	case "http", "soap", "java_http":
	default:
		return false, false
	}
	inherited := InheritedHTTPVersion(script)
	walkHTTP3RequestLeaves(script, func(_ string, leaf http3Leaf) {
		version, err := EffectiveHTTPVersion(leaf.action, inherited)
		if err != nil {
			return
		}
		exact = exact || version == httpprotocol.VersionHTTP3
		upgrade = upgrade || version == httpprotocol.VersionAutoH3
	})
	return exact, upgrade
}

// ScriptUsesHTTP3 reports whether any request resolves to an HTTP/3 policy.
func ScriptUsesHTTP3(script *Script) bool {
	exact, upgrade := ScriptHTTP3Policies(script)
	return exact || upgrade
}

// RequiredHTTPRuntimeFeatures admits HTTP/3 work only on generators that
// advertise the capabilities it needs.
func RequiredHTTPRuntimeFeatures(script *Script) []string {
	exact, upgrade := ScriptHTTP3Policies(script)
	switch {
	case upgrade:
		return []string{FeatureNativeHTTP3V1, FeatureHTTP3UpgradeV1}
	case exact:
		return []string{FeatureNativeHTTP3V1}
	}
	return nil
}

// MaxHTTP3ProbeOrigins bounds the origins a preflight checks per script.
const MaxHTTP3ProbeOrigins = 16

// HTTP3ProbeOrigin is an origin a script's HTTP/3 policy will reach. A
// dynamic one (a target built from a template) is listed for the record
// but cannot be checked before the run resolves it.
type HTTP3ProbeOrigin struct {
	Origin  string // scheme://host[:port], or the raw template when dynamic
	Policy  string // 3 or auto+h3; 3 when requests use both
	Dynamic bool
}

// HTTP3ProbeOrigins lists, in first-use order, the https origins requests
// reach under an HTTP/3 policy, at most MaxHTTP3ProbeOrigins. Cleartext
// targets are not HTTP/3 origins: auto+h3 keeps them on TCP and validation
// refuses them for 3.
func HTTP3ProbeOrigins(script *Script) []HTTP3ProbeOrigin {
	if script == nil {
		return nil
	}
	switch ProtocolFamily(script.Protocol) {
	case "http", "soap", "java_http":
	default:
		return nil
	}
	inherited := InheritedHTTPVersion(script)
	base := strings.TrimSpace(script.Config.BaseURL)
	var out []HTTP3ProbeOrigin
	index := map[string]int{}
	walkHTTP3RequestLeaves(script, func(_ string, leaf http3Leaf) {
		version, err := EffectiveHTTPVersion(leaf.action, inherited)
		if err != nil || !httpprotocol.VersionUsesQUIC(version) {
			return
		}
		origin, dynamic, ok := http3LeafOrigin(strings.TrimSpace(leaf.target), base)
		if !ok {
			return
		}
		if at, seen := index[origin]; seen {
			if version == httpprotocol.VersionHTTP3 {
				out[at].Policy = version
			}
			return
		}
		if len(out) >= MaxHTTP3ProbeOrigins {
			return
		}
		index[origin] = len(out)
		out = append(out, HTTP3ProbeOrigin{Origin: origin, Policy: version, Dynamic: dynamic})
	})
	return out
}

// http3LeafOrigin resolves a request target to its origin. ok is false for
// a cleartext or unusable target. Only a template in the scheme or
// authority makes the origin dynamic; one in the path, query or fragment
// does not change where the request goes.
func http3LeafOrigin(target, base string) (origin string, dynamic, ok bool) {
	full := target
	if !strings.Contains(target, "://") {
		if strings.HasPrefix(target, "{{") {
			// The whole URL may come from the template.
			return truncateTemplate(target), true, true
		}
		if base == "" {
			return "", false, false
		}
		full = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(target, "/")
	}
	scheme, rest, _ := strings.Cut(full, "://")
	authority := rest
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		authority = rest[:end]
	}
	if strings.Contains(scheme, "{{") || strings.Contains(authority, "{{") {
		return truncateTemplate(scheme + "://" + authority), true, true
	}
	parsed, err := url.Parse(scheme + "://" + authority)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
		return "", false, false
	}
	return "https://" + parsed.Host, false, true
}

// truncateTemplate bounds a recorded template to 200 characters.
func truncateTemplate(template string) string {
	const maxTemplate = 200
	if runes := []rune(template); len(runes) > maxTemplate {
		return string(runes[:maxTemplate])
	}
	return template
}
