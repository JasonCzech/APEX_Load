package engine

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// Platform-managed credential composites — the single origin gate.
//
// A "composite" is a wire spelling that a transport builds by CONCATENATING a
// credential with other material before encoding it: base64(username + ":" +
// password) for HTTP Basic, and the whole connection string for a database DSN.
// The distinction matters because the exact-value matcher already expands every
// registered value through the reversible encodings it knows
// (runtimeSecretRedactionVariants: percent, base64, hex, JSON, XML/HTML). A wire
// form that encodes a secret ALONE is therefore already covered by the bare
// value snapshotReferencedSecrets registered from {{secret.NAME}}. A
// concatenation is not: base64 of a pair is not derivable from base64 of either
// half, so the composite needs a registration of its own.
//
// Registration is gated on ORIGIN, and origin survives only on the AUTHORED
// field — by the time execution sees a value, {{secret.NAME}} has already been
// replaced by what the project secret store returned. Only platform-managed
// material qualifies. A literal credential authored or imported into the script
// is script-sourced: the operator already reads it in their own script, so
// blanking it out of their own run evidence protects nothing and destroys the
// evidence.
//
// Every surface in the product that can carry such a composite is enumerated
// in exactly two places, split by WHEN the resolved value exists and by nothing
// else:
//
//   - scriptCredentialCompositeSurfaces — script-config fields, resolved once at
//     engine/replay/shakeout/group construction (runtimeProtocolAuthCompositeValues).
//   - actionCredentialCompositeSurfaces — per-action fields, resolved only after
//     resolveAction interpolates them (runtimeActionCompositeValues).
//
// A third caller, handleHTTPOAuth2TokenAction, registers one surface built by
// the same helper because it interpolates its own params and never produces a
// protocol.Action.
//
// All of them funnel through appendPlatformManagedCompositeValues, which is the
// only place the origin test is applied and the only place a composite spelling
// is derived. platform_managed_credential_composites_test.go fails if a
// credential-bearing field is added to dsl.ScriptConfig or dsl.Action without
// being classified as covered or explicitly excluded.

// platformManagedCredentialSurface is one credential-bearing field pair: the
// script's own spelling of each half beside what interpolation produced for it.
//
// The authored halves exist purely for the origin test; the resolved halves are
// the only thing that can ever be registered.
type platformManagedCredentialSurface struct {
	// name identifies the surface in coverage tests and failure messages. It is
	// never part of the redaction matcher.
	name string

	authoredUsername string
	authoredPassword string
	resolvedUsername string
	resolvedPassword string

	// basic marks a surface whose transport sends base64(username+":"+password).
	// Only Basic auth does. A bearer token is sent whole, so the bare value
	// snapshotReferencedSecrets registered already covers it. Digest — which the
	// OpenAPI importer emits for an `http`/`digest` security scheme — sends
	// response="<MD5 over username:realm:password:nonce:...>", a keyed hash that
	// no exact-value matcher can cover by construction; pkg/protocol/http
	// substitutes "Digest [REDACTED]" for the whole header in its own snapshots
	// instead (buildRedactedActionAuthHeader).
	basic       bool
	oauthMethod string

	// connectionString marks a surface whose resolvedPassword is a whole
	// credential-bearing connection string rather than one half of a pair. Its
	// spellings come from collectDSN, which understands DSN, JDBC and
	// key=value forms that a user/password pair cannot express.
	connectionString bool
	storageState     bool

	// dedicatedSecretField marks an authored field that NAMES a project secret
	// (config.database.dsn_secret) instead of embedding a {{secret.NAME}}
	// template in a value field. Origin for those fields is the field itself,
	// so any non-empty content is treated as store-managed.
	//
	// The engine does not verify that the content really is a logical secret
	// name: pkg/api/script_projection.go and pkg/storage/import_batch_credentials.go
	// validate it at the server boundaries, but the engine/CLI/library boundary
	// does not re-check. A literal parked there is therefore treated as
	// store-managed and over-masked, which errs toward protection.
	dedicatedSecretField bool
}

// usernameFromSecretStore and passwordFromSecretStore are the origin test. They
// are the ONLY predicate any surface is allowed to use, so a new surface cannot
// accidentally get a different rule than the ones already covered.
func (surface platformManagedCredentialSurface) usernameFromSecretStore() bool {
	return surface.authoredDefersToSecretStore(surface.authoredUsername)
}

func (surface platformManagedCredentialSurface) passwordFromSecretStore() bool {
	return surface.authoredDefersToSecretStore(surface.authoredPassword)
}

func (surface platformManagedCredentialSurface) authoredDefersToSecretStore(authored string) bool {
	if surface.dedicatedSecretField {
		return strings.TrimSpace(authored) != ""
	}
	return containsPlatformSecretReference(authored)
}

// appendPlatformManagedCompositeValues applies the origin test to each surface
// and appends the spellings the exact-value matcher cannot derive on its own.
//
// A surface whose halves are both script literals contributes nothing: the
// operator reads them in their own script. A surface with either half resolving
// from the project secret store contributes the composite, because the composite
// embeds a value the operator does not otherwise hold.
func appendPlatformManagedCompositeValues(
	values []string,
	surfaces ...platformManagedCredentialSurface,
) []string {
	for _, surface := range surfaces {
		secretUsername := surface.usernameFromSecretStore()
		secretPassword := surface.passwordFromSecretStore()
		if !secretUsername && !secretPassword {
			continue
		}
		if surface.storageState {
			values = append(values, browserprotocol.StorageStateSecretValues(surface.resolvedPassword)...)
			continue
		}
		if surface.connectionString {
			resolved := strings.TrimSpace(surface.resolvedPassword)
			if resolved == "" {
				continue
			}
			collector := importedCredentialCollector{
				resolved: true,
				seen:     make(map[string]struct{}),
			}
			collector.collectDSN(resolved)
			values = append(values, collector.values...)
			continue
		}
		if surface.resolvedUsername == "" && surface.resolvedPassword == "" {
			continue
		}
		if secretPassword && surface.resolvedPassword != "" {
			// Redundant with snapshotReferencedSecrets for a bare
			// {{secret.NAME}} field, and filterRuntimeCredentialBaseValues drops
			// the duplicate. It is not redundant when the store value arrives
			// inside a larger field — a whole URL from one {{secret.NAME}} — in
			// which case only the enclosing string was registered.
			values = append(values, surface.resolvedPassword)
		}
		if !surface.basic {
			continue
		}
		pair := surface.resolvedUsername + ":" + surface.resolvedPassword
		if surface.oauthMethod == "client_secret_basic" {
			pair = url.QueryEscape(surface.resolvedUsername) + ":" + url.QueryEscape(surface.resolvedPassword)
		} else if surface.oauthMethod == "kafka_3_3_1_basic" {
			pair = strings.TrimFunc(surface.resolvedUsername, func(r rune) bool { return r <= 0x20 }) + ":" + strings.TrimFunc(surface.resolvedPassword, func(r rune) bool { return r <= 0x20 })
			values = append(values, base64.URLEncoding.EncodeToString([]byte(pair)))
		}
		payload := base64.StdEncoding.EncodeToString([]byte(pair))
		values = append(values, pair, payload, "Basic "+payload)
	}
	return values
}

// urlUserinfoCredentialSurface projects a URL-valued field onto the same
// surface as an explicit auth block.
//
// Nothing in this engine writes an Authorization header for
// https://user:password@host/path: net/http synthesises "Basic
// base64(user:password)" inside Client.send, and the proxy transport does the
// same for Proxy-Authorization. URL userinfo is therefore an auth surface with
// no auth block, and it needs the identical composite registration.
//
// authored is the script's own spelling, which is why the userinfo is split
// lexically rather than by net/url — see splitURLUserinfo.
func urlUserinfoCredentialSurface(name, authored, resolved string) (platformManagedCredentialSurface, bool) {
	parsed, err := url.Parse(strings.TrimSpace(resolved))
	if err != nil || parsed.User == nil {
		// A resolved URL net/url rejects is a URL no transport will dial, so no
		// composite can reach the wire from it.
		return platformManagedCredentialSurface{}, false
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		// Without a password half there is no pair, and a bare username is
		// intentionally not a secret.
		return platformManagedCredentialSurface{}, false
	}
	surface := platformManagedCredentialSurface{
		name: name,
		// url.Userinfo reports the decoded halves, and those decoded halves are
		// exactly what net/http base64s.
		resolvedUsername: parsed.User.Username(),
		resolvedPassword: password,
		basic:            true,
	}
	authoredUsername, authoredPassword, ok := splitURLUserinfo(authored)
	if !ok {
		// The authored URL carries no literal userinfo of its own, so whatever
		// userinfo interpolation produced came out of a template spanning the
		// whole field. Test the whole field for origin: if it defers to the
		// secret store the credential is store-managed, and if it does not, the
		// userinfo came from script-sourced material and stays visible.
		authoredUsername, authoredPassword = authored, authored
	}
	surface.authoredUsername = authoredUsername
	surface.authoredPassword = authoredPassword
	return surface, true
}

// splitURLUserinfo reads the userinfo halves out of a URL that may still carry
// unresolved {{...}} templates.
//
// net/url refuses those outright — url.Parse of
// "https://usr:{{secret.P}}@host/x" fails with "invalid userinfo" because '{'
// is not a legal userinfo byte — and the authored spelling is the only place a
// credential's origin can still be read. The split therefore mirrors
// net/url.parseAuthority by hand: the userinfo ends at the LAST '@' in the
// authority, and its halves are separated by the FIRST ':'.
func splitURLUserinfo(raw string) (string, string, bool) {
	authority := strings.TrimSpace(raw)
	if scheme := strings.Index(authority, "://"); scheme >= 0 {
		authority = authority[scheme+len("://"):]
	} else if strings.HasPrefix(authority, "//") {
		authority = authority[len("//"):]
	} else {
		return "", "", false
	}
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return "", "", false
	}
	username, password, found := strings.Cut(authority[:at], ":")
	if !found {
		return "", "", false
	}
	return username, password, true
}

// scriptCredentialCompositeSurfaces enumerates every script-config field that
// can carry a platform-managed credential composite onto the wire.
//
// config is the proto config the same script produced, which is where the
// resolved half of each surface lives.
//
// Deliberately NOT listed, because no composite exists for them to leak. The
// field-by-field version of this list, which a test enforces, is
// credentialSurfaceDispositions in the _test file:
//   - config.soap.ws_security.username_token — PasswordText sends the bare
//     password (already covered by snapshotReferencedSecrets); PasswordDigest
//     sends base64(sha1(nonce+created+password)), a derived proof no
//     exact-value matcher can cover by construction.
//   - config.http.auth with type digest/bearer/ntlm — same: a derived proof or
//     a bare token. pkg/protocol/http replaces the whole header with
//     "<scheme> [REDACTED]" in its own snapshots.
//   - config.network.username/password — read into pkg/protocol/network at
//     network.go:75-76 and never used; there is no wire form.
//   - config.jms.broker_url — a URL, but dialed by a JMS/AMQP adapter rather
//     than an HTTP client, so nothing synthesises a Basic header from its
//     userinfo. Same for config.jms/kafka(sasl_*)/eventhubs/sapgui credentials:
//     broker and session material whose bare values snapshotReferencedSecrets
//     registers and whose concatenated spellings never enter run evidence.
//   - config.eventhubs.checkpoint_container_url — processor validation and
//     identitypolicy reject userinfo and query credentials before Blob client
//     construction, so it cannot synthesize a Basic composite.
//   - config.observability.external_metrics[].url — polled by the API server,
//     never part of buildProtoConfig, so it is outside this matcher entirely.
//   - config.bruno.secret_bindings — names bound inside an opaque Bruno bundle.
//     The composite, if any, is built by the Bruno runner from bundle content
//     this engine never sees, so it cannot be derived here.
func scriptCredentialCompositeSurfaces(
	script *dsl.Script,
	config map[string]any,
) []platformManagedCredentialSurface {
	if script == nil {
		return nil
	}
	surfaces := make([]platformManagedCredentialSurface, 0, 8)
	appendPair := func(name, authoredUsername, authoredPassword, usernameKey, passwordKey string, enabled bool) {
		if !enabled {
			return
		}
		surfaces = append(surfaces, platformManagedCredentialSurface{
			name:             name,
			authoredUsername: authoredUsername,
			authoredPassword: authoredPassword,
			resolvedUsername: protocol.ConfigString(config, usernameKey),
			resolvedPassword: protocol.ConfigString(config, passwordKey),
			basic:            true,
		})
	}
	appendURL := func(name, authored, resolvedKey string) {
		if surface, ok := urlUserinfoCredentialSurface(
			name, authored, protocol.ConfigString(config, resolvedKey),
		); ok {
			surfaces = append(surfaces, surface)
		}
	}

	if oauth := script.Config.Kafka.OAuth; oauth != nil {
		clientID, _ := config["kafka_oauth_client_id"].(string)
		clientSecret, _ := config["kafka_oauth_client_secret"].(string)
		surfaces = append(surfaces, platformManagedCredentialSurface{
			name: "config.kafka.oauth", authoredUsername: oauth.ClientID, authoredPassword: oauth.ClientSecretRef,
			resolvedUsername: clientID,
			resolvedPassword: clientSecret,
			basic:            oauth.AuthenticationMethod == "client_secret_basic" || oauth.AuthenticationMethod == "kafka_3_3_1_basic",
			oauthMethod:      oauth.AuthenticationMethod, dedicatedSecretField: true,
		})
	}
	httpAuth := script.Config.HTTP.Auth
	httpProxy := script.Config.HTTP.Proxy
	surfaces = append(surfaces, platformManagedCredentialSurface{
		name: "config.browser.storage_state", authoredPassword: script.Config.Browser.StorageState,
		resolvedPassword: protocol.ConfigString(config, "browser_storage_state"), storageState: true,
	})
	browserCredentials := script.Config.Browser.HTTPCredentials
	browserProxy := script.Config.Browser.Proxy

	appendPair("config.http.auth", httpAuth.Username, httpAuth.Password,
		"auth_username", "auth_password",
		strings.EqualFold(strings.TrimSpace(protocol.ConfigString(config, "auth_type")), "basic"))
	appendPair("config.http.proxy", httpProxy.Username, httpProxy.Password,
		"proxy_username", "proxy_password",
		protocol.ConfigString(config, "proxy_username") != "" ||
			protocol.ConfigString(config, "proxy_password") != "")
	appendPair("config.browser.http_credentials",
		browserCredentials.Username, browserCredentials.Password,
		"browser_http_credentials_username", "browser_http_credentials_password",
		protocol.ConfigString(config, "browser_http_credentials_username") != "" ||
			protocol.ConfigString(config, "browser_http_credentials_password") != "")
	appendPair("config.browser.proxy", browserProxy.Username, browserProxy.Password,
		"browser_proxy_username", "browser_proxy_password",
		protocol.ConfigString(config, "browser_proxy_username") != "" ||
			protocol.ConfigString(config, "browser_proxy_password") != "")

	// URL-userinfo twins of the pairs above: every config field the engine hands
	// to an HTTP-family dialer, which synthesises the Basic header from userinfo
	// on its own. base_url is prefixed onto every relative action target
	// (protocolhttp.ResolveURL), proxy_url feeds http.ProxyURL whenever
	// proxy_username is empty (pkg/protocol/http/config.go), browser_proxy_server
	// is handed to Playwright verbatim, and kafka_schema_registry_url is dialed
	// by a plain net/http client (pkg/protocol/kafka/schemaregistry.go), as is
	// appium_server_url (pkg/protocol/appium/webdriver.go).
	appendURL("config.base_url", script.Config.BaseURL, "base_url")
	appendURL("config.http.proxy.url", httpProxy.URL, "proxy_url")
	appendURL("config.browser.proxy.server", browserProxy.Server, "browser_proxy_server")
	appendURL("config.kafka.schema_registry_url",
		script.Config.Kafka.SchemaRegistryURL, "kafka_schema_registry_url")
	appendURL("config.appium.server_url", script.Config.Appium.ServerURL, "appium_server_url")

	// A DSN is only protected when the field that actually supplies it defers to
	// the secret store. An imported literal DSN is script-sourced: the operator
	// reads it in the script, so it stays visible in run evidence.
	database := platformManagedCredentialSurface{
		name:             "config.database.dsn",
		connectionString: true,
	}
	if authored := strings.TrimSpace(script.Config.Database.DSN); authored != "" {
		database.authoredPassword = authored
	} else {
		database.name = "config.database.dsn_secret"
		database.authoredPassword = strings.TrimSpace(script.Config.Database.DSNSecret)
		database.dedicatedSecretField = true
	}
	database.resolvedPassword = strings.TrimSpace(protocol.ConfigString(config, "database_dsn"))
	if database.resolvedPassword == "" {
		database.resolvedPassword = strings.TrimSpace(protocol.ConfigString(config, "database_dsn_secret"))
	}
	surfaces = append(surfaces, database)

	return surfaces
}

// actionCredentialCompositeSurfaces enumerates the per-action twins of the
// script-config surfaces above. They are separate only because their resolved
// halves do not exist until resolveAction has interpolated them.
//
// authored is the script's own action; resolved is what resolveAction produced
// from it. Both are required: origin is readable only on the first, values only
// on the second.
//
// The OAuth2 token endpoint is the one per-action surface not listed here.
// handleHTTPOAuth2TokenAction interpolates its own params and never builds a
// protocol.Action, so it calls urlUserinfoCredentialSurface directly on
// params.token_url.
func actionCredentialCompositeSurfaces(
	authored *dsl.Action,
	resolved *protocol.Action,
) []platformManagedCredentialSurface {
	if authored == nil || resolved == nil {
		return nil
	}
	surfaces := make([]platformManagedCredentialSurface, 0, 2)
	if authored.Auth != nil && resolved.Auth != nil {
		surfaces = append(surfaces, platformManagedCredentialSurface{
			name:             "action.auth",
			authoredUsername: authored.Auth.Username,
			authoredPassword: authored.Auth.Password,
			resolvedUsername: resolved.Auth.Username,
			resolvedPassword: resolved.Auth.Password,
			basic:            strings.EqualFold(strings.TrimSpace(resolved.Auth.Type), "basic"),
		})
	}
	if surface, ok := urlUserinfoCredentialSurface(
		"action.target", authored.Target, resolved.Target,
	); ok {
		surfaces = append(surfaces, surface)
	}
	return surfaces
}
