package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

// saveJMeterMessagingImport saves the synthetic messaging plan through the
// JMeter import save route, on a server that can hold project secrets.
func saveJMeterMessagingImport(t *testing.T) (*Server, []StoredScript) {
	t.Helper()
	source, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	source = bytes.ReplaceAll(source, []byte(`<stringProp name="sharedAccessKey">***</stringProp>`), []byte(`<stringProp name="sharedAccessKey"></stringProp>`))
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x3c}, 32), KeyID: "jmeter-saved-credential-repair"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store, BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	preview := server.newScriptImportResponseWithSourceSecrets(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil), scriptImportResponseOptions{Scripts: result.Scripts, Script: result.Script})
	payload, err := json.Marshal(map[string]any{"scripts": preview["scripts"]})
	if err != nil {
		t.Fatal(err)
	}
	response := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", payload, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("save status %d: %s", response.Code, response.Body.String())
	}
	var saved struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	return server, saved.Scripts
}

// savedScriptByProtocol reads the durable copy of the saved script of protocolName.
func savedScriptByProtocol(t *testing.T, server *Server, scripts []StoredScript, protocolName string) *StoredScript {
	t.Helper()
	for _, script := range scripts {
		if current := durableRepairSnapshot(t, server, script.ID); current.Script.Protocol == protocolName {
			return current
		}
	}
	t.Fatalf("no saved %s script", protocolName)
	return nil
}

// findingPaths lists the steps whose import review holds code as one of its
// reasons, as JMeter review codes are held.
func findingPaths(t *testing.T, script *dsl.Script, code string) []string {
	t.Helper()
	var paths []string
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		review, _ := ref.Metadata()["import_review"].(map[string]any)
		if slices.Contains(dslwalk.MetadataStrings(review, "reason_codes"), code) {
			paths = append(paths, ref.Path)
		}
		return nil
	})
	return paths
}

// A saved JMeter import keeps its credential review code, and the batch
// repairs that clear it are locked once the import is saved. The editor's
// saved-script repair binds the credential to a project secret, which must
// exist, and clears the code; the other gates stay.
func TestStoredScriptRepairBindsJMeterMessagingCredentials(t *testing.T) {
	server, saved := saveJMeterMessagingImport(t)
	const clientID, clientSecret, connection = "WI_SYNTHETIC_KAFKA_CLIENT_ID", "WI_SYNTHETIC_KAFKA_CLIENT_SECRET", "WI_SYNTHETIC_EVENTHUBS_CONNECTION"

	cases := []struct {
		protocol, code string
		operation      ImportRepairOperation
		secrets        map[string]string
		check          func(t *testing.T, stored *StoredScript)
	}{
		{
			protocol:  "kafka",
			code:      "jmeter_kafka_oauth_credentials_required",
			operation: ImportRepairOperation{Type: "bind_kafka_credentials", ClientIDSecret: clientID, SecretName: clientSecret},
			secrets:   map[string]string{clientID: "synthetic-client-id", clientSecret: "synthetic-client-secret"},
			check: func(t *testing.T, stored *StoredScript) {
				oauth := stored.Script.Config.Kafka.OAuth
				if oauth == nil || oauth.ClientID != "{{secret."+clientID+"}}" || oauth.ClientSecretRef != "{{secret."+clientSecret+"}}" {
					t.Fatalf("Kafka OAuth after the repair = %+v", oauth)
				}
			},
		},
		{
			protocol:  "eventhubs",
			code:      "jmeter_eventhubs_credentials_required",
			operation: ImportRepairOperation{Type: "bind_eventhubs_credentials", SecretName: connection},
			secrets:   map[string]string{connection: "Endpoint=sb://synthetic.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=synthetic"},
			check: func(t *testing.T, stored *StoredScript) {
				config := stored.Script.Config.EventHubs
				if config.ConnectionString != "" || config.ConnectionStringSecret != "{{secret."+connection+"}}" {
					t.Fatalf("Event Hubs credential after the repair = %q / %q", config.ConnectionString, config.ConnectionStringSecret)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			script := savedScriptByProtocol(t, server, saved, tc.protocol)
			paths := findingPaths(t, script.Script, tc.code)
			if len(paths) == 0 {
				t.Fatalf("saved %s script has no %s gate", tc.protocol, tc.code)
			}
			_, tag := requireAuthoringAPIProjection(t, server, script.ID)
			repair := ScriptRepairRequest{Finding: tc.code, ActionPath: paths[0], Operation: tc.operation}

			// The secrets do not exist yet: the binding is refused and nothing changes.
			refused := scriptRepairCall(t, server, script.ID, "apply", repair, tag)
			if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "Create the project secret") {
				t.Fatalf("binding a missing secret = %d %s", refused.Code, refused.Body.String())
			}
			if durableRepairSnapshot(t, server, script.ID).ContentRevision != script.ContentRevision {
				t.Fatal("a refused credential binding changed the script")
			}

			for name, value := range tc.secrets {
				if _, err := server.projectSecretStore.Create(context.Background(), storage.DefaultProjectID, name, value, "test"); err != nil {
					t.Fatal(err)
				}
			}
			decodeScriptRepair(t, scriptRepairCall(t, server, script.ID, "apply", repair, tag))
			current := durableRepairSnapshot(t, server, script.ID)
			if got := findingPaths(t, current.Script, tc.code); len(got) != 0 {
				t.Fatalf("%s still gates %v after the binding", tc.code, got)
			}
			// The messaging replay gate is not a credential gate and stays.
			if got := findingPaths(t, current.Script, "jmeter_messaging_compatibility_unverified"); len(got) == 0 {
				t.Fatal("the credential binding cleared the messaging compatibility gate")
			}
			tc.check(t, current)
		})
	}
}

// The binding names the code it clears: a credential repair aimed at
// another review code, such as the messaging gate nothing clears yet, is
// refused.
func TestStoredScriptRepairJMeterCredentialBindingNeedsItsFinding(t *testing.T) {
	server, saved := saveJMeterMessagingImport(t)
	script := savedScriptByProtocol(t, server, saved, "eventhubs")
	paths := findingPaths(t, script.Script, "jmeter_messaging_compatibility_unverified")
	if len(paths) == 0 {
		t.Fatal("fixture has no messaging gate")
	}
	_, tag := requireAuthoringAPIProjection(t, server, script.ID)
	repair := ScriptRepairRequest{Finding: "jmeter_messaging_compatibility_unverified", ActionPath: paths[0], Operation: ImportRepairOperation{Type: "bind_eventhubs_credentials", SecretName: "WI_SYNTHETIC_EVENTHUBS_CONNECTION"}}
	response := scriptRepairCall(t, server, script.ID, "preview", repair, tag)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("binding against the messaging gate = %d %s", response.Code, response.Body.String())
	}
}
