package dsl

import httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"

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
