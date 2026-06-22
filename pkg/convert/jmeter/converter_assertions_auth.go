package jmeter

// converter_assertions_auth.go — split from converter.go (same package, behavior-preserving).

import (
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseAuthManager(node *xmlNode, diagnostics *diag.Collector) []jmeterAuth {
	var auths []jmeterAuth
	location := jmeterElementLocation(node)
	for _, element := range descendantsByAttr(node, "elementType", "Authorization") {
		username := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "Authorization.username"), prop(element, "username")), diagnostics, location)
		password := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "Authorization.password"), prop(element, "password")), diagnostics, location)
		mechanism := strings.TrimSpace(convertutil.FirstNonEmpty(prop(element, "Authorization.mechanism"), prop(element, "Authorization.Mechanism"), prop(element, "mechanism")))
		auth := jmeterAuth{
			URL:       convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "Authorization.url"), prop(element, "url")), diagnostics, location),
			Username:  username,
			Password:  password,
			Domain:    convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "Authorization.domain"), prop(element, "domain")), diagnostics, location),
			Mechanism: mechanism,
		}
		auth.Config = authConfigFromJMeterAuth(auth, diagnostics, location)
		if auth.Config.Type == "" {
			continue
		}
		auths = append(auths, auth)
	}
	return auths
}

func authConfigFromJMeterAuth(auth jmeterAuth, diagnostics *diag.Collector, location string) dsl.AuthConfig {
	mechanism := strings.ToLower(strings.TrimSpace(auth.Mechanism))
	switch {
	case strings.Contains(mechanism, "bearer") || strings.Contains(mechanism, "token"):
		return dsl.AuthConfig{Type: "bearer", Token: convertutil.FirstNonEmpty(auth.Password, auth.Username, "{{env.API_TOKEN}}")}
	case strings.Contains(mechanism, "digest"):
		// Digest cannot be executed by the HTTP engine (validateAuthType fails
		// closed on it), so emitting Type:"digest" would break replay. Drop it
		// with a warning rather than producing an unrunnable script.
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter AuthManager", location, "Digest auth cannot be executed at HTTP replay (fails closed); configure native auth before replay")
		}
		return dsl.AuthConfig{}
	case mechanism == "" || strings.Contains(mechanism, "basic") || strings.Contains(mechanism, "kerberos"):
		if strings.Contains(mechanism, "kerberos") && diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter AuthManager", location, "Kerberos auth was approximated as basic credentials; configure native auth before replay")
		}
		return dsl.AuthConfig{Type: "basic", Username: convertutil.FirstNonEmpty(auth.Username, "{{env.API_USERNAME}}"), Password: convertutil.FirstNonEmpty(auth.Password, "{{env.API_PASSWORD}}")}
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter AuthManager", location, "auth mechanism "+auth.Mechanism+" was not converted")
		}
		return dsl.AuthConfig{}
	}
}

func authForSampler(auths []jmeterAuth, absoluteURL string, rawPath string) (dsl.AuthConfig, bool) {
	for _, auth := range auths {
		if auth.Config.Type == "" || strings.TrimSpace(auth.URL) == "" {
			continue
		}
		if authMatchesURL(auth.URL, absoluteURL) || authMatchesURL(auth.URL, rawPath) {
			config := auth.Config
			return config, true
		}
	}
	return dsl.AuthConfig{}, false
}

func authMatchesURL(authURL string, candidate string) bool {
	authURL = strings.TrimRight(strings.TrimSpace(authURL), "/")
	candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
	if authURL == "" || candidate == "" {
		return false
	}
	return strings.HasPrefix(candidate, authURL)
}

func parseResponseAssertions(node *xmlNode, diagnostics *diag.Collector) []dsl.Assertion {
	field := strings.TrimSpace(prop(node, "Assertion.test_field"))
	if field == "" {
		field = "Assertion.response_data"
	}
	testType := parseInt(prop(node, "Assertion.test_type"))
	location := jmeterElementLocation(node)
	values := assertionTestStrings(node)
	if len(values) == 0 {
		return nil
	}
	if field == "Assertion.response_message" || field == "Assertion.response_headers" {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "assertion field "+field+" has no native APEX Load assertion mapping and was not converted")
		}
		return nil
	}
	negated := testType&4 != 0
	if strings.EqualFold(field, "Assertion.response_code") {
		out := make([]dsl.Assertion, 0, len(values))
		for _, value := range values {
			status, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				if diagnostics != nil {
					diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "response-code assertion "+value+" is not an integer and was skipped")
				}
				continue
			}
			if negated {
				out = append(out, dsl.Assertion{Type: "status_code_not", Expected: status})
			} else {
				out = append(out, dsl.Assertion{Type: "status_code", Expected: status})
			}
		}
		return out
	}
	switch field {
	case "Assertion.response_data", "Assertion.response_data_as_document":
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "assertion field "+field+" was approximated as a response body assertion")
		}
	}
	assertionType := "body_contains"
	if negated {
		assertionType = "body_not_contains"
	}
	if testType&(1|8) != 0 && diagnostics != nil {
		diagnostics.Add(diag.Info, "JMeter ResponseAssertion", location, "regex or equality assertion semantics were approximated with body contains checks")
	}
	var out []dsl.Assertion
	for _, text := range values {
		out = append(out, dsl.Assertion{Type: assertionType, Value: convertVariablesWithDiagnostics(text, diagnostics, location)})
	}
	return out
}

func assertionTestStrings(node *xmlNode) []string {
	var values []string
	for _, child := range node.Children {
		if child.Name != "collectionProp" || !strings.Contains(strings.ToLower(child.Attrs["name"]), "test_strings") {
			continue
		}
		for _, grand := range child.Children {
			text := strings.TrimSpace(grand.Text)
			if text != "" {
				values = append(values, text)
			}
		}
	}
	if len(values) > 0 {
		return values
	}
	for _, valueNode := range childProps(node, "") {
		text := strings.TrimSpace(valueNode.Text)
		if text != "" {
			values = append(values, text)
		}
	}
	return values
}

func hasStatusAssertion(assertions []dsl.Assertion) bool {
	for _, assertion := range assertions {
		if assertion.Type == "status_code" {
			return true
		}
	}
	return false
}
