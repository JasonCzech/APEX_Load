package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func writeRuntimeOverridesFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write overrides file: %v", err)
	}
	return path
}

func TestLoadRuntimeOverrides(t *testing.T) {
	const partial = `{"override_fields":["http.request_timeout_ms","think_time.type"],"http":{"request_timeout_ms":5000},"think_time":{"type":"none"},"pacing":{"type":"fixed"},"run_logic":{"iterations":3},"error_handling":{"continue_on_error":true},"log":{"level":"debug"},"soap":{"version":"1.2"},"env":{"REGION":"east"},"base_url":"https://preview.example.com"}`
	tests := []struct {
		name         string
		inline       string
		file         string // file content; "" means no file flag
		missingFile  bool
		wantErr      string
		wantNil      bool
		wantRaw      string
		wantUnknown  string
		wantFields   []string
		wantCredsLen int
	}{
		{name: "neither flag", wantNil: true},
		{name: "inline JSON", inline: partial, wantRaw: partial, wantFields: []string{"http.request_timeout_ms", "think_time.type"}},
		{name: "file", file: "{\n  \"override_fields\": [\"log.level\"],\n  \"log\": {\"level\": \"warn\"}\n}\n", wantRaw: `{"override_fields":["log.level"],"log":{"level":"warn"}}`, wantFields: []string{"log.level"}},
		{name: "legacy row without override_fields", inline: `{"http":{"request_timeout_ms":1}}`, wantRaw: `{"http":{"request_timeout_ms":1}}`},
		{name: "invalid JSON", inline: `{"http":`, wantErr: "invalid --runtime-overrides"},
		{name: "invalid JSON in file", file: `{"http": nope}`, wantErr: "invalid --runtime-overrides-file"},
		{name: "wrong type", inline: `{"http":"fast"}`, wantErr: "cannot unmarshal"},
		{name: "not an object", inline: `["http"]`, wantErr: "must be a JSON object"},
		{name: "null", inline: `null`, wantErr: "must be a JSON object"},
		{name: "trailing data", inline: `{} {}`, wantErr: "single JSON object"},
		{name: "unknown field is forwarded like the API", inline: `{"thinktime":{"type":"none"}}`, wantRaw: `{"thinktime":{"type":"none"}}`, wantUnknown: "thinktime"},
		{name: "missing file", missingFile: true, wantErr: "read --runtime-overrides-file"},
		{name: "both flags", inline: `{}`, file: `{}`, wantErr: "not both"},
		{name: "empty override field", inline: `{"override_fields":[" "]}`, wantErr: "override_fields cannot contain empty field paths"},
		{name: "reserved secret reference", inline: `{"env":{"TOKEN":"{{secret.APEX_RUNTIME_OVERRIDE_abc}}"}}`, wantErr: "reserved internal secret references"},
		{name: "reserved secret reference in an unknown field", inline: `{"later":"{{ secret.apex-runtime-override-x }}"}`, wantErr: "reserved internal secret references"},
		{name: "project secret reference", inline: `{"http":{"auth":{"type":"basic","username":"svc","password":"{{secret.SVC_PASSWORD}}"}}}`, wantRaw: `{"http":{"auth":{"type":"basic","username":"svc","password":"{{secret.SVC_PASSWORD}}"}}}`, wantCredsLen: 1},
		{name: "redaction marker credential", inline: `{"soap":{"ws_security":{"username_token":{"username":"svc","password":"[REDACTED]"}}}}`, wantErr: "soap.ws_security.username_token.password is [REDACTED]"},
		{name: "redaction marker in a plain field", inline: `{"log":{"level":"[REDACTED]"}}`, wantRaw: `{"log":{"level":"[REDACTED]"}}`},
		// With override_fields the controller keeps only the listed fields
		// (mergeRuntimeSettingsPartial), so a marker in an unlisted section
		// never reaches it.
		{name: "redaction marker outside override_fields", inline: `{"override_fields":["log.level"],"log":{"level":"info"},"http":{"auth":{"password":"[REDACTED]"}}}`, wantRaw: `{"override_fields":["log.level"],"log":{"level":"info"},"http":{"auth":{"password":"[REDACTED]"}}}`, wantFields: []string{"log.level"}},
		{name: "redaction marker in an unlisted env key", inline: `{"override_fields":["env.OTHER"],"env":{"OTHER":"x","API_TOKEN":"[REDACTED]"}}`, wantRaw: `{"override_fields":["env.OTHER"],"env":{"OTHER":"x","API_TOKEN":"[REDACTED]"}}`, wantFields: []string{"env.OTHER"}},
		{name: "redaction marker under a listed section", inline: `{"override_fields":["http.auth"],"http":{"auth":{"password":"[REDACTED]"}}}`, wantErr: "http.auth.password is [REDACTED]"},
		{name: "redaction marker under a listed section, any case", inline: `{"override_fields":["config.HTTP.Auth.Password"],"http":{"auth":{"password":"[REDACTED]"}}}`, wantErr: "http.auth.password is [REDACTED]"},
		{name: "redaction marker under a listed top section", inline: `{"override_fields":["http"],"http":{"auth":{"password":"[REDACTED]"}}}`, wantErr: "http.auth.password is [REDACTED]"},
		{name: "redaction marker in env without env fields", inline: `{"override_fields":["log.level"],"log":{"level":"info"},"env":{"API_TOKEN":"[REDACTED]"}}`, wantErr: "env.API_TOKEN is [REDACTED]"},
		{name: "redaction marker in a listed browser_config section", inline: `{"override_fields":["browser_config.http_credentials"],"browser_config":{"http_credentials":{"username":"u","password":"[REDACTED]"}}}`, wantErr: "browser.http_credentials.password is [REDACTED]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := ""
			if tc.file != "" {
				path = writeRuntimeOverridesFile(t, tc.file)
			}
			if tc.missingFile {
				path = filepath.Join(t.TempDir(), "absent.json")
			}
			got, err := loadRuntimeOverrides(tc.inline, path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadRuntimeOverrides: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("overrides = %#v, want nil", got)
				}
				return
			}
			if string(got.raw) != tc.wantRaw {
				t.Fatalf("raw = %s, want %s", got.raw, tc.wantRaw)
			}
			if got.unknownField != tc.wantUnknown {
				t.Fatalf("unknownField = %q, want %q", got.unknownField, tc.wantUnknown)
			}
			if !reflect.DeepEqual(got.parsed.OverrideFields, tc.wantFields) {
				t.Fatalf("override_fields = %#v, want %#v", got.parsed.OverrideFields, tc.wantFields)
			}
			if len(got.credentials) != tc.wantCredsLen {
				t.Fatalf("credentials = %d, want %d", len(got.credentials), tc.wantCredsLen)
			}
		})
	}
}

func TestParseRunArgsRuntimeOverridesFlags(t *testing.T) {
	file := writeRuntimeOverridesFile(t, `{"override_fields":["env.REGION"],"env":{"REGION":"east"}}`)
	api := []string{"--script", "s.json", "--api", "http://localhost:8080"}
	tests := []struct {
		name    string
		args    []string
		wantErr string
		wantRaw string
	}{
		{name: "inline", args: append(append([]string{}, api...), "--runtime-overrides", `{"log":{"level":"info"}}`), wantRaw: `{"log":{"level":"info"}}`},
		{name: "file", args: append(append([]string{}, api...), "--runtime-overrides-file", file), wantRaw: `{"override_fields":["env.REGION"],"env":{"REGION":"east"}}`},
		{name: "requires api", args: []string{"--script", "s.json", "--runtime-overrides", `{}`}, wantErr: "--runtime-overrides requires --api"},
		{name: "env flag on another key combines", args: append(append([]string{}, api...), "--env", "TENANT=t1", "--runtime-overrides-file", file), wantRaw: `{"override_fields":["env.REGION"],"env":{"REGION":"east"}}`},
		{name: "env flag with the same value", args: append(append([]string{}, api...), "--env", "REGION=east", "--runtime-overrides-file", file), wantRaw: `{"override_fields":["env.REGION"],"env":{"REGION":"east"}}`},
		{name: "env flag with another value", args: append(append([]string{}, api...), "--env", "REGION=west", "--runtime-overrides-file", file), wantErr: "--env REGION and env.REGION in the runtime overrides differ"},
		{name: "base-url without base_url combines", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"log":{"level":"info"}}`), wantRaw: `{"log":{"level":"info"}}`},
		{name: "base-url with the same base_url", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"base_url":"https://a.example.com"}`), wantRaw: `{"base_url":"https://a.example.com"}`},
		{name: "base-url with another base_url", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"base_url":"https://b.example.com"}`), wantErr: "--base-url and base_url in the runtime overrides differ"},
		{name: "invalid JSON", args: append(append([]string{}, api...), "--runtime-overrides", `{`), wantErr: "invalid --runtime-overrides"},
		// override_fields decides what the controller keeps, so only kept
		// env keys and a kept base_url can conflict with the flags.
		{name: "env flag beside an env key outside override_fields", args: append(append([]string{}, api...), "--env", "REGION=west", "--runtime-overrides", `{"override_fields":["env.OTHER"],"env":{"REGION":"east","OTHER":"x"}}`), wantRaw: `{"override_fields":["env.OTHER"],"env":{"REGION":"east","OTHER":"x"}}`},
		// Env given without any env field is kept whole (the controller adds
		// an implicit "env" field), so it still conflicts.
		{name: "env flag beside env kept implicitly", args: append(append([]string{}, api...), "--env", "REGION=west", "--runtime-overrides", `{"override_fields":["log.level"],"log":{"level":"info"},"env":{"REGION":"east"}}`), wantErr: "--env REGION and env.REGION in the runtime overrides differ"},
		{name: "env flag beside an env key under env", args: append(append([]string{}, api...), "--env", "REGION=west", "--runtime-overrides", `{"override_fields":["env"],"env":{"REGION":"east"}}`), wantErr: "--env REGION and env.REGION in the runtime overrides differ"},
		{name: "base-url beside base_url outside override_fields", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"override_fields":["log.level"],"log":{"level":"info"},"base_url":"https://b.example.com"}`), wantRaw: `{"override_fields":["log.level"],"log":{"level":"info"},"base_url":"https://b.example.com"}`},
		{name: "base-url beside a listed base_url", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"override_fields":["config.base_url"],"base_url":"https://b.example.com"}`), wantErr: "--base-url and base_url in the runtime overrides differ"},
		// A listed field with no value is still kept: the controller copies
		// the empty value (applyConfigOverrideField, and an empty value for a
		// listed env key), so it conflicts with a flag that sets one.
		{name: "base-url beside a listed empty base_url", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"override_fields":["base_url"]}`), wantErr: "--base-url and base_url in the runtime overrides differ"},
		{name: "listed empty base_url without base-url", args: append(append([]string{}, api...), "--runtime-overrides", `{"override_fields":["base_url"]}`), wantRaw: `{"override_fields":["base_url"]}`},
		{name: "env flag beside a listed env key with no value", args: append(append([]string{}, api...), "--env", "REGION=west", "--runtime-overrides", `{"override_fields":["env.REGION"]}`), wantErr: "--env REGION and env.REGION in the runtime overrides differ"},
		{name: "empty env flag beside a listed env key with no value", args: append(append([]string{}, api...), "--env", "REGION=", "--runtime-overrides", `{"override_fields":["env.REGION"]}`), wantRaw: `{"override_fields":["env.REGION"]}`},
		// A listed whole env with no env map applies nothing.
		{name: "env flag beside a listed empty env", args: append(append([]string{}, api...), "--env", "REGION=west", "--runtime-overrides", `{"override_fields":["env"]}`), wantRaw: `{"override_fields":["env"]}`},
		// Without override_fields an empty base_url leaves the script's.
		{name: "base-url beside an empty v1 base_url", args: append(append([]string{}, api...), "--base-url", "https://a.example.com", "--runtime-overrides", `{"base_url":""}`), wantRaw: `{"base_url":""}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRunArgs(tc.args)
			if tc.wantErr != "" {
				if got.err == nil || !strings.Contains(got.err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", got.err, tc.wantErr)
				}
				if got.runtimeOverrides != nil {
					t.Fatal("refused overrides were kept")
				}
				return
			}
			if got.err != nil {
				t.Fatalf("parseRunArgs: %v", got.err)
			}
			if got.runtimeOverrides == nil || string(got.runtimeOverrides.raw) != tc.wantRaw {
				t.Fatalf("overrides = %#v, want raw %s", got.runtimeOverrides, tc.wantRaw)
			}
		})
	}
}

func TestRuntimeOverrideCredentialsAreNotPrinted(t *testing.T) {
	overrides, err := parseRuntimeOverridesJSON([]byte(`{
		"env": {"API_TOKEN": "tok-env-7731", "REGION": "east-region-value"},
		"base_url": "https://svc:url-pass-5521@api.example.com",
		"http": {
			"auth": {"type": "basic", "username": "svc-user-1188", "password": "http-pass-9921"},
			"default_headers": {"Authorization": "Bearer hdr-bearer-6610", "X-Trace": "trace-value-1"}
		},
		"soap": {"ws_security": {"username_token": {"username": "soap-user-4471", "password": "soap-pass-3390", "password_type": "PasswordText"}}}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rc := withRuntimeOverrideCredentialRedaction(nil, overrides)
	output := scrubCLIText("controller said: tok-env-7731 url-pass-5521 svc-user-1188 http-pass-9921 hdr-bearer-6610 soap-user-4471 soap-pass-3390 east-region-value trace-value-1", rc)
	for _, secret := range []string{"tok-env-7731", "url-pass-5521", "svc-user-1188", "http-pass-9921", "hdr-bearer-6610", "soap-user-4471", "soap-pass-3390"} {
		if strings.Contains(output, secret) {
			t.Fatalf("credential %q printed: %s", secret, output)
		}
	}
	// Values that are not credentials stay readable, as for --env.
	for _, plain := range []string{"east-region-value", "trace-value-1"} {
		if !strings.Contains(output, plain) {
			t.Fatalf("plain value %q was scrubbed: %s", plain, output)
		}
	}
}

func TestRuntimeOverridesAttachToCreateTestOnly(t *testing.T) {
	type seen struct {
		path string
		body map[string]json.RawMessage
	}
	var requests []seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(body, &fields)
		requests = append(requests, seen{path: r.URL.Path, body: fields})
		switch {
		case r.URL.Path == "/apex/api/v1/tests" && r.Header.Get("X-Test-Redirect") != "":
			http.Redirect(w, r, "/apex/api/v1/moved-tests", http.StatusTemporaryRedirect)
		case r.URL.Path == "/apex/api/v1/tests" || r.URL.Path == "/apex/api/v1/moved-tests":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"test-1"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"script-1"}`))
		}
	}))
	defer server.Close()

	overrides, err := parseRuntimeOverridesJSON([]byte(`{"override_fields":["http.request_timeout_ms"], "http":{"request_timeout_ms":2500}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client := NewApiClientWithAuth(server.URL+"/apex/", "", "")
	if err := attachRuntimeOverridesToClient(client, overrides); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := client.uploadScript("s.json", []byte(`{"script":{"name":"s"}}`)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	testID, err := client.createTestContext(context.Background(), "script-1", 2, "1m", "0s", nil)
	if err != nil || testID != "test-1" {
		t.Fatalf("create test = %q, %v", testID, err)
	}
	// A same-origin 307 replay of the create, to another path, keeps the
	// runtime settings.
	resp, err := client.doContextWithHeaders(context.Background(), http.MethodPost, "/api/v1/tests", strings.NewReader(`{"name":"x"}`), "application/json", http.Header{"X-Test-Redirect": {"1"}})
	if err != nil {
		t.Fatalf("redirected create: %v", err)
	}
	resp.Body.Close()

	if len(requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(requests))
	}
	want := `{"override_fields":["http.request_timeout_ms"],"http":{"request_timeout_ms":2500}}`
	if got := string(requests[2].body["runtime_settings"]); got != want {
		t.Fatalf("create before redirect runtime_settings = %s, want %s", got, want)
	}
	if _, ok := requests[0].body["runtime_settings"]; ok {
		t.Fatalf("script upload carried runtime_settings: %s", requests[0].path)
	}
	if got := string(requests[1].body["runtime_settings"]); requests[1].path != "/apex/api/v1/tests" || got != want {
		t.Fatalf("create-test %s runtime_settings = %s, want %s", requests[1].path, got, want)
	}
	if got := string(requests[1].body["script_id"]); got != `"script-1"` {
		t.Fatalf("create-test script_id = %s; the original body was not kept", got)
	}
	if got := string(requests[3].body["runtime_settings"]); requests[3].path != "/apex/api/v1/moved-tests" || got != want {
		t.Fatalf("redirected create %s runtime_settings = %s, want %s", requests[3].path, got, want)
	}
}

func TestRunWithoutRuntimeOverridesLeavesClientUnchanged(t *testing.T) {
	client := NewApiClientWithAuth("http://localhost:8080", "", "")
	before := client.Client
	if err := attachRuntimeOverridesToClient(client, nil); err != nil {
		t.Fatalf("attach nil: %v", err)
	}
	if client.Client != before {
		t.Fatal("client replaced without runtime overrides")
	}
}

// The CLI cannot import pkg/api, so it keeps a copy of the controller's
// override types; this keeps their JSON field sets equal.
func TestCLIRuntimeSettingsOverrideMirrorsControllerFields(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "..", "pkg", "api", "runner.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse pkg/api/runner.go: %v", err)
	}
	controller := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		var tags []string
		for _, field := range structType.Fields.List {
			if field.Tag == nil {
				continue
			}
			raw, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				t.Fatalf("tag %s: %v", field.Tag.Value, err)
			}
			if name := strings.Split(reflect.StructTag(raw).Get("json"), ",")[0]; name != "" && name != "-" {
				tags = append(tags, name)
			}
		}
		sort.Strings(tags)
		controller[spec.Name.Name] = tags
		return true
	})
	for controllerType, mirror := range map[string]any{
		"RuntimeSettingsOverride":  cliRuntimeSettingsOverride{},
		"RuntimeJavaVuserOverride": cliRuntimeJavaVuserOverride{},
		"RuntimeBrowserOverride":   cliRuntimeBrowserOverride{},
	} {
		want, ok := controller[controllerType]
		if !ok {
			t.Fatalf("pkg/api/runner.go has no %s", controllerType)
		}
		mirrorType := reflect.TypeOf(mirror)
		var got []string
		for i := 0; i < mirrorType.NumField(); i++ {
			got = append(got, strings.Split(mirrorType.Field(i).Tag.Get("json"), ",")[0])
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s JSON fields = %v, controller %s = %v", mirrorType.Name(), got, controllerType, want)
		}
	}
}

// Every credential class the controller turns into a platform secret
// (pkg/api sensitiveEnvKey, sensitiveEnvValue, maskURLSecrets) is kept out
// of CLI output, including the secret part of a value echoed on its own.
func TestRuntimeOverrideCredentialClassesMatchController(t *testing.T) {
	tests := []struct {
		name      string
		overrides string
		hidden    []string
		shown     []string
	}{
		{name: "scheme-less userinfo in env", overrides: `{"env":{"PEER":"peeru:peerpass-4410@host.example"}}`, hidden: []string{"peeru:peerpass-4410@host.example", "peerpass-4410"}},
		{name: "access key id in env", overrides: `{"env":{"AWS":"AKIAABCDEFGHIJKLMNOP"}}`, hidden: []string{"AKIAABCDEFGHIJKLMNOP"}},
		{name: "session key id in env", overrides: `{"env":{"AWS":"ASIAABCDEFGHIJKLMNOP"}}`, hidden: []string{"ASIAABCDEFGHIJKLMNOP"}},
		{name: "signed URL in env", overrides: `{"env":{"SAS":"https://acct.blob.example/c?sv=1&sig=sigval-5521"}}`, hidden: []string{"sigval-5521"}},
		{name: "signature query parameter in env", overrides: `{"env":{"SIGNED":"https://x.example/o?X-Amz-Signature=amzsig-6620"}}`, hidden: []string{"amzsig-6620"}},
		{name: "sensitive query parameter in env", overrides: `{"env":{"FEED":"https://x.example/feed?token=qtok-7781"}}`, hidden: []string{"qtok-7781"}},
		{name: "account key connection string in env", overrides: `{"env":{"STORE":"DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=acctkey-8890;"}}`, hidden: []string{"acctkey-8890"}, shown: []string{"acct;"}},
		{name: "shared access key connection string in env", overrides: `{"env":{"BUS":"Endpoint=sb://bus.example/;SharedAccessKeyName=root;SharedAccessKey=sakey-9901"}}`, hidden: []string{"sakey-9901"}},
		{name: "database URL key with a plain value", overrides: `{"env":{"DATABASE_URL":"orders-db-host-1101"}}`, hidden: []string{"orders-db-host-1101"}},
		{name: "DSN key suffix with a plain value", overrides: `{"env":{"ORDERS_DSN":"orders-dsn-value-1202"}}`, hidden: []string{"orders-dsn-value-1202"}},
		{name: "connection string key with a plain value", overrides: `{"env":{"CONNECTION_STRING":"conn-value-1303"}}`, hidden: []string{"conn-value-1303"}},
		{name: "plain env values stay readable", overrides: `{"env":{"REGION":"east-1404","CONTACT":"ops@example.com","MAIL":"mailto:ops:team@example.com"}}`, shown: []string{"east-1404", "ops@example.com", "mailto:ops:team@example.com"}},
		{name: "scheme-less userinfo in a default header", overrides: `{"http":{"default_headers":{"X-Upstream":"hdru:hdrpass-1505@up.example"}}}`, hidden: []string{"hdrpass-1505"}},
		{name: "access key id in a default header list entry", overrides: `{"http":{"default_header_list":[{"name":"X-Key","value":"AKIAQRSTUVWXYZ123456"}]}}`, hidden: []string{"AKIAQRSTUVWXYZ123456"}},
		{name: "signed URL in a default header", overrides: `{"http":{"default_headers":{"X-Callback":"https://cb.example/x?sig=hdrsig-1606"}}}`, hidden: []string{"hdrsig-1606"}},
		{name: "access key id as a SOAP username", overrides: `{"soap":{"ws_security":{"username_token":{"username":"AKIASOAPUSERNAME0001"}}}}`, hidden: []string{"AKIASOAPUSERNAME0001"}},
		{name: "scheme-less userinfo in base_url", overrides: `{"base_url":"baseu:basepass-1707@api.example"}`, hidden: []string{"basepass-1707"}},
		{name: "credential query value in base_url", overrides: `{"base_url":"https://api.example/?token=basetok-1808"}`, hidden: []string{"basetok-1808"}},
		{name: "credential fragment value in base_url", overrides: `{"base_url":"https://api.example/#access_token=basefrag-1818"}`, hidden: []string{"basefrag-1818"}},
		{name: "proxy URL password", overrides: `{"http":{"proxy":{"url":"http://pu:proxypass-1909@proxy.example:3128"}}}`, hidden: []string{"proxypass-1909"}},
		{name: "kafka SASL password and active username", overrides: `{"kafka":{"sasl_mechanism":"plain","sasl_username":"kuser-2001","sasl_password":"kpass-2002"}}`, hidden: []string{"kuser-2001", "kpass-2002"}},
		{name: "kafka OAuth client secret", overrides: `{"kafka":{"oauth":{"mode":"client_credentials","token_url":"","client_id":"cid-2101","client_secret":"oauth-2102"}}}`, hidden: []string{"oauth-2102"}, shown: []string{"cid-2101"}},
		{name: "kafka broker userinfo and credential property", overrides: `{"kafka":{"brokers":["bu:bpass-2201@k.example:9092"],"properties":{"ssl.key.password":"keypass-2202","acks":"all-2203"}}}`, hidden: []string{"bpass-2201", "keypass-2202"}, shown: []string{"all-2203"}},
		{name: "jms password", overrides: `{"jms":{"username":"juser-2301","password":"jpass-2302"}}`, hidden: []string{"juser-2301", "jpass-2302"}},
		{name: "event hubs connection string", overrides: `{"eventhubs":{"connection_string":"Endpoint=sb://ns/;SharedAccessKey=ehkey-2401"}}`, hidden: []string{"ehkey-2401"}},
		{name: "database DSN", overrides: `{"database":{"driver":"postgres","dsn":"postgres://app:dbpass-2501@db.example/app"}}`, hidden: []string{"dbpass-2501"}},
		{name: "network password", overrides: `{"network":{"username":"nuser-2601","password":"npass-2602"}}`, hidden: []string{"nuser-2601", "npass-2602"}},
		{name: "gRPC authorization metadata", overrides: `{"grpc":{"metadata":{"authorization":"Bearer gtok-2701","x-tenant":"east-2702"}}}`, hidden: []string{"gtok-2701"}, shown: []string{"east-2702"}},
		{name: "appium hub userinfo and capability key", overrides: `{"appium":{"server_url":"https://au:apass-2801@hub.example/wd/hub","capabilities":{"cloud:options":{"accessKey":"akey-2802"}}}}`, hidden: []string{"apass-2801", "akey-2802"}},
		{name: "kafka OAuth token URL credential query", overrides: `{"kafka":{"oauth":{"mode":"client_credentials","token_url":"https://idp.example/token?client_secret=tokq-3101","client_id":"cid-3100"}}}`, hidden: []string{"tokq-3101"}, shown: []string{"cid-3100"}},
		{name: "kafka JAAS property password", overrides: `{"kafka":{"properties":{"sasl.jaas.config":"org.apache.kafka.common.security.plain.PlainLoginModule required username=\"ju-3109\" password=\"jaaspw-3102\";"}}}`, hidden: []string{"jaaspw-3102"}},
		{name: "jms property URL userinfo", overrides: `{"jms":{"properties":{"failover.uri":"failover:(tcp://fu:fpass-3103@b1.example:61616)"}}}`, hidden: []string{"fpass-3103"}},
		{name: "appium device-cloud key capabilities", overrides: `{"appium":{"capabilities":{"browserstack.user":"bsu-2804","browserstack.key":"bskey-2803","testobject_api_key":"tokey-2805","appium:keyboard":"hwkb-2806"}}}`, hidden: []string{"bskey-2803", "tokey-2805"}, shown: []string{"bsu-2804", "hwkb-2806"}},
		{name: "appium capability credential text", overrides: `{"appium":{"capabilities":{"options":{"login":"user=cu-3110 password=cappw-3104"}}}}`, hidden: []string{"cappw-3104"}},
		{name: "browser session state and credentials", overrides: `{"browser_config":{"storage_state":"state-3201","session":{"identities":[{"username":"obs-3200","password":"idpw-3202","storage_state":"idstate-3203"}],"jwt":{"login_url":"https://l.example","client_id":"cid","private_key":"jwtkey-3204"}}}}`, hidden: []string{"state-3201", "idpw-3202", "idstate-3203", "jwtkey-3204"}, shown: []string{"obs-3200"}},
		{name: "network address userinfo", overrides: `{"network":{"address":"smtp://mu:netpw-3105@mail.example:25"}}`, hidden: []string{"netpw-3105"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			overrides, err := parseRuntimeOverridesJSON([]byte(tc.overrides))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			rc := withRuntimeOverrideCredentialRedaction(nil, overrides)
			for _, secret := range tc.hidden {
				if output := scrubCLIText("err: "+secret+" end", rc); strings.Contains(output, secret) {
					t.Fatalf("credential %q printed: %s", secret, output)
				}
			}
			for _, plain := range tc.shown {
				if output := scrubCLIText("err: "+plain+" end", rc); !strings.Contains(output, plain) {
					t.Fatalf("plain value %q was scrubbed: %s", plain, output)
				}
			}
		})
	}
}

// The protocol free-text and URL leaves the controller protects carry
// credentials for the service-account warning too, and a reference-only
// JAAS template carries none.
func TestRuntimeOverrideProtocolFreeTextCredentials(t *testing.T) {
	for name, raw := range map[string]string{
		"jaas password":    `{"kafka":{"properties":{"sasl.jaas.config":"PlainLoginModule required username=\"u\" password=\"jaaspw-3102\";"}}}`,
		"token url secret": `{"kafka":{"oauth":{"token_url":"https://idp.example/token?client_secret=tokq-3101"}}}`,
		"network address":  `{"network":{"address":"smtp://mu:netpw-3105@mail.example:25"}}`,
	} {
		overrides, err := parseRuntimeOverridesJSON([]byte(raw))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if got := runtimeOverrideServiceAccountWarning(runArgs{apiKey: "k", runtimeOverrides: overrides}); got == "" {
			t.Errorf("%s: no service-account warning", name)
		}
	}
	for name, raw := range map[string]string{
		"jaas reference":   `{"kafka":{"properties":{"sasl.jaas.config":"PlainLoginModule required username=\"u\" password=\"{{secret.JAAS_PW}}\";"}}}`,
		"plain property":   `{"kafka":{"properties":{"acks":"all"}}}`,
		"plain address":    `{"network":{"address":"mail.example:25"}}`,
		"plain token url":  `{"kafka":{"oauth":{"token_url":"https://idp.example/token"}}}`,
		"failover no user": `{"jms":{"properties":{"failover.uri":"failover:(tcp://b1.example:61616)"}}}`,
	} {
		overrides, err := parseRuntimeOverridesJSON([]byte(raw))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if len(overrides.credentials) != 0 {
			t.Errorf("%s: credentials = %q, want none", name, overrides.credentials)
		}
	}
}

// Every native protocol credential leaf the controller classifies
// (stringLeaf/stringMapLeaves/anyMapLeaves in runtimeProtocolCredentialLeaves)
// has a CLI entry at the same path, so a new controller leaf cannot be missed
// by the CLI's output redaction.
func TestRuntimeOverrideProtocolCredentialPathsMatchController(t *testing.T) {
	controller, err := os.ReadFile(filepath.Join("..", "..", "pkg", "api", "runtime_protocol_sections.go"))
	if err != nil {
		t.Fatalf("read controller leaves: %v", err)
	}
	cli, err := os.ReadFile("run_overrides.go")
	if err != nil {
		t.Fatalf("read CLI entries: %v", err)
	}
	leaf := regexp.MustCompile(`(?:stringLeaf|stringMapLeaves|anyMapLeaves)\("([a-z_.]+)"(?:\s*\+[^,]*)?,\s*(runtimeCredential\w+)?`)
	matches := leaf.FindAllStringSubmatch(string(controller), -1)
	if len(matches) < 15 {
		t.Fatalf("found %d controller leaves; the pattern no longer matches runtimeProtocolCredentialLeaves", len(matches))
	}
	for _, match := range matches {
		path, kind := strings.TrimSuffix(match[1], "."), match[2]
		if kind == "runtimeCredentialSecretName" {
			// A secret-name field holds a name; the controller refuses a
			// literal there, so there is no value to scrub.
			continue
		}
		if !strings.Contains(string(cli), `"`+path) {
			t.Errorf("controller credential leaf %s (%s) has no CLI entry", path, kind)
		}
	}
}

func TestRuntimeOverrideServiceAccountWarning(t *testing.T) {
	withCredential, err := parseRuntimeOverridesJSON([]byte(`{"http":{"auth":{"type":"basic","username":"svc","password":"pw-2020"}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	referencesOnly, err := parseRuntimeOverridesJSON([]byte(`{"http":{"auth":{"type":"basic","username":"{{secret.SVC_USER}}","password":"{{secret.SVC_PASSWORD}}"}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := runtimeOverrideServiceAccountWarning(runArgs{apiKey: "k", runtimeOverrides: withCredential}); !strings.Contains(got, "{{secret.NAME}}") {
		t.Fatalf("API key with a credential literal: warning = %q", got)
	}
	for name, args := range map[string]runArgs{
		"bearer token":    {bearerToken: "b", runtimeOverrides: withCredential},
		"references only": {apiKey: "k", runtimeOverrides: referencesOnly},
		"no overrides":    {apiKey: "k"},
	} {
		if got := runtimeOverrideServiceAccountWarning(args); got != "" {
			t.Fatalf("%s: warning = %q, want none", name, got)
		}
	}
}

// TestRunTestSendsRuntimeOverridesAndScrubsTheirCredentials drives the real
// `apexctl run --api` path in a child process against a controller that
// echoes an override credential in its create-test refusal.
func TestRunTestSendsRuntimeOverridesAndScrubsTheirCredentials(t *testing.T) {
	if raw := os.Getenv("APEXLOAD_TEST_RUN_RUNTIME_OVERRIDES_ARGS"); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			t.Fatalf("child args: %v", err)
		}
		os.Args = append([]string{"apexctl", "run"}, args...)
		runTest()
		return
	}
	const credential = "http-pass-9921"
	var createBody map[string]json.RawMessage
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/scripts":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"script-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			creates++
			_ = json.Unmarshal(body, &createBody)
			http.Error(w, "runtime override rejected: "+credential+" at http.auth", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	script := writeTempScript(t, `{"name":"overrides","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`)
	junit := filepath.Join(t.TempDir(), "results.xml")
	overrides := `{"override_fields":["http.auth"],"http":{"auth":{"type":"basic","username":"svc","password":"` + credential + `"}}}`
	args, _ := json.Marshal([]string{"--api", server.URL, "--script", script, "--vusers", "1", "--duration", "1s", "--format", "json", "--junit", junit, "--runtime-overrides", overrides})
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestSendsRuntimeOverridesAndScrubsTheirCredentials$")
	cmd.Env = append(os.Environ(),
		"APEXLOAD_TEST_RUN_RUNTIME_OVERRIDES_ARGS="+string(args),
		"APEXLOAD_API_KEY=ci-key",
		"APEXLOAD_BEARER_TOKEN=",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("run succeeded although the controller refused the create\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if creates != 1 {
		t.Fatalf("create-test requests = %d, want 1\nstderr: %s", creates, stderr.String())
	}
	var sent map[string]any
	if err := json.Unmarshal(createBody["runtime_settings"], &sent); err != nil {
		t.Fatalf("create-test runtime_settings = %s: %v", createBody["runtime_settings"], err)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(overrides), &want)
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("create-test runtime_settings = %v, want %v", sent, want)
	}
	junitXML, err := os.ReadFile(junit)
	if err != nil {
		t.Fatalf("read JUnit: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "runtime override rejected") || !strings.Contains(string(junitXML), "runtime override rejected") {
		t.Fatalf("controller refusal missing from output\nstdout: %s\njunit: %s", stdout.String(), junitXML)
	}
	for name, output := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String(), "junit": string(junitXML)} {
		if strings.Contains(output, credential) {
			t.Fatalf("%s printed the override credential: %s", name, output)
		}
	}
	if !strings.Contains(stderr.String(), "{{secret.NAME}}") {
		t.Fatalf("stderr lacks the service-account warning: %s", stderr.String())
	}
}

// TestRunTestRuntimeOverrideCreateRefusalIsConfigurationError drives the real
// `apexctl run --api` path in a child process: a controller that refuses the
// create-test request carrying runtime overrides (a service account's typed
// credential: 403; an unknown override field: 400) refuses the operator's
// configuration, so the run exits 2, not as a controller/transport failure.
// Without runtime overrides the same refusal keeps its usual classification.
func TestRunTestRuntimeOverrideCreateRefusalIsConfigurationError(t *testing.T) {
	if raw := os.Getenv("APEXLOAD_TEST_RUN_RUNTIME_OVERRIDES_REFUSAL_ARGS"); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			t.Fatalf("child args: %v", err)
		}
		os.Args = append([]string{"apexctl", "run"}, args...)
		runTest()
		return
	}
	tests := []struct {
		name      string
		status    int
		overrides string
		format    string
		wantCode  int
	}{
		{name: "service account credential refused", status: http.StatusForbidden, overrides: `{"http":{"auth":{"type":"basic","username":"u","password":"p-77"}}}`, format: "json", wantCode: exitCodeUsageOrConfiguration},
		{name: "service account credential refused, text output", status: http.StatusForbidden, overrides: `{"http":{"auth":{"type":"basic","username":"u","password":"p-77"}}}`, format: "text", wantCode: exitCodeUsageOrConfiguration},
		{name: "unknown override field refused", status: http.StatusBadRequest, overrides: `{"override_fields":["http.nope"]}`, format: "json", wantCode: exitCodeUsageOrConfiguration},
		{name: "controller failure stays a controller failure", status: http.StatusInternalServerError, overrides: `{"log":{"level":"info"}}`, format: "json", wantCode: exitCodeControllerOrTransport},
		{name: "refusal without runtime overrides keeps its classification", status: http.StatusForbidden, format: "json", wantCode: exitCodeControllerOrTransport},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/scripts":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"script-1"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
					http.Error(w, `{"error":"refused"}`, tc.status)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			script := writeTempScript(t, `{"name":"overrides","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`)
			argv := []string{"--api", server.URL, "--script", script, "--vusers", "1", "--duration", "1s", "--format", tc.format}
			if tc.overrides != "" {
				argv = append(argv, "--runtime-overrides", tc.overrides)
			}
			args, _ := json.Marshal(argv)
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestRuntimeOverrideCreateRefusalIsConfigurationError$")
			cmd.Env = append(os.Environ(),
				"APEXLOAD_TEST_RUN_RUNTIME_OVERRIDES_REFUSAL_ARGS="+string(args),
				"APEXLOAD_API_KEY=ci-key",
				"APEXLOAD_BEARER_TOKEN=",
			)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("run err = %v, want an exit status\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			if code := exitErr.ExitCode(); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), "p-77") {
				t.Fatalf("output printed the override credential\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
			}
		})
	}
}
