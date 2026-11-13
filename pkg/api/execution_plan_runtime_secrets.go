package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Execution plan entries store runtime_overrides as written: plans have no
// protect/mask/reconcile pass. So every secret-bearing field the run-side mask
// covers (runtime_mask.go) must be empty or hold an exact {{secret.NAME}}
// reference to a project credential, never a typed value and never a
// controller-reserved APEX_RUNTIME_OVERRIDE_* name. This mirrors the
// dashboard's planEntryTypedCredentialPaths (executionPlanRuntimeSecrets.ts)
// so API and MCP callers get the same rule.

var executionPlanSchemedValuePattern = regexp.MustCompile(`(?i)^(bearer|basic|apikey|token|digest|negotiate|ntlm)\s+(.+)$`)
var executionPlanAuthorityPattern = regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9+.-]*://)?([^/?#\s]*)@[^/?#\s]`)
var executionPlanReferenceParamPattern = regexp.MustCompile(`[?&#;][^=&#;?]*=\{\{\s*secret\.[^}]*\}\}`)

func isExecutionPlanSecretReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	return runtimeOverrideExactSecretPattern.MatchString(trimmed) && !isReservedRuntimeOverrideSecretReference(trimmed)
}

// isExecutionPlanSchemedSecretReference accepts an exact reference, or an
// Authorization scheme followed by one ("Bearer {{secret.API_TOKEN}}").
func isExecutionPlanSchemedSecretReference(value string) bool {
	if isExecutionPlanSecretReference(value) {
		return true
	}
	match := executionPlanSchemedValuePattern.FindStringSubmatch(strings.TrimSpace(value))
	return len(match) == 3 && isExecutionPlanSecretReference(match[2])
}

func executionPlanCookieOfReferences(value string) bool {
	pairs, ok := parseCookieHeaderValue(value)
	if !ok {
		return false
	}
	for _, pair := range pairs {
		if !isExecutionPlanSecretReference(pair.value) {
			return false
		}
	}
	return true
}

// executionPlanURLCarriesTypedCredential reports a URL whose userinfo or
// credential-shaped query/fragment values are typed rather than references.
func executionPlanURLCarriesTypedCredential(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	rest := value
	if match := executionPlanAuthorityPattern.FindStringSubmatchIndex(value); match != nil {
		userinfo := value[match[2]:match[3]]
		for _, part := range strings.Split(userinfo, ":") {
			decoded := part
			if unescaped, err := url.PathUnescape(part); err == nil {
				decoded = unescaped
			}
			if part != "" && !isExecutionPlanSecretReference(decoded) {
				return true
			}
		}
		// The userinfo holds only references: check the rest without it.
		rest = value[:match[2]] + value[match[3]+1:]
	}
	// Whatever the run-side mask would still redact, once each query or
	// fragment value that is a reference is set aside, is a typed credential.
	// A pair is set aside only when its whole value, up to the next separator
	// or the end, is the reference: "token={{secret.T}}hunter2" keeps its typed
	// suffix in the value and stays a credential.
	var neutral strings.Builder
	last := 0
	for _, match := range executionPlanReferenceParamPattern.FindAllStringIndex(rest, -1) {
		start, end := match[0], match[1]
		if end < len(rest) && !strings.ContainsRune("&;#", rune(rest[end])) {
			continue
		}
		pair := rest[start:end]
		separator := strings.IndexByte(pair, '=')
		if separator < 0 || !isExecutionPlanSecretReference(pair[separator+1:]) {
			continue
		}
		neutral.WriteString(rest[last:start])
		neutral.WriteString(pair[:1])
		last = end
	}
	neutral.WriteString(rest[last:])
	return maskURLSecrets(neutral.String()) != neutral.String()
}

func executionPlanHeaderNeedsReference(name, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(name), "Cookie") {
		return !executionPlanCookieOfReferences(value)
	}
	if runtimeHTTPHeaderSecretBearing(name, value) {
		return !isExecutionPlanSchemedSecretReference(value)
	}
	return false
}

func executionPlanVMParameterNeedsReference(parameter string) bool {
	if value, ok := javaSystemPropertyAssignment(parameter); ok {
		if strings.TrimSpace(value) == "" || isExecutionPlanSchemedSecretReference(value) {
			return false
		}
		return javarunner.ContainsSensitiveVMParameterValue(parameter) || executionPlanURLCarriesTypedCredential(value)
	}
	return javarunner.ContainsSensitiveVMParameterValue(parameter)
}

// javaSystemPropertyAssignment returns the value of a -Dname=value parameter.
func javaSystemPropertyAssignment(parameter string) (string, bool) {
	trimmed := strings.TrimSpace(parameter)
	if len(trimmed) < 2 || !strings.EqualFold(trimmed[:2], "-D") {
		return "", false
	}
	equals := strings.IndexByte(trimmed, '=')
	if equals <= 2 {
		return "", false
	}
	return trimmed[equals+1:], true
}

// executionPlanMapKey is a location step that names a map entry (a header or
// env name): unlike a struct field's JSON key, it matches exactly.
type executionPlanMapKey string

// executionPlanTypedCredential is one field of a plan entry row that holds a
// typed credential instead of a project credential reference. location is its
// place in the row's JSON (struct field keys, map keys and array indices).
type executionPlanTypedCredential struct {
	path     string
	location []any
	value    string
	url      bool
}

// executionPlanTypedCredentials lists the fields of a plan entry row that hold
// a typed credential instead of a project credential reference. Empty means
// the row is safe to store. Usernames are identities, not secrets, and may be
// typed (as in the dashboard).
func executionPlanTypedCredentials(overrides *RuntimeSettingsOverride) []executionPlanTypedCredential {
	if overrides == nil {
		return nil
	}
	var found []executionPlanTypedCredential
	add := func(path string, value string, url bool, location ...any) {
		found = append(found, executionPlanTypedCredential{path: path, location: location, value: value, url: url})
	}
	requireReference := func(path, value string, location ...any) {
		if strings.TrimSpace(value) != "" && !isExecutionPlanSecretReference(value) {
			add(path, value, false, location...)
		}
	}
	requireCleanURL := func(path, value string, location ...any) {
		if executionPlanURLCarriesTypedCredential(value) {
			add(path, value, true, location...)
		}
	}

	if soap := overrides.SOAP; soap != nil && soap.WSSecurity != nil && soap.WSSecurity.UsernameToken != nil {
		requireReference("soap.ws_security.username_token.password", soap.WSSecurity.UsernameToken.Password, "soap", "ws_security", "username_token", "password")
	}
	if http := overrides.HTTP; http != nil {
		requireReference("http.auth.password", http.Auth.Password, "http", "auth", "password")
		requireReference("http.auth.token", http.Auth.Token, "http", "auth", "token")
		requireReference("http.proxy.password", http.Proxy.Password, "http", "proxy", "password")
		requireCleanURL("http.proxy.url", http.Proxy.URL, "http", "proxy", "url")
		requireCleanURL("http.proxy.pac_url", http.Proxy.PACURL, "http", "proxy", "pac_url")
		for name, value := range http.DefaultHeaders {
			if executionPlanHeaderNeedsReference(name, value) {
				add("http.default_headers."+name, value, false, "http", "default_headers", executionPlanMapKey(name))
			}
		}
		for index, header := range http.DefaultHeaderList {
			if executionPlanHeaderNeedsReference(header.Name, header.Value) {
				add("http.default_header_list."+header.Name, header.Value, false, "http", "default_header_list", index, "value")
			}
		}
	}
	requireCleanURL("base_url", overrides.BaseURL, "base_url")
	if browser := overrides.BrowserConfig; browser != nil {
		requireReference("browser_config.http_credentials.password", browser.HTTPCredentials.Password, "browser_config", "http_credentials", "password")
		requireReference("browser_config.proxy.password", browser.Proxy.Password, "browser_config", "proxy", "password")
		// A Playwright cookies/localStorage snapshot is a session credential.
		requireReference("browser_config.storage_state", browser.StorageState, "browser_config", "storage_state")
		requireCleanURL("browser_config.proxy.server", browser.Proxy.Server, "browser_config", "proxy", "server")
		for index, certificate := range browser.ClientCertificates {
			requireReference("browser_config.client_certificates."+strconv.Itoa(index)+".passphrase", certificate.Passphrase, "browser_config", "client_certificates", index, "passphrase")
		}
		if session := browser.Session; session != nil {
			for index, identity := range session.Identities {
				requireReference("browser_config.session.identities."+strconv.Itoa(index)+".password", identity.Password, "browser_config", "session", "identities", index, "password")
				requireReference("browser_config.session.identities."+strconv.Itoa(index)+".storage_state", identity.StorageState, "browser_config", "session", "identities", index, "storage_state")
			}
			if session.JWT != nil {
				requireReference("browser_config.session.jwt.private_key", session.JWT.PrivateKey, "browser_config", "session", "jwt", "private_key")
			}
		}
	}
	if java := overrides.JavaVuser; java != nil {
		for index, parameter := range java.AdditionalVMParameters {
			if executionPlanVMParameterNeedsReference(parameter) {
				add("java_vuser.additional_vm_parameters."+strconv.Itoa(index), parameter, false, "java_vuser", "additional_vm_parameters", index)
			}
		}
	}
	for key, value := range overrides.Env {
		if strings.TrimSpace(value) == "" || isExecutionPlanSchemedSecretReference(value) {
			continue
		}
		if sensitiveEnvKey(key) || sensitiveEnvValue(value) {
			add("env."+key, value, false, "env", executionPlanMapKey(key))
		}
	}
	return found
}

func executionPlanTypedCredentialPaths(overrides *RuntimeSettingsOverride) []string {
	found := executionPlanTypedCredentials(overrides)
	paths := make([]string, 0, len(found))
	for _, credential := range found {
		paths = append(paths, credential.path)
	}
	return paths
}

// validateExecutionPlanRuntimeOverrideSecrets keeps typed credentials out of
// plan entries, which are stored and returned verbatim.
func validateExecutionPlanRuntimeOverrideSecrets(overrides *RuntimeSettingsOverride) error {
	paths := executionPlanTypedCredentialPaths(overrides)
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fmt.Errorf("entry runtime_overrides %s must be {{secret.NAME}} references to project credentials; typed passwords, tokens and URL credentials are not stored in execution plans", strings.Join(paths, ", "))
}

// executionPlanRedactedPaths lists every string in a plan entry row that
// carries the redaction placeholder a masked plan read returns. Plans have no
// reconcile pass, so the placeholder would otherwise be stored and run as a
// literal value.
func executionPlanRedactedPaths(raw json.RawMessage) []string {
	var row any
	if len(raw) == 0 || json.Unmarshal(raw, &row) != nil {
		return nil
	}
	var paths []string
	var walk func(prefix string, node any)
	walk = func(prefix string, node any) {
		join := func(key string) string {
			if prefix == "" {
				return key
			}
			return prefix + "." + key
		}
		switch value := node.(type) {
		case map[string]any:
			for key, child := range value {
				walk(join(key), child)
			}
		case []any:
			for index, child := range value {
				walk(join(strconv.Itoa(index)), child)
			}
		case string:
			if executionPlanValueCarriesRedaction(value) {
				paths = append(paths, prefix)
			}
		}
	}
	walk("", row)
	sort.Strings(paths)
	return paths
}

func executionPlanValueCarriesRedaction(value string) bool {
	if strings.Contains(value, scrub.Redacted) {
		return true
	}
	lower := strings.ToLower(value)
	return strings.Contains(lower, strings.ToLower(url.QueryEscape(scrub.Redacted))) || strings.Contains(lower, strings.ToLower(url.PathEscape(scrub.Redacted)))
}

// validateExecutionPlanRuntimeOverrideRedactions rejects a row that sends the
// redaction placeholder back, so it can never be stored.
func validateExecutionPlanRuntimeOverrideRedactions(raw json.RawMessage) error {
	paths := executionPlanRedactedPaths(raw)
	if len(paths) == 0 {
		return nil
	}
	return fmt.Errorf("entry runtime_overrides %s still hold the %s placeholder from a masked read; enter a {{secret.NAME}} reference or clear the value", strings.Join(paths, ", "), scrub.Redacted)
}

// maskExecutionPlanForResponse masks the typed credentials in a stored entry
// row (one saved before every secret-bearing field was checked), so a plan
// read never returns them in plaintext. Only those fields are masked: the
// row's usernames, references and other values are returned as stored, and
// the row is edited as JSON so no field is lost to a struct round trip. The
// plan is not mutated.
func maskExecutionPlanForResponse(plan *storage.ExecutionPlan) *storage.ExecutionPlan {
	if plan == nil {
		return nil
	}
	var masked *storage.ExecutionPlan
	for index, entry := range plan.Entries {
		raw, changed := maskExecutionPlanRuntimeOverridesRow(entry.RuntimeOverrides)
		if !changed {
			continue
		}
		if masked == nil {
			clone := *plan
			clone.Entries = append([]storage.ExecutionPlanEntry(nil), plan.Entries...)
			masked = &clone
		}
		masked.Entries[index].RuntimeOverrides = raw
	}
	if masked == nil {
		return plan
	}
	return masked
}

// maskExecutionPlanRuntimeOverridesRow returns a copy of one plan or plan-run
// entry's runtime_overrides row with its typed credentials masked, and whether
// it masked anything. The input row is never mutated: a run's stored snapshot
// keeps its values, because resume replays it.
func maskExecutionPlanRuntimeOverridesRow(row json.RawMessage) (json.RawMessage, bool) {
	if len(row) == 0 {
		return row, false
	}
	var overrides *RuntimeSettingsOverride
	if err := json.Unmarshal(row, &overrides); err != nil {
		return row, false
	}
	credentials := executionPlanTypedCredentials(overrides)
	// A row that repeats a key (in any letter case) was decoded last-wins,
	// so the scan may not have seen an earlier typed value: the tree decode
	// redacts every object holding a repeated key, and the row is always
	// rewritten so no shadowed value is returned.
	decoded, duplicates, err := decodeExecutionPlanRowTree(row)
	if len(credentials) == 0 && len(duplicates) == 0 && err == nil {
		return row, false
	}
	if err != nil {
		decoded = nil
	}
	for _, credential := range credentials {
		replacement := scrub.Redacted
		if credential.url {
			replacement = maskURLSecrets(credential.value)
		}
		if !setExecutionPlanRowValue(decoded, credential.location, replacement) {
			// The struct saw a value the JSON walk cannot place: never
			// return the row in plaintext.
			decoded = nil
			break
		}
	}
	if decoded == nil {
		return maskExecutionPlanOverridesFallback(overrides, credentials), true
	}
	raw, err := json.Marshal(decoded)
	if err != nil {
		raw = []byte("null")
	}
	return raw, true
}

// maskExecutionPlanOverridesFallback masks a row the JSON walk could not edit
// from its decoded struct. The editor mask does not cover every field the plan
// scan reports (session identities, the JWT key), so each reported location is
// also redacted on the struct's canonical JSON; a value that still cannot be
// placed fails closed.
func maskExecutionPlanOverridesFallback(overrides *RuntimeSettingsOverride, credentials []executionPlanTypedCredential) json.RawMessage {
	raw, err := json.Marshal(maskRuntimeSettingsForEditor(overrides))
	if err != nil {
		return json.RawMessage("null")
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil {
		return json.RawMessage("null")
	}
	for _, credential := range credentials {
		replacement := scrub.Redacted
		if credential.url {
			replacement = maskURLSecrets(credential.value)
		}
		setExecutionPlanRowValue(decoded, credential.location, replacement)
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return json.RawMessage("null")
	}
	for _, credential := range credentials {
		if credential.url {
			continue
		}
		encoded, _ := json.Marshal(credential.value)
		if bytes.Contains(out, encoded[1:len(encoded)-1]) {
			return json.RawMessage("null")
		}
	}
	return out
}

// executionPlanFoldKey maps a key to one representative of its case-folding
// class (the smallest rune of each rune's fold orbit), so two keys collide
// exactly when encoding/json could match them to the same struct field.
func executionPlanFoldKey(key string) string {
	var builder strings.Builder
	builder.Grow(len(key))
	for _, r := range key {
		least := r
		for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
			if folded < least {
				least = folded
			}
		}
		builder.WriteRune(least)
	}
	return builder.String()
}

// maxExecutionPlanRowDepth matches encoding/json's nesting limit.
const maxExecutionPlanRowDepth = 10000

// decodeExecutionPlanRowTree decodes a plan entry row token by token, so a
// key repeated in the same object (exactly or in another letter case), which
// a map or struct decode silently collapses, is seen. It returns the decoded
// tree (numbers as json.Number, later exact duplicates winning as in
// encoding/json) with every object that repeats a key redacted, plus the
// sorted paths of the repeated keys. The struct scan only saw the last of
// each repeated key, so this redaction fails closed on whatever it missed.
func decodeExecutionPlanRowTree(raw json.RawMessage) (any, []string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var duplicates []string
	var parse func(prefix string, depth int) (any, error)
	parse = func(prefix string, depth int) (any, error) {
		if depth > maxExecutionPlanRowDepth {
			return nil, fmt.Errorf("runtime_overrides nests deeper than %d levels", maxExecutionPlanRowDepth)
		}
		join := func(key string) string {
			if prefix == "" {
				return key
			}
			return prefix + "." + key
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch delim := token.(type) {
		case json.Delim:
			switch delim {
			case '{':
				object := map[string]any{}
				seen := map[string]string{}
				// Keys of every repeated group, and whether any occurrence
				// of a repeated key held a scalar.
				repeatedKeys := map[string]bool{}
				members := map[string][]string{}
				scalarRepeat := false
				scalarFold := map[string]bool{}
				for decoder.More() {
					keyToken, err := decoder.Token()
					if err != nil {
						return nil, err
					}
					key, ok := keyToken.(string)
					if !ok {
						return nil, fmt.Errorf("runtime_overrides object key is not a string")
					}
					folded := executionPlanFoldKey(key)
					earlier, repeat := seen[folded]
					if repeat {
						duplicates = append(duplicates, join(earlier)+" / "+join(key))
					} else {
						seen[folded] = key
					}
					members[folded] = append(members[folded], key)
					child, err := parse(join(key), depth+1)
					if err != nil {
						return nil, err
					}
					switch child.(type) {
					case map[string]any, []any:
					default:
						scalarFold[folded] = true
					}
					if repeat {
						repeatedKeys[folded] = true
					}
					object[key] = child
				}
				if _, err := decoder.Token(); err != nil {
					return nil, err
				}
				for folded := range repeatedKeys {
					if scalarFold[folded] {
						scalarRepeat = true
					}
				}
				switch {
				case scalarRepeat:
					// A repeated scalar can change how its siblings read
					// (a header name, say): redact the whole object.
					redactExecutionPlanRowStrings(object)
				case len(repeatedKeys) > 0:
					// Repeated objects or arrays merge or replace one
					// another: redact every member of each group.
					for folded := range repeatedKeys {
						for _, key := range members[folded] {
							redactExecutionPlanRowStrings(object[key])
						}
					}
				}
				return object, nil
			case '[':
				array := []any{}
				for decoder.More() {
					child, err := parse(join(strconv.Itoa(len(array))), depth+1)
					if err != nil {
						return nil, err
					}
					array = append(array, child)
				}
				if _, err := decoder.Token(); err != nil {
					return nil, err
				}
				return array, nil
			}
			return nil, fmt.Errorf("runtime_overrides has an unexpected %q", delim)
		default:
			return token, nil
		}
	}
	tree, err := parse("", 0)
	if err != nil {
		return nil, duplicates, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, duplicates, fmt.Errorf("runtime_overrides holds trailing data")
	}
	sort.Strings(duplicates)
	return tree, duplicates, nil
}

// redactExecutionPlanRowStrings replaces every string below node that is not
// empty or a project credential reference.
func redactExecutionPlanRowStrings(node any) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if text, ok := child.(string); ok {
				if strings.TrimSpace(text) != "" && !isExecutionPlanSchemedSecretReference(text) {
					value[key] = scrub.Redacted
				}
				continue
			}
			redactExecutionPlanRowStrings(child)
		}
	case []any:
		for index, child := range value {
			if text, ok := child.(string); ok {
				if strings.TrimSpace(text) != "" && !isExecutionPlanSchemedSecretReference(text) {
					value[index] = scrub.Redacted
				}
				continue
			}
			redactExecutionPlanRowStrings(child)
		}
	}
}

// validateExecutionPlanRuntimeOverrideKeys rejects a row that repeats a key
// in one object, exactly or in another letter case: encoding/json keeps the
// last, so an earlier typed credential would be stored unchecked behind a
// later reference.
func validateExecutionPlanRuntimeOverrideKeys(raw json.RawMessage) error {
	_, duplicates, err := decodeExecutionPlanRowTree(raw)
	if err != nil {
		return fmt.Errorf("entry runtime_overrides must be a valid runtime settings object")
	}
	if len(duplicates) == 0 {
		return nil
	}
	return fmt.Errorf("entry runtime_overrides repeats a key in the same object (keys may not differ only in letter case): %s", strings.Join(duplicates, ", "))
}

// setExecutionPlanRowValue replaces the string at location in a decoded JSON
// row. Struct field keys resolve the way encoding/json reads them, an exact
// key or any case variant, and every variant is replaced, since the struct
// scan saw whichever came last. Map keys match exactly. It reports false when
// no string was found at location.
func setExecutionPlanRowValue(row any, location []any, value string) bool {
	if len(location) == 0 {
		return false
	}
	last := len(location) == 1
	var children []any
	replaced := false
	switch key := location[0].(type) {
	case string, executionPlanMapKey:
		object, ok := row.(map[string]any)
		if !ok {
			return false
		}
		name, exact := "", false
		switch typed := key.(type) {
		case string:
			name = typed
		case executionPlanMapKey:
			name, exact = string(typed), true
		}
		for candidate, child := range object {
			if candidate != name && (exact || !strings.EqualFold(candidate, name)) {
				continue
			}
			if !last {
				children = append(children, child)
				continue
			}
			if _, isString := child.(string); isString {
				object[candidate] = value
				replaced = true
			}
		}
	case int:
		array, ok := row.([]any)
		if !ok || key < 0 || key >= len(array) {
			return false
		}
		if !last {
			children = append(children, array[key])
			break
		}
		if _, isString := array[key].(string); isString {
			array[key] = value
			replaced = true
		}
	default:
		return false
	}
	for _, child := range children {
		if setExecutionPlanRowValue(child, location[1:], value) {
			replaced = true
		}
	}
	return replaced
}

func maskExecutionPlansForResponse(plans []*storage.ExecutionPlan) []*storage.ExecutionPlan {
	out := make([]*storage.ExecutionPlan, len(plans))
	for index, plan := range plans {
		out[index] = maskExecutionPlanForResponse(plan)
	}
	return out
}
