package dsl

import httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"

// FeatureNativeHTTP3V1 is the generator capability for executing the HTTP/3
// policies (auto+h3 and 3). A generator that does not advertise it, including
// any older generator, is treated as unable to run them.
const FeatureNativeHTTP3V1 = "native_http3_v1"

// ScriptUsesHTTP3 reports whether any request in the script resolves to an
// HTTP/3 policy after precedence (request, then script, then alias). A
// script-level 3 that every request overrides does not use HTTP/3.
func ScriptUsesHTTP3(script *Script) bool {
	if script == nil {
		return false
	}
	switch ProtocolFamily(script.Protocol) {
	case "http", "soap", "java_http":
	default:
		return false
	}
	inherited := InheritedHTTPVersion(script)
	uses := false
	walkHTTP3RequestLeaves(script, func(_ string, leaf http3Leaf) {
		if uses {
			return
		}
		version, err := EffectiveHTTPVersion(leaf.action, inherited)
		uses = err == nil && httpprotocol.VersionUsesQUIC(version)
	})
	return uses
}

// RequiredHTTPRuntimeFeatures admits HTTP/3 work only on generators that
// advertise native_http3_v1.
func RequiredHTTPRuntimeFeatures(script *Script) []string {
	if ScriptUsesHTTP3(script) {
		return []string{FeatureNativeHTTP3V1}
	}
	return nil
}
