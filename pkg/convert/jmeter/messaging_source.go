package jmeter

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"net/url"
	"regexp"
	"slices"
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
	// disabled marks an element JMeter skips: enabled="false" on the element
	// or on an ancestor, whose subtree is the hashTree that follows it.
	var visit func(n *xmlNode, disabled bool)
	visit = func(n *xmlNode, disabled bool) {
		disabled = disabled || !jmeterElementEnabled(n)
		if n.Name == diKafkaConfig || n.Name == diKafkaSampler || n.Name == pnopEventHubsSampler {
			messaging = true
		}
		name := n.Attrs["name"]
		if credential.SensitiveName(name) || credential.SensitiveXMLName(name) || name == "sharedAccessKey" {
			if strings.TrimSpace(n.Text) != "" {
				protected = append(protected, credentialpolicy.Requirement{Value: n.Text})
			}
		}
		if n.Name == "elementProp" && !credential.NonCredentialName(prop(n, "configKey")) &&
			(credential.SensitiveName(prop(n, "configKey")) || credential.SensitiveXMLName(prop(n, "configKey"))) {
			if value := prop(n, "configValue"); value != "" {
				protected = append(protected, credentialpolicy.Requirement{Value: value})
			}
		} else if n.Name == "elementProp" {
			// A URL setting such as sasl.oauthbearer.token.endpoint.url is an
			// address, not a credential by name (credential.NonCredentialName),
			// but its userinfo or query can still carry one.
			for _, value := range messagingURLCredentialValues(prop(n, "configValue")) {
				for _, form := range jmeterXMLTextForms(value) {
					protected = append(protected, credentialpolicy.Requirement{Value: form})
				}
			}
		}
		if n.Name == "elementProp" && prop(n, "configKey") == "sasl.jaas.config" {
			raw := prop(n, "configValue")
			// Only a JAAS the converter would read must be provably redactable.
			// A disabled one never runs; masking its values in place keeps the
			// rest of the plan reviewable instead of omitting the document, but
			// only when every option value is found: a variable reference, a
			// comment or anything else the option scan cannot read fails closed.
			if _, err := parseMessagingOAuthJAAS(raw); err != nil && (!disabled || !messagingJAASOptionsReadable(raw)) {
				unsupportedJAAS = true
			}
			// Each value is protected as JAAS writes it (Java escapes kept), as
			// Java reads it (escapes undone), and in the XML text forms a plan
			// writer uses for both, so a value whose quotes the plan stores as
			// &quot; is still masked in place; whatever survives is caught by the
			// decoded check, which omits the document.
			protect := func(value string) {
				for _, form := range jmeterXMLTextForms(value) {
					protected = append(protected, credentialpolicy.Requirement{Value: form})
				}
			}
			protect(raw)
			for _, m := range messagingJAASValue.FindAllStringSubmatch(raw, -1) {
				for _, v := range m[1:] {
					if v != "" {
						protect(v)
						protect(strings.ReplaceAll(strings.ReplaceAll(v, `\"`, `"`), `\\`, `\`))
					}
				}
			}
		}
		skipNext := false
		for _, child := range n.Children {
			childDisabled := disabled || (skipNext && child.Name == "hashTree")
			skipNext = child.Name != "hashTree" && !jmeterElementEnabled(child)
			visit(child, childDisabled)
		}
	}
	visit(root, false)
	if !messaging {
		return content, false
	}
	if unsupportedJAAS {
		return omittedJMeterCredentialSource(content, "UNSUPPORTED JAAS REQUIRES PROTECTED REVIEW"), true
	}
	return redactJMeterCredentialValues(content, protected, nil)
}

// messagingURLCredentialValues returns the credential parts of a URL value:
// its userinfo and the values of credential-named query or fragment
// parameters (credential.SensitiveName), each as written and percent-decoded.
// The value is scanned as text, not through url.Parse, so a templated host
// (${IDP_HOST}), a schemeless or unparseable address and a fragment are all
// covered. A parameter whose name cannot be decoded or is a variable
// reference, or userinfo that cannot be split, cannot be judged, so the whole
// value is returned (fail closed).
func messagingURLCredentialValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	var values []string
	add := func(value string) {
		values = append(values, value)
		if decoded, err := url.QueryUnescape(value); err == nil && decoded != value {
			values = append(values, decoded)
		}
	}
	if at := strings.IndexAny(raw, "?#"); at >= 0 {
		for _, pair := range strings.FieldsFunc(raw[at+1:], func(r rune) bool { return r == '&' || r == ';' || r == '?' || r == '#' }) {
			name, value, _ := strings.Cut(pair, "=")
			decoded, err := url.QueryUnescape(name)
			if err != nil || strings.Contains(decoded, "${") {
				return []string{raw}
			}
			if credential.SensitiveName(decoded) && credential.MatchableNamedValue(decoded, value) {
				add(value)
			}
		}
	}
	// Userinfo sits between "//" (or the start of a schemeless value) and the
	// first '/', '?' or '#'. Without "//" only a user:password form counts,
	// so a plain name@domain value is not treated as a username.
	authority, slashes := raw, false
	if scheme, rest, ok := strings.Cut(raw, "//"); ok && !strings.ContainsAny(scheme, "/?#") {
		authority, slashes = rest, true
	}
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		// Userinfo holding whitespace, a quote, '=' or ';' (base64 padding, a
		// JAAS-like value) cannot be split with confidence: fail closed.
		if strings.ContainsAny(authority, " \t\r\n\"'=;") {
			return []string{raw}
		}
		username, password, hasPassword := strings.Cut(authority[:at], ":")
		if (slashes || hasPassword) && credential.MatchableNamedValue("username", username) {
			add(username)
		}
		if hasPassword {
			add(password)
		}
	}
	return slices.DeleteFunc(values, func(v string) bool { return strings.TrimSpace(v) == "" })
}

// jmeterXMLTextForms returns value with the XML text encodings a JMeter
// plan may store it in: JMeter's XStream writer (&quot; and &apos;), the same
// with literal apostrophes, and numeric references (&#34; and &#39;).
func jmeterXMLTextForms(value string) []string {
	forms := []string{value}
	base := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
	for _, replacer := range []*strings.Replacer{
		strings.NewReplacer(`"`, "&quot;", "'", "&apos;"),
		strings.NewReplacer(`"`, "&quot;"),
		strings.NewReplacer(`"`, "&#34;", "'", "&#39;"),
	} {
		if form := replacer.Replace(base); form != value {
			forms = append(forms, form)
		}
	}
	return forms
}

var (
	messagingJAASHeader = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]*\s+(?i:required|requisite|sufficient|optional)(?:\s+|$)`)
	// messagingJAASOption is one whole option, its value quoted or a bare word
	// with no quote, comment slash or separator in it.
	messagingJAASOption = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*\s*=\s*(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^;\s"'/=]+)`)
)

// messagingJAASOptionsReadable reports whether raw is one literal login module
// whose options are all whitespace-separated key=value pairs, so the option
// scan in redactMessagingSource finds every value it holds.
func messagingJAASOptionsReadable(raw string) bool {
	if strings.Contains(raw, "${") {
		return false
	}
	remaining := strings.TrimRight(strings.TrimSpace(raw), "; \t\r\n")
	header := messagingJAASHeader.FindString(remaining)
	if header == "" {
		return false
	}
	for remaining = strings.TrimSpace(remaining[len(header):]); remaining != ""; {
		option := messagingJAASOption.FindString(remaining)
		if option == "" {
			return false
		}
		tail := remaining[len(option):]
		if tail != "" && !strings.ContainsAny(tail[:1], " \t\r\n") {
			return false
		}
		remaining = strings.TrimSpace(tail)
	}
	return true
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
