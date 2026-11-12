package dsl

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func TestConfigCatalogReflectionCoverageAndInvariants(t *testing.T) {
	groups := ConfigGroups()
	if len(groups) != 7 {
		t.Fatalf("len(ConfigGroups()) = %d, want 7", len(groups))
	}
	validGroups := make(map[string]bool, len(groups))
	for _, g := range groups {
		if g.ID == "" || g.Label == "" || g.Description == "" {
			t.Fatalf("incomplete ConfigGroupMeta: %+v", g)
		}
		if !isSentenceCaseConfigLabel(g.Label) {
			t.Errorf("group %q label %q is not sentence case", g.ID, g.Label)
		}
		validGroups[g.ID] = true
	}

	validTypes := map[string]bool{
		ConfigFieldTypeString:      true,
		ConfigFieldTypeInt:         true,
		ConfigFieldTypeFloat:       true,
		ConfigFieldTypeBoolean:     true,
		ConfigFieldTypeSelect:      true,
		ConfigFieldTypeStringList:  true,
		ConfigFieldTypeKeyValueMap: true,
		ConfigFieldTypeObjectList:  true,
		ConfigFieldTypeObject:      true,
	}
	validUnits := map[string]bool{
		"":      true,
		"ms":    true,
		"s":     true,
		"bytes": true,
		"pct":   true,
	}
	validScopes := map[string]bool{
		ConfigScopeImport:   true,
		ConfigScopeAuthor:   true,
		ConfigScopeDebug:    true,
		ConfigScopeScenario: true,
	}

	catalog := ConfigCatalog()
	if len(catalog) == 0 {
		t.Fatal("ConfigCatalog() returned empty slice")
	}

	byPath := make(map[string]ConfigFieldMeta, len(catalog))
	for _, entry := range catalog {
		if entry.Path == "" || !strings.HasPrefix(entry.Path, "config.") {
			t.Errorf("invalid catalog path %q", entry.Path)
		}
		if _, dup := byPath[entry.Path]; dup {
			t.Errorf("duplicate catalog path %q", entry.Path)
		}
		byPath[entry.Path] = entry

		if !validGroups[entry.Group] {
			t.Errorf("%s: unknown group %q", entry.Path, entry.Group)
		}
		if entry.Label == "" {
			t.Errorf("%s: empty label", entry.Path)
		} else if !isSentenceCaseConfigLabel(entry.Label) {
			t.Errorf("%s: label %q is not sentence case", entry.Path, entry.Label)
		}
		if !validTypes[entry.Type] {
			t.Errorf("%s: invalid type %q", entry.Path, entry.Type)
		}
		if entry.Type == ConfigFieldTypeSelect && len(entry.Options) == 0 {
			t.Errorf("%s: select field must declare non-empty Options", entry.Path)
		}
		if !validUnits[entry.Unit] {
			t.Errorf("%s: invalid unit %q", entry.Path, entry.Unit)
		}
		if len(entry.Protocols) == 0 {
			t.Errorf("%s: empty protocols", entry.Path)
		}
		for _, s := range entry.Scopes {
			if !validScopes[s] {
				t.Errorf("%s: invalid scope %q", entry.Path, s)
			}
		}
		if strings.HasPrefix(entry.Path, "config.jmeter.") || entry.Path == "config.sampler_schedules" {
			if slices.Contains(entry.Scopes, ConfigScopeScenario) {
				t.Errorf("%s: compatibility field must not include scenario scope", entry.Path)
			}
		}
		if lookedUp, ok := ConfigFieldForPath(entry.Path); !ok || lookedUp.Path != entry.Path {
			t.Errorf("ConfigFieldForPath(%q) failed", entry.Path)
		}
	}

	reflected := make(map[string]reflect.Type)
	collectScriptConfigPaths(t, reflect.TypeOf(ScriptConfig{}), "config", reflected)
	if len(reflected) == 0 {
		t.Fatal("reflection found 0 fields on ScriptConfig")
	}

	for path, fieldType := range reflected {
		meta, ok := byPath[path]
		if !ok {
			t.Errorf("missing ConfigCatalog() entry for ScriptConfig field %s (%v)", path, fieldType)
			continue
		}
		checkCatalogFieldTypeMatchesGoType(t, path, meta, fieldType)
	}

	for path := range byPath {
		if _, ok := reflected[path]; !ok {
			t.Errorf("ConfigCatalog() entry %s does not correspond to any exported JSON field on ScriptConfig", path)
		}
	}
}

func collectScriptConfigPaths(t *testing.T, typ reflect.Type, prefix string, out map[string]reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		jsonName, _, _ := strings.Cut(jsonTag, ",")
		jsonName = strings.TrimSpace(jsonName)
		if jsonName == "" {
			if field.Anonymous {
				collectScriptConfigPaths(t, field.Type, prefix, out)
			}
			continue
		}
		fullPath := prefix + "." + jsonName
		ft := field.Type
		elem := ft
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			collectScriptConfigPaths(t, elem, fullPath, out)
			continue
		}
		out[fullPath] = ft
	}
}

func checkCatalogFieldTypeMatchesGoType(t *testing.T, path string, meta ConfigFieldMeta, goType reflect.Type) {
	t.Helper()
	isPtr := goType.Kind() == reflect.Pointer
	elem := goType
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	switch elem.Kind() {
	case reflect.Bool:
		if meta.Type != ConfigFieldTypeBoolean {
			t.Errorf("%s: Go bool must have catalog type boolean, got %q", path, meta.Type)
		}
		if isPtr != meta.TriState {
			t.Errorf("%s: Go *bool=%v must match TriState=%v", path, isPtr, meta.TriState)
		}
	case reflect.Int, reflect.Int64:
		if meta.Type != ConfigFieldTypeInt {
			t.Errorf("%s: Go int/int64 must have catalog type int, got %q", path, meta.Type)
		}
	case reflect.Float64:
		if meta.Type != ConfigFieldTypeFloat {
			t.Errorf("%s: Go float64 must have catalog type float, got %q", path, meta.Type)
		}
	case reflect.String:
		if meta.Type != ConfigFieldTypeString && meta.Type != ConfigFieldTypeSelect {
			t.Errorf("%s: Go string must have catalog type string or select, got %q", path, meta.Type)
		}
	case reflect.Slice:
		if elem.Elem().Kind() == reflect.String {
			if meta.Type != ConfigFieldTypeStringList {
				t.Errorf("%s: Go []string must have catalog type string_list, got %q", path, meta.Type)
			}
		} else if meta.Type != ConfigFieldTypeObjectList {
			t.Errorf("%s: Go slice of structs must have catalog type object_list, got %q", path, meta.Type)
		}
	case reflect.Map:
		if elem.Elem().Kind() == reflect.String {
			if meta.Type != ConfigFieldTypeKeyValueMap {
				t.Errorf("%s: Go map[string]string must have catalog type key_value_map, got %q", path, meta.Type)
			}
		} else if meta.Type != ConfigFieldTypeObject && meta.Type != ConfigFieldTypeKeyValueMap {
			t.Errorf("%s: Go map[string]any must have catalog type object or key_value_map, got %q", path, meta.Type)
		}
	default:
		t.Errorf("%s: unexpected Go kind %v", path, elem.Kind())
	}
}

var allowedLabelAcronyms = map[string]bool{
	"URL": true, "URLs": true, "HTTP": true, "HTTP/2": true, "HTTP/3": true,
	"TLS": true, "SSL": true, "mTLS": true, "QUIC": true, "TCP": true, "UDP": true,
	"IP": true, "DNS": true, "MIME": true, "HTML": true, "XML": true, "JSON": true,
	"CSV": true, "PAC": true, "NTLM": true, "W3C": true, "SOAP": true, "WS-Security": true,
	"VUser": true, "VUsers": true, "JVM": true, "JVMs": true, "JDK": true, "JAR": true,
	"Java": true, "JavaScript": true, "gRPC": true, "RPC": true, "Protobuf": true,
	"WebSocket": true, "SQL": true, "DSN": true, "LDAP": true, "DN": true, "SMTP": true,
	"Appium": true, "WebDriver": true, "HAR": true, "SLA": true, "EPT": true,
	"Salesforce": true, "Aura": true, "JWT": true, "OAuth": true, "SASL": true,
	"SAS": true, "JMS": true, "JMSReplyTo": true, "Solace": true, "VPN": true,
	"Kafka": true, "Schema": true, "Registry": true, "Event": true, "Hubs": true,
	"Hub": true, "Blob": true, "SAP": true, "GUI": true, "JMeter": true, "Bruno": true,
	"SHA-256": true, "ID": true, "IDs": true, "PRNG": true, "AIMD": true, "TTL": true,
	"CA": true, "Alt-Svc": true, "User-Agent": true, "Accept-Encoding": true,
	"Accept-Language": true, "Content-Type": true, "Accept": true, "Origin": true,
	"Retry-After": true, "-Xbootclasspath": true, "(HTML)": true, "(DSN)": true,
	"Fault": true, "Playwright": true,
}

func isSentenceCaseConfigLabel(label string) bool {
	words := strings.Fields(label)
	if len(words) == 0 {
		return false
	}
	first := []rune(words[0])
	if !unicode.IsUpper(first[0]) && first[0] != '-' {
		return false
	}
	for _, word := range words[1:] {
		cleaned := strings.Trim(word, "(),:;\"'")
		if cleaned == "" || allowedLabelAcronyms[cleaned] || allowedLabelAcronyms[word] {
			continue
		}
		// Hyphenated tokens like "non-HTML" or "keep-alive"
		if strings.Contains(cleaned, "-") {
			parts := strings.Split(cleaned, "-")
			allValid := true
			for _, part := range parts {
				if part == "" || allowedLabelAcronyms[part] {
					continue
				}
				r := []rune(part)
				if unicode.IsUpper(r[0]) {
					allValid = false
					break
				}
			}
			if allValid {
				continue
			}
		}
		r := []rune(cleaned)
		if unicode.IsUpper(r[0]) {
			return false
		}
	}
	return true
}

func TestConfigCatalogForFilter(t *testing.T) {
	soapImport := ConfigCatalogForFilter("soap", ConfigScopeImport)
	if len(soapImport) == 0 {
		t.Fatal("expected SOAP import fields")
	}
	var sawSOAPFault, sawHTTPTLS, sawBaseURL bool
	for _, entry := range soapImport {
		switch entry.Path {
		case "config.soap.fault_handling.fail_on_fault":
			sawSOAPFault = true
		case "config.http.tls.max_version":
			sawHTTPTLS = true
		case "config.base_url":
			sawBaseURL = true
		case "config.kafka.brokers":
			t.Errorf("unexpected kafka field in soap filter: %s", entry.Path)
		case "config.bruno.bundle_ref":
			t.Errorf("unexpected retired bruno field in import scope: %s", entry.Path)
		}
	}
	if !sawSOAPFault || !sawHTTPTLS || !sawBaseURL {
		t.Fatalf("missing expected SOAP/shared HTTP fields: fault=%v tls=%v base_url=%v", sawSOAPFault, sawHTTPTLS, sawBaseURL)
	}

	scenarioFields := ConfigCatalogForFilter("", ConfigScopeScenario)
	for _, entry := range scenarioFields {
		if strings.HasPrefix(entry.Path, "config.jmeter.") || entry.Path == "config.sampler_schedules" {
			t.Errorf("unexpected non-scenario field %s in scenario filter", entry.Path)
		}
	}
}
