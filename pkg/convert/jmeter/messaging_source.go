package jmeter

import (
	"fmt"
	"regexp"
	"strings"
)

// redactMessagingSource returns the raw JMX source without redacting authored
// messaging settings or credentials; source files are preserved verbatim for
// operator authoring and review.
func redactMessagingSource(_ *xmlNode, content string) (string, bool) {
	return content, false
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

var (
	messagingOAuthOption      = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.]*)\s*=\s*"((?:[^"\\]|\\["\\])*)"`)
	messagingSASLModuleHeader = regexp.MustCompile(`^(org\.apache\.kafka\.common\.security\.(?:plain\.PlainLoginModule|scram\.ScramLoginModule))\s+(?i:required)(?:\s+|$)`)
	messagingSASLLoginOption  = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.]*)\s*=\s*(?:"((?:[^"\\]|\\["\\])*)"|'((?:[^'\\]|\\['\\])*)'|([^;\s"'=\\]+))`)
	jaasDoubleQuoteUnescaper  = strings.NewReplacer(`\\`, `\`, `\"`, `"`)
	jaasSingleQuoteUnescaper  = strings.NewReplacer(`\\`, `\`, `\'`, `'`)
)

func parseMessagingSASLLoginJAAS(source string) (string, map[string]string, error) {
	trimmed := strings.TrimSpace(source)
	if len(source) > 16384 {
		return "", nil, fmt.Errorf("JAAS exceeds size limit")
	}
	header := messagingSASLModuleHeader.FindStringSubmatch(trimmed)
	if header == nil {
		return "", nil, fmt.Errorf("unsupported JAAS module")
	}
	module := header[1]
	remaining := strings.TrimSpace(trimmed[len(header[0]):])
	if !strings.HasSuffix(remaining, ";") {
		return "", nil, fmt.Errorf("JAAS must end with semicolon")
	}
	remaining = strings.TrimSpace(strings.TrimSuffix(remaining, ";"))
	options := map[string]string{}
	for remaining != "" {
		match := messagingSASLLoginOption.FindStringSubmatch(remaining)
		if match == nil {
			return "", nil, fmt.Errorf("malformed JAAS options")
		}
		key := match[1]
		switch key {
		case "username", "password":
		case "tokenauth":
			if module != "org.apache.kafka.common.security.scram.ScramLoginModule" {
				return "", nil, fmt.Errorf("unsupported JAAS option")
			}
		default:
			return "", nil, fmt.Errorf("unsupported JAAS option")
		}
		if _, exists := options[key]; exists {
			return "", nil, fmt.Errorf("duplicate JAAS option")
		}
		rawRHS := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(match[0], key)), "="))
		var val string
		switch {
		case strings.HasPrefix(rawRHS, `"`):
			val = jaasDoubleQuoteUnescaper.Replace(match[2])
		case strings.HasPrefix(rawRHS, `'`):
			val = jaasSingleQuoteUnescaper.Replace(match[3])
		default:
			val = match[4]
		}
		if key == "tokenauth" && !strings.EqualFold(strings.TrimSpace(val), "false") {
			return "", nil, fmt.Errorf("delegation-token SCRAM authentication is unsupported")
		}
		options[key] = val
		tail := remaining[len(match[0]):]
		if tail != "" && !strings.ContainsAny(tail[:1], " \t\r\n") {
			return "", nil, fmt.Errorf("JAAS options require whitespace")
		}
		remaining = strings.TrimSpace(tail)
	}
	if len(options) == 0 {
		return "", nil, fmt.Errorf("JAAS credentials missing")
	}
	return module, options, nil
}

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
		options[key] = jaasDoubleQuoteUnescaper.Replace(match[2])
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
