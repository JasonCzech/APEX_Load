package bruno

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestInspectStandaloneOpenCollectionRequest(t *testing.T) {
	content := []byte(`info:
  name: Login
  type: http
  seq: 2
http:
  method: POST
  url: "{{base_url}}/login"
  auth:
    type: basic
    username: literal-user
    password: literal-password
runtime:
  scripts:
    - type: after-response
      code: |
        test("ok", () => expect(res.getStatus()).to.equal(200));
settings:
  timeout: 0
`)
	result, err := Inspect(Input{Filename: "login.yml", Content: content, ProjectID: "project-a"})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "bruno" || len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "bruno_run" {
		t.Fatalf("canonical script = %#v", result.Script)
	}
	if result.Collection.Name != "Login" || len(result.Collection.Items) != 1 || result.Collection.Items[0].Type != "http" {
		t.Fatalf("collection = %#v", result.Collection)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v", result.SecretRequirements)
	}
	values := map[string]string{}
	for _, requirement := range result.SecretRequirements {
		values[requirement.Key] = requirement.Value
	}
	if values["http.auth.username"] != "literal-user" || values["http.auth.password"] != "literal-password" {
		t.Fatalf("in-memory migration values = %#v", values)
	}
	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(publicJSON, []byte("literal-user")) || bytes.Contains(publicJSON, []byte("literal-password")) {
		t.Fatal("serialized inspect result leaked imported secret values")
	}
	if bytes.Contains(result.SanitizedArchive, []byte("literal-password")) || bytes.Contains(result.SanitizedArchive, []byte("literal-user")) {
		t.Fatal("sanitized archive retained credentials")
	}
	if !bytes.Contains(result.RawArchive, []byte("literal-password")) {
		t.Fatal("raw archive did not preserve exact source content")
	}
	if len(result.MissingVariables) != 1 || result.MissingVariables[0] != "base_url" {
		t.Fatalf("missing variables = %#v", result.MissingVariables)
	}
	if result.Fidelity.Status != "lossless" || result.Fidelity.DroppedItems != 0 {
		t.Fatalf("fidelity = %#v", result.Fidelity)
	}
}

func TestInspectMixedCollectionPreservesAppsAndAllRequestTypes(t *testing.T) {
	files := map[string][]byte{
		"demo/opencollection.yml": []byte("opencollection: 1.0.0\ninfo:\n  name: Demo\n"),
		"demo/a.yml":              []byte("info:\n  name: HTTP\n  type: http\nhttp:\n  method: GET\n  url: https://example.test\n"),
		"demo/b.yml":              []byte("info:\n  name: Graph\n  type: graphql\ngraphql:\n  url: https://example.test/graphql\n"),
		"demo/c.yml":              []byte("info:\n  name: RPC\n  type: grpc\ngrpc:\n  url: grpc.example.test\n"),
		"demo/d.yml":              []byte("info:\n  name: Socket\n  type: websocket\nwebsocket:\n  url: wss://example.test\n"),
		"demo/app.yml":            []byte("info:\n  name: Helper UI\n  type: app\napp:\n  type: visualization\n"),
	}
	result, err := Inspect(Input{Files: files})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if result.RootPath != "demo" || result.Collection.Name != "Demo" {
		t.Fatalf("root/name = %q/%q", result.RootPath, result.Collection.Name)
	}
	if got := strings.Join(result.Capabilities.RequestTypes, ","); got != "graphql,grpc,http,websocket" {
		t.Fatalf("request types = %q", got)
	}
	if !result.Capabilities.HasBrunoApps || result.Fidelity.PreservedApps != 1 || result.Fidelity.ExecutableItems != 4 {
		t.Fatalf("capabilities/fidelity = %#v %#v", result.Capabilities, result.Fidelity)
	}
}

func TestInspectBuildsRecursiveCollectionTreeWithFolderMetadata(t *testing.T) {
	result, err := Inspect(Input{Files: map[string][]byte{
		"demo/opencollection.yml":        []byte("opencollection: 1.0.0\ninfo:\n  name: Demo\n"),
		"demo/accounts/folder.yml":       []byte("info:\n  name: Account APIs\n  uid: folder-accounts\n  seq: 2\n"),
		"demo/accounts/read.yml":         []byte("info:\n  name: Read account\n  type: http\n  seq: 2\nhttp:\n  method: GET\n  url: https://example.test/accounts/1\n"),
		"demo/accounts/admin/delete.yml": []byte("info:\n  name: Delete account\n  type: http\n  seq: 1\nhttp:\n  method: DELETE\n  url: https://example.test/accounts/1\n"),
		"demo/health.yml":                []byte("info:\n  name: Health\n  type: http\n  seq: 1\nhttp:\n  method: GET\n  url: https://example.test/health\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Collection.Items) != 2 || result.Collection.Items[0].Name != "Health" {
		t.Fatalf("root collection tree = %#v", result.Collection.Items)
	}
	accounts := result.Collection.Items[1]
	if accounts.Type != "folder" || accounts.Name != "Account APIs" || accounts.UID != "folder-accounts" || len(accounts.Children) != 2 {
		t.Fatalf("accounts folder = %#v", accounts)
	}
	if accounts.Children[0].Name != "Read account" {
		t.Fatalf("folder source order = %#v", accounts.Children)
	}
	admin := accounts.Children[1]
	if admin.Type != "folder" || admin.Name != "admin" ||
		len(admin.Children) != 1 || admin.Children[0].Name != "Delete account" {
		t.Fatalf("synthetic nested folder = %#v", admin)
	}
}

func TestInspectHonorsClassicUIDAndKeepsFallbackIdentityAcrossMoves(t *testing.T) {
	withUID, err := Inspect(Input{Filename: "request.bru", Content: []byte(`meta {
  name: Request
  type: http
  uid: request-uid
}
get {
  url: https://example.test
}
`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := withUID.Collection.Items[0].UID; got != "request-uid" {
		t.Fatalf("classic UID = %q", got)
	}

	first, err := Inspect(Input{Files: map[string][]byte{
		"first/request.bru": []byte("meta {\n  name: Request\n  type: http\n}\nget {\n  url: https://example.test\n}\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(Input{Files: map[string][]byte{
		"second/request.bru": []byte("meta {\n  name: Request\n  type: http\n}\nget {\n  url: https://example.test\n}\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var requestUID func([]CollectionItem) string
	requestUID = func(items []CollectionItem) string {
		for _, item := range items {
			if item.Executable {
				return item.UID
			}
			if uid := requestUID(item.Children); uid != "" {
				return uid
			}
		}
		return ""
	}
	firstUID, secondUID := requestUID(first.Collection.Items), requestUID(second.Collection.Items)
	if firstUID == "" || firstUID != secondUID {
		t.Fatalf("fallback identity changed across a move: %q != %q",
			firstUID, secondUID)
	}
}

func TestInspectRejectsUnsafeArchivePathsAndSymlinks(t *testing.T) {
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	entry, err := zw.Create("../escape.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("info: {name: bad, type: http}\nhttp: {url: https://example.test}\n"))
	_ = zw.Close()
	if _, err := Inspect(Input{Filename: "bad.zip", Content: zipBytes.Bytes()}); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("unsafe ZIP error = %v", err)
	}

	var tarBytes bytes.Buffer
	tw := tar.NewWriter(&tarBytes)
	if err := tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	if _, err := Inspect(Input{Filename: "bad.tar", Content: tarBytes.Bytes()}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink TAR error = %v", err)
	}
}

func TestInspectRejectsUnicodeNormalizedPathCollisions(t *testing.T) {
	_, err := Inspect(Input{Files: map[string][]byte{
		"Caf\u00e9.yml":  []byte("info: {name: A, type: http}\nhttp: {url: https://a.test}\n"),
		"Cafe\u0301.yml": []byte("info: {name: B, type: http}\nhttp: {url: https://b.test}\n"),
	}})
	if err == nil || !strings.Contains(err.Error(), "collide after normalization") {
		t.Fatalf("Unicode collision error = %v", err)
	}
}

func TestInspectRejectsPathsThatWouldBeSilentlyTrimmed(t *testing.T) {
	_, err := Inspect(Input{Files: map[string][]byte{
		" request.yml": []byte("info: {name: Request, type: http}\nhttp: {url: https://example.test}\n"),
	}})
	if err == nil || !strings.Contains(err.Error(), "leading or trailing whitespace") {
		t.Fatalf("whitespace path error = %v", err)
	}
}

func TestInspectRejectsCompressedTARExpansionBomb(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range map[string][]byte{
		"request.yml": []byte("info: {name: Safe, type: http}\nhttp: {url: https://example.test}\n"),
		"fixture.bin": bytes.Repeat([]byte{0}, 64<<10),
	} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Inspect(Input{
		Filename: "bomb.tar.gz",
		Content:  archive.Bytes(),
		Limits:   Limits{MaxCompressionRatio: 2},
	})
	if err == nil || !strings.Contains(err.Error(), "compression-ratio limit") {
		t.Fatalf("compression bomb error = %v", err)
	}
}

func TestDeterministicBundle(t *testing.T) {
	input := Input{Files: map[string][]byte{
		"b.yml": []byte("info: {name: B, type: http}\nhttp: {url: https://b.test}\n"),
		"a.yml": []byte("info: {name: A, type: http}\nhttp: {url: https://a.test}\n"),
	}}
	first, err := Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.SanitizedArchive, second.SanitizedArchive) || first.Manifest.BundleSHA256 != second.Manifest.BundleSHA256 {
		t.Fatal("bundle output is not deterministic")
	}
	reader := tar.NewReader(bytes.NewReader(first.SanitizedArchive))
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if strings.Join(names, ",") != "a.yml,b.yml" {
		t.Fatalf("tar order = %v", names)
	}
}

func TestPublicInspectFingerprintsDoNotRevealSourceSecretChanges(t *testing.T) {
	inspect := func(secret string) *Result {
		t.Helper()
		result, err := Inspect(Input{
			Filename:  "login.yml",
			ProjectID: "project-a",
			Content: []byte(`info:
  name: Login
  type: http
http:
  method: POST
  url: https://example.test/login
  auth:
    type: bearer
    token: ` + secret + "\n"),
		})
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		return result
	}

	first := inspect("short-source-secret")
	second := inspect("a-completely-different-and-longer-source-secret")
	if first.Manifest.RawArchiveSHA256 == second.Manifest.RawArchiveSHA256 {
		t.Fatal("test setup must produce distinct protected raw-source fingerprints")
	}
	if first.Manifest.BundleSHA256 != second.Manifest.BundleSHA256 ||
		first.Manifest.SHA256 != second.Manifest.SHA256 ||
		first.Manifest.TotalBytes != second.Manifest.TotalBytes {
		t.Fatalf("public sanitized manifest changed with only the hidden value:\nfirst=%#v\nsecond=%#v", first.Manifest, second.Manifest)
	}
	if first.Collection.Items[0].UID != second.Collection.Items[0].UID {
		t.Fatalf("public collection UID fingerprints hidden source values: %q != %q", first.Collection.Items[0].UID, second.Collection.Items[0].UID)
	}

	first.InspectedAt = time.Time{}
	second.InspectedAt = time.Time{}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("serialized public inspect result changes with only hidden source material:\n%s\n%s", firstJSON, secondJSON)
	}
	if bytes.Contains(firstJSON, []byte("raw_archive_sha256")) {
		t.Fatalf("public result exposed a raw-source fingerprint: %s", firstJSON)
	}
}

func TestSanitizeJSONRemainsJSON(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.json",
		Content: []byte(`{
  "info": {"name": "JSON request", "type": "http"},
  "http": {
    "url": "https://example.test",
    "auth": {"type": "bearer", "token": "literal-token"}
  }
}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(result.SanitizedArchive))
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("sanitized JSON is invalid: %v\n%s", err, content)
	}
	if bytes.Contains(content, []byte("literal-token")) {
		t.Fatal("sanitized JSON retained token")
	}
}

func TestStructuredSanitizationTreatsRedactedSentinelAsUnrecoverable(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "redacted.yml",
		Content: []byte(`info: {name: Redacted, type: http}
http:
  url: https://example.test
  auth: {type: bearer, token: "[REDACTED]"}
`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 1 || result.SecretRequirements[0].Value != "" || result.SecretRequirements[0].SourceValueAvailable {
		t.Fatalf("redacted requirement = %#v", result.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "redacted.yml")
	if bytes.Contains(sanitized, []byte("[REDACTED]")) || !bytes.Contains(sanitized, []byte("{{secret.REDACTED_TOKEN}}")) {
		t.Fatalf("redacted sentinel was not converted to a deferred reference:\n%s", sanitized)
	}
}

func TestStructuredSanitizationRemovesCredentialBodyFieldsAndUsesUniqueFolderNames(t *testing.T) {
	result, err := Inspect(Input{Files: map[string][]byte{
		"admin/login.yml": []byte(`info: {name: Admin login, type: http}
http:
  url: https://example.test/admin
  auth: {type: basic, password: admin-secret}
  body:
    type: json
    data: {password: deliberately-wrong, token: fixture-token}
`),
		"user/login.yml": []byte(`info: {name: User login, type: http}
http:
  url: https://example.test/user
  auth: {type: basic, password: user-secret}
`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 4 {
		t.Fatalf("secret requirements = %#v", result.SecretRequirements)
	}
	names := map[string]bool{}
	for _, requirement := range result.SecretRequirements {
		names[requirement.SuggestedName] = true
	}
	if !names["ADMIN_LOGIN_PASSWORD"] || !names["ADMIN_LOGIN_BODY_PASSWORD"] ||
		!names["ADMIN_LOGIN_BODY_TOKEN"] || !names["USER_LOGIN_PASSWORD"] {
		t.Fatalf("secret names = %#v", names)
	}
	admin := tarFile(t, result.SanitizedArchive, "admin/login.yml")
	if bytes.Contains(admin, []byte("deliberately-wrong")) || bytes.Contains(admin, []byte("fixture-token")) {
		t.Fatalf("sanitized request body retained credentials:\n%s", admin)
	}
	if !bytes.Contains(admin, []byte("{{secret.ADMIN_LOGIN_BODY_PASSWORD}}")) ||
		!bytes.Contains(admin, []byte("{{secret.ADMIN_LOGIN_BODY_TOKEN}}")) {
		t.Fatalf("sanitized request body did not contain governed references:\n%s", admin)
	}
}

func TestSecretRequirementsUseDistinctStableBindingsForRepeatedFieldNames(t *testing.T) {
	tests := []struct {
		name         string
		input        Input
		artifactPath string
		wantValues   []string
	}{
		{
			name: "structured YAML paths",
			input: Input{Filename: "request.yml", Content: []byte(`info: {name: Repeated, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data:
      - password: first-structured-password
      - password: second-structured-password
`)},
			artifactPath: "request.yml",
			wantValues:   []string{"first-structured-password", "second-structured-password"},
		},
		{
			name: "classic BRU occurrences",
			input: Input{Filename: "request.bru", Content: []byte(`meta {
  name: Repeated
  type: http
}
vars:secret {
  password: first-bru-password
}
vars:secret {
  password: second-bru-password
}
get {
  url: https://example.test
}
`)},
			artifactPath: "request.bru",
			wantValues:   []string{"first-bru-password", "second-bru-password"},
		},
		{
			name: "dotenv duplicate assignments",
			input: Input{Files: map[string][]byte{
				"request.yml": []byte("info: {name: Env request, type: http}\nhttp: {method: GET, url: https://example.test}\n"),
				"environments/test.env": []byte("TOKEN=first-env-token\n" +
					"TOKEN=second-env-token\n"),
			}},
			artifactPath: "environments/test.env",
			wantValues:   []string{"first-env-token", "second-env-token"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first, err := Inspect(test.input)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Inspect(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.SecretRequirements) != 2 {
				t.Fatalf("requirements = %#v, want two distinct credentials", first.SecretRequirements)
			}
			ids := map[string]bool{}
			names := map[string]bool{}
			values := map[string]bool{}
			for index, requirement := range first.SecretRequirements {
				ids[requirement.ID] = true
				names[requirement.SuggestedName] = true
				values[requirement.Value] = true
				if requirement.ID != second.SecretRequirements[index].ID ||
					requirement.SuggestedName != second.SecretRequirements[index].SuggestedName {
					t.Fatalf("requirement identity is not deterministic:\n%#v\n%#v", first.SecretRequirements, second.SecretRequirements)
				}
			}
			if len(ids) != 2 || len(names) != 2 {
				t.Fatalf("requirements collapsed identity or binding names: %#v", first.SecretRequirements)
			}
			for _, value := range test.wantValues {
				if !values[value] {
					t.Fatalf("source value %q missing from distinct requirements: %#v", value, first.SecretRequirements)
				}
			}
			sanitized := tarFile(t, first.SanitizedArchive, test.artifactPath)
			for _, value := range test.wantValues {
				if bytes.Contains(sanitized, []byte(value)) {
					t.Fatalf("sanitized artifact retained %q:\n%s", value, sanitized)
				}
			}
			for name := range names {
				if !bytes.Contains(sanitized, []byte("{{secret."+name+"}}")) {
					t.Fatalf("sanitized artifact lacks distinct binding %q:\n%s", name, sanitized)
				}
			}
		})
	}
}

func TestStructuredSanitizationRemovesSensitiveHeaderListsInYAMLAndJSON(t *testing.T) {
	tests := []struct {
		name          string
		filename      string
		source        string
		secretValues  []string
		expectedNames []string
	}{
		{
			name:     "yaml authorization and body password",
			filename: "yaml-login.yml",
			source: `info: {name: YAML login, type: http}
http:
  method: POST
  url: https://example.test/login
  headers:
    - name: Authorization
      value: Bearer yaml-header-token
    - name: X-API-Key
      value: yaml-header-key
  body:
    type: json
    data: {password: yaml-body-password, display_name: ordinary-value}
`,
			secretValues:  []string{"yaml-header-token", "yaml-header-key", "yaml-body-password"},
			expectedNames: []string{"YAML_LOGIN_HEADER_AUTHORIZATION", "YAML_LOGIN_HEADER_X_API_KEY", "YAML_LOGIN_BODY_PASSWORD"},
		},
		{
			name:     "json api key and body token",
			filename: "json-login.json",
			source: `{
  "info": {"name": "JSON login", "type": "http"},
  "http": {
    "method": "POST",
    "url": "https://example.test/login",
    "headers": [{"name": "X-API-Key", "value": "json-header-key"}],
    "body": {"type": "json", "data": {"token": "json-body-token", "display_name": "ordinary-value"}}
  }
}`,
			secretValues:  []string{"json-header-key", "json-body-token"},
			expectedNames: []string{"JSON_LOGIN_HEADER_X_API_KEY", "JSON_LOGIN_BODY_TOKEN"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{Filename: test.filename, Content: []byte(test.source)})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.SecretRequirements) != len(test.expectedNames) {
				t.Fatalf("secret requirements = %#v", result.SecretRequirements)
			}
			names := make(map[string]bool, len(result.SecretRequirements))
			for _, requirement := range result.SecretRequirements {
				names[requirement.SuggestedName] = true
			}
			for _, expected := range test.expectedNames {
				if !names[expected] {
					t.Fatalf("missing suggested name %q in %#v", expected, names)
				}
			}
			sanitized := tarFile(t, result.SanitizedArchive, test.filename)
			for _, secret := range test.secretValues {
				if bytes.Contains(sanitized, []byte(secret)) {
					t.Fatalf("sanitized %s retained %q:\n%s", test.filename, secret, sanitized)
				}
			}
			if !bytes.Contains(sanitized, []byte("ordinary-value")) {
				t.Fatalf("sanitization removed an ordinary body field:\n%s", sanitized)
			}
		})
	}
}

func TestInspectReusesAndCanonicalizesExistingSecretReferences(t *testing.T) {
	result, err := Inspect(Input{Files: map[string][]byte{
		"login.yml": []byte(`info: {name: Login, type: http}
http:
  method: GET
  url: https://example.test/login
  headers:
    - name: Authorization
      value: 'Bearer {{secret.login-password}}'
  auth:
    type: basic
    password: raw-login-password
`),
		"classic.bru": []byte(`meta {
  name: Classic
  type: http
  seq: 2
}
get {
  url: https://example.test/classic
  body: none
  auth: none
}
headers {
  Authorization: Bearer {{secret.LOGIN_PASSWORD}}
  X-API-Key: {{ Secret.login.password }}
}
`),
	}})
	if err != nil {
		t.Fatal(err)
	}

	var existing, detected []SecretRequirement
	for _, requirement := range result.SecretRequirements {
		if requirement.ExistingReference {
			existing = append(existing, requirement)
		} else {
			detected = append(detected, requirement)
		}
	}
	if len(existing) != 2 || len(detected) != 1 {
		t.Fatalf("requirements = %#v, want two grouped source locations and one detected value", result.SecretRequirements)
	}
	for _, requirement := range existing {
		if requirement.SuggestedName != "LOGIN_PASSWORD" || requirement.Value != "" || requirement.SourceValueAvailable {
			t.Fatalf("existing reference = %#v", requirement)
		}
	}
	if detected[0].SuggestedName != "LOGIN_PASSWORD_2" || detected[0].Value != "raw-login-password" {
		t.Fatalf("detected credential did not avoid the existing binding: %#v", detected[0])
	}
	for key, want := range map[string]string{
		"LOGIN_PASSWORD":   "{{secret.LOGIN_PASSWORD}}",
		"LOGIN_PASSWORD_2": "{{secret.LOGIN_PASSWORD_2}}",
	} {
		if got := result.Script.Config.Bruno.SecretBindings[key]; got != want {
			t.Fatalf("secret binding %q = %q, want %q", key, got, want)
		}
	}
	for _, filename := range []string{"login.yml", "classic.bru"} {
		sanitized := tarFile(t, result.SanitizedArchive, filename)
		if bytes.Contains(sanitized, []byte("raw-login-password")) ||
			bytes.Contains(sanitized, []byte("secret.login-password")) ||
			bytes.Contains(sanitized, []byte("Secret.login.password")) {
			t.Fatalf("sanitized %s retained plaintext or a noncanonical alias:\n%s", filename, sanitized)
		}
		if !bytes.Contains(sanitized, []byte("{{secret.LOGIN_PASSWORD}}")) {
			t.Fatalf("sanitized %s did not reuse the canonical source reference:\n%s", filename, sanitized)
		}
	}
}

func TestInspectRejectsMalformedExistingSecretReference(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "invalid.yml",
		Content: []byte(`info: {name: Invalid, type: http}
http:
  method: GET
  url: 'https://example.test?token={{secret.bad/name}}'
`),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid secret reference") {
		t.Fatalf("invalid secret reference error = %v", err)
	}
}

func TestClassicBRUHeaderBlocksRemoveEverySensitiveValue(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "headers.bru",
		Content: []byte(`meta {
  name: Header security
  type: http
}
get {
  url: https://example.test
  body: none
  auth: none
}
headers {
  Authorization: Bearer first-authorization-value
  Proxy-Authorization: Basic proxy-authorization-value
  Cookie: sid=cookie-value
  X-API-Key: api-key-value
  Authorization: [redacted]
  Accept: application/json
}
script:pre-request {
  const note = "Authorization: harmless-script-text";
}
`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 5 {
		t.Fatalf("secret requirements = %#v, want every duplicate sensitive header", result.SecretRequirements)
	}
	names := make(map[string]struct{}, len(result.SecretRequirements))
	unrecoverable := 0
	for _, requirement := range result.SecretRequirements {
		names[requirement.SuggestedName] = struct{}{}
		if requirement.Value == "" {
			unrecoverable++
		}
	}
	if len(names) != 5 || unrecoverable != 1 {
		t.Fatalf("header requirements = %#v", result.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "headers.bru")
	for _, value := range []string{
		"first-authorization-value",
		"proxy-authorization-value",
		"cookie-value",
		"api-key-value",
		"[redacted]",
	} {
		if bytes.Contains(bytes.ToLower(sanitized), []byte(strings.ToLower(value))) {
			t.Fatalf("sanitized BRU retained %q:\n%s", value, sanitized)
		}
	}
	if !bytes.Contains(sanitized, []byte("Accept: application/json")) ||
		!bytes.Contains(sanitized, []byte("Authorization: harmless-script-text")) {
		t.Fatalf("block-aware sanitization changed noncredential content:\n%s", sanitized)
	}
}

func TestStructuredSanitizationOnlyExemptsExactTemplates(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		source    string
		secret    string
		template  string
		wantExact bool
	}{
		{
			name:     "yaml mixed literal",
			filename: "mixed.yml",
			source: `info: {name: Mixed YAML, type: http}
http:
  url: https://example.test
  auth: {type: basic, password: 'yaml-secret{{suffix}}'}
`,
			secret: "yaml-secret{{suffix}}",
		},
		{
			name:     "json malformed close braces",
			filename: "mixed.json",
			source:   `{"info":{"name":"Mixed JSON","type":"http"},"http":{"url":"https://example.test","auth":{"type":"bearer","token":"json-secret}}suffix"}}}`,
			secret:   "json-secret}}suffix",
		},
		{
			name:     "classic bru mixed literal",
			filename: "mixed.bru",
			source: `meta {
  name: Mixed BRU
  type: http
}
get {
  url: https://example.test
}
auth:basic {
  password: bru-secret{{suffix}}
}
`,
			secret: "bru-secret{{suffix}}",
		},
		{
			name:     "exact template remains a source variable",
			filename: "template.yml",
			source: `info: {name: Template, type: http}
http:
  url: https://example.test
  auth: {type: bearer, token: '{{token}}'}
`,
			template:  "{{token}}",
			wantExact: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{Filename: test.filename, Content: []byte(test.source)})
			if err != nil {
				t.Fatal(err)
			}
			sanitized := tarFile(t, result.SanitizedArchive, test.filename)
			if test.wantExact {
				if len(result.SecretRequirements) != 0 || !bytes.Contains(sanitized, []byte(test.template)) {
					t.Fatalf("exact template was not preserved as a variable: requirements=%#v\n%s", result.SecretRequirements, sanitized)
				}
				return
			}
			if len(result.SecretRequirements) != 1 {
				t.Fatalf("mixed credential requirements = %#v", result.SecretRequirements)
			}
			if bytes.Contains(sanitized, []byte(test.secret)) || !bytes.Contains(sanitized, []byte("{{secret.")) {
				t.Fatalf("mixed credential was not sanitized:\n%s", sanitized)
			}
		})
	}
}

func TestStructuredSanitizationResolvesSensitiveYAMLAliasesAndMergeKeys(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "aliases.yml",
		Content: []byte(`info: {name: Aliases, type: http}
defaults: &auth_defaults
  password: anchored-password
token_value: &token_value anchored-token
http:
  url: https://example.test
  auth:
    type: basic
    <<: *auth_defaults
  headers:
    - name: Authorization
      value: *token_value
`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("alias requirements = %#v", result.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "aliases.yml")
	if bytes.Contains(sanitized, []byte("anchored-password")) || bytes.Contains(sanitized, []byte("anchored-token")) {
		t.Fatalf("sanitized aliases retained anchored credentials:\n%s", sanitized)
	}
	if bytes.Count(sanitized, []byte("{{secret.")) < 2 {
		t.Fatalf("sanitized aliases omitted governed references:\n%s", sanitized)
	}
}

func TestStructuredSanitizationRejectsNonScalarSensitiveYAMLAlias(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "complex-alias.yml",
		Content: []byte(`info: {name: Complex alias, type: http}
credential: &credential
  value: secret
http:
  url: https://example.test
  auth:
    type: basic
    password: *credential
`),
	})
	if err == nil || !strings.Contains(err.Error(), "does not resolve to a scalar") {
		t.Fatalf("complex credential alias error = %v", err)
	}
}

func TestEnvironmentSanitizationHandlesNamedValuesAndClassicBRUVars(t *testing.T) {
	result, err := Inspect(Input{Files: map[string][]byte{
		"opencollection.yml":     []byte("opencollection: 1.0.0\ninfo: {name: Environment test}\n"),
		"request.yml":            []byte("info: {name: Request, type: http}\nhttp: {method: GET, url: '{{base_url}}'}\n"),
		"environments/prod.json": []byte(`{"name":"prod","variables":[{"name":"password","value":"named-secret","enabled":true}]}`),
		"environments/local.bru": []byte("vars {\n  base_url: https://example.test\n  passphrase: classic-secret\n}\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v", result.SecretRequirements)
	}
	if bytes.Contains(result.SanitizedArchive, []byte("named-secret")) || bytes.Contains(result.SanitizedArchive, []byte("classic-secret")) {
		t.Fatal("sanitized environments retained imported credentials")
	}
	for _, missing := range result.MissingVariables {
		if missing == "base_url" {
			t.Fatalf("classic BRU environment variable was reported missing: %#v", result.MissingVariables)
		}
	}
}

func TestOpaqueInvalidStructuredAssetIsPreserved(t *testing.T) {
	fixture := []byte(`{"fixture": "kept",}`)
	result, err := Inspect(Input{Files: map[string][]byte{
		"request.yml":        []byte("info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n"),
		"fixtures/data.json": fixture,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := tarFile(t, result.SanitizedArchive, "fixtures/data.json"); !bytes.Equal(got, fixture) {
		t.Fatalf("opaque asset changed: %q", got)
	}
}

func TestInspectMarksPrivateKeyFilesAsRestrictedSourceMaterial(t *testing.T) {
	result, err := Inspect(Input{Files: map[string][]byte{
		"request.yml": []byte("info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n"),
		"client.pem":  []byte("-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Capabilities.HasCertificates || !result.Capabilities.HasPrivateKeyFiles {
		t.Fatalf("capabilities = %#v", result.Capabilities)
	}
}

func TestInspectRejectsEmptyScopedSelection(t *testing.T) {
	for _, selection := range []string{"folder", "request"} {
		t.Run(selection, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte("info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n"),
				Options:  InspectOptions{Selection: selection},
			})
			if err == nil || !strings.Contains(err.Error(), "params.paths") {
				t.Fatalf("selection %q error = %v", selection, err)
			}
		})
	}
}

func TestSecretNameAlwaysProducesPortableBoundedReference(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		key      string
	}{
		{name: "unicode input", filename: "顧客/ログイン.bru", key: "pässwörd"},
		{name: "punctuation only", filename: "🔥.bru", key: "🔑"},
		{name: "long input", filename: strings.Repeat("collection-", 40) + ".bru", key: strings.Repeat("token", 80)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := secretName(test.filename, test.key)
			if got == "" || len(got) > 249 {
				t.Fatalf("secretName() = %q (%d bytes)", got, len(got))
			}
			for _, r := range got {
				if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
					t.Fatalf("secretName() = %q contains non-portable rune %q", got, r)
				}
			}
			if got != secretName(test.filename, test.key) {
				t.Fatalf("secretName() is not deterministic: %q", got)
			}
		})
	}
}
