package jmeter

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"regexp"
	"strings"
)

var messagingJAASValue = regexp.MustCompile(`(?:^|\s)[A-Za-z][A-Za-z0-9_.]*\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^;\s]+))`)

// Messaging plugins duplicate values in arbitrary XML attributes (including a
// message's elementProp name). Redact by decoded value, then verify the entire
// XML token stream. Entity-obscured leftovers omit the public source, preserving
// line numbers. This does not itself persist the original as a protected source artifact.
func redactMessagingSource(root *xmlNode, content string) (string, bool) {
	var protected []credentialpolicy.Requirement
	messaging := false
	unsupportedJAAS := false
	var visit func(*xmlNode)
	visit = func(n *xmlNode) {
		if n.Name == diKafkaConfig || n.Name == diKafkaSampler || n.Name == pnopEventHubsSampler {
			messaging = true
		}
		name := n.Attrs["name"]
		if credential.SensitiveName(name) || credential.SensitiveXMLName(name) || name == "sharedAccessKey" {
			if strings.TrimSpace(n.Text) != "" {
				protected = append(protected, credentialpolicy.Requirement{Value: n.Text})
			}
		}
		if n.Name == "elementProp" && (credential.SensitiveName(prop(n, "configKey")) || credential.SensitiveXMLName(prop(n, "configKey"))) {
			if value := prop(n, "configValue"); value != "" {
				protected = append(protected, credentialpolicy.Requirement{Value: value})
			}
		}
		if n.Name == "elementProp" && prop(n, "configKey") == "sasl.jaas.config" {
			raw := prop(n, "configValue")
			if _, err := parseMessagingOAuthJAAS(raw); err != nil {
				unsupportedJAAS = true
			}
			protected = append(protected, credentialpolicy.Requirement{Value: raw})
			for _, m := range messagingJAASValue.FindAllStringSubmatch(raw, -1) {
				for _, v := range m[1:] {
					if v != "" {
						protected = append(protected, credentialpolicy.Requirement{Value: strings.ReplaceAll(strings.ReplaceAll(v, `\"`, `"`), `\\`, `\`)})
					}
				}
			}
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	visit(root)
	if !messaging {
		return content, false
	}
	if unsupportedJAAS {
		return omittedJMeterCredentialSource(content, "UNSUPPORTED JAAS REQUIRES PROTECTED REVIEW"), true
	}
	return redactJMeterCredentialValues(content, protected, nil)
}

var messagingOAuthOption = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.]*)\s*=\s*"((?:[^"\\]|\\["\\])*)"`)

func parseMessagingOAuthJAAS(source string) (map[string]string, error) {
	const module = "org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required"
	trimmed := strings.TrimSpace(source)
	if len(source) > 16384 || !strings.HasPrefix(trimmed, module) || len(trimmed) <= len(module) || !strings.ContainsAny(trimmed[len(module):len(module)+1], " \t\r\n") {
		return nil, fmt.Errorf("unsupported JAAS module")
	}
	remaining := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), module))
	if !strings.HasSuffix(remaining, ";") {
		return nil, fmt.Errorf("JAAS must end with semicolon")
	}
	remaining = strings.TrimSpace(strings.TrimSuffix(remaining, ";"))
	options := map[string]string{}
	for remaining != "" {
		match := messagingOAuthOption.FindStringSubmatch(remaining)
		if match == nil {
			return nil, fmt.Errorf("malformed JAAS options")
		}
		key := match[1]
		switch key {
		case "clientId", "clientSecret", "scope", "cid", "cs":
		default:
			return nil, fmt.Errorf("unsupported JAAS option")
		}
		if _, exists := options[key]; exists {
			return nil, fmt.Errorf("duplicate JAAS option")
		}
		options[key] = strings.ReplaceAll(strings.ReplaceAll(match[2], `\"`, `"`), `\\`, `\`)
		tail := remaining[len(match[0]):]
		if tail != "" && !strings.ContainsAny(tail[:1], " \t\r\n") {
			return nil, fmt.Errorf("JAAS options require whitespace")
		}
		remaining = strings.TrimSpace(tail)
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("JAAS credentials missing")
	}
	return options, nil
}
