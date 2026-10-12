package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// projectionReuseTestScripts converts an archive whose draft carries inline
// credentials and the converter's typed metadata values.
func projectionReuseTestScripts(t *testing.T) []*dsl.Script {
	t.Helper()
	archive := enterpriseCollectionTestZIP(t, map[string][]byte{
		"Reuse.usr":   []byte("[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction=Action.c\n"),
		"default.cfg": []byte("[HTTP]\nConnectTimeout=30\n[Streaming]\nStreamTimeout=60\n"),
		"Action.c": []byte(`Action() {
	web_add_header("Authorization", "Basic dXNlcjpyZXVzZS1jYW5hcnk=");
	web_set_user("alice", "reuse-password-canary", "example.test:443");
	web_custom_request("Login", "URL=https://example.test/login?token=reuse-token-canary", "Method=POST", "Body=username=alice&password=reuse-body-canary", LAST);
	web_url("Home", "URL=https://example.test/home", "Referer=https://example.test/login", LAST);
	return 0;
}`),
	})
	converted, err := enterprisearchive.ConvertZip(archive, enterprisearchive.ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatal(err)
	}
	scripts := append([]*dsl.Script(nil), converted.Scripts...)
	if len(scripts) == 0 {
		scripts = []*dsl.Script{converted.Script}
	}
	return scripts
}

// The response's private projection reuses the projection the draft's
// attestation fingerprinted. It must equal projecting the response's own JSON
// copy of the draft, which carries the attestation marker and has lost the
// converter's typed metadata values.
func TestImportResponseReusesAttestationProjection(t *testing.T) {
	scripts := projectionReuseTestScripts(t)
	attested := map[*dsl.Script]*dsl.Script{}
	for _, script := range scripts {
		typed, err := scriptAsJSONValue(maskScriptSecrets(script))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := scriptAsJSONValue(maskScriptSecrets(cloneScriptDefinition(script)))
		if err != nil || !reflect.DeepEqual(typed, decoded) {
			t.Fatalf("masking a converter draft differs from masking its JSON copy: %v", err)
		}
		_, projection, err := fingerprintImportDraftCredentialsWithProjection(script, nil)
		if err != nil || projection.script == nil {
			t.Fatalf("fingerprint projection: %v", err)
		}
		ensureScriptImportPreserved(script, script.Metadata.Import.Source)[importDraftAttestationMetadataKey] = "reuse-attestation-token"
		attested[script] = projection.script
	}
	for name, value := range map[string]any{"scripts": scripts, "script": scripts[0]} {
		got := credentialPreservingImportResponseValueFromExact(value, importResponseRawScriptValue(value), attested)
		want := credentialPreservingImportResponseValue(value)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: reused projection differs from a fresh projection", name)
		}
		payload, _ := json.Marshal(got)
		for _, canary := range []string{"reuse-password-canary", "reuse-body-canary", "reuse-token-canary"} {
			if strings.Contains(string(payload), canary) {
				t.Fatalf("%s: projection disclosed %s", name, canary)
			}
		}
	}
	if got, want := credentialPreservingImportResponseValueFromExact(scripts, importResponseRawScriptValue(scripts), nil), credentialPreservingImportResponseValue(scripts); !reflect.DeepEqual(got, want) {
		t.Fatal("without attested projections the response must project as before")
	}
}

// Attestation records each draft's projection and the response builder
// consumes it; losing either half silently masks every draft a third time.
func TestImportResponseWiresAttestationProjection(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reuse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})
	scripts := projectionReuseTestScripts(t)
	opts := scriptImportResponseOptions{Script: scripts[0], Scripts: scripts, textMemo: &importProjectionTextMemo{}}
	if err := server.attachImportDraftAttestations(httptest.NewRequest(http.MethodPost, "/", nil), &opts); err != nil {
		t.Fatal(err)
	}
	projection := opts.attestedProjections[scripts[0]]
	if projection == nil {
		t.Fatal("attestation did not record the draft's projection")
	}
	marked := cloneScriptDefinition(projection)
	marked.Name = "attested-projection-marker"
	opts.attestedProjections[scripts[0]] = marked
	_, private := newScriptImportResponseWithProjection(opts)
	payload, err := json.Marshal(private)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "attested-projection-marker") {
		t.Fatal("the response projected the draft again instead of reusing its attested projection")
	}
}

// The response may publish the raw tree it hands the projection, so restored
// credential locations must be copies, never shared subtrees.
func TestProtectedCredentialLocationsAreCopied(t *testing.T) {
	raw := []any{map[string]any{
		"id":        "credential-1",
		"locations": []any{map[string]any{"path": "/actions/0/headers/Authorization", "occurrence": float64(0)}},
	}}
	projected := []any{map[string]any{"id": "credential-1", "locations": "[REDACTED]"}}
	copyProtectedImportCredentialLocations(raw, projected)
	locations, ok := projected[0].(map[string]any)["locations"].([]any)
	if !ok || len(locations) != 1 {
		t.Fatalf("locations were not restored: %#v", projected[0])
	}
	locations[0].(map[string]any)["path"] = "/changed"
	rawPath := raw[0].(map[string]any)["locations"].([]any)[0].(map[string]any)["path"]
	if rawPath != "/actions/0/headers/Authorization" {
		t.Fatal("changing the projection's restored locations changed the raw tree")
	}
}
