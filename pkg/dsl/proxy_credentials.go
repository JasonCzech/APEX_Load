package dsl

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Proxy credential fields, spelled as the slash paths the engine's executable
// secret-reference walk reports (pkg/engine ReferencedSecretNames). The
// dashboard applies the same active/inactive rule to its readiness checks.
const (
	HTTPProxyUsernamePath    = "/config/http/proxy/username"
	HTTPProxyPasswordPath    = "/config/http/proxy/password"
	BrowserProxyUsernamePath = "/config/browser/proxy/username"
	BrowserProxyPasswordPath = "/config/browser/proxy/password"
)

// EncryptedProxyPasswordFindingCode is the enterprise-archive import finding
// that calls out a VuGen-encrypted proxy password. It is advisory: it never
// blocks replay, debug, shakeout or a test by itself, including on scripts
// stored before it became advisory that still carry it as execution_blocker
// metadata. The live required-secret check (an active custom proxy whose
// credential secret is not bound) is the only proxy credential blocker.
const EncryptedProxyPasswordFindingCode = "encrypted_proxy_password"

// AdvisoryExecutionBlockerCode reports whether an execution_blocker(s)
// metadata code is a callout that must not block execution. Every evaluator of
// execution_blocker metadata skips these codes.
func AdvisoryExecutionBlockerCode(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), EncryptedProxyPasswordFindingCode)
}

// AdvisoryCalloutsMetadataKey is the step metadata list that carries advisory
// callout codes (AdvisoryExecutionBlockerCode), such as
// encrypted_proxy_password. Importers write these codes here, never as
// execution_blocker(s): a generator or controller built before the callout
// became advisory blocks every execution_blocker code it reads. Import review
// (FindingsFromScript), guided repairs and the dashboard read this key, plus
// the legacy execution_blocker(s) spelling on scripts stored earlier.
const AdvisoryCalloutsMetadataKey = "advisory_callouts"

// MoveAdvisoryExecutionBlockers rewrites legacy execution_blocker(s) metadata
// in place: advisory callout codes move to AdvisoryCalloutsMetadataKey and
// every other blocker code stays where it was. It reports whether metadata
// changed. The controller applies it to the script copy it dispatches, so a
// generator built before the callout became advisory never sees the code as
// a blocker.
func MoveAdvisoryExecutionBlockers(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	var advisory, kept []string
	classify := func(code string) {
		code = strings.TrimSpace(code)
		switch {
		case code == "":
		case AdvisoryExecutionBlockerCode(code):
			advisory = append(advisory, code)
		default:
			kept = append(kept, code)
		}
	}
	single, _ := metadata["execution_blocker"].(string)
	classify(single)
	switch blockers := metadata["execution_blockers"].(type) {
	case []string:
		for _, code := range blockers {
			classify(code)
		}
	case []any:
		for _, raw := range blockers {
			if code, ok := raw.(string); ok {
				classify(code)
			}
		}
	}
	if len(advisory) == 0 {
		return false
	}
	kept = sortedUniqueAdvisoryCodes(kept)
	if len(kept) == 0 {
		delete(metadata, "execution_blocker")
		delete(metadata, "execution_blockers")
	} else {
		if AdvisoryExecutionBlockerCode(single) {
			metadata["execution_blocker"] = kept[0]
		}
		metadata["execution_blockers"] = kept
	}
	switch existing := metadata[AdvisoryCalloutsMetadataKey].(type) {
	case []string:
		advisory = append(advisory, existing...)
	case []any:
		for _, raw := range existing {
			if code, ok := raw.(string); ok {
				advisory = append(advisory, code)
			}
		}
	}
	metadata[AdvisoryCalloutsMetadataKey] = sortedUniqueAdvisoryCodes(advisory)
	return true
}

// sortedUniqueAdvisoryCodes trims, drops empty values, deduplicates and sorts
// codes so rewritten metadata is stable.
func sortedUniqueAdvisoryCodes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

// HTTPProxyCredentialsActive reports whether the HTTP runtime sends
// config.http.proxy username/password. It matches applyProxy in
// pkg/protocol/http/config.go: only a custom proxy, or the legacy empty mode
// with a proxy URL, authenticates to a proxy. No proxy, System and PAC never
// send these credentials.
func HTTPProxyCredentialsActive(cfg HTTPConfig) bool {
	switch strings.TrimSpace(cfg.ProxyMode) {
	case "custom":
		return true
	case "":
		return strings.TrimSpace(cfg.Proxy.URL) != ""
	default:
		return false
	}
}

// BrowserProxyCredentialsActive reports whether the browser runtime uses
// config.browser.proxy username/password. The browser proxy has no mode: the
// credentials are used only beside a proxy server.
func BrowserProxyCredentialsActive(cfg BrowserProxyConfig) bool {
	return strings.TrimSpace(cfg.Server) != ""
}

// InactiveProxyCredentialPaths returns the proxy credential fields the runtime
// never sends for this script's current proxy settings. A secret referenced
// only from these fields is not required.
func InactiveProxyCredentialPaths(script *Script) []string {
	if script == nil {
		return nil
	}
	var paths []string
	if !HTTPProxyCredentialsActive(script.Config.HTTP) {
		paths = append(paths, HTTPProxyUsernamePath, HTTPProxyPasswordPath)
	}
	if !BrowserProxyCredentialsActive(script.Config.Browser.Proxy) {
		paths = append(paths, BrowserProxyUsernamePath, BrowserProxyPasswordPath)
	}
	return paths
}

// HTTPProxyModeLabel names an HTTP proxy mode as the script editor shows it.
func HTTPProxyModeLabel(cfg HTTPConfig) string {
	switch strings.TrimSpace(cfg.ProxyMode) {
	case "system":
		return "System"
	case "pac":
		return "PAC"
	case "custom":
		return "Custom"
	case "":
		if strings.TrimSpace(cfg.Proxy.URL) != "" {
			return "Custom"
		}
	}
	return "No proxy"
}

// ProxyCredentialSecretReference is one {{secret.NAME}} reference in a proxy
// credential field.
type ProxyCredentialSecretReference struct {
	// Name is the logical secret name as written (trimmed), matching the
	// names pkg/engine ReferencedSecretNames reports.
	Name string
	// Path is one of the *Proxy*Path constants.
	Path string
	// Browser is true for config.browser.proxy, false for config.http.proxy.
	Browser bool
	// Field is "username" or "password".
	Field string
	// Active reports whether the runtime sends this field for the script's
	// current proxy settings.
	Active bool
	// ModeLabel is the HTTP proxy mode label; empty for a browser reference.
	ModeLabel string
}

var proxySecretTemplatePattern = regexp.MustCompile(`\{\{([^{}\r\n]+)\}\}`)

// ProxyCredentialSecretReferences lists every secret reference in the HTTP and
// browser proxy credential fields, active or not.
func ProxyCredentialSecretReferences(script *Script) []ProxyCredentialSecretReference {
	if script == nil {
		return nil
	}
	httpCfg := script.Config.HTTP
	browserProxy := script.Config.Browser.Proxy
	httpActive := HTTPProxyCredentialsActive(httpCfg)
	browserActive := BrowserProxyCredentialsActive(browserProxy)
	modeLabel := HTTPProxyModeLabel(httpCfg)
	fields := []struct {
		value   string
		path    string
		browser bool
		field   string
		active  bool
	}{
		{httpCfg.Proxy.Username, HTTPProxyUsernamePath, false, "username", httpActive},
		{httpCfg.Proxy.Password, HTTPProxyPasswordPath, false, "password", httpActive},
		{browserProxy.Username, BrowserProxyUsernamePath, true, "username", browserActive},
		{browserProxy.Password, BrowserProxyPasswordPath, true, "password", browserActive},
	}
	var refs []ProxyCredentialSecretReference
	for _, field := range fields {
		seen := map[string]struct{}{}
		for _, match := range proxySecretTemplatePattern.FindAllStringSubmatch(field.value, -1) {
			expression := strings.TrimSpace(match[1])
			if !strings.HasPrefix(expression, "secret.") {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(expression, "secret."))
			if name == "" {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				continue
			}
			seen[name] = struct{}{}
			ref := ProxyCredentialSecretReference{
				Name:    name,
				Path:    field.path,
				Browser: field.browser,
				Field:   field.field,
				Active:  field.active,
			}
			if !field.browser {
				ref.ModeLabel = modeLabel
			}
			refs = append(refs, ref)
		}
	}
	return refs
}

// BlockerTitle is the title of the blocker shown when this active proxy
// credential's secret is not bound.
func (r ProxyCredentialSecretReference) BlockerTitle() string {
	if r.Browser {
		return "Browser proxy credential not bound"
	}
	return "Custom proxy credential not bound"
}

// BlockerDetail is the body of the blocker shown when this active proxy
// credential's secret is not bound.
func (r ProxyCredentialSecretReference) BlockerDetail() string {
	if r.Browser {
		return fmt.Sprintf("The browser proxy %s uses the secret %s, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server.", r.Field, r.Name)
	}
	return fmt.Sprintf("The proxy %s uses the secret %s, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.", r.Field, r.Name)
}

// BlockerMessage joins the blocker title and body for plain-text surfaces
// (issue lists, API errors): "<title>: <body>".
func (r ProxyCredentialSecretReference) BlockerMessage() string {
	return r.BlockerTitle() + ": " + r.BlockerDetail()
}

// InactiveNote is the non-blocking note for a secret reference in a proxy
// credential field the runtime does not send.
func (r ProxyCredentialSecretReference) InactiveNote() string {
	if r.Browser {
		return fmt.Sprintf("The browser proxy %s uses the secret %s, but no browser proxy server is set, so it is not needed. It becomes required if you set a browser proxy server.", r.Field, r.Name)
	}
	return fmt.Sprintf("The proxy %s uses the secret %s, but Proxy mode is %s, so it is not needed. It becomes required if you switch Proxy mode to Custom.", r.Field, r.Name, r.ModeLabel)
}
