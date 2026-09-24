package engine

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// credentialSurfaceDisposition records, for one credential-bearing DSL field,
// whether appendPlatformManagedCompositeValues covers it and why.
//
// A non-empty surface means covered: the field is read by the named entry of
// scriptCredentialCompositeSurfaces or actionCredentialCompositeSurfaces, so a
// store-managed value in it produces a registered composite. The name is checked
// against what those functions actually emit, so this column cannot drift into
// wishful documentation.
//
// An empty surface means excluded: no composite exists for it to leak. Every
// exclusion has to name the reason, because "we did not think about it" and
// "there is nothing to protect" are indistinguishable from an empty list.
type credentialSurfaceDisposition struct {
	surface string
	reason  string
}

func covered(surface, reason string) credentialSurfaceDisposition {
	return credentialSurfaceDisposition{surface: surface, reason: reason}
}

func excluded(reason string) credentialSurfaceDisposition {
	return credentialSurfaceDisposition{reason: reason}
}

// credentialSurfaceDispositions classifies every credential-bearing field the
// walk below can reach from dsl.ScriptConfig and dsl.Action.
//
// This is the enumeration the composite mechanism is built against. Adding a
// credential-bearing field to the DSL fails TestPlatformManagedCredentialSurfaceEnumerationIsComplete
// until it is classified here, which is the point: the defect this mechanism
// replaced was a fix applied to one of two sibling surfaces, twice.
var credentialSurfaceDispositions = map[string]credentialSurfaceDisposition{
	"config.http.proxy.pac_url": excluded("PAC fetch rejects URL userinfo and never exposes raw URL or download errors; no synthesized credential composite"),
	// --- covered: HTTP Basic composites -------------------------------------
	"config.http.auth.username":  covered("config.http.auth", "basic only"),
	"config.http.auth.password":  covered("config.http.auth", "basic only"),
	"config.http.proxy.username": covered("config.http.proxy", "Proxy-Authorization"),
	"config.http.proxy.password": covered("config.http.proxy", "Proxy-Authorization"),
	"config.browser.http_credentials.username": covered(
		"config.browser.http_credentials", "Playwright HTTP credentials"),
	"config.browser.http_credentials.password": covered(
		"config.browser.http_credentials", "Playwright HTTP credentials"),
	"config.browser.proxy.username": covered("config.browser.proxy", "browser proxy auth"),
	"config.browser.proxy.password": covered("config.browser.proxy", "browser proxy auth"),
	"action.auth.username":          covered("action.auth", "basic only"),
	"action.auth.password":          covered("action.auth", "basic only"),

	// --- covered: URL userinfo ----------------------------------------------
	"config.base_url": covered("config.base_url",
		"prefixed onto relative targets; net/http synthesises Basic from userinfo"),
	"config.http.proxy.url": covered("config.http.proxy.url",
		"http.ProxyURL userinfo -> Proxy-Authorization"),
	"config.browser.proxy.server": covered("config.browser.proxy.server",
		"Playwright proxy server userinfo"),
	"config.kafka.oauth.client_secret_ref": covered("config.kafka.oauth", "OAuth Basic credential pair"),
	"config.kafka.oauth.token_url":         excluded("runtime rejects endpoint userinfo; token endpoint never synthesizes Basic from a URL"),
	"config.kafka.schema_registry_url": covered("config.kafka.schema_registry_url",
		"dialed by a net/http client in pkg/protocol/kafka"),
	"action.target": covered("action.target",
		"net/http synthesises Basic from userinfo; the surface this file was written for"),

	// --- covered: connection string -----------------------------------------
	"config.database.dsn": covered("config.database.dsn",
		"collectDSN spellings of a store-managed DSN"),
	"config.database.dsn_secret": covered("config.database.dsn_secret",
		"collectDSN spellings; dedicated logical-secret field"),

	// --- excluded: bare value on the wire, already covered by the base matcher
	"config.http.auth.token": excluded(
		"bearer sends the token whole; snapshotReferencedSecrets already registered it"),
	"action.auth.token": excluded(
		"bearer sends the token whole; snapshotReferencedSecrets already registered it"),
	"config.soap.ws_security.username_token.username": excluded(
		"PasswordText sends the bare password; PasswordDigest sends a keyed hash no exact-value matcher can cover"),
	"config.soap.ws_security.username_token.password": excluded(
		"PasswordText sends the bare password; PasswordDigest sends a keyed hash no exact-value matcher can cover"),
	"config.soap.ws_security.username_token.password_type": excluded(
		"selects text vs digest; carries no credential"),

	// --- excluded: no wire form reachable from this matcher -----------------
	"config.network.username": excluded(
		"read into pkg/protocol/network at network.go:75-76 and never used"),
	"config.network.password": excluded(
		"read into pkg/protocol/network at network.go:75-76 and never used"),
	"config.network.address": excluded(
		"host:port for a raw socket; no userinfo grammar and no HTTP dialer"),
	"config.jms.broker_url": excluded(
		"dialed by a JMS/AMQP adapter, not an HTTP client; no userinfo -> Basic synthesis"),
	"config.jms.username": excluded(
		"broker session credential; no concatenated spelling is retained in evidence"),
	"config.jms.password": excluded(
		"broker session credential; no concatenated spelling is retained in evidence"),
	"config.jms.tls.ca_cert_secret": excluded("names a TLS trust bundle, not a credential pair"),
	"config.kafka.sasl_username": excluded(
		"SASL handshake credential; no concatenated spelling is retained in evidence"),
	"config.kafka.sasl_password": excluded(
		"SASL handshake credential; no concatenated spelling is retained in evidence"),
	"config.kafka.sasl_password_secret": excluded(
		"SASL handshake credential; no concatenated spelling is retained in evidence"),
	"config.eventhubs.connection_string": excluded(
		"AMQP SAS connection string; the whole value is one secret, registered bare"),
	"config.eventhubs.connection_string_secret": excluded(
		"AMQP SAS connection string; the whole value is one secret, registered bare"),
	"config.sapgui.connection_string": excluded("SAP GUI connection string; no HTTP dialer"),
	"config.sapgui.username":          excluded("SAP GUI logon credential; no HTTP dialer"),
	"config.sapgui.password":          excluded("SAP GUI logon credential; no HTTP dialer"),
	"config.observability.external_metrics.url": excluded(
		"polled by the API server (pkg/api/external_metrics.go); never enters buildProtoConfig or this matcher"),
}

// credentialSurfaceFieldTriggers are the json-name tokens that make a string
// field credential-bearing for the purposes of the walk. They are matched
// against whole underscore-separated tokens so "user_agent" and
// "browser_version" do not masquerade as credentials.
var credentialSurfaceFieldTriggers = map[string]struct{}{
	"password": {}, "passwords": {}, "secret": {}, "secrets": {},
	"credential": {}, "credentials": {}, "username": {}, "usernames": {},
	"token": {}, "tokens": {}, "dsn": {}, "connection": {},
	"url": {}, "urls": {}, "uri": {}, "server": {}, "address": {},
	"endpoint": {}, "target": {},
}

// TestPlatformManagedCredentialSurfaceEnumerationIsComplete fails when a
// credential-bearing field is added to dsl.ScriptConfig or dsl.Action without a
// disposition.
//
// It cannot prove that a covered field is registered correctly — the replay
// tests do that — but it does prevent the failure mode this whole file exists
// for: a new sibling surface appearing beside a covered one and nobody noticing
// until a leak is reported.
func TestPlatformManagedCredentialSurfaceEnumerationIsComplete(t *testing.T) {
	found := map[string]struct{}{}
	for _, root := range []struct {
		prefix string
		typ    reflect.Type
	}{
		{"config", reflect.TypeOf(dsl.ScriptConfig{})},
		{"action", reflect.TypeOf(dsl.Action{})},
	} {
		walkCredentialSurfaceFields(root.prefix, root.typ, map[reflect.Type]struct{}{}, found)
	}

	var unclassified []string
	for path := range found {
		if _, ok := credentialSurfaceDispositions[path]; !ok {
			unclassified = append(unclassified, path)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("credential-bearing DSL fields with no disposition: %v\n"+
			"Classify each in credentialSurfaceDispositions: covered(...) if "+
			"scriptCredentialCompositeSurfaces / actionCredentialCompositeSurfaces "+
			"registers its composite, excluded(...) with the reason no composite can leak.",
			unclassified)
	}

	var stale []string
	for path := range credentialSurfaceDispositions {
		if _, ok := found[path]; !ok {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("credentialSurfaceDispositions names fields the DSL no longer has: %v", stale)
	}

	for path, disposition := range credentialSurfaceDispositions {
		if strings.TrimSpace(disposition.reason) == "" {
			t.Errorf("%s has an empty disposition reason", path)
		}
	}
}

// TestCredentialSurfaceDispositionsMatchTheEmittedSurfaces closes the loop in
// both directions, so neither side can drift into fiction:
//
//   - every field marked covered names a surface the enumerations really emit,
//     which fails if a surface is deleted or renamed while its fields keep
//     claiming coverage;
//   - every surface the enumerations emit is claimed by at least one field,
//     which fails if a surface is added without recording which fields it
//     protects.
//
// action.params.token_url is the one covered surface outside this loop: it lives
// in a map[string]any, so no struct walk reaches it and no enumeration function
// emits it. TestOAuth2TokenURLUserinfoCompositeIsRegistered covers it
// behaviourally instead.
func TestCredentialSurfaceDispositionsMatchTheEmittedSurfaces(t *testing.T) {
	emitted := emittedCredentialSurfaceNames()
	claimed := map[string]struct{}{}
	for path, disposition := range credentialSurfaceDispositions {
		if disposition.surface == "" {
			continue
		}
		if _, ok := emitted[disposition.surface]; !ok {
			t.Errorf("%s claims surface %q, which no enumeration emits", path, disposition.surface)
			continue
		}
		claimed[disposition.surface] = struct{}{}
	}

	var unclaimed []string
	for surface := range emitted {
		if _, ok := claimed[surface]; !ok {
			unclaimed = append(unclaimed, surface)
		}
	}
	sort.Strings(unclaimed)
	if len(unclaimed) > 0 {
		t.Fatalf("surfaces emitted but claimed by no classified field: %v\n"+
			"Record which DSL fields each one protects in credentialSurfaceDispositions.",
			unclaimed)
	}
}

// emittedCredentialSurfaceNames drives both enumerations with fixtures that
// populate EVERY field in credentialSurfaceDispositions — the excluded ones
// included, and both mutually exclusive database spellings — and returns the
// surface names they produce.
//
// Populating the excluded fields is what gives the "unclaimed" half of
// TestCredentialSurfaceDispositionsMatchTheEmittedSurfaces teeth: an author who
// starts covering, say, config.jms.broker_url gets a surface out of these
// fixtures and has to move that field's disposition from excluded to covered.
// With a sparse fixture the new surface would simply not appear and the check
// would pass on silence.
func emittedCredentialSurfaceNames() map[string]struct{} {
	names := map[string]struct{}{}
	add := func(surfaces []platformManagedCredentialSurface) {
		for _, surface := range surfaces {
			names[surface.name] = struct{}{}
		}
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) { return "pw", name == "P" })
	fullConfig := func(database dsl.DatabaseConfig) dsl.ScriptConfig {
		return dsl.ScriptConfig{
			BaseURL: "https://u:{{secret.P}}@base.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{
					Type: "basic", Username: "u", Password: "{{secret.P}}", Token: "{{secret.P}}",
				},
				Proxy: dsl.ProxyConfig{
					URL: "http://u:{{secret.P}}@proxy.test:8080", Username: "u", Password: "{{secret.P}}",
				},
			},
			SOAP: dsl.SOAPConfig{WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "u", Password: "{{secret.P}}", PasswordType: "text",
				},
			}},
			Network: dsl.NetworkConfig{
				Address: "u:{{secret.P}}@net.test:1234", Username: "u", Password: "{{secret.P}}",
			},
			Browser: dsl.BrowserConfig{
				HTTPCredentials: dsl.BrowserHTTPCredentials{Username: "u", Password: "{{secret.P}}"},
				Proxy: dsl.BrowserProxyConfig{
					Server: "http://u:{{secret.P}}@bproxy.test:8080", Username: "u", Password: "{{secret.P}}",
				},
			},
			SAPGUI: dsl.SAPGUIConfig{
				ConnectionString: "/H/sap.test/S/3200", Username: "u", Password: "{{secret.P}}",
			},
			JMS: dsl.JMSConfig{
				BrokerURL: "tcp://u:{{secret.P}}@broker.test:55555",
				Username:  "u", Password: "{{secret.P}}",
				TLS: &dsl.JMSTLSConfig{CACertSecret: "JMS_CA"},
			},
			Kafka: dsl.KafkaConfig{
				OAuth:             &dsl.KafkaOAuthConfig{ClientID: "u", ClientSecretRef: "{{secret.P}}", AuthenticationMethod: "client_secret_basic"},
				SchemaRegistryURL: "https://u:{{secret.P}}@registry.test",
				SASLUsername:      "u", SASLPassword: "{{secret.P}}", SASLPasswordSecret: "KAFKA_PASSWORD",
			},
			EventHubs: dsl.EventHubsConfig{
				ConnectionString:       "Endpoint=sb://eh.test/;SharedAccessKey={{secret.P}}",
				ConnectionStringSecret: "EVENTHUBS_CONNECTION",
			},
			Observability: dsl.ObservabilityConfig{ExternalMetrics: []dsl.ExternalMetricConfig{
				{Name: "external", URL: "https://u:{{secret.P}}@metrics.test/api"},
			}},
			Database: database,
		}
	}
	for _, database := range []dsl.DatabaseConfig{
		{DSN: "postgres://u:{{secret.P}}@db.test/app"},
		{DSNSecret: "DATABASE_DSN"},
	} {
		script := &dsl.Script{Protocol: "http", Config: fullConfig(database)}
		add(scriptCredentialCompositeSurfaces(script, buildProtoConfigWithSecretResolver(script, resolver)))
	}
	add(actionCredentialCompositeSurfaces(
		&dsl.Action{
			Target: "https://u:{{secret.P}}@api.test/x",
			Auth: &dsl.AuthConfig{
				Type: "basic", Username: "u", Password: "{{secret.P}}", Token: "{{secret.P}}",
			},
		},
		&protocol.Action{
			Target: "https://u:pw@api.test/x",
			Auth: &protocol.AuthConfig{
				Type: "basic", Username: "u", Password: "pw", Token: "pw",
			},
		},
	))
	return names
}

func walkCredentialSurfaceFields(
	prefix string,
	typ reflect.Type,
	visiting map[reflect.Type]struct{},
	found map[string]struct{},
) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
		typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	if _, cycle := visiting[typ]; cycle {
		return
	}
	visiting[typ] = struct{}{}
	defer delete(visiting, typ)

	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			name = strings.ToLower(field.Name)
		}
		path := prefix + "." + name
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.String {
			for _, token := range strings.Split(name, "_") {
				if _, trigger := credentialSurfaceFieldTriggers[token]; trigger {
					found[path] = struct{}{}
					break
				}
			}
			continue
		}
		walkCredentialSurfaceFields(path, fieldType, visiting, found)
	}
}

// TestCredentialSurfaceTriggerTokensRecognizeAPassword guards the guard: if the
// trigger list stopped matching, the enumeration test above would pass by
// finding nothing at all.
func TestCredentialSurfaceTriggerTokensRecognizeAPassword(t *testing.T) {
	found := map[string]struct{}{}
	walkCredentialSurfaceFields("config", reflect.TypeOf(dsl.ScriptConfig{}),
		map[reflect.Type]struct{}{}, found)
	for _, required := range []string{
		"config.http.auth.password",
		"config.http.proxy.url",
		"config.database.dsn",
		"config.browser.http_credentials.password",
	} {
		if _, ok := found[required]; !ok {
			t.Fatalf("walk no longer finds %s; the enumeration guard is inert", required)
		}
	}
}

// TestActionCredentialCompositeSurfacesCoversTargetUserinfo pins the two
// per-action surfaces to the same origin rule, at the level of the surface list
// rather than end to end, so a regression names the surface that lost coverage.
func TestActionCredentialCompositeSurfacesCoversTargetUserinfo(t *testing.T) {
	authored := &dsl.Action{
		Target: "https://usr:{{secret.BASIC_PASSWORD}}@api.example.test/x",
		Auth:   &dsl.AuthConfig{Type: "basic", Username: "a", Password: "{{ secret.BASIC_PASSWORD }}"},
	}
	resolved := &protocol.Action{
		Target: "https://usr:b@api.example.test/x",
		Auth:   &protocol.AuthConfig{Type: "basic", Username: "a", Password: "b"},
	}
	names := make([]string, 0, 2)
	for _, surface := range actionCredentialCompositeSurfaces(authored, resolved) {
		names = append(names, surface.name)
	}
	sort.Strings(names)
	if want := []string{"action.auth", "action.target"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("actionCredentialCompositeSurfaces names = %v, want %v", names, want)
	}

	values := runtimeActionCompositeValues(authored, resolved)
	for _, want := range []string{"a:b", "YTpi", "Basic YTpi", "usr:b", "dXNyOmI=", "Basic dXNyOmI="} {
		if !containsString(values, want) {
			t.Fatalf("runtimeActionCompositeValues = %v, missing %q", values, want)
		}
	}
}

// TestActionCredentialCompositeSurfacesLeavesScriptLiteralsVisible is the other
// direction of the owner's rule, at the same level: a target whose userinfo is
// written into the script registers nothing, because the operator reads it.
func TestActionCredentialCompositeSurfacesLeavesScriptLiteralsVisible(t *testing.T) {
	authored := &dsl.Action{Target: "https://usr:b@api.example.test/x"}
	resolved := &protocol.Action{Target: "https://usr:b@api.example.test/x"}
	if values := runtimeActionCompositeValues(authored, resolved); len(values) != 0 {
		t.Fatalf("script-literal userinfo registered %v; the operator already reads it", values)
	}
}

// TestSplitURLUserinfoReadsTemplatedAuthority covers the reason the origin test
// cannot use net/url: the authored spelling is the only place origin survives,
// and net/url rejects it outright.
func TestSplitURLUserinfoReadsTemplatedAuthority(t *testing.T) {
	for _, testCase := range []struct {
		raw      string
		username string
		password string
		ok       bool
	}{
		{raw: "https://usr:{{secret.P}}@host/x", username: "usr", password: "{{secret.P}}", ok: true},
		{raw: "https://usr:{{ secret.P }}@host/x", username: "usr", password: "{{ secret.P }}", ok: true},
		{raw: "https://{{secret.U}}:{{secret.P}}@host/x", username: "{{secret.U}}", password: "{{secret.P}}", ok: true},
		{raw: "//usr:pwd@host/x", username: "usr", password: "pwd", ok: true},
		// A password containing '@' still ends at the LAST '@' of the authority,
		// matching net/url.parseAuthority.
		{raw: "https://usr:p@ss@host/x", username: "usr", password: "p@ss", ok: true},
		{raw: "https://usr@host/x"},
		{raw: "https://host/x"},
		{raw: "/relative/path"},
		{raw: ""},
		// A path or query '@' is not userinfo.
		{raw: "https://host/x?to=a@b.test"},
	} {
		username, password, ok := splitURLUserinfo(testCase.raw)
		if ok != testCase.ok || username != testCase.username || password != testCase.password {
			t.Errorf("splitURLUserinfo(%q) = (%q, %q, %v), want (%q, %q, %v)",
				testCase.raw, username, password, ok,
				testCase.username, testCase.password, testCase.ok)
		}
	}
}

// TestScriptCredentialCompositeSurfacesCoversEveryConfigSurface asserts the
// config-level enumeration by name, so a surface silently dropped from the list
// fails here instead of in a leak report.
func TestScriptCredentialCompositeSurfacesCoversEveryConfigSurface(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://u:{{secret.P}}@base.test",
			HTTP: dsl.HTTPConfig{
				Auth:  dsl.AuthConfig{Type: "basic", Username: "u", Password: "{{secret.P}}"},
				Proxy: dsl.ProxyConfig{URL: "http://u:{{secret.P}}@proxy.test:8080", Username: "u", Password: "{{secret.P}}"},
			},
			Browser: dsl.BrowserConfig{
				HTTPCredentials: dsl.BrowserHTTPCredentials{Username: "u", Password: "{{secret.P}}"},
				Proxy:           dsl.BrowserProxyConfig{Server: "http://u:{{secret.P}}@bproxy.test:8080", Username: "u", Password: "{{secret.P}}"},
			},
			Kafka:    dsl.KafkaConfig{SchemaRegistryURL: "https://u:{{secret.P}}@registry.test"},
			Database: dsl.DatabaseConfig{DSN: "postgres://u:{{secret.P}}@db.test/app"},
		},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) { return "pw", name == "P" })
	config := buildProtoConfigWithSecretResolver(script, resolver)

	names := make([]string, 0, 9)
	for _, surface := range scriptCredentialCompositeSurfaces(script, config) {
		names = append(names, surface.name)
	}
	sort.Strings(names)
	want := []string{
		"config.base_url",
		"config.browser.http_credentials",
		"config.browser.proxy",
		"config.browser.proxy.server",
		"config.database.dsn",
		"config.http.auth",
		"config.http.proxy",
		"config.http.proxy.url",
		"config.kafka.schema_registry_url",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("scriptCredentialCompositeSurfaces names = %v, want %v", names, want)
	}

	values := runtimeProtocolAuthCompositeValues(script, resolver)
	if !containsString(values, "u:pw") || !containsString(values, "dTpwdw==") {
		t.Fatalf("runtimeProtocolAuthCompositeValues = %v, missing the u:pw composite", values)
	}
}

// TestOAuth2TokenURLUserinfoCompositeIsRegistered covers the third registration
// site. handleHTTPOAuth2TokenAction never builds a protocol.Action, so it is the
// one surface the resolved-action path cannot reach.
func TestOAuth2TokenURLUserinfoCompositeIsRegistered(t *testing.T) {
	const encodedCredentials = "dXNyOmI=" // base64("usr:b")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"neutral-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()

	vu := &VUser{
		Script: &dsl.Script{Protocol: "http"},
		SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
			return "b", name == "BASIC_PASSWORD"
		}),
	}
	tokenURL := strings.Replace(server.URL, "http://", "http://usr:{{secret.BASIC_PASSWORD}}@", 1) + "/token"
	if err := vu.handleHTTPOAuth2TokenAction(context.Background(), dsl.Action{
		Name: "token", Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     tokenURL,
			"grant_type":    "client_credentials",
			"client_id":     "id",
			"client_secret": "shh",
			"save_as":       "handle",
		},
	}); err != nil {
		t.Fatalf("handleHTTPOAuth2TokenAction: %v", err)
	}
	if got := scrubSensitiveTextWithConfig(
		"evidence "+encodedCredentials, vu.RedactionConfig,
	); strings.Contains(got, encodedCredentials) {
		t.Fatalf("token_url userinfo composite survived: %q", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestKafkaOAuthCredentialComposites(t *testing.T) {
	for _, method := range []string{"client_secret_basic", "kafka_3_3_1_basic"} {
		t.Run(method, func(t *testing.T) {
			script := &dsl.Script{Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{OAuth: &dsl.KafkaOAuthConfig{ClientID: " client+id ", ClientSecretRef: "{{secret.oauth}}", AuthenticationMethod: method}}}}
			resolver := SecretResolverFunc(func(name string) (string, bool) { return " secret/? ", name == "oauth" })
			values := runtimeProtocolAuthCompositeValues(script, resolver)
			pair := url.QueryEscape(" client+id ") + ":" + url.QueryEscape(" secret/? ")
			encoding := base64.StdEncoding
			if method == "kafka_3_3_1_basic" {
				pair = "client+id:secret/?"
				encoding = base64.URLEncoding
			}
			if !containsString(values, encoding.EncodeToString([]byte(pair))) {
				t.Fatal("missing encoded OAuth credential composite")
			}
		})
	}
}

func TestMissingKafkaOAuthLogicalSecretNeverBecomesLiteral(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{OAuth: &dsl.KafkaOAuthConfig{ClientSecretRef: "missing_oauth"}}}}
	for _, resolver := range []SecretResolver{nil, SecretResolverFunc(func(string) (string, bool) { return "", false })} {
		cfg := buildProtoConfigWithSecretResolver(script, resolver)
		if cfg["kafka_oauth_client_secret"] != "{{secret.missing_oauth}}" {
			t.Fatal("unresolved logical name became a credential")
		}
	}
}
